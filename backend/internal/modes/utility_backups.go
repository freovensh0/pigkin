package modes

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const manifestName = "manifest.json"

type backupManifest struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
	PiVersion string `json:"piVersion"`
}

// backupPath accepts only a plain .tar.gz name, never a path into the config
// folder.
func backupPath(env host.Env, name string) (string, error) {
	if filepath.Base(name) != name || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".tar.gz") || strings.Contains(name, "\x00") {
		return "", host.NewError("bad_name", "Not a backup name: "+name, nil)
	}
	return filepath.Join(backupsDir(env), name), nil
}

func notBackup() error {
	return host.NewError("not_backup", "This file is not a Pigkin configuration backup", &host.Text{Key: "backup_not_pigkin"})
}

// readBackupManifest reads only the first entry of a backup's tar.
func readBackupManifest(path string) (backupManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return backupManifest{}, err
	}
	defer file.Close()
	zip, err := gzip.NewReader(file)
	if err != nil {
		return backupManifest{}, unreadableBackup()
	}
	defer zip.Close()
	reader := tar.NewReader(zip)
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return backupManifest{}, host.NewError("bad_backup", "The archive is empty", &host.Text{Key: "archive_empty"})
	}
	if err != nil {
		return backupManifest{}, unreadableBackup()
	}
	if header.Typeflag != tar.TypeReg || header.Name != manifestName || header.Size > 64*1024 {
		return backupManifest{}, notBackup()
	}
	data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
	if err != nil || int64(len(data)) != header.Size {
		return backupManifest{}, notBackup()
	}
	var manifest backupManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != "pigkin-config-backup" {
		return backupManifest{}, notBackup()
	}
	if manifest.Version != 1 {
		return backupManifest{}, host.NewError("backup_version", "This backup is from a newer Pigkin; update Pigkin to restore it", &host.Text{Key: "backup_from_newer_pigkin"})
	}
	return manifest, nil
}

func unreadableBackup() error {
	return host.NewError("bad_backup", "This file can't be read as a backup", &host.Text{Key: "backup_unreadable"})
}

func backupInfo(env host.Env, name string) (host.Message, error) {
	path, err := backupPath(env, name)
	if err != nil {
		return nil, err
	}
	stats, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	info := host.Message{
		"name":      name,
		"size":      stats.Size(),
		"createdAt": isoTime(stats.ModTime()),
		"piVersion": nil,
		"error":     nil,
	}
	manifest, err := readBackupManifest(path)
	if err != nil {
		var hostErr *host.Error
		if errors.As(err, &hostErr) {
			info["error"] = hostErr.Message
			info["errorI18n"] = hostErr.I18n
		} else {
			info["error"] = "This file can't be read as a backup"
			info["errorI18n"] = &host.Text{Key: "backup_unreadable"}
		}
		return info, nil
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err == nil {
		info["createdAt"] = manifest.CreatedAt
	}
	if manifest.PiVersion != "" {
		info["piVersion"] = manifest.PiVersion
	}
	return info, nil
}

func isoTime(when time.Time) string {
	return when.UTC().Format("2006-01-02T15:04:05.000Z")
}

// listBackupsOp lists the backups folder, newest first. A file that isn't a
// readable backup is listed with the reason it failed.
func listBackupsOp(ctx *Context) (host.Message, error) {
	dir := backupsDir(ctx.Env)
	entries, err := os.ReadDir(dir)
	if err != nil {
		entries = nil
	}
	backups := []host.Message{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		if stats, err := os.Stat(filepath.Join(dir, name)); err != nil || !stats.Mode().IsRegular() {
			continue
		}
		info, err := backupInfo(ctx.Env, name)
		if err != nil {
			return nil, err
		}
		backups = append(backups, info)
	}
	sort.SliceStable(backups, func(i, j int) bool {
		left, right := backups[i]["createdAt"].(string), backups[j]["createdAt"].(string)
		if left != right {
			return left > right
		}
		return backups[i]["name"].(string) > backups[j]["name"].(string)
	})
	return host.Message{"dir": dir, "backups": backups}, nil
}

// backupDeleteOp removes a backup; one already gone counts as deleted.
func backupDeleteOp(ctx *Context) (host.Message, error) {
	name, err := stringParam(ctx.Params, "name")
	if err != nil {
		return nil, err
	}
	path, err := backupPath(ctx.Env, name)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return host.Message{}, nil
}
