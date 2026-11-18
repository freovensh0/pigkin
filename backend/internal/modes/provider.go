package modes

import (
	"net/url"
	"regexp"
	"sort"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

var supportedAPIs = map[string]bool{
	"openai-completions": true,
	"openai-responses":   true,
}

func providerModelsOp(ctx *Context) (host.Message, error) {
	baseURL, err := stringParam(ctx.Params, "baseUrl")
	if err != nil {
		return nil, err
	}
	apiKey, err := stringParam(ctx.Params, "apiKey")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		apiKey = noAPIKey
	}
	api, err := stringParam(ctx.Params, "api")
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, host.NewError("bad_url", baseURL+" is not an http:// or https:// URL", nil)
	}
	endpoint, err := endpointURL(baseURL, "/models")
	if err != nil {
		return nil, host.NewError("bad_url", baseURL+" is not an http:// or https:// URL", nil)
	}
	detected, err := fetchModelList(baseURL, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	cat, err := catalogue(ctx, api)
	if err != nil {
		return nil, err
	}
	result := make([]host.Message, 0, len(detected))
	for id, model := range detected {
		if fallback, ok := cat[id]; ok {
			model = fillMissing(model, fallback)
		}
		result = append(result, model.object())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["id"].(string) < result[j]["id"].(string)
	})
	return host.Message{"models": result}, nil
}

func modelDefaultsOp(ctx *Context) (host.Message, error) {
	ids, err := stringArrayParam(ctx.Params, "ids")
	if err != nil {
		return nil, err
	}
	api, err := stringParam(ctx.Params, "api")
	if err != nil {
		return nil, err
	}
	cat, err := catalogue(ctx, api)
	if err != nil {
		return nil, err
	}
	models := make([]host.Message, 0, len(ids))
	for _, id := range ids {
		if def, ok := cat[id]; ok {
			models = append(models, def.object())
		} else {
			models = append(models, host.Message{"id": id})
		}
	}
	return host.Message{"models": models}, nil
}

func providerSaveOp(ctx *Context) (host.Message, error) {
	id, err := stringParam(ctx.Params, "id")
	if err != nil {
		return nil, err
	}
	baseURL, err := stringParam(ctx.Params, "baseUrl")
	if err != nil {
		return nil, err
	}
	api, err := stringParam(ctx.Params, "api")
	if err != nil {
		return nil, err
	}
	apiKey, err := stringParam(ctx.Params, "apiKey")
	if err != nil {
		return nil, err
	}
	models, err := modelDefsParam(ctx.Params, "models")
	if err != nil {
		return nil, err
	}
	if !providerIDPattern.MatchString(id) {
		return nil, host.NewError("bad_id", "Use 1–40 lowercase letters, digits or dashes, starting with a letter or digit",
			&host.Text{Key: "provider_id_invalid"})
	}
	if !supportedAPIs[api] {
		return nil, host.NewError("bad_params", "Unsupported API type "+api, nil)
	}

	doc, err := readModelsJSON(ctx.Env.ConfigDir)
	if err != nil {
		return nil, err
	}
	builtin, err := createRuntime(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, provider := range builtin.GetProviders() {
		if provider.ID == id {
			builtin.Close()
			return nil, host.NewError("builtin_id", `"`+id+`" is a built-in provider; choose another ID`,
				&host.Text{Key: "provider_id_builtin", Args: host.Message{"id": id}})
		}
	}
	builtin.Close()

	settings, settingsErr := readSettings(ctx.Env.ConfigDir)
	var patterns []string
	hasSettings := settingsErr == nil
	if hasSettings {
		patterns, err = enabledModels(settings)
		if err != nil {
			hasSettings = false
		}
	}

	existing, _ := doc.providers[id].(map[string]any)
	existingModels := objectsById(existing["models"])
	entry := cloneMessage(existing)
	entry["baseUrl"] = baseURL
	entry["api"] = api
	if apiKey == "" {
		entry["apiKey"] = noAPIKey
	} else {
		entry["apiKey"] = apiKey
	}
	saved := make([]any, 0, len(models))
	for _, model := range models {
		saved = append(saved, mergeManagedFields(model, existingModels[model.ID]))
	}
	entry["models"] = saved
	doc.providers[id] = entry
	if err := doc.write(ctx.Env.ConfigDir); err != nil {
		return nil, err
	}

	if hasSettings && len(patterns) > 0 {
		runtime, err := createRuntime(ctx, false)
		if err != nil {
			return nil, err
		}
		defer runtime.Close()
		universe, err := scopeUniverse(runtime)
		if err != nil {
			return nil, err
		}
		scopeModels := make([]scopeModel, 0, len(models))
		for _, model := range models {
			name := model.Name
			if name == "" {
				name = model.ID
			}
			scopeModels = append(scopeModels, scopeModel{Provider: id, ID: model.ID, Name: name})
		}
		universe = replaceProviderModels(universe, id, scopeModels)
		changed := keepProviderInScope(patterns, id, defaultRef(settings), universe)
		if changed != nil {
			setEnabledModels(settings, changed)
			if err := writeSettings(ctx.Env.ConfigDir, settings); err != nil {
				return nil, err
			}
		}
	}
	return host.Message{}, nil
}

func providerRemoveOp(ctx *Context) (host.Message, error) {
	id, err := stringParam(ctx.Params, "id")
	if err != nil {
		return nil, err
	}
	doc, err := readModelsJSON(ctx.Env.ConfigDir)
	if err != nil {
		return nil, err
	}
	settings, settingsErr := readSettings(ctx.Env.ConfigDir)
	hasSettings := settingsErr == nil
	var patterns []string
	if hasSettings {
		patterns, err = enabledModels(settings)
		if err != nil {
			hasSettings = false
		}
	}
	if _, ok := doc.providers[id]; ok {
		delete(doc.providers, id)
		if err := doc.write(ctx.Env.ConfigDir); err != nil {
			return nil, err
		}
	}
	if hasSettings {
		kept := dropProvider(patterns, id)
		if len(kept) != len(patterns) {
			setEnabledModels(settings, kept)
			if err := writeSettings(ctx.Env.ConfigDir, settings); err != nil {
				return nil, err
			}
		}
	}
	return host.Message{}, nil
}
