package modes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

var (
	errModelsInvalid   = host.NewError("models_invalid", "models.json is not valid JSON. Ask the agent to fix it.", &host.Text{Key: "models_invalid"})
	errSettingsInvalid = host.NewError("settings_invalid", "settings.json is not valid. Ask the agent to fix it.", &host.Text{Key: "settings_invalid"})
)

func readJSONFile(path string) (host.Message, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(text)
}

func decodeJSONObject(text []byte) (host.Message, error) {
	// JSON.parse tolerates a leading UTF-8 BOM.
	if len(text) >= 3 && string(text[:3]) == "\xef\xbb\xbf" {
		text = text[3:]
	}
	var value any
	if err := json.Unmarshal(text, &value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return object, nil
}

// configWriteMu serializes writers of the same config file: separate utility
// requests run on separate goroutines and must not race to publish a rename.
var configWriteMu sync.Mutex

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type modelsJSON struct {
	root      host.Message
	providers host.Message
}

func readModelsJSON(configDir string) (modelsJSON, error) {
	root, err := readJSONFile(filepath.Join(configDir, "models.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return modelsJSON{root: host.Message{}, providers: host.Message{}}, nil
		}
		return modelsJSON{}, errModelsInvalid
	}
	providers, ok := root["providers"]
	if !ok {
		return modelsJSON{root: root, providers: host.Message{}}, nil
	}
	object, ok := providers.(map[string]any)
	if !ok {
		return modelsJSON{}, errModelsInvalid
	}
	return modelsJSON{root: root, providers: object}, nil
}

func (m modelsJSON) write(configDir string) error {
	root := cloneMessage(m.root)
	root["providers"] = m.providers
	return writeJSONFile(filepath.Join(configDir, "models.json"), root)
}

// readSettings parses settings.json into a map, reporting settings_invalid for
// a malformed file. A missing file is an empty object.
func readSettings(configDir string) (host.Message, error) {
	settings, err := readJSONFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return host.Message{}, nil
		}
		return nil, errSettingsInvalid
	}
	return settings, nil
}

func writeSettings(configDir string, settings host.Message) error {
	return writeJSONFile(filepath.Join(configDir, "settings.json"), settings)
}

func enabledModels(settings host.Message) ([]string, error) {
	value, ok := settings["enabledModels"]
	if !ok || value == nil {
		return []string{}, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, errSettingsInvalid
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, errSettingsInvalid
		}
		result = append(result, text)
	}
	return result, nil
}

func setEnabledModels(settings host.Message, patterns []string) {
	if len(patterns) == 0 {
		delete(settings, "enabledModels")
		return
	}
	values := make([]any, 0, len(patterns))
	for _, pattern := range patterns {
		values = append(values, pattern)
	}
	settings["enabledModels"] = values
}

func defaultRef(settings host.Message) string {
	provider, _ := settings["defaultProvider"].(string)
	model, _ := settings["defaultModel"].(string)
	if provider != "" && model != "" {
		return provider + "/" + model
	}
	return ""
}
