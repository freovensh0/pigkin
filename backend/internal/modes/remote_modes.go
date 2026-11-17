package modes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// remoteFolder is the remote folder a workspace stand-in names, and the host to
// reach it through.
type remoteFolder struct {
	remote remote
	path   string
}

func remoteFolderForEnv(env host.Env, cwd string) (*remoteFolder, error) {
	paths := remoteLayoutFor(env)
	standIn, err := readStandIn(paths.remotes, cwd)
	if err != nil {
		return nil, err
	}
	if standIn == nil {
		return nil, nil
	}
	hostID, _ := standIn["hostId"].(string)
	path, _ := standIn["path"].(string)
	target, err := findHost(paths, hostID)
	if err != nil {
		return nil, err
	}
	return &remoteFolder{remote: remote{paths: paths, host: target}, path: path}, nil
}

func asRemoteError(r remote, err error) *host.Error {
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		return hostErr
	}
	return remoteFailed(r.host.Name, err.Error())
}

func asHostError(err error) *host.Error {
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		return hostErr
	}
	return host.NewError("remote_failed", err.Error(), nil)
}

// connectionCode is a failed connection's code, which a reply adds beside its
// error so the app can offer the fix; empty for any other failure.
func connectionCode(err *host.Error) string {
	if err.Code == "ssh_missing" || strings.HasPrefix(err.Code, "remote_") {
		return err.Code
	}
	return ""
}

// remoteWorkspace reports whether root is a remote workspace's stand-in folder:
// a folder under the remotes beside Pi's config whose manifest names the same
// remote folder its name hashes.
func remoteWorkspace(env host.Env, root string) bool {
	remotes := filepath.Join(filepath.Dir(env.ConfigDir), "remotes")
	folder := filepath.Clean(root)
	parent := filepath.Dir(filepath.Dir(folder))
	if parent != remotes {
		real, err := filepath.EvalSymlinks(remotes)
		if err != nil || parent != real {
			return false
		}
	}
	data, err := os.ReadFile(filepath.Join(folder, "remote.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Version int    `json:"version"`
		HostID  string `json:"hostId"`
		Path    string `json:"path"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 {
		return false
	}
	if manifest.HostID == "" || !strings.HasPrefix(manifest.Path, "/") {
		return false
	}
	sum := sha256.Sum256([]byte(manifest.Path))
	return filepath.Base(filepath.Dir(folder)) == manifest.HostID && filepath.Base(folder) == hex.EncodeToString(sum[:])[:16]
}
