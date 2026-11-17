package modes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/freovensh0/pigkin/backend/internal/host"
)

// The provider ids Pigkin connects, and how. Mirrors host/src/runtime.ts.
const (
	loginOAuth  = "oauth"
	loginAPIKey = "api_key"
)

var loginTypes = map[string]string{
	"anthropic":    loginOAuth,
	"openai-codex": loginOAuth,
	"google":       loginAPIKey,
	"zai":          loginAPIKey,
	"kimi-coding":  loginAPIKey,
	"openrouter":   loginAPIKey,
}

var builtinProviders = []string{"anthropic", "openai-codex", "google", "zai", "kimi-coding", "openrouter"}

var modelListPaths = map[string]string{"zai": "/models", "kimi-coding": "/models", "openrouter": "/models/user"}

const noAPIKey = "none"

func loginType(providerID string) (string, bool) {
	kind, ok := loginTypes[providerID]
	return kind, ok
}

func modelListPath(providerID string) (string, bool) {
	path, ok := modelListPaths[providerID]
	return path, ok
}

func hiddenUntilPicked(providerID string) bool {
	_, ok := modelListPaths[providerID]
	return ok
}

// configurePi makes PiG read Pi's config directories from the host's environment.
// PiG defaults to its own PIG_* variables; PIG_USE_PI_DIRS selects Pi's PI_* ones.
func configurePi(ctx *Context) {
	_ = os.Setenv("PIG_USE_PI_DIRS", "1")
	_ = os.Setenv("PI_CODING_AGENT_DIR", ctx.Env.ConfigDir)
}

func piContext(ctx *Context) context.Context {
	return context.Background()
}

// createRuntime opens Pi's model/auth runtime over PI_CODING_AGENT_DIR.
// builtinOnly skips models.json, leaving Pi's built-in providers and catalogue.
func createRuntime(ctx *Context, builtinOnly bool) (*coding.ModelRuntime, error) {
	configurePi(ctx)
	options := coding.CreateModelRuntimeOptions{
		AuthPath:        filepath.Join(ctx.Env.ConfigDir, "auth.json"),
		RefreshOnCreate: new(bool),
	}
	if builtinOnly {
		var disabled *string
		options.ModelsPath = &disabled
	} else {
		path := filepath.Join(ctx.Env.ConfigDir, "models.json")
		enabled := &path
		options.ModelsPath = &enabled
	}
	options.ModelsStorePath = filepath.Join(ctx.Env.ConfigDir, "models-store.json")
	return coding.CreateModelRuntime(piContext(ctx), options)
}

// newSettingsManager returns Pi's file-backed settings manager over the host's config dir.
func newSettingsManager(ctx *Context) (*coding.AgentSessionServices, *coding.SettingsManager, error) {
	configurePi(ctx)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
		CWD:      ctx.Env.Home,
		AgentDir: ctx.Env.ConfigDir,
	})
	if err != nil {
		return nil, nil, err
	}
	return services, services.SettingsManager(), nil
}

// modelDef is the per-model settings Pigkin reads and writes, in Pi's models.json shape.
type modelDef struct {
	ID               string             `json:"id"`
	Name             string             `json:"name,omitempty"`
	Reasoning        *bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	Input            []string           `json:"input,omitempty"`
	ContextWindow    float64            `json:"contextWindow,omitempty"`
	MaxTokens        float64            `json:"maxTokens,omitempty"`
	Cost             *modelCost         `json:"cost,omitempty"`
	// extra keeps fields Pigkin does not own (compat, headers, cost tiers, ...).
	extra host.Message
}

type modelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Tiers      any     `json:"tiers,omitempty"`
}

var managedFields = []string{"name", "reasoning", "thinkingLevelMap", "input", "contextWindow", "maxTokens", "cost"}

var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func isThinkingLevel(value string) bool {
	for _, level := range thinkingLevels {
		if value == level {
			return true
		}
	}
	return false
}

