package modes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/freovensh0/pigkin/backend/internal/host"
)

type promptResult struct {
	value string
	err   error
}

// authBridge carries Pi's login prompts and events over the socket.
func authBridge(ctx *Context) ai.AuthInteraction {
	var mu sync.Mutex
	pending := map[int]chan promptResult{}
	nextID := 1

	ctx.Conn.Listen(func(message host.Message) {
		id, ok := messageID(message["id"])
		if !ok {
			return
		}
		mu.Lock()
		ch := pending[id]
		delete(pending, id)
		mu.Unlock()
		if ch == nil {
			return
		}
		switch message["type"] {
		case "pigkin_auth_response":
			if value, ok := message["value"].(string); ok {
				ch <- promptResult{value: value}
				return
			}
		case "pigkin_auth_cancel":
			ch <- promptResult{err: errors.New("cancelled")}
			return
		}
		ch <- promptResult{err: errors.New("cancelled")}
	})

	forget := func(id int) {
		mu.Lock()
		delete(pending, id)
		mu.Unlock()
	}

	return ai.AuthInteraction{
		Notify: func(event ai.AuthEvent) {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			var value host.Message
			if json.Unmarshal(data, &value) != nil {
				return
			}
			ctx.Conn.Send(host.Message{"type": "pigkin_auth_event", "event": value})
		},
		Prompt: func(promptCtx context.Context, prompt ai.AuthPrompt) (string, error) {
			data, err := json.Marshal(prompt)
			if err != nil {
				return "", err
			}
			var description host.Message
			if json.Unmarshal(data, &description) != nil {
				return "", errors.New("invalid auth prompt")
			}
			mu.Lock()
			id := nextID
			nextID++
			ch := make(chan promptResult, 1)
			pending[id] = ch
			mu.Unlock()
			ctx.Conn.Send(host.Message{"type": "pigkin_auth_prompt", "id": id, "prompt": description})
			select {
			case result := <-ch:
				return result.value, result.err
			case <-promptCtx.Done():
				forget(id)
				return "", errors.New("cancelled")
			case <-ctx.Conn.Done():
				forget(id)
				return "", errors.New("cancelled")
			}
		},
	}
}

func messageID(value any) (int, bool) {
	switch id := value.(type) {
	case float64:
		return int(id), true
	case int:
		return id, true
	case json.Number:
		parsed, err := id.Int64()
		return int(parsed), err == nil
	}
	return 0, false
}

// getOrCreateDeviceID keeps ChatGPT's stable device id in Pi's settings.json.
func getOrCreateDeviceID(ctx *Context) string {
	settings, err := readSettings(ctx.Env.ConfigDir)
	if err == nil {
		if id, ok := settings["deviceId"].(string); ok && id != "" {
			return id
		}
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	id := hex.EncodeToString(buffer)
	if err == nil {
		settings["deviceId"] = id
		_ = writeSettings(ctx.Env.ConfigDir, settings)
	}
	return id
}

func login(ctx *Context) (host.Message, error) {
	providerID, err := stringParam(ctx.Params, "providerId")
	if err != nil {
		return nil, err
	}
	kind, ok := loginType(providerID)
	if !ok {
		return nil, host.NewError("bad_params", "Pigkin cannot sign in to "+providerID, nil)
	}
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()

	deviceID := getOrCreateDeviceID(ctx)
	_, err = runtime.Login(piContext(ctx), providerID, ai.CredentialType(kind), authBridge(ctx), ai.LoginOptions{
		GetDeviceID: func() string { return deviceID },
	})
	if err != nil {
		return nil, err
	}

	if err := maintainScopeAfterLogin(ctx, providerID); err != nil {
		return nil, err
	}
	return host.Message{}, nil
}

func maintainScopeAfterLogin(ctx *Context, providerID string) error {
	settings, err := readSettings(ctx.Env.ConfigDir)
	if err != nil {
		if errors.Is(err, errSettingsInvalid) {
			return nil
		}
		return err
	}
	patterns, err := enabledModels(settings)
	if err != nil {
		return nil
	}
	if len(patterns) == 0 {
		return nil
	}
	hidden := hiddenUntilPicked(providerID)
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return err
	}
	defer runtime.Close()
	universe, err := scopeUniverse(runtime)
	if err != nil {
		return err
	}
	var changed []string
	if hidden {
		changed = hideUnpickedProvider(patterns, providerID, defaultRef(settings), universe)
	} else {
		changed = keepProviderInScope(patterns, providerID, defaultRef(settings), universe)
	}
	if changed != nil {
		setEnabledModels(settings, changed)
		return writeSettings(ctx.Env.ConfigDir, settings)
	}
	return nil
}
