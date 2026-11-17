package modes

import (
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/freovensh0/pigkin/backend/internal/host"
)

const localProvider = "pigkin-local"

var localCompat = host.Message{
	"sendSessionAffinityHeaders": true,
	"sessionAffinityFormat":      "openai-nosession",
	"supportsDeveloperRole":      false,
	"supportsStore":              false,
	"supportsReasoningEffort":    true,
	"supportsUsageInStreaming":   true,
	"maxTokensField":             "max_tokens",
	"thinkingFormat":             "openai",
}

var freeCost = host.Message{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}

const (
	piAIContextMargin = 4096
	keptShare         = 16
)

func subscriptionModelsOp(ctx *Context) (host.Message, error) {
	providerID, err := stringParam(ctx.Params, "providerId")
	if err != nil {
		return nil, err
	}
	if kind, ok := loginType(providerID); !ok || kind != loginAPIKey {
		return nil, host.NewError("bad_params", providerID+" is not a provider Pigkin connects with an API key", nil)
	}
	path, ok := modelListPath(providerID)
	if !ok {
		return host.Message{}, nil
	}
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()

	baseURL := ""
	if provider := runtime.GetProvider(providerID); provider != nil {
		baseURL = provider.BaseURL
	}
	apiKey := ""
	if auth, err := runtime.GetAuth(piContext(ctx), providerID); err == nil && auth != nil {
		apiKey = auth.Auth.APIKey
	}
	if baseURL == "" || apiKey == "" {
		return nil, host.NewError("no_key", "No API key is saved for "+providerID, &host.Text{Key: "subscription_no_key"})
	}
	listed, err := fetchModelList(baseURL, strings.TrimRight(baseURL, "/")+path, apiKey)
	if err != nil {
		return nil, err
	}
	known := map[string]modelDef{}
	for _, model := range runtime.GetModels(providerID) {
		known[model.ID] = modelDefFromPi(model)
	}
	cat, err := catalogue(ctx, "openai-completions")
	if err != nil {
		return nil, err
	}
	models := make([]host.Message, 0, len(listed))
	for id, detected := range listed {
		if base, ok := known[id]; ok {
			models = append(models, base.object())
			continue
		}
		if fallback, ok := cat[id]; ok {
			detected = fillMissing(detected, fallback)
		}
		models = append(models, detected.object())
	}
	return host.Message{"models": models}, nil
}

func defaultModelOp(ctx *Context) (host.Message, error) {
	provider, providerSet, err := nullableStringParam(ctx.Params, "provider")
	if err != nil {
		return nil, err
	}
	modelID, modelSet, err := nullableStringParam(ctx.Params, "modelId")
	if err != nil {
		return nil, err
	}
	thinkingLevel, levelSet, err := nullableStringParam(ctx.Params, "thinkingLevel")
	if err != nil {
		return nil, err
	}
	if providerSet != modelSet || (providerSet && (provider == "" || modelID == "")) {
		return nil, host.NewError("bad_params", "provider and modelId go together", nil)
	}
	if levelSet && !isThinkingLevel(thinkingLevel) {
		return nil, host.NewError("bad_params", "Unknown thinking level "+thinkingLevel, nil)
	}
	if !providerSet && !levelSet {
		return nil, host.NewError("bad_params", "Nothing to save", nil)
	}
	settings, err := readSettings(ctx.Env.ConfigDir)
	if err != nil {
		return nil, err
	}
	if providerSet {
		settings["defaultProvider"] = provider
		settings["defaultModel"] = modelID
	}
	if levelSet {
		settings["defaultThinkingLevel"] = thinkingLevel
	}
	return host.Message{}, writeSettings(ctx.Env.ConfigDir, settings)
}

func hasEfforts(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for level, effort := range object {
		if level != "off" && effort != nil {
			return true
		}
	}
	return false
}

func compatObject(compat *ai.ModelCompat) (host.Message, bool) {
	if compat == nil {
		return nil, false
	}
	data, err := json.Marshal(compat)
	if err != nil {
		return nil, false
	}
	var object host.Message
	if json.Unmarshal(data, &object) != nil {
		return nil, false
	}
	return object, true
}