// modelDefFromPi projects a Pi runtime model onto Pigkin's model settings.
func modelDefFromPi(model *ai.Model) modelDef {
	def := modelDef{ID: model.ID}
	if model.DisplayName != "" {
		def.Name = model.DisplayName
	}
	if model.ProviderMeta.Reasoning {
		reasoning := true
		def.Reasoning = &reasoning
	}
	if len(model.ThinkingLevelMap) > 0 {
		levels := map[string]*string{}
		for level, value := range model.ThinkingLevelMap {
			levels[string(level)] = value
		}
		def.ThinkingLevelMap = levels
	}
	def.Input = append([]string(nil), model.Input...)
	caps := model.Capabilities
	if caps.ContextWindow > 0 {
		def.ContextWindow = float64(caps.ContextWindow)
	}
	if caps.MaxOutputTokens > 0 {
		def.MaxTokens = float64(caps.MaxOutputTokens)
	}
	if caps.InputCostPer1M != 0 || caps.OutputCostPer1M != 0 || caps.CacheReadCostPer1M != 0 || caps.CacheWriteCostPer1M != 0 {
		def.Cost = &modelCost{
			Input:      caps.InputCostPer1M,
			Output:     caps.OutputCostPer1M,
			CacheRead:  caps.CacheReadCostPer1M,
			CacheWrite: caps.CacheWriteCostPer1M,
			Tiers:      costTiers(caps.CostTiers),
		}
	}
	return def
}

func costTiers(tiers []ai.CostTier) any {
	if len(tiers) == 0 {
		return nil
	}
	values := make([]any, 0, len(tiers))
	for _, tier := range tiers {
		values = append(values, host.Message{
			"inputTokensAbove": tier.InputTokensAbove,
			"input":            tier.InputCostPer1M,
			"output":           tier.OutputCostPer1M,
			"cacheRead":        tier.CacheReadCostPer1M,
			"cacheWrite":       tier.CacheWriteCostPer1M,
		})
	}
	return values
}

func (d modelDef) object() host.Message {
	object := host.Message{}
	for key, value := range d.extra {
		object[key] = value
	}
	object["id"] = d.ID
	if d.Name != "" {
		object["name"] = d.Name
	}
	if d.Reasoning != nil {
		object["reasoning"] = *d.Reasoning
	}
	if len(d.ThinkingLevelMap) > 0 {
		object["thinkingLevelMap"] = d.ThinkingLevelMap
	}
	if len(d.Input) > 0 {
		object["input"] = d.Input
	}
	if d.ContextWindow > 0 {
		object["contextWindow"] = d.ContextWindow
	}
	if d.MaxTokens > 0 {
		object["maxTokens"] = d.MaxTokens
	}
	if d.Cost != nil {
		cost := host.Message{"input": d.Cost.Input, "output": d.Cost.Output, "cacheRead": d.Cost.CacheRead, "cacheWrite": d.Cost.CacheWrite}
		if d.Cost.Tiers != nil {
			cost["tiers"] = d.Cost.Tiers
		}
		object["cost"] = cost
	}
	return object
}

// catalogue looks model ids up in Pi's built-in catalogue, preferring the
// requested api then a few well-known providers. Mirrors host/src/catalogue.ts.
func catalogue(ctx *Context, api string) (map[string]modelDef, error) {
	runtime, err := createRuntime(ctx, true)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()

	preferred := map[string]int{"openai": 0, "openai-codex": 1, "anthropic": 2, "google": 3}
	best := map[string]struct {
		rank int
		def  modelDef
	}{}
	for _, model := range runtime.GetModels() {
		rank := 100
		if p, ok := preferred[model.ProviderMeta.ProviderID]; ok {
			rank = p
		}
		if string(model.ProviderMeta.API) == api {
			rank -= 100
		}
		def := modelDefFromPi(model)
		if current, ok := best[model.ID]; !ok || rank < current.rank {
			best[model.ID] = struct {
				rank int
				def  modelDef
			}{rank, def}
		}
	}
	result := make(map[string]modelDef, len(best))
	for id, entry := range best {
		result[id] = entry.def
	}
	return result, nil
}

func jsEqual(a, b any) bool {
	left, lerr := json.Marshal(a)
	right, rerr := json.Marshal(b)
	return lerr == nil && rerr == nil && string(left) == string(right)
}

func lower(value string) string { return strings.ToLower(value) }
