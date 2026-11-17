package modes

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	listTimeout  = 15 * time.Second
	bodyPreview  = 300
	perMillion   = 1_000_000
	htmlBodyText = `^\s*<(!doctype|html|head|body)`
)

var htmlBody = regexp.MustCompile(`(?i)` + htmlBodyText)

func providerError(code, message, key string, args host.Message) error {
	text := &host.Text{Key: key}
	if args != nil {
		text.Args = args
	}
	return host.NewError(code, message, text)
}

// fetchModelList fetches an OpenAI-style /models list and reads each entry's
// capabilities, keyed by id in the server's order.
func fetchModelList(baseURL, url, apiKey string) (map[string]modelDef, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, providerError("network", "Could not reach "+baseURL, "provider_unreachable", host.Message{"url": baseURL})
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: listTimeout}
	response, err := client.Do(request)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, host.NewError("timeout", "No response from "+baseURL+" within 15 s", nil)
		}
		return nil, providerError("network", "Could not reach "+baseURL, "provider_unreachable", host.Message{"url": baseURL})
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, providerError("network", "Could not reach "+baseURL, "provider_unreachable", host.Message{"url": baseURL})
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := httpErrorCode(response.StatusCode)
		said := serverMessage(string(body))
		if said == "" {
			return nil, providerError(code, "HTTP "+strconv.Itoa(response.StatusCode), "provider_http_status", host.Message{"status": response.StatusCode})
		}
		return nil, providerError(code, "HTTP "+strconv.Itoa(response.StatusCode)+": "+said, "provider_http_status_said", host.Message{"status": response.StatusCode, "message": said})
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		text := string(body)
		if htmlBody.MatchString(text) {
			return nil, providerError("bad_response", url+" returned a web page, not JSON", "provider_web_page", host.Message{"url": url})
		}
		preview := text
		if len(preview) > bodyPreview {
			preview = preview[:bodyPreview]
		}
		return nil, providerError("bad_response", "The server did not return JSON: "+preview, "provider_not_json", host.Message{"body": preview})
	}
	envelope, _ := parsed.(map[string]any)
	var list any
	if envelope != nil {
		if envelope["object"] == "list" && envelope["data"] == nil {
			list = []any{}
		} else {
			list = envelope["data"]
		}
	}
	entries, ok := list.([]any)
	if !ok {
		return nil, providerError("bad_response", "The server's /models response has no data list", "provider_no_data_list", nil)
	}
	detected := map[string]modelDef{}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		id, hasID := entry["id"].(string)
		if ok && hasID {
			if _, exists := detected[id]; !exists {
				detected[id] = detectModel(id, entry)
			}
		}
	}
	return detected, nil
}

func httpErrorCode(status int) string {
	switch {
	case status == 401 || status == 403:
		return "unauthorized"
	case status == 404:
		return "not_found"
	case status == 429:
		return "rate_limited"
	case status >= 500:
		return "server_error"
	default:
		return "http_error"
	}
}

func serverMessage(body string) string {
	if htmlBody.MatchString(body) {
		return ""
	}
	var parsed any
	if json.Unmarshal([]byte(body), &parsed) == nil {
		if object, ok := parsed.(map[string]any); ok {
			candidates := []any{}
			if err, ok := object["error"].(map[string]any); ok {
				candidates = append(candidates, err["message"])
			} else {
				candidates = append(candidates, object["error"])
			}
			candidates = append(candidates, object["message"], object["detail"])
			for _, value := range candidates {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					return clip(strings.TrimSpace(text), bodyPreview)
				}
			}
		}
	}
	return clip(strings.TrimSpace(body), bodyPreview)
}

func clip(text string, n int) string {
	if len(text) > n {
		return text[:n]
	}
	return text
}

