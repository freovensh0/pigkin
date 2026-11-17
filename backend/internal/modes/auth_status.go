package modes

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/freovensh0/pigkin/backend/internal/host"
)

var npmSource = regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@.+)?$`)

// userNpmPackages lists the npm package names in the user's global Pi settings.
func userNpmPackages(settings host.Message) []string {
	result := []string{}
	packages, ok := settings["packages"].([]any)
	if !ok {
		return result
	}
	seen := map[string]bool{}
	for _, value := range packages {
		source, ok := value.(string)
		if !ok {
			if object, ok := value.(map[string]any); ok {
				source, _ = object["source"].(string)
			}
		}
		if !strings.HasPrefix(source, "npm:") {
			continue
		}
		spec := strings.TrimSpace(strings.TrimPrefix(source, "npm:"))
		name := spec
		if match := npmSource.FindStringSubmatch(spec); len(match) > 1 {
			name = match[1]
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func i18nOf(err error) *host.Text {
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		return hostErr.I18n
	}
	return nil
}

func authStatusOp(ctx *Context) (host.Message, error) {
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	builtinRuntime, err := createRuntime(ctx, true)
	if err != nil {
		return nil, err
	}
	defer builtinRuntime.Close()

	builtinIDs := map[string]bool{}
	for _, provider := range builtinRuntime.GetProviders() {
		builtinIDs[provider.ID] = true
	}

	var providers host.Message
	var modelsError string
	var modelsErrorI18n *host.Text
	if text := runtime.GetError(); text != "" {
		modelsError = text
	}
	doc, err := readModelsJSON(ctx.Env.ConfigDir)
	if err != nil {
		modelsError = err.Error()
		modelsErrorI18n = i18nOf(err)
	} else {
		providers = doc.providers
	}

	var scope []scopeRef
	var hasScope bool
	var settingsError string
	var settingsErrorI18n *host.Text
	settings, err := readSettings(ctx.Env.ConfigDir)
	if err != nil {
		settingsError, settingsErrorI18n = err.Error(), i18nOf(err)
	} else {
		patterns, err := enabledModels(settings)
		if err != nil {
			settingsError, settingsErrorI18n = err.Error(), i18nOf(err)
		} else if len(patterns) > 0 {
			universe, err := scopeUniverse(runtime)
			if err != nil {
				settingsError = err.Error()
			} else {
				scope, hasScope = resolveScope(patterns, universe, false), true
			}
		}
	}

	builtin := []any{}
	for _, id := range builtinProviders {
		var check *ai.AuthCheck
		check, _ = runtime.CheckAuth(piContext(ctx), id)
		name := id
		if provider := runtime.GetProvider(id); provider != nil && provider.Name != "" {
			name = provider.Name
		}
		models := []any{}
		for _, model := range runtime.GetModels(id) {
			models = append(models, modelDefFromPi(model).object())
		}
		extraIDs := []string{}
		if entry, ok := providers[id].(map[string]any); ok {
			for modelID := range objectsById(entry["models"]) {
				extraIDs = append(extraIDs, modelID)
			}
		}
		sort.Strings(extraIDs)
		var enabled any
		if hasScope {
			ids := []string{}
			for _, ref := range scope {
				if ref.Provider == id {
					ids = append(ids, ref.ID)
				}
			}
			enabled = ids
		}
		var checkType any
		if check != nil {
			checkType = string(check.Type)
		}
		builtin = append(builtin, host.Message{
			"id":         id,
			"name":       name,
			"configured": check != nil,
			"type":       checkType,
			"models":     models,
			"extraIds":   extraIDs,
			"enabled":    enabled,
		})
	}

	custom := []any{}
	customIDs := []string{}
	for id := range providers {
		if !builtinIDs[id] {
			customIDs = append(customIDs, id)
		}
	}
	sort.Strings(customIDs)
	for _, id := range customIDs {
		value := providers[id]
		if builtinIDs[id] {
			continue
		}
		provider, ok := value.(map[string]any)
		if !ok {
			continue
		}
		apiKey, _ := provider["apiKey"].(string)
		if apiKey == noAPIKey {
			apiKey = ""
		}
		models := []any{}
		if list, ok := provider["models"].([]any); ok {
			for _, value := range list {
				if modelID, ok := value.(string); ok {
					models = append(models, host.Message{"id": modelID})
					continue
				}
				if object, ok := value.(map[string]any); ok {
					if modelID, ok := object["id"].(string); ok {
						models = append(models, parseModelDef(modelID, object, nil).object())
					}
				}
			}
		}
		custom = append(custom, host.Message{
			"id":      id,
			"baseUrl": provider["baseUrl"],
			"api":     provider["api"],
			"apiKey":  apiKey,
			"models":  models,
		})
	}

	var scopeValue any
	if hasScope {
		values := []string{}
		for _, ref := range scope {
			values = append(values, ref.pattern())
		}
		scopeValue = values
	}

	result := host.Message{
		"builtin":     builtin,
		"custom":      custom,
		"scope":       scopeValue,
		"npmPackages": userNpmPackages(settings),
	}
	if modelsError != "" {
		result["modelsError"] = modelsError
		if modelsErrorI18n != nil {
			result["modelsErrorI18n"] = modelsErrorI18n
		}
	}
	if settingsError != "" {
		result["settingsError"] = settingsError
		if settingsErrorI18n != nil {
			result["settingsErrorI18n"] = settingsErrorI18n
		}
	}
	return result, nil
}
