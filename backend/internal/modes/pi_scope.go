package modes

import (
	"regexp"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

const noModels = "_pigkin/no-models"

type scopeModel struct {
	Provider string
	ID       string
	Name     string
	Unusable bool
}

type scopeRef struct {
	Provider      string
	ID            string
	ThinkingLevel string
}

func (r scopeRef) pattern() string {
	if r.ThinkingLevel == "" {
		return r.Provider + "/" + r.ID
	}
	return r.Provider + "/" + r.ID + ":" + r.ThinkingLevel
}

// scopeUniverse lists the models Pi can use now plus the models of the
// providers Pigkin connects that need signing in.
func scopeUniverse(runtime *coding.ModelRuntime) ([]scopeModel, error) {
	available, err := runtime.GetAvailable(piContext(&Context{}))
	if err != nil {
		return nil, err
	}
	usable := map[string]bool{}
	for _, model := range available {
		usable[model.ProviderMeta.ProviderID+"/"+model.ID] = true
	}
	var universe []scopeModel
	for _, model := range runtime.GetModels() {
		ref := model.ProviderMeta.ProviderID + "/" + model.ID
		if usable[ref] {
			universe = append(universe, scopeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName})
			continue
		}
		if _, ok := loginTypes[model.ProviderMeta.ProviderID]; !ok || hiddenUntilPicked(model.ProviderMeta.ProviderID) {
			continue
		}
		universe = append(universe, scopeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Unusable: true})
	}
	return universe, nil
}

func replaceProviderModels(universe []scopeModel, provider string, models []scopeModel) []scopeModel {
	index := -1
	for i, model := range universe {
		if model.Provider == provider {
			index = i
			break
		}
	}
	var others []scopeModel
	for _, model := range universe {
		if model.Provider != provider {
			others = append(others, model)
		}
	}
	if index == -1 {
		index = len(others)
	}
	result := make([]scopeModel, 0, len(others)+len(models))
	result = append(result, others[:index]...)
	result = append(result, models...)
	result = append(result, others[index:]...)
	return result
}

func usableModels(universe []scopeModel, usableOnly bool) []scopeModel {
	if !usableOnly {
		return universe
	}
	var result []scopeModel
	for _, model := range universe {
		if !model.Unusable {
			result = append(result, model)
		}
	}
	return result
}

var globChars = regexp.MustCompile(`[*?[]`)

func globRegexp(pattern string) *regexp.Regexp {
	var builder strings.Builder
	builder.WriteString("(?i)^")
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i += 2
			} else {
				builder.WriteString("[^/]*")
				i++
			}
		case '?':
			builder.WriteString("[^/]")
			i++
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	builder.WriteString("$")
	return regexp.MustCompile(builder.String())
}

func splitThinkingSuffix(pattern string) (string, string) {
	index := strings.LastIndex(pattern, ":")
	if index <= 0 || index == len(pattern)-1 {
		return pattern, ""
	}
	level := pattern[index+1:]
	if isThinkingLevel(level) {
		return pattern[:index], level
	}
	return pattern, ""
}

