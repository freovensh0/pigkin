package modes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestParseChannelLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     string
		wantType string
		want     string
	}{
		{"socket", `{"topic":"todos","data":[1,2]}`, "", "todos"},
		{"app", `{"type":"pigkin_channel","topic":"background","data":{"active":1}}`, channelType, "background"},
		{"missing data", `{"type":"pigkin_channel","topic":"todos"}`, channelType, "todos"},
		{"bad json", `{"topic":`, "", ""},
		{"bad topic", `{"topic":"Bad Topic","data":1}`, "", ""},
		{"wrong type", `{"topic":"todos","data":1}`, channelType, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, ok := parseChannelLine([]byte(tc.line), tc.wantType)
			if ok != (tc.want != "") || (ok && message.Topic != tc.want) {
				t.Fatalf("got %q, %v; want %q", message.Topic, ok, tc.want)
			}
			if tc.name == "missing data" {
				line, err := encodeChannelMessage(message)
				if err != nil || !bytes.Contains(line, []byte(`"data":null`)) {
					t.Fatalf("missing data encoded as %q, %v", line, err)
				}
			}
		})
	}
}

func TestReadChannelDropsMalformedAndKeepsReading(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	var messages []channelMessage
	var dropped []string
	done := make(chan struct{})
	go func() {
		readChannel(left, func(message channelMessage) {
			messages = append(messages, message)
		}, func(reason string) {
			dropped = append(dropped, reason)
		})
		close(done)
	}()
	long := strings.Repeat("x", maxSocketLine+1)
	for _, line := range []string{
		`{"topic":"todos","data":1}`,
		"not json",
		`{"topic":"todos","data":"` + long + `"}`,
		`{"topic":"background","data":2}`,
	} {
		if _, err := io.WriteString(right, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	_ = right.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel reader did not stop")
	}
	if len(messages) != 2 || messages[0].Topic != "todos" || messages[1].Topic != "background" {
		t.Fatalf("messages: %+v dropped: %v", messages, dropped)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped: %v", dropped)
	}
}

func TestRPCChannelEndToEndWithFakeChild(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	packageDir := filepath.Join(root, "pi", "node_modules", filepath.FromSlash(piPackage))
	if err := os.MkdirAll(filepath.Join(packageDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "extensions", "pigkin-todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(`{"bin":{"pi":"bin/pi.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "bin", "pi.js"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	source := []byte("export default function () {}\n")
	if err := os.WriteFile(filepath.Join(root, "extensions", "pigkin-todo", "index.js"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	sum := hex.EncodeToString(digest[:])

	binDir := t.TempDir()
	seen := filepath.Join(t.TempDir(), "app-line")
	helloFile := filepath.Join(t.TempDir(), "hello")
	fakeNode := "#!/bin/sh\n" +
		"case $(readlink /proc/self/fd/3) in socket:*) ;; *) exit 1 ;; esac\n" +
		"IFS= read -r hello <&3\n" +
		"printf '%s' \"$hello\" > " + channelQuote(helloFile) + "\n" +
		"IFS= read -r app <&3\n" +
		"printf '%s' \"$app\" > " + channelQuote(seen) + "\n" +
		"printf '%s\\n' '{\"topic\":\"todos\",\"data\":{\"state\":42}}' >&3\n" +
		"printf '%s\\n' '{\"type\":\"agent_settled\"}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "node"), []byte(fakeNode), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	appSide, hostSide := net.Pipe()
	defer appSide.Close()
	defer hostSide.Close()
	conn := host.NewConnection(hostSide)
	request := host.Message{"type": "pigkin_request", "params": host.Message{
		"cwd": root, "sessionPath": nil,
		"pigkinExtensions": []any{host.Message{
			"name": "pigkin-todo", "sha256": sum, "usesRuntimePackages": false,
		}},
	}}
	if err := writeLine(appSide, request); err != nil {
		t.Fatal(err)
	}
	if _, status := conn.First(time.Second); status != host.FirstMessage {
		t.Fatalf("first message: %v", status)
	}
	result := make(chan error, 1)
	go func() {
		_, err := rpc(&Context{Env: host.Env{
			Root: root, ConfigDir: configDir, Home: root, Prefix: root,
		}, Params: request["params"].(host.Message), Conn: conn})
		result <- err
	}()
	if err := writeLine(appSide, host.Message{
		"type": "pigkin_channel", "topic": "background", "data": host.Message{"action": "stop"},
	}); err != nil {
		t.Fatal(err)
	}

	scanner := host.ScanLines(appSide)
	seenChannel := false
	seenSettled := false
	seenExit := false
	deadline := time.After(10 * time.Second)
	for !seenChannel || !seenSettled || !seenExit {
		select {
		case <-deadline:
			t.Fatalf("missing events: channel=%v settled=%v exit=%v", seenChannel, seenSettled, seenExit)
		default:
		}
		if !scanner.Scan() {
			t.Fatal("app connection ended early")
		}
		var line host.Message
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		switch line["type"] {
		case channelType:
			if line["topic"] != "todos" {
				t.Fatalf("channel topic: %v", line)
			}
			data, _ := line["data"].(map[string]any)
			if data["state"] != float64(42) {
				t.Fatalf("channel data: %v", line)
			}
			seenChannel = true
		case "agent_settled":
			seenSettled = true
		case "pigkin_exit":
			seenExit = true
		}
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"topic":"background"`)) || !bytes.Contains(data, []byte(`"action":"stop"`)) {
		t.Fatalf("app channel line did not reach the child: %q", data)
	}
	hello, err := os.ReadFile(helloFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(hello) != `{"topic":"pigkin","data":null}` {
		t.Fatalf("hello: %q", hello)
	}
}

func TestOutputMergerFlushesChannelWithoutStdout(t *testing.T) {
	var written [][]byte
	merger := &outputMerger{write: func(line []byte) {
		written = append(written, append([]byte(nil), line...))
	}}
	merger.channel(channelMessage{Topic: "todos", Data: host.Message{"state": 42}})
	if len(written) != 1 {
		t.Fatalf("wrote %d lines before any stdout, want 1", len(written))
	}
	var message host.Message
	if err := json.Unmarshal(written[0], &message); err != nil {
		t.Fatal(err)
	}
	if message["type"] != channelType || message["topic"] != "todos" {
		t.Fatalf("flushed line = %s", written[0])
	}
}

func writeLine(conn net.Conn, message host.Message) error {
	line, err := host.EncodeLine(message)
	if err != nil {
		return err
	}
	_, err = conn.Write(line)
	return err
}

func channelQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
