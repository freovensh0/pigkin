package modes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestScheduledTaskPrompt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		params  host.Message
		want    string
		wantErr bool
	}{
		{"missing", host.Message{}, "", true},
		{"empty", host.Message{"prompt": ""}, "", true},
		{"blank", host.Message{"prompt": " \n\t "}, "", true},
		{"wrong type", host.Message{"prompt": 42}, "", true},
		{"valid", host.Message{"prompt": "say hi"}, "say hi", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scheduledTaskPrompt(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected bad_request")
				}
				if err.Code != "bad_request" || err.I18n == nil || err.I18n.Key == "" {
					t.Fatalf("error = %+v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("prompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScheduledTaskArgs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		model       string
		thinking    string
		id          string
		sessionPath string
		want        []string
	}{
		{"neither", "", "", "", "", []string{"entry", "--print", "say hi"}},
		{"model", "openai/gpt-4o", "", "", "", []string{"entry", "--model", "openai/gpt-4o", "--print", "say hi"}},
		{"thinking", "", "high", "", "", []string{"entry", "--thinking", "high", "--print", "say hi"}},
		{"both", "sonnet", "medium", "", "", []string{"entry", "--model", "sonnet", "--thinking", "medium", "--print", "say hi"}},
		{"blank omitted", " \t ", "\n", "", "", []string{"entry", "--print", "say hi"}},
		{"trimmed", "  opus  ", " high ", "", "", []string{"entry", "--model", "opus", "--thinking", "high", "--print", "say hi"}},
		{"first run", "", "", "task-1", "", []string{"entry", "--session-dir", "/cfg/scheduled", "--session-id", "task-1", "--print", "say hi"}},
		{"later run", "", "", "task-1", "/cfg/scheduled/old.jsonl", []string{"entry", "--session", "/cfg/scheduled/old.jsonl", "--print", "say hi"}},
		{"invalid id", "", "", "bad/id", "", []string{"entry", "--print", "say hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduledTaskArgs("entry", "say hi", tc.model, tc.thinking, tc.id, tc.sessionPath, "/cfg/scheduled")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScheduledTaskResult(t *testing.T) {
	got := scheduledTaskResult("  hello\n\n", "/cfg/scheduled/a.jsonl", "/home/user")
	result, ok := got["result"].(string)
	if !ok || result != "hello" {
		t.Fatalf("result = %v", got)
	}
	if got["sessionPath"] != "/cfg/scheduled/a.jsonl" || got["cwd"] != "/home/user" || len(got) != 3 {
		t.Fatalf("result has unexpected fields: %v", got)
	}
}

func TestScheduledTaskSession(t *testing.T) {
	dir := t.TempDir()
	if got := scheduledTaskSession(dir, "task-1"); got != "" {
		t.Fatalf("session before the first run = %q", got)
	}
	for _, name := range []string{"2026-01-01_task-2.jsonl", "2026-01-02_task-1.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(dir, "2026-01-02_task-1.jsonl")
	if got := scheduledTaskSession(dir, "task-1"); got != want {
		t.Fatalf("session = %q, want %q", got, want)
	}
}