func sharedCompat(builtin []*ai.Model, def modelDef) host.Message {
	thinks := hasEfforts(def.ThinkingLevelMap)
	compats := []host.Message{}
	for _, model := range builtin {
		if string(model.ProviderMeta.API) != "openai-completions" {
			continue
		}
		object, ok := compatObject(model.ProviderMeta.Compat)
		if !ok {
			object = host.Message{}
		}
		mapValue := any(model.ThinkingLevelMap)
		if hasEfforts(mapValue) == thinks {
			compats = append(compats, object)
		}
	}
	if len(compats) == 0 {
		return nil
	}
	shared := host.Message{}
	for key, value := range compats[0] {
		all := true
		for _, other := range compats[1:] {
			if !jsEqual(value, other[key]) {
				all = false
				break
			}
		}
		if all {
			shared[key] = value
		}
	}
	if len(shared) == 0 {
		return nil
	}
	return shared
}

func modelOverride(def, base modelDef, stored host.Message) host.Message {
	result := withoutManagedFields(stored)
	defObject, baseObject := def.object(), base.object()
	for _, field := range managedFields {
		value, ok := defObject[field]
		if ok && !jsEqual(value, baseObject[field]) {
			result[field] = value
		}
	}
	if tiers := storedCostTiers(stored); tiers != nil {
		cost, _ := result["cost"].(map[string]any)
		if cost == nil {
			cost = host.Message{}
		}
		cost["tiers"] = tiers
		result["cost"] = cost
	}
	return result
}

func subscriptionSaveOp(ctx *Context) (host.Message, error) {
	providerID, err := stringParam(ctx.Params, "providerId")
	if err != nil {
		return nil, err
	}
	inBuiltin := false
	for _, id := range builtinProviders {
		if id == providerID {
			inBuiltin = true
		}
	}
	if !inBuiltin {
		return nil, host.NewError("bad_params", providerID+" is not a provider Pigkin connects", nil)
	}
	enabled, err := stringArrayParam(ctx.Params, "enabled")
	if err != nil {
		return nil, err
	}
	models, err := modelDefsParam(ctx.Params, "models")
	if err != nil {
		return nil, err
	}

	doc, err := readModelsJSON(ctx.Env.ConfigDir)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(ctx.Env.ConfigDir)
	if err != nil {
		return nil, err
	}
	patterns, err := enabledModels(settings)
	if err != nil {
		return nil, err
	}
	builtinRuntime, err := createRuntime(ctx, true)
	if err != nil {
		return nil, err
	}
	builtinModels := builtinRuntime.GetModels(providerID)
	builtinRuntime.Close()
	catalogueDefs := map[string]modelDef{}
	for _, model := range builtinModels {
		catalogueDefs[model.ID] = modelDefFromPi(model)
	}

	before := doc.providers[providerID]
	entry := cloneMessage(beforeAsObject(before))
	storedModels := objectsById(entry["models"])
	overrides, _ := entry["modelOverrides"].(map[string]any)
	if overrides == nil {
		overrides = host.Message{}
	}
	extras := []any{}
	ids := map[string]bool{}
	for id := range catalogueDefs {
		ids[id] = true
	}
	for _, def := range models {
		base, hasBase := catalogueDefs[def.ID]
		stored, _ := overrides[def.ID].(map[string]any)
		var next host.Message
		if !hasBase || storedModels[def.ID] != nil {
			var compat host.Message
			if storedModels[def.ID] == nil {
				compat = sharedCompat(builtinModels, def)
			}
			var existing host.Message
			if storedModels[def.ID] != nil {
				existing = storedModels[def.ID]
			} else if compat != nil {
				existing = host.Message{"compat": compat}
			}
			extras = append(extras, mergeManagedFields(def, existing))
			ids[def.ID] = true
			next = withoutManagedFields(stored)
		} else {
			next = modelOverride(def, base, stored)
		}
		if len(next) > 0 {
			overrides[def.ID] = next
		} else {
			delete(overrides, def.ID)
		}
	}
	for _, id := range enabled {
		if !ids[id] {
			return nil, host.NewError("bad_params", `"`+id+`" is not a `+providerID+` model`, nil)
		}
	}

	if len(extras) > 0 {
		entry["models"] = extras
	} else {
		delete(entry, "models")
	}
	if len(overrides) > 0 {
		entry["modelOverrides"] = overrides
	} else {
		delete(entry, "modelOverrides")
	}
	if len(entry) > 0 {
		doc.providers[providerID] = entry
	} else {
		delete(doc.providers, providerID)
	}
	if !jsEqual(before, doc.providers[providerID]) {
		if err := doc.write(ctx.Env.ConfigDir); err != nil {
			return nil, err
		}
	}

	all := true
	for id := range ids {
		found := false
		for _, candidate := range enabled {
			if id == candidate {
				found = true
			}
		}
		if !found {
			all = false
		}
	}
	if len(patterns) == 0 && all {
		return host.Message{}, nil
	}
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	universe, err := scopeUniverse(runtime)
	if err != nil {
		return nil, err
	}
	enabledValue := []string(enabled)
	var target []string
	if all {
		target = nil
	} else {
		target = enabledValue
	}
	rebuilt := rebuildForProvider(patterns, providerID, target, universe)
	rebuilt = putDefaultFirst(rebuilt, defaultRef(settings), universe)
	setEnabledModels(settings, rebuilt)
	return host.Message{}, writeSettings(ctx.Env.ConfigDir, settings)
}

