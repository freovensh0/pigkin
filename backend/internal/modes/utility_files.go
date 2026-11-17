package modes

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func stringParam(params host.Message, key string) (string, error) {
	value, ok := params[key].(string)
	if !ok {
		return "", host.NewError("bad_params", `Missing string parameter "`+key+`"`, nil)
	}
	return value, nil
}

func nullableStringParam(params host.Message, key string) (string, bool, error) {
	value := params[key]
	if value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", false, host.NewError("bad_params", `Parameter "`+key+`" must be a string or null`, nil)
	}
	return text, true, nil
}

func stringArrayParam(params host.Message, key string) ([]string, error) {
	values, ok := params[key].([]any)
	if !ok {
		return nil, host.NewError("bad_params", `Parameter "`+key+`" must be an array of strings`, nil)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, host.NewError("bad_params", `Parameter "`+key+`" must be an array of strings`, nil)
		}
		result = append(result, text)
	}
	return result, nil
}

func notFound(path string) error {
	return host.NewError("not_found", "Session not found: "+path, &host.Text{Key: "session_not_found", Args: map[string]any{"path": path}})
}

// sessionFile resolves path and refuses one outside the sessions folder.
func sessionFile(configDir, path string) (string, error) {
	base, err := realPath(filepath.Join(configDir, "sessions"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", notFound(path)
		}
		return "", err
	}
	target, err := realPath(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", notFound(path)
		}
		return "", err
	}
	if !insideUtility(base, target) {
		return "", host.NewError("outside_sessions", "That file is not inside Pigkin's sessions folder", nil)
	}
	return target, nil
}

// containedFolder is folder's real path, or empty when it is missing or
// resolves outside the sessions folder.
func containedFolder(configDir, folder string) (string, error) {
	target, err := realPath(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	base, err := realPath(filepath.Join(configDir, "sessions"))
	if err != nil {
		return "", err
	}
	if !insideUtility(base, target) {
		return "", host.NewError("outside_sessions", "That folder is not inside Pigkin's sessions folder", nil)
	}
	return target, nil
}

func realPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func insideUtility(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var sessionIDPattern = regexp.MustCompile(`^[\w-]+$`)

// subagentFolder holds the transcripts of the session id saved at path.
func subagentFolder(path, id string) string {
	if !sessionIDPattern.MatchString(id) {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "subagents", id)
}

// sessionID reads the id in a session file's header, if it has one.
func sessionID(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(line, &header) != nil || header.Type != "session" {
		return ""
	}
	return header.ID
}

type remotePaths struct {
	remotes string
}

func remotePathsFor(env host.Env) remotePaths {
	state := filepath.Dir(env.ConfigDir)
	return remotePaths{remotes: filepath.Join(state, "remotes")}
}

// readStandIn reports what cwd stands for, or nil for a local folder. Only a
// manifest in its own place under remotes counts.
func readStandIn(remotes, cwd string) (host.Message, error) {
	folder, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(filepath.Dir(folder))
	resolved, err := filepath.Abs(remotes)
	if err != nil {
		return nil, err
	}
	match := parent == resolved
	if !match {
		if real, err := realPath(remotes); err == nil {
			match = parent == real
		}
	}
	if !match {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(folder, "remote.json"))
	if err != nil {
		return nil, nil
	}
	var manifest struct {
		Version int    `json:"version"`
		HostID  string `json:"hostId"`
		Path    string `json:"path"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || manifest.HostID == "" || !strings.HasPrefix(manifest.Path, "/") {
		return nil, nil
	}
	if filepath.Base(filepath.Dir(folder)) != manifest.HostID || filepath.Base(folder) != pathHash(manifest.Path) {
		return nil, nil
	}
	return host.Message{"hostId": manifest.HostID, "path": manifest.Path}, nil
}

// pathHash is the stand-in folder name: the first 16 hex digits of the SHA-256
// of the remote path.
func pathHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:16]
}

func backupsDir(env host.Env) string {
	return filepath.Join(filepath.Dir(env.ConfigDir), "backups")
}
