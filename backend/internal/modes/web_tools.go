package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// defaultWebToolsToEager makes Pigkin's bundled web access register its tools
// eagerly for a fresh chat instead of hiding them behind web_enable. A config
// that already chose an activation mode, or asked for deferred exposure, is
// left alone. Mirrors host/src/modes/rpc.ts:defaultWebToolsToEager.
func defaultWebToolsToEager(configDir string) {
	path := filepath.Join(configDir, "web-search.json")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	config := map[string]any{}
	if err == nil && json.Unmarshal(data, &config) != nil {
		return
	}
	if _, set := config["toolActivation"]; set {
		return
	}
	if exposure, _ := config["toolExposure"].(string); exposure == "deferred" {
		return
	}
	config["toolActivation"] = "eager"
	formatted, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(formatted, '\n'), 0o644)
}