func beforeAsObject(value any) host.Message {
	if object, ok := value.(map[string]any); ok {
		return cloneMessage(object)
	}
	return host.Message{}
}

func localModelsSyncOp(ctx *Context) (host.Message, error) {
	baseURL, err := stringParam(ctx.Params, "baseUrl")
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

	before, _ := json.Marshal(doc.providers[localProvider])
	if len(models) == 0 {
		delete(doc.providers, localProvider)
	} else {
		entry := host.Message{
			"baseUrl": baseURL,
			"api":     "openai-completions",
			"apiKey":  apiKey,
			"compat":  localCompat,
		}
		savedModels := make([]any, 0, len(models))
		for _, model := range models {
			object := model.object()
			object["input"] = []string{"text"}
			object["cost"] = freeCost
			savedModels = append(savedModels, object)
		}
		entry["models"] = savedModels
		doc.providers[localProvider] = entry
	}
	providerChanged := false
	after, _ := json.Marshal(doc.providers[localProvider])
	if string(before) != string(after) {
		providerChanged = true
		if err := doc.write(ctx.Env.ConfigDir); err != nil {
			return nil, err
		}
	}
	if !hasSettings {
		return host.Message{"changed": providerChanged}, nil
	}

	scopeChanged, err := saveLocalScope(ctx, settings, patterns, models)
	if err != nil {
		return nil, err
	}
	compactionChanged, err := saveLocalCompaction(ctx, settings, models)
	if err != nil {
		return nil, err
	}
	return host.Message{"changed": providerChanged || scopeChanged || compactionChanged}, nil
}

func saveLocalScope(ctx *Context, settings host.Message, patterns []string, models []modelDef) (bool, error) {
	if len(patterns) == 0 {
		return false, nil
	}
	if len(models) == 0 {
		kept := dropProvider(patterns, localProvider)
		if len(kept) == len(patterns) {
			return false, nil
		}
		setEnabledModels(settings, kept)
		return true, writeSettings(ctx.Env.ConfigDir, settings)
	}
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return false, err
	}
	defer runtime.Close()
	universe, err := scopeUniverse(runtime)
	if err != nil {
		return false, err
	}
	scopeModels := make([]scopeModel, 0, len(models))
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		scopeModels = append(scopeModels, scopeModel{Provider: localProvider, ID: model.ID, Name: name})
	}
	universe = replaceProviderModels(universe, localProvider, scopeModels)
	changed := keepProviderInScope(patterns, localProvider, defaultRef(settings), universe)
	if changed == nil {
		return false, nil
	}
	setEnabledModels(settings, changed)
	return true, writeSettings(ctx.Env.ConfigDir, settings)
}

func saveLocalCompaction(ctx *Context, settings host.Message, models []modelDef) (bool, error) {
	compaction, _ := settings["compaction"].(map[string]any)
	current, _ := compaction["modelOverrides"].(map[string]any)
	overrides := host.Message{}
	prefix := localProvider + "/"
	for key, value := range current {
		if !strings.HasPrefix(key, prefix) {
			overrides[key] = value
		}
	}
	for _, model := range models {
		if model.ContextWindow == 0 || model.MaxTokens == 0 {
			continue
		}
		overrides[prefix+model.ID] = host.Message{
			"reserveTokens":    model.MaxTokens + piAIContextMargin,
			"keepRecentTokens": int(model.ContextWindow / keptShare),
		}
	}
	var next any
	if len(overrides) > 0 {
		next = overrides
	}
	if jsEqual(next, current) {
		return false, nil
	}
	if compaction == nil {
		compaction = host.Message{}
		settings["compaction"] = compaction
	}
	if next == nil {
		delete(compaction, "modelOverrides")
	} else {
		compaction["modelOverrides"] = next
	}
	return true, writeSettings(ctx.Env.ConfigDir, settings)
}