// resolveScope resolves enabledModels patterns against the universe. It
// supports exact provider/id references, globs and plain/fuzzy id and name
// matching, mirroring the subset of Pi's resolver Pigkin's host relies on.
func resolveScope(patterns []string, universe []scopeModel, usableOnly bool) []scopeRef {
	models := usableModels(universe, usableOnly)
	var refs []scopeRef
	seen := map[string]bool{}
	add := func(ref scopeRef) {
		key := strings.ToLower(ref.pattern())
		if !seen[key] {
			seen[key] = true
			refs = append(refs, ref)
		}
	}
	for _, pattern := range patterns {
		if globChars.MatchString(pattern) {
			glob, level := splitThinkingSuffix(pattern)
			re := globRegexp(glob)
			for _, model := range models {
				if re.MatchString(model.Provider+"/"+model.ID) || re.MatchString(model.ID) {
					add(scopeRef{Provider: model.Provider, ID: model.ID, ThinkingLevel: level})
				}
			}
			continue
		}
		text, level := splitThinkingSuffix(pattern)
		var matches []scopeModel
		for _, model := range models {
			if strings.EqualFold(model.Provider+"/"+model.ID, text) {
				matches = append(matches, model)
			}
		}
		if len(matches) == 0 && !strings.Contains(text, "/") {
			for _, model := range models {
				if strings.EqualFold(model.ID, text) {
					matches = append(matches, model)
				}
			}
		}
		if len(matches) == 0 {
			needle := strings.ToLower(text)
			for _, model := range models {
				if strings.Contains(strings.ToLower(model.ID), needle) || (model.Name != "" && strings.Contains(strings.ToLower(model.Name), needle)) {
					matches = append(matches, model)
				}
			}
		}
		for _, model := range matches {
			add(scopeRef{Provider: model.Provider, ID: model.ID, ThinkingLevel: level})
		}
	}
	return refs
}

func ownedBy(pattern, provider string) bool {
	return strings.HasPrefix(lower(pattern), lower(provider)+"/")
}

func namedProvider(pattern string) string {
	index := strings.Index(pattern, "/")
	if index < 0 {
		return ""
	}
	return lower(pattern[:index])
}

func dedupe(patterns []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, pattern := range patterns {
		key := lower(pattern)
		if !seen[key] {
			seen[key] = true
			result = append(result, pattern)
		}
	}
	return result
}

func dropProvider(patterns []string, provider string) []string {
	result := []string{}
	for _, pattern := range patterns {
		if !ownedBy(pattern, provider) {
			result = append(result, pattern)
		}
	}
	return result
}

func providerPatterns(provider string, enabled []string, universe []scopeModel) []string {
	if enabled != nil {
		patterns := make([]string, 0, len(enabled))
		for _, id := range enabled {
			patterns = append(patterns, provider+"/"+id)
		}
		return patterns
	}
	var own []scopeModel
	for _, model := range universe {
		if model.Provider == provider {
			own = append(own, model)
		}
	}
	glob := provider + "/*"
	for _, model := range own {
		if strings.Contains(model.ID, "/") {
			glob = provider + "/**"
			break
		}
	}
	refs := resolveScope([]string{glob}, universe, false)
	for _, ref := range refs {
		if ref.Provider != provider {
			var patterns []string
			for _, model := range own {
				patterns = append(patterns, provider+"/"+model.ID)
			}
			return patterns
		}
	}
	return []string{glob}
}

func everyProvider(universe []scopeModel) []string {
	providers := []string{}
	seen := map[string]bool{}
	for _, model := range universe {
		if !seen[model.Provider] {
			seen[model.Provider] = true
			providers = append(providers, model.Provider)
		}
	}
	sort.Strings(providers)
	var patterns []string
	for _, provider := range providers {
		patterns = append(patterns, providerPatterns(provider, nil, universe)...)
	}
	return patterns
}

func dropMisdirected(patterns []string, universe []scopeModel) []string {
	providers := map[string]bool{}
	usable := map[string]bool{}
	for _, model := range universe {
		providers[lower(model.Provider)] = true
		if !model.Unusable {
			usable[lower(model.Provider)] = true
		}
	}
	signedOut := map[string]bool{}
	var rewritten []string
	for _, pattern := range patterns {
		named := namedProvider(pattern)
		refs := []scopeRef{}
		if providers[named] {
			refs = resolveScope([]string{pattern}, universe, true)
		}
		if len(refs) > 0 {
			allNamed := true
			for _, ref := range refs {
				if lower(ref.Provider) != named {
					allNamed = false
				}
			}
			if allNamed {
				rewritten = append(rewritten, pattern)
				continue
			}
		}
		if usable[named] {
			for _, ref := range refs {
				if lower(ref.Provider) == named {
					rewritten = append(rewritten, ref.pattern())
				}
			}
		} else {
			signedOut[named] = true
		}
	}
	var result []string
	for _, pattern := range dedupe(rewritten) {
		if !signedOut[namedProvider(pattern)] {
			result = append(result, pattern)
		}
	}
	return result
}

