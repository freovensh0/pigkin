package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	webAccessExtension  = "pigkin-web-access"
	webAccessNpmPackage = "pi-web-access"
)

// pigkinExtensionPath is where ensure writes and rpc finds a bundled
// extension's index.js. An extension that uses runtime packages lives inside
// the runtime so Node resolves those packages; the others stay outside, where
// Pi's loader supplies its own copies of Pi's imports. This mirrors
// host/src/pigkin-extensions.ts:extensionPath.
func pigkinExtensionPath(root, name string, usesRuntimePackages bool) string {
	if usesRuntimePackages {
		return filepath.Join(root, "pi", "extensions", name, "index.js")
	}
	return filepath.Join(root, "extensions", name, "index.js")
}

// installPigkinExtensions writes every bundled extension's source to disk.
// A file is rewritten only when its source changed, through a rename, so a
// chat's Pi starting at the same time never loads half a file. Mirrors
// host/src/pigkin-extensions.ts:installPigkinExtensions.
func installPigkinExtensions(root string, extensions []any) error {
	for _, item := range extensions {
		fields := item.(map[string]any) // validateExtensions checked the shape
		name := fields["name"].(string)
		source := fields["source"].(string)
		path := pigkinExtensionPath(root, name, fields["usesRuntimePackages"].(bool))
		if sameContents(path, source) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		temporary := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
		if err := os.WriteFile(temporary, []byte(source), 0o644); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return err
		}
	}
	return nil
}

// extensionsOutdated is the refusal that makes the app run ensure and retry a
// chat when the extensions it expects aren't installed correctly.
func extensionsOutdated() *host.Error {
	return host.NewError("pi_version", "The agent's bundled extensions are out of date", &host.Text{Key: "agent_extensions_outdated"})
}

// globalSettings reads Pi's global settings, the file `pi install` writes its
// packages to. A missing or malformed file is treated as empty, as the
// TypeScript host does.
func globalSettings(configDir string) host.Message {
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return host.Message{}
	}
	var settings host.Message
	if json.Unmarshal(data, &settings) != nil {
		return host.Message{}
	}
	return settings
}

// installedNpmPackage reports whether a chat should skip Pigkin's bundled copy
// of an extension because the user installed its npm package instead. Pi exits
// when two extensions register the same tools.
func installedNpmPackage(configDir, name string) bool {
	for _, installed := range userNpmPackages(globalSettings(configDir)) {
		if installed == name {
			return true
		}
	}
	return false
}