func atEntry(entry host.Message, path string) any {
	var value any = entry
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func firstPositive(entry host.Message, paths []string) (float64, bool) {
	for _, path := range paths {
		if value, ok := positiveNumber(atEntry(entry, path)); ok {
			return value, true
		}
	}
	return 0, false
}

func anyOf(answers []bool) (bool, bool) {
	for _, answer := range answers {
		if answer {
			return true, true
		}
	}
	for _, answer := range answers {
		if !answer {
			return false, true
		}
	}
	return false, false
}

func flag(entry host.Message, path string) (bool, bool) {
	value, ok := atEntry(entry, path).(bool)
	return value, ok
}

func listContains(entry host.Message, path, item string) (bool, bool) {
	list, ok := atEntry(entry, path).([]any)
	if !ok {
		return false, false
	}
	for _, value := range list {
		if text, ok := value.(string); ok && text == item {
			return true, true
		}
	}
	return false, false
}

func efforts(entry host.Message) map[string]bool {
	if levels, ok := atEntry(entry, "metadata.supported_reasoning_levels").([]any); ok {
		result := map[string]bool{}
		for _, value := range levels {
			if object, ok := value.(map[string]any); ok {
				if effort, ok := object["effort"].(string); ok {
					result[effort] = true
				}
			}
		}
		return result
	}
	if valid, ok := atEntry(entry, "reasoning.effort.valid").([]any); ok {
		result := map[string]bool{}
		for _, value := range valid {
			if effort, ok := value.(string); ok {
				result[effort] = true
			}
		}
		return result
	}
	return nil
}

func detectThinkingLevelMap(entry host.Message) map[string]*string {
	listed := efforts(entry)
	if listed == nil {
		return nil
	}
	known := listed["none"]
	for _, level := range thinkingLevels {
		if listed[level] {
			known = true
		}
	}
	if !known {
		return nil
	}
	result := map[string]*string{}
	for _, level := range thinkingLevels {
		if level == "off" {
			if listed["none"] {
				none := "none"
				result[level] = &none
			} else {
				result[level] = nil
			}
			continue
		}
		if listed[level] {
			value := level
			result[level] = &value
		} else {
			result[level] = nil
		}
	}
	return result
}

func pricing(entry host.Message, key string) (float64, bool) {
	value := atEntry(entry, "pricing."+key)
	var perToken float64
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		perToken = parsed
	case float64:
		perToken = typed
	default:
		return 0, false
	}
	if perToken < 0 || perToken > 1e308 {
		return 0, false
	}
	// toPrecision(12) drops float noise such as 0.000003 * 1e6.
	rate, err := strconv.ParseFloat(strconv.FormatFloat(perToken*perMillion, 'g', 12, 64), 64)
	if err != nil {
		return 0, false
	}
	return rate, true
}

func detectCost(entry host.Message) *modelCost {
	input, ok1 := pricing(entry, "prompt")
	output, ok2 := pricing(entry, "completion")
	if !ok1 || !ok2 {
		return nil
	}
	cacheRead, _ := pricing(entry, "input_cache_read")
	cacheWrite, _ := pricing(entry, "input_cache_write")
	return &modelCost{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite}
}

// detectModel reads the capabilities an OpenAI-compatible /models entry reports.
func detectModel(id string, entry host.Message) modelDef {
	def := modelDef{ID: id, extra: cloneMessage(entry)}
	if name, ok := atEntry(entry, "metadata.display_name").(string); ok && name != "" {
		def.Name = name
	} else if name, ok := entry["display_name"].(string); ok && name != "" {
		def.Name = name
	} else if name, ok := entry["name"].(string); ok && name != "" {
		def.Name = name
	}

	levels := detectThinkingLevelMap(entry)
	var reasoning bool
	var hasReasoning bool
	if levels == nil {
		answers := []bool{}
		for _, path := range []string{"supports_reasoning", "capabilities.supports_reasoning"} {
			if value, ok := flag(entry, path); ok {
				answers = append(answers, value)
			}
		}
		if value, ok := listContains(entry, "supported_parameters", "reasoning"); ok {
			answers = append(answers, value)
		}
		reasoning, hasReasoning = anyOf(answers)
	} else {
		reasoning, hasReasoning = true, true
	}
	if hasReasoning {
		def.Reasoning = &reasoning
	}
	if levels != nil {
		def.ThinkingLevelMap = levels
	}

	imageAnswers := []bool{}
	for _, path := range []string{"supports_images", "supports_vision", "supports_image_in", "capabilities.supports_images"} {
		if value, ok := flag(entry, path); ok {
			imageAnswers = append(imageAnswers, value)
		}
	}
	for _, path := range []string{"input_modalities", "metadata.input_modalities", "capabilities.input_modalities", "architecture.input_modalities", "modalities.input"} {
		if value, ok := listContains(entry, path, "image"); ok {
			imageAnswers = append(imageAnswers, value)
		}
	}
	if images, ok := anyOf(imageAnswers); ok {
		if images {
			def.Input = []string{"text", "image"}
		} else {
			def.Input = []string{"text"}
		}
	}

	if value, ok := firstPositive(entry, []string{"context_length", "context_window", "contextLength", "metadata.context_window", "capabilities.context_length"}); ok {
		def.ContextWindow = value
	}
	if value, ok := firstPositive(entry, []string{"max_output_tokens", "maxOutputTokens", "metadata.max_output_tokens", "top_provider.max_completion_tokens"}); ok {
		def.MaxTokens = value
	}
	if cost := detectCost(entry); cost != nil {
		def.Cost = cost
	}
	return def
}
