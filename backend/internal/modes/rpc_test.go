package modes

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestRemoteSystemAppend(t *testing.T) {
	prompt := remoteSystemAppend(Host{Name: "devbox", Address: "me@devbox.lan"}, "/home/me/src/api")
	for _, want := range []string{pigkinIdentity, "/home/me/src/api", "devbox (me@devbox.lan)", "over SSH", "no grep, find or ls tools"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("remote prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "pkg") || strings.Contains(prompt, "$PREFIX") {
		t.Fatalf("remote prompt carried Termux guidance: %s", prompt)
	}
	if label := hostLabel(Host{Name: "box", Address: "box"}); label != "box" {
		t.Fatalf("host label = %q", label)
	}
}

func TestRemoteChatFor(t *testing.T) {
	env := testEnv(t)
	id := saveTestHost(t, env, host.Message{"name": "devbox", "auth": "key", "address": "me@devbox.lan", "port": float64(22)})
	cwd := opData(t, runOp(t, env, "remote_workspace", host.Message{"hostId": id, "path": "/home/me/src/api"}))["cwd"].(string)
	extensions := []pigkinExtension{{name: "pigkin-background", path: "/bg"}, {name: remoteExtension, path: "/remote"}}
	chat, err := remoteChatFor(&Context{Env: env}, cwd, extensions)
	if err != nil {
		t.Fatal(err)
	}
	if chat.extension != "/remote" || !strings.Contains(chat.prompt, "/home/me/src/api") {
		t.Fatalf("remote chat = %+v", chat)
	}
	_, err = remoteChatFor(&Context{Env: env}, cwd, extensions[:1])
	if failure, ok := err.(*host.Error); !ok || failure.I18n == nil || failure.I18n.Key != "agent_extensions_outdated" {
		t.Fatalf("missing extension: %v", err)
	}
	local, err := remoteChatFor(&Context{Env: env}, env.Home, extensions)
	if err != nil || local != nil {
		t.Fatalf("local chat: %v, %+v", err, local)
	}
}

func TestRPCRefusesMissingFolder(t *testing.T) {
	_, err := rpc(&Context{Params: host.Message{"cwd": filepath.Join(t.TempDir(), "gone")}})
	hostErr, ok := err.(*host.Error)
	if !ok || hostErr.Code != "cwd_missing" {
		t.Fatalf("got %v, want cwd_missing", err)
	}
	if hostErr.I18n == nil || hostErr.I18n.Key != "folder_not_found" {
		t.Fatalf("got i18n %v, want folder_not_found", hostErr.I18n)
	}
}

var (
	buildOnce sync.Once
	builtHost string
	buildErr  error
)

func rpcBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtHost = filepath.Join(t.TempDir(), "pigkin-host")
		cmd := exec.Command("go", "build", "-o", builtHost, "./cmd/pigkin-host")
		cmd.Dir = filepath.Join("..", "..")
		if output, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build: %s", output)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtHost
}

func TestRPCChatWithFauxProvider(t *testing.T) {
	binary := rpcBinary(t)
	previous := pigRPCBinary
	pigRPCBinary = func() (string, error) { return binary, nil }
	defer func() { pigRPCBinary = previous }()

	t.Setenv("PIG_TEST_FAUX", "1")
	t.Setenv("PIG_TEST_FAUX_SCENARIO", "parity-basic")
	t.Setenv("PIG_OFFLINE", "1")

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	appConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer appConn.Close()
	hostConn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conn := host.NewConnection(hostConn)
	ctx := &Context{
		Env:    host.Env{ConfigDir: configDir, Home: root, Root: root, Prefix: root},
		Params: host.Message{"cwd": root, "sessionPath": nil},
		Conn:   conn,
	}
	write := func(message host.Message) {
		t.Helper()
		line, err := host.EncodeLine(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := appConn.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	write(host.Message{"type": "pigkin_request", "params": host.Message{"cwd": root}})
	request, status := conn.First(time.Second)
	if status != host.FirstMessage || request["type"] != "pigkin_request" {
		t.Fatalf("first message: %v, %v", request, status)
	}
	go func() { _, _ = rpc(ctx) }()

	scanner := host.ScanLines(appConn)
	write(host.Message{"type": "get_state", "id": "r1"})
	write(host.Message{"type": "prompt", "id": "r2", "message": "Run: expr 20 + 22"})

	seen := map[string]bool{}
	toolRan := false
	for {
		if !scanner.Scan() {
			t.Fatalf("app connection ended before agent settled; events seen: %v", seen)
		}
		var line host.Message
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		switch line["type"] {
		case "response":
			if line["id"] == "r2" && line["success"] != true {
				t.Fatalf("prompt refused: %v", line)
			}
		case "tool_execution_end":
			toolRan = line["toolCallId"] != nil
		case "message_update":
			update, _ := line["assistantMessageEvent"].(map[string]any)
			if update["type"] == "text_delta" {
				seen["text_delta"] = true
			}
		case "agent_settled":
			seen["agent_settled"] = true
			if err := appConn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
		case "pigkin_exit":
			if line["code"] != float64(0) {
				t.Fatalf("agent exited with %v: %v", line["code"], line["stderr"])
			}
			if !seen["agent_settled"] || !toolRan || !seen["text_delta"] {
				t.Fatalf("missing events: settled=%v tool=%v text=%v", seen["agent_settled"], toolRan, seen["text_delta"])
			}
			files, err := filepath.Glob(filepath.Join(configDir, "sessions", "*.jsonl"))
			if err != nil || len(files) == 0 {
				t.Fatalf("no session file: %v, %v", files, err)
			}
			return
		default:
			if kind, ok := line["type"].(string); ok {
				seen[kind] = true
			}
		}
	}
}
