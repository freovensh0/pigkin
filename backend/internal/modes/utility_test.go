package modes

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestUtilityRequestResponseEnvelope(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	connection := host.NewConnection(server)
	ctx := &Context{Env: testEnv(t), Conn: connection}
	returned := make(chan struct{})
	go func() {
		_, _ = utility(ctx)
		close(returned)
	}()

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := host.ScanLines(client)
	if !scanner.Scan() {
		t.Fatal("no ready line:", scanner.Err())
	}
	var ready host.Message
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil || ready["type"] != "pigkin_utility_ready" {
		t.Fatalf("ready = %s (%v)", scanner.Bytes(), err)
	}

	for _, request := range []host.Message{
		{"type": "pigkin_utility_request", "id": 1, "op": "sessions", "params": map[string]any{}},
		{"type": "pigkin_utility_request", "id": 2, "op": "nope", "params": map[string]any{}},
		{"type": "pigkin_utility_request", "id": 3, "op": "sessions", "params": "not an object"},
	} {
		line, err := host.EncodeLine(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write(line); err != nil {
			t.Fatal(err)
		}
	}

	got := map[float64]host.Message{}
	for range 3 {
		if !scanner.Scan() {
			t.Fatal("missing result:", scanner.Err())
		}
		var result host.Message
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		id, ok := result["id"].(float64)
		if !ok {
			t.Fatalf("result has no id: %s", scanner.Bytes())
		}
		if result["type"] != "pigkin_result" {
			t.Fatalf("result type = %v", result["type"])
		}
		got[id] = result
	}
	if got[1]["ok"] != true {
		t.Fatalf("sessions result = %v", got[1])
	}
	if data, ok := got[1]["data"].(map[string]any); !ok || len(data["sessions"].([]any)) != 0 {
		t.Fatalf("sessions data = %v", got[1]["data"])
	}
	for id, want := range map[float64]struct{ code, message string }{
		2: {"unknown_op", "Unknown operation nope"},
		3: {"bad_request", "Expected params"},
	} {
		if got[id]["ok"] != false || got[id]["code"] != want.code || got[id]["error"] != want.message {
			t.Fatalf("result %v = %v", id, got[id])
		}
	}

	_ = client.Close()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("utility did not return after hang-up")
	}
}
