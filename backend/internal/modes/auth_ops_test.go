package modes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.WriteFile(path, []byte(mustJSON(value)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(data)
}

func asMessages(t *testing.T, value any) []host.Message {
	t.Helper()
	switch list := value.(type) {
	case []host.Message:
		return list
	case []any:
		result := make([]host.Message, 0, len(list))
		for _, item := range list {
			object, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("item = %T", item)
			}
			result = append(result, object)
		}
		return result
	}
	t.Fatalf("value = %T", value)
	return nil
}

func readFileJSON(t *testing.T, path string) host.Message {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value host.Message
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAuthStatusOp(t *testing.T) {
	env := testEnv(t)
	data := opData(t, runOp(t, env, "auth_status", host.Message{}))
	if len(data["builtin"].([]any)) != len(builtinProviders) {
		t.Fatalf("builtin = %v", data["builtin"])
	}
	if got := data["custom"].([]any); len(got) != 0 {
		t.Fatalf("custom = %v", got)
	}
	if data["scope"] != nil || len(data["npmPackages"].([]string)) != 0 {
		t.Fatalf("scope/npm = %v / %v", data["scope"], data["npmPackages"])
	}
	for _, item := range data["builtin"].([]any) {
		entry := item.(host.Message)
		if entry["configured"] != false || entry["type"] != nil {
			t.Fatalf("fresh builtin = %v", entry)
		}
	}
}

func TestAuthStatusOpCustomProviders(t *testing.T) {
	env := testEnv(t)
	writeJSON(t, filepath.Join(env.ConfigDir, "models.json"), host.Message{
		"providers": host.Message{
			"my-llm": host.Message{
				"baseUrl": "http://127.0.0.1:1234/v1",
				"api":     "openai-completions",
				"apiKey":  "dummy-secret",
				"models":  []any{host.Message{"id": "m1", "reasoning": true}, host.Message{"id": "m2"}},
			},
			"keyless": host.Message{"baseUrl": "http://x/v1", "api": "openai-completions", "apiKey": noAPIKey, "models": []any{"plain"}},
		},
	})
	writeJSON(t, filepath.Join(env.ConfigDir, "settings.json"), host.Message{
		"packages": []any{host.Message{"source": "npm:@scope/pkg@1.2.3"}, "npm:other"},
	})
	data := opData(t, runOp(t, env, "auth_status", host.Message{}))
	custom := data["custom"].([]any)
	if len(custom) != 2 {
		t.Fatalf("custom = %v", custom)
	}
	byID := map[string]host.Message{}
	for _, item := range custom {
		entry := item.(host.Message)
		byID[entry["id"].(string)] = entry
	}
	llm := byID["my-llm"]
	if llm["apiKey"] != "dummy-secret" || llm["baseUrl"] != "http://127.0.0.1:1234/v1" {
		t.Fatalf("my-llm = %v", llm)
	}
	if models := llm["models"].([]any); len(models) != 2 || models[0].(host.Message)["reasoning"] != true {
		t.Fatalf("my-llm models = %v", models)
	}
	if byID["keyless"]["apiKey"] != "" {
		t.Fatalf("keyless apiKey = %v", byID["keyless"]["apiKey"])
	}
	pkgs := data["npmPackages"].([]string)
	if !reflect.DeepEqual(pkgs, []string{"@scope/pkg", "other"}) {
		t.Fatalf("npmPackages = %v", pkgs)
	}
}

func TestProviderSaveRemoveOps(t *testing.T) {
	env := testEnv(t)
	for _, params := range []host.Message{
		{"id": "BAD_ID", "baseUrl": "http://x/v1", "api": "openai-completions", "apiKey": "k", "models": []any{}},
		{"id": "anthropic", "baseUrl": "http://x/v1", "api": "openai-completions", "apiKey": "k", "models": []any{}},
		{"id": "ok-id", "baseUrl": "http://x/v1", "api": "bad-api", "apiKey": "k", "models": []any{}},
	} {
		result := runOp(t, env, "provider_save", params)
		if result["ok"] != false {
			t.Fatalf("save %v = %v", params["id"], result)
		}
	}
	if opCode(runOp(t, env, "provider_save", host.Message{"id": "BAD_ID"})) != "bad_params" {
		t.Fatal("bad id should be bad_params when id param is missing")
	}

	params := host.Message{
		"id": "my-llm", "baseUrl": "http://x/v1", "api": "openai-completions", "apiKey": "",
		"models": []any{host.Message{"id": "m1", "reasoning": true, "contextWindow": 8192}},
	}
	if result := runOp(t, env, "provider_save", params); result["ok"] != true {
		t.Fatal(result)
	}
	doc := readFileJSON(t, filepath.Join(env.ConfigDir, "models.json"))
	entry := doc["providers"].(host.Message)["my-llm"].(host.Message)
	if entry["apiKey"] != noAPIKey || entry["api"] != "openai-completions" {
		t.Fatalf("entry = %v", entry)
	}
	model := entry["models"].([]any)[0].(host.Message)
	if model["reasoning"] != true || model["contextWindow"] != float64(8192) {
		t.Fatalf("model = %v", model)
	}

	if result := runOp(t, env, "provider_remove", host.Message{"id": "my-llm"}); result["ok"] != true {
		t.Fatal(result)
	}
	doc = readFileJSON(t, filepath.Join(env.ConfigDir, "models.json"))
	if _, ok := doc["providers"].(host.Message)["my-llm"]; ok {
		t.Fatal("provider was not removed")
	}
}

func TestModelDefaultsOp(t *testing.T) {
	env := testEnv(t)
	result := runOp(t, env, "model_defaults", host.Message{"ids": []any{}, "api": "openai-responses"})
	models := asMessages(t, opData(t, result)["models"])
	if len(models) != 0 {
		t.Fatalf("models = %v", models)
	}
	result = runOp(t, env, "model_defaults", host.Message{
		"ids": []any{"totally-unknown-model-xyz", "gpt-4o"}, "api": "openai-responses",
	})
	if result["ok"] != true {
		t.Fatal(result)
	}
	models = asMessages(t, opData(t, result)["models"])
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	if first := models[0]; first["id"] != "totally-unknown-model-xyz" || len(first) != 1 {
		t.Fatalf("unknown = %v", first)
	}
	known := models[1]
	if known["id"] != "gpt-4o" || known["name"] == nil {
		t.Fatalf("known = %v", known)
	}
}

func TestDefaultModelOp(t *testing.T) {
	env := testEnv(t)
	settingsPath := filepath.Join(env.ConfigDir, "settings.json")
	writeJSON(t, settingsPath, host.Message{"enabledModels": []any{"lb/m1"}})

	for _, params := range []host.Message{
		{"provider": "lb", "modelId": nil, "thinkingLevel": nil},
		{"provider": nil, "modelId": nil, "thinkingLevel": "ultra"},
		{"provider": nil, "modelId": nil, "thinkingLevel": nil},
	} {
		if result := runOp(t, env, "default_model", params); opCode(result) != "bad_params" {
			t.Fatalf("params %v = %v", params, result)
		}
	}
	result := runOp(t, env, "default_model", host.Message{
		"provider": "lb", "modelId": "m1", "thinkingLevel": "high",
	})
	if result["ok"] != true {
		t.Fatal(result)
	}
	settings := readFileJSON(t, settingsPath)
	if settings["defaultProvider"] != "lb" || settings["defaultModel"] != "m1" || settings["defaultThinkingLevel"] != "high" {
		t.Fatalf("settings = %v", settings)
	}
	if !reflect.DeepEqual(settings["enabledModels"], []any{"lb/m1"}) {
		t.Fatalf("enabledModels = %v", settings["enabledModels"])
	}
}

func TestProviderModelsOp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer dummy-key" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m1","name":"Model 1","context_length":8192,"supports_reasoning":true},{"id":"m2"}]}`)
	}))
	defer server.Close()
	env := testEnv(t)
	result := runOp(t, env, "provider_models", host.Message{
		"baseUrl": server.URL + "/v1", "apiKey": "dummy-key", "api": "openai-completions",
	})
	if result["ok"] != true {
		t.Fatal(result)
	}
	models := asMessages(t, opData(t, result)["models"])
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	first := models[0]
	if first["id"] != "m1" || first["contextWindow"] != float64(8192) || first["reasoning"] != true {
		t.Fatalf("first = %v", first)
	}
	if models[1]["id"] != "m2" {
		t.Fatalf("second = %v", models[1])
	}
	for _, bad := range []string{"ftp://x", "not a url"} {
		if opCode(runOp(t, env, "provider_models", host.Message{"baseUrl": bad, "apiKey": "k", "api": "x"})) != "bad_url" {
			t.Fatalf("%s should be bad_url", bad)
		}
	}
}

func TestSubscriptionModelsOp(t *testing.T) {
	env := testEnv(t)
	if opCode(runOp(t, env, "subscription_models", host.Message{"providerId": "anthropic"})) != "bad_params" {
		t.Fatal("anthropic is not an API-key provider")
	}
	if opCode(runOp(t, env, "subscription_models", host.Message{"providerId": "zai"})) != "no_key" {
		t.Fatal("zai without a saved key should be no_key")
	}
}

func TestSubscriptionSaveOp(t *testing.T) {
	env := testEnv(t)
	settingsPath := filepath.Join(env.ConfigDir, "settings.json")
	writeJSON(t, settingsPath, host.Message{"enabledModels": []any{"zai/existing"}})

	result := runOp(t, env, "subscription_save", host.Message{
		"providerId": "not-builtin", "enabled": []any{}, "models": []any{},
	})
	if opCode(result) != "bad_params" {
		t.Fatal(result)
	}
	result = runOp(t, env, "subscription_save", host.Message{
		"providerId": "zai", "enabled": []any{"unknown-id"}, "models": []any{},
	})
	if opCode(result) != "bad_params" {
		t.Fatal(result)
	}

	// A new model Pi does not ship becomes a models.json extra with shared compat.
	result = runOp(t, env, "subscription_save", host.Message{
		"providerId": "zai",
		"enabled":    []any{"my-new-model"},
		"models":     []any{host.Message{"id": "my-new-model", "reasoning": true, "contextWindow": 16384}},
	})
	if result["ok"] != true {
		t.Fatal(result)
	}
	doc := readFileJSON(t, filepath.Join(env.ConfigDir, "models.json"))
	entry := doc["providers"].(host.Message)["zai"].(host.Message)
	models := entry["models"].([]any)
	if len(models) != 1 || models[0].(host.Message)["id"] != "my-new-model" {
		t.Fatalf("zai entry = %v", entry)
	}
}

func TestLocalModelsSyncOp(t *testing.T) {
	env := testEnv(t)
	settingsPath := filepath.Join(env.ConfigDir, "settings.json")
	writeJSON(t, settingsPath, host.Message{})

	model := host.Message{"id": "local-1", "contextWindow": 16384, "maxTokens": 4096}
	params := host.Message{
		"baseUrl": "http://127.0.0.1:8080/v1", "apiKey": "dummy",
		"models": []any{model},
	}
	result := runOp(t, env, "local_models_sync", params)
	if result["ok"] != true || opData(t, result)["changed"] != true {
		t.Fatal(result)
	}
	doc := readFileJSON(t, filepath.Join(env.ConfigDir, "models.json"))
	entry := doc["providers"].(host.Message)[localProvider].(host.Message)
	if entry["api"] != "openai-completions" || entry["compat"] == nil {
		t.Fatalf("local entry = %v", entry)
	}
	saved := entry["models"].([]any)[0].(host.Message)
	if !reflect.DeepEqual(saved["input"], []any{"text"}) {
		t.Fatalf("input = %v", saved["input"])
	}
	settings := readFileJSON(t, settingsPath)
	overrides := settings["compaction"].(host.Message)["modelOverrides"].(host.Message)
	entry2 := overrides[localProvider+"/local-1"].(host.Message)
	if entry2["reserveTokens"] != float64(4096+piAIContextMargin) || entry2["keepRecentTokens"] != float64(16384/keptShare) {
		t.Fatalf("compaction = %v", entry2)
	}

	// A second identical sync writes nothing.
	result = runOp(t, env, "local_models_sync", params)
	if opData(t, result)["changed"] != false {
		t.Fatalf("second sync = %v", result)
	}

	// Removing all models removes the provider and its compaction overrides.
	result = runOp(t, env, "local_models_sync", host.Message{
		"baseUrl": "http://127.0.0.1:8080/v1", "apiKey": "dummy", "models": []any{},
	})
	if result["ok"] != true || opData(t, result)["changed"] != true {
		t.Fatal(result)
	}
	doc = readFileJSON(t, filepath.Join(env.ConfigDir, "models.json"))
	if _, ok := doc["providers"].(host.Message)[localProvider]; ok {
		t.Fatal("local provider was not removed")
	}
	settings = readFileJSON(t, settingsPath)
	if _, ok := settings["compaction"].(host.Message)["modelOverrides"]; ok {
		t.Fatal("compaction overrides were not removed")
	}
}

func TestLogoutOp(t *testing.T) {
	env := testEnv(t)
	writeJSON(t, filepath.Join(env.ConfigDir, "auth.json"), host.Message{
		"zai": host.Message{"type": "api_key", "key": "dummy-key"},
	})
	data := opData(t, runOp(t, env, "auth_status", host.Message{}))
	for _, item := range data["builtin"].([]any) {
		if entry := item.(host.Message); entry["id"] == "zai" && entry["configured"] != true {
			t.Fatalf("zai not configured before logout: %v", entry)
		}
	}
	if result := runOp(t, env, "logout", host.Message{"providerId": "zai"}); result["ok"] != true {
		t.Fatal(result)
	}
	auth := readFileJSON(t, filepath.Join(env.ConfigDir, "auth.json"))
	if _, ok := auth["zai"]; ok {
		t.Fatalf("credential not deleted: %v", auth)
	}
	if result := runOp(t, env, "logout", host.Message{}); opCode(result) != "bad_params" {
		t.Fatal(result)
	}
}

func TestSubscriptionModelsOpWithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/user" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer dummy-key" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"known-model"},{"id":"new-model","context_length":4096}]}`)
	}))
	defer server.Close()
	env := testEnv(t)
	writeJSON(t, filepath.Join(env.ConfigDir, "auth.json"), host.Message{
		"openrouter": host.Message{"type": "api_key", "key": "dummy-key"},
	})
	writeJSON(t, filepath.Join(env.ConfigDir, "models.json"), host.Message{
		"providers": host.Message{
			"openrouter": host.Message{"baseUrl": server.URL, "api": "openai-completions", "apiKey": noAPIKey},
		},
	})
	result := runOp(t, env, "subscription_models", host.Message{"providerId": "openrouter"})
	if result["ok"] != true {
		t.Fatal(result)
	}
	models := asMessages(t, opData(t, result)["models"])
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		id, _ := model["id"].(string)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"known-model", "new-model"}) {
		t.Fatalf("models = %v", models)
	}
}
