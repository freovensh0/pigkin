package modes

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func hostBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "pigkin-host")
	build := exec.Command("go", "build", "-o", binary, "./cmd/pigkin-host")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return binary
}

func TestHelloReportsRuntimeAndSharedStorage(t *testing.T) {
	binary := hostBinary(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if tcp, ok := listener.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(15 * time.Second))
	}
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := exec.Command(binary, "hello", port)
	cmd.Env = append(os.Environ(),
		"PIGKIN_TOKEN=test-token",
		"PIGKIN_ROOT="+filepath.Join(base, "root"),
		"PI_CODING_AGENT_DIR="+configDir,
		"PREFIX="+filepath.Join(base, "prefix"),
		"HOME="+filepath.Join(base, "home"),
		"EXTERNAL_STORAGE="+base,
	)
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	scanner := host.ScanLines(connection)
	if !scanner.Scan() {
		t.Fatalf("no hello: %s", output.String())
	}
	var hello host.Message
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	if hello["type"] != "pigkin_hello" || hello["protocol"] != float64(1) || hello["mode"] != "hello" || hello["token"] != "test-token" {
		t.Fatalf("hello = %v", hello)
	}
	if hello["piVersion"] != RuntimeVersion() {
		t.Fatalf("piVersion = %v, want %v", hello["piVersion"], RuntimeVersion())
	}
	if node, ok := hello["nodeVersion"].(string); !ok || node == "" {
		t.Fatalf("nodeVersion = %v", hello["nodeVersion"])
	}
	if hello["configDir"] != configDir || hello["home"] != filepath.Join(base, "home") {
		t.Fatalf("paths in hello = %v", hello)
	}
	if hello["sharedStorage"] != true {
		t.Fatalf("sharedStorage = %v", hello["sharedStorage"])
	}
}
