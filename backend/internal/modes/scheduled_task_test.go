package modes

import (
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
		name     string
		model    string
		thinking string
		want     []string
	}{
		{"neither", "", "", []string{"entry", "--print", "say hi"}},
		{"model", "openai/gpt-4o", "", []string{"entry", "--model", "openai/gpt-4o", "--print", "say hi"}},
		{"thinking", "", "high", []string{"entry", "--thinking", "high", "--print", "say hi"}},
		{"both", "sonnet", "medium", []string{"entry", "--model", "sonnet", "--thinking", "medium", "--print", "say hi"}},
		{"blank omitted", " \t ", "\n", []string{"entry", "--print", "say hi"}},
		{"trimmed", "  opus  ", " high ", []string{"entry", "--model", "opus", "--thinking", "high", "--print", "say hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduledTaskArgs("entry", "say hi", tc.model, tc.thinking)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScheduledTaskResult(t *testing.T) {
	got := scheduledTaskResult("  hello\n\n")
	result, ok := got["result"].(string)
	if !ok || result != "hello" {
		t.Fatalf("result = %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("result has extra fields: %v", got)
	}
}
