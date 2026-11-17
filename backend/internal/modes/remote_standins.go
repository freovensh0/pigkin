package modes

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const standInManifest = "remote.json"

// normalizeRemotePath is path as stand-ins key it: absolute and normalised.
func normalizeRemotePath(value string) (string, error) {
	if !strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) {
		return "", host.NewError("bad_params", "Not an absolute remote path: "+value, nil)
	}
	return path.Clean(value), nil
}

func standInFor(remotes, hostID, remotePath string) string {
	normalized, err := normalizeRemotePath(remotePath)
	if err != nil {
		normalized = remotePath
	}
	return filepath.Join(remotes, hostID, pathHash(normalized))
}

// createStandIn makes the stand-in for a host's folder if needed, and returns
// it. The same folder always gets the same stand-in.
func createStandIn(remotes, hostID, value string) (string, error) {
	remotePath, err := normalizeRemotePath(value)
	if err != nil {
		return "", err
	}
	cwd := standInFor(remotes, hostID, remotePath)
	if saved, err := readStandIn(remotes, cwd); err == nil && saved != nil && saved["path"] == remotePath {
		return cwd, nil
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Version int    `json:"version"`
		HostID  string `json:"hostId"`
		Path    string `json:"path"`
	}{Version: 1, HostID: hostID, Path: remotePath})
	if err != nil {
		return "", err
	}
	if err := writeAtomically(filepath.Join(cwd, standInManifest), string(data)+"\n", 0o644); err != nil {
		return "", err
	}
	return cwd, nil
}

// remoteFolderFor reports the host and folder a stand-in cwd names, or nil for
// a local folder. A removed host is a refusal the app can show.
func remoteFolderFor(ctx *Context, cwd string) (*remote, string, error) {
	paths := remoteLayoutFor(ctx.Env)
	standIn, err := readStandIn(paths.remotes, cwd)
	if err != nil || standIn == nil {
		return nil, "", err
	}
	hostID, _ := standIn["hostId"].(string)
	remotePath, _ := standIn["path"].(string)
	target, err := findHost(paths, hostID)
	if err != nil {
		return nil, "", err
	}
	return &remote{paths: paths, host: target}, remotePath, nil
}

// listStandIns reports every stand-in, including those of removed hosts whose
// chats still need a label.
func listStandIns(remotes string) ([]host.Message, error) {
	hosts, err := os.ReadDir(remotes)
	if err != nil {
		return []host.Message{}, nil
	}
	workspaces := []host.Message{}
	for _, hostDir := range hosts {
		if !hostDir.IsDir() {
			continue
		}
		hashes, err := os.ReadDir(filepath.Join(remotes, hostDir.Name()))
		if err != nil {
			continue
		}
		for _, folder := range hashes {
			cwd := filepath.Join(remotes, hostDir.Name(), folder.Name())
			saved, err := readStandIn(remotes, cwd)
			if err != nil || saved == nil {
				continue
			}
			workspaces = append(workspaces, host.Message{
				"cwd": cwd, "hostId": saved["hostId"], "path": saved["path"],
			})
		}
	}
	return workspaces, nil
}