func othersFallback(provider string, universe []scopeModel) []string {
	patterns := everyProvider(universe)
	var others []string
	for _, pattern := range patterns {
		named := namedProvider(pattern)
		if named != lower(provider) && !hiddenUntilPicked(named) {
			others = append(others, pattern)
		}
	}
	kept := dropMisdirected(others, universe)
	if len(kept) > 0 {
		return kept
	}
	return []string{noModels}
}

func rebuildForProvider(patterns []string, provider string, enabled []string, universe []scopeModel) []string {
	var start []string
	if len(patterns) > 0 {
		for _, pattern := range patterns {
			if pattern != noModels {
				start = append(start, pattern)
			}
		}
	} else {
		start = everyProvider(universe)
	}
	var rebuilt []string
	for _, pattern := range start {
		if ownedBy(pattern, provider) {
			continue
		}
		matched := resolveScope([]string{pattern}, universe, false)
		others := []scopeRef{}
		for _, ref := range matched {
			if ref.Provider != provider {
				others = append(others, ref)
			}
		}
		if len(others) == len(matched) {
			rebuilt = append(rebuilt, pattern)
		} else {
			for _, ref := range others {
				rebuilt = append(rebuilt, ref.pattern())
			}
		}
	}
	for _, pattern := range providerPatterns(provider, enabled, universe) {
		rebuilt = append(rebuilt, pattern)
	}
	kept := dropMisdirected(dedupe(rebuilt), universe)
	if len(kept) > 0 {
		return kept
	}
	return othersFallback(provider, universe)
}

func withoutMisdirected(patterns []string, universe []scopeModel) []string {
	kept := dropMisdirected(patterns, universe)
	if len(kept) > 0 || len(patterns) == 0 {
		return kept
	}
	return othersFallback("", universe)
}

func putDefaultFirst(patterns []string, defaultRef string, universe []scopeModel) []string {
	if defaultRef == "" || len(patterns) == 0 {
		return append([]string(nil), patterns...)
	}
	target := lower(defaultRef)
	found := false
	for _, ref := range resolveScope(patterns, universe, false) {
		if lower(ref.Provider+"/"+ref.ID) == target {
			found = true
		}
	}
	if !found {
		return append([]string(nil), patterns...)
	}
	index := -1
	for i, pattern := range patterns {
		lowerPattern := lower(pattern)
		if !globChars.MatchString(pattern) && (lowerPattern == target || strings.HasPrefix(lowerPattern, target+":")) {
			index = i
			break
		}
	}
	if index == -1 {
		return append([]string{defaultRef}, patterns...)
	}
	result := []string{patterns[index]}
	result = append(result, patterns[:index]...)
	result = append(result, patterns[index+1:]...)
	return result
}

func keepProviderInScope(patterns []string, provider, defaultRef string, universe []scopeModel) []string {
	if len(patterns) == 0 {
		return nil
	}
	for _, ref := range resolveScope(patterns, universe, false) {
		if ref.Provider == provider {
			return nil
		}
	}
	var added []string
	for _, pattern := range patterns {
		if pattern != noModels {
			added = append(added, pattern)
		}
	}
	added = append(added, providerPatterns(provider, nil, universe)...)
	return putDefaultFirst(dropMisdirected(dedupe(added), universe), defaultRef, universe)
}

func hideUnpickedProvider(patterns []string, provider, defaultRef string, universe []scopeModel) []string {
	picked := false
	for _, pattern := range patterns {
		if ownedBy(pattern, provider) {
			picked = true
		}
	}
	var narrowed []string
	if picked {
		narrowed = withoutMisdirected(patterns, universe)
	} else {
		narrowed = rebuildForProvider(patterns, provider, []string{}, universe)
	}
	if jsEqual(narrowed, patterns) {
		return nil
	}
	return putDefaultFirst(narrowed, defaultRef, universe)
}
