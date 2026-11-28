package modes

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestLoginAPIKeyFlow(t *testing.T) {
	env := testEnv(t)
	hostConn, appConn := net.Pipe()
	defer hostConn.Close()
	defer appConn.Close()
	conn := host.NewConnection(hostConn)
	ctx := &Context{Env: env, Params: host.Message{"providerId": "google"}, Conn: conn}

	type outcome struct {
		data host.Message
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		data, err := login(ctx)
		done <- outcome{data, err}
	}()

	promptCh := make(chan host.Message, 16)
	go func() {
		scanner := host.ScanLines(appConn)
		for scanner.Scan() {
			var message host.Message
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}
			if message["type"] == "pigkin_auth_prompt" {
				promptCh <- message
				line, _ := host.EncodeLine(host.Message{
					"type": "pigkin_auth_response", "id": message["id"], "value": "dummy-key-123",
				})
				_, _ = appConn.Write(line)
			}
		}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("login: %v", result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not finish")
	}
	prompts := []host.Message{}
	for {
		select {
		case prompt := <-promptCh:
			prompts = append(prompts, prompt)
		default:
			goto checked
		}
	}
checked:
	if len(prompts) == 0 || prompts[0]["prompt"].(map[string]any)["type"] != "secret" {
		t.Fatalf("prompts = %v", prompts)
	}
	auth := readFileJSON(t, filepath.Join(env.ConfigDir, "auth.json"))
	credential := auth["google"].(map[string]any)
	if credential["type"] != "api_key" || credential["key"] != "dummy-key-123" {
		t.Fatalf("credential = %v", credential)
	}
	data := opData(t, runOp(t, env, "auth_status", host.Message{}))
	for _, item := range data["builtin"].([]any) {
		entry := item.(host.Message)
		if entry["id"] == "google" && (entry["configured"] != true || entry["type"] != "api_key") {
			t.Fatalf("google status = %v", entry)
		}
	}
}

func TestAuthBridgeDismissesPromptOnCancel(t *testing.T) {
	hostConn, appConn := net.Pipe()
	defer hostConn.Close()
	defer appConn.Close()
	conn := host.NewConnection(hostConn)
	ctx := &Context{Conn: conn}

	lines := make(chan host.Message, 16)
	go func() {
		scanner := host.ScanLines(appConn)
		for scanner.Scan() {
			var message host.Message
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				lines <- message
			}
		}
	}()

	interaction := authBridge(ctx)
	promptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := interaction.Prompt(promptCtx, ai.AuthSecretPrompt{Message: "Key"})
		result <- err
	}()

	select {
	case prompt := <-lines:
		if prompt["type"] != "pigkin_auth_prompt" {
			t.Fatalf("first message = %v", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no prompt was sent")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancellation did not fail the prompt")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return on cancellation")
	}
	select {
	case dismiss := <-lines:
		if dismiss["type"] != "pigkin_auth_prompt_dismiss" {
			t.Fatalf("message after cancellation = %v", dismiss)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no dismiss was sent when the prompt was cancelled")
	}
}

func TestLoginRejectsUnknownProvider(t *testing.T) {
	env := testEnv(t)
	result, err := login(&Context{Env: env, Params: host.Message{"providerId": "vercel-ai-gateway"}})
	if err == nil || result != nil {
		t.Fatalf("login = %v, %v", result, err)
	}
	if got := opCode(Failure(err)); got != "bad_params" {
		t.Fatalf("code = %s", got)
	}
}
