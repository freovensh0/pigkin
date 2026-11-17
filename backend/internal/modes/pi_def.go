package modes

import (
	"math"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func toFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	}
	return 0, false
}

func positiveNumber(value any) (float64, bool) {
	number, ok := toFloat(value)
	return number, ok && !math.IsInf(number, 0) && !math.IsNaN(number) && number > 0
}

func rate(value any) (float64, bool) {
	number, ok := toFloat(value)
	return number, ok && !math.IsInf(number, 0) && !math.IsNaN(number) && number >= 0
}

func parseThinkingLevelMap(value any) (map[string]*string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	result := map[string]*string{}
	for key, level := range object {
		if !isThinkingLevel(key) {
			return nil, false
		}
		if level == nil {
			result[key] = nil
			continue
		}
		text, ok := level.(string)
		if !ok {
			return nil, false
		}
		result[key] = &text
	}
	return result, true
}

func parseInput(value any) ([]string, bool) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	image := false
	for _, item := range list {
		text, ok := item.(string)
		if !ok || (text != "text" && text != "image") {
			return nil, false
		}
		if text == "image" {
			image = true
		}
	}
	if image {
		return []string{"text", "image"}, true
	}
	return []string{"text"}, true
}

func parseCost(value any) (*modelCost, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	input, ok1 := rate(object["input"])
	output, ok2 := rate(object["output"])
	cacheRead, ok3 := rate(object["cacheRead"])
	cacheWrite, ok4 := rate(object["cacheWrite"])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, false
	}
	return &modelCost{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite}, true
}

// parseModelDef reads the managed fields of a stored model, skipping fields
// with the wrong type and reporting them through onInvalid.
func parseModelDef(id string, source host.Message, onInvalid func(string)) modelDef {
	def := modelDef{ID: id, extra: cloneMessage(source)}
	invalid := func(field string) {
		if onInvalid != nil {
			onInvalid(field)
		}
	}
	if value, ok := source["name"]; ok && value != nil {
		if text, ok := value.(string); ok && text != "" {
			def.Name = text
		} else {
			invalid("name")
		}
	}
	if value, ok := source["reasoning"]; ok && value != nil {
		if flag, ok := value.(bool); ok {
			def.Reasoning = &flag
		} else {
			invalid("reasoning")
		}
	}
	if value, ok := source["thinkingLevelMap"]; ok && value != nil {
		if parsed, ok := parseThinkingLevelMap(value); ok {
			def.ThinkingLevelMap = parsed
		} else {
			invalid("thinkingLevelMap")
		}
	}
	if value, ok := source["input"]; ok && value != nil {
		if parsed, ok := parseInput(value); ok {
			def.Input = parsed
		} else {
			invalid("input")
		}
	}
	if value, ok := source["contextWindow"]; ok && value != nil {
		if parsed, ok := positiveNumber(value); ok {
			def.ContextWindow = parsed
		} else {
			invalid("contextWindow")
		}
	}
	if value, ok := source["maxTokens"]; ok && value != nil {
		if parsed, ok := positiveNumber(value); ok {
			def.MaxTokens = parsed
		} else {
			invalid("maxTokens")
		}
	}
	if value, ok := source["cost"]; ok && value != nil {
		if parsed, ok := parseCost(value); ok {
			def.Cost = parsed
		} else {
			invalid("cost")
		}
	}
	return def
}

// fillMissing fills every managed field primary lacks from fallback.
func fillMissing(primary, fallback modelDef) modelDef {
	if fallback.Name != "" && primary.Name == "" {
		primary.Name = fallback.Name
	}
	if fallback.Reasoning != nil && primary.Reasoning == nil {
		primary.Reasoning = fallback.Reasoning
	}
	if len(fallback.ThinkingLevelMap) > 0 && len(primary.ThinkingLevelMap) == 0 {
		primary.ThinkingLevelMap = fallback.ThinkingLevelMap
	}
	if len(fallback.Input) > 0 && len(primary.Input) == 0 {
		primary.Input = fallback.Input
	}
	if fallback.ContextWindow > 0 && primary.ContextWindow == 0 {
		primary.ContextWindow = fallback.ContextWindow
	}
	if fallback.MaxTokens > 0 && primary.MaxTokens == 0 {
		primary.MaxTokens = fallback.MaxTokens
	}
	if fallback.Cost != nil && primary.Cost == nil {
		primary.Cost = fallback.Cost
	}
	return primary
}

// modelDefsParam parses a strict ModelDef[] request parameter.
func modelDefsParam(params host.Message, key string) ([]modelDef, error) {
	values, ok := params[key].([]any)
	if !ok {
		return nil, host.NewError("bad_params", `Parameter "`+key+`" must be an array of models`, nil)
	}
	seen := map[string]bool{}
	result := make([]modelDef, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		id, hasID := object["id"].(string)
		if !ok || !hasID || id == "" {
			return nil, host.NewError("bad_params", "Every model needs a non-empty string id", nil)
		}
		if seen[id] {
			return nil, host.NewError("bad_params", `Model "`+id+`" is listed twice`, nil)
		}
		seen[id] = true
		var invalidField string
		def := parseModelDef(id, object, func(field string) {
			if invalidField == "" {
				invalidField = field
			}
		})
		if invalidField != "" {
			return nil, host.NewError("bad_params", `Model "`+id+`" has an invalid `+invalidField, nil)
		}
		result = append(result, def)
	}
	return result, nil
}

// objectsById indexes the object entries of a models.json model list by id.
func objectsById(list any) map[string]host.Message {
	result := map[string]host.Message{}
	values, ok := list.([]any)
	if !ok {
		return result
	}
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			if id, ok := object["id"].(string); ok {
				result[id] = object
			}
		}
	}
	return result
}

// withoutManagedFields copies a stored model without the fields Pigkin owns.
func withoutManagedFields(stored host.Message) host.Message {
	result := cloneMessage(stored)
	for _, field := range managedFields {
		delete(result, field)
	}
	return result
}

func storedCostTiers(stored host.Message) any {
	if cost, ok := stored["cost"].(map[string]any); ok {
		return cost["tiers"]
	}
	return nil
}

// mergeManagedFields gives Pigkin's managed fields precedence while keeping
// everything the agent or user put on the stored model.
func mergeManagedFields(def modelDef, stored host.Message) host.Message {
	merged := withoutManagedFields(stored)
	for key, value := range def.object() {
		merged[key] = value
	}
	if def.Cost != nil {
		if tiers := storedCostTiers(stored); tiers != nil {
			if cost, ok := merged["cost"].(map[string]any); ok {
				cost["tiers"] = tiers
			}
		}
	}
	return merged
}

func cloneMessage(source host.Message) host.Message {
	result := make(host.Message, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
