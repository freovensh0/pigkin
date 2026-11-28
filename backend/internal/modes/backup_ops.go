package modes

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const backupConfig = "config"

// backupExcluded are top-level config entries that are state, not
// configuration: a backup leaves them out and a restore leaves them alone.
var backupExcluded = map[string]bool{
	"sessions":     true,
	"bin":          true,
	"tmp":          true,
	"pi-debug.log": true,
	"crashes.json": true,
}

type backupProgress struct {
	conn *host.Connection
	verb string
	key  string
	last time.Time
}

func (p *backupProgress) count(n int) {
	if p.conn == nil || !p.last.IsZero() && time.Since(p.last) < 300*time.Millisecond {
		return
	}
	p.last = time.Now()
	noun := "items"
	if n == 1 {
		noun = "item"
	}
	p.conn.Send(host.Message{
		"type":     "pigkin_progress",
		"line":     fmt.Sprintf("%s %d %s", p.verb, n, noun),
		"lineI18n": &host.Text{Key: p.key, Args: map[string]any{"count": n}},
	})
}

// createBackup writes the config folder, minus sessions, caches and logs, to
// a new .tar.gz in the backups folder. The manifest is the first entry, as the
// TypeScript tool writes it, so either host can read either backup.
func createBackup(ctx *Context) (host.Message, error) {
	release, err := withConfigLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := settleRestores(ctx, false); err != nil {
		return nil, err
	}
	dir := backupsDir(ctx.Env)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	removePartialBackups(dir)
	name, err := newBackupName(dir)
	if err != nil {
		return nil, err
	}
	partial := filepath.Join(dir, "."+name+".partial-"+strconv.Itoa(os.Getpid()))
	manifest, err := json.Marshal(host.Message{
		"format":    "pigkin-config-backup",
		"version":   1,
		"createdAt": isoTime(time.Now()),
		"piVersion": nil,
	})
	if err != nil {
		return nil, err
	}
	progress := &backupProgress{conn: ctx.Conn, verb: "Backed up", key: "backup_progress_backed_up"}
	if err := writeConfigBackup(partial, manifest, ctx.Env.ConfigDir, progress); err != nil {
		_ = os.Remove(partial)
		return nil, err
	}
	if err := os.Rename(partial, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(partial)
		return nil, err
	}
	info, err := backupInfo(ctx.Env, name)
	if err != nil {
		return nil, err
	}
	return host.Message{"backup": info}, nil
}

func writeConfigBackup(path string, manifest []byte, configDir string, progress *backupProgress) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	zip := gzip.NewWriter(file)
	writer := tar.NewWriter(zip)
	count := 0
	writeHeader := func(header *tar.Header, content io.Reader) error {
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if content == nil {
			return nil
		}
		if _, err := io.CopyN(writer, content, header.Size); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		count++
		progress.count(count)
		return nil
	}
	now := time.Now().Unix()
	if err := writeHeader(&tar.Header{
		Name: manifestName, Mode: 0o644, Size: int64(len(manifest)),
		ModTime: time.Unix(now, 0), Typeflag: tar.TypeReg,
	}, strings.NewReader(string(manifest))); err != nil {
		return err
	}
	// The app counts backed-up config entries, not the manifest.
	count = 0
	if err := walkConfig(configDir, func(header *tar.Header, content io.Reader) error {
		return writeHeader(header, content)
	}); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return zip.Close()
}

// walkConfig emits sorted tar entries with folders before their contents, so
// a backup has the same entry order as the TypeScript tool's. A file that
// vanishes while the backup runs is skipped; one that changes size is cut or
// zero-padded to the size it had when listed.
func walkConfig(configDir string, emit func(*tar.Header, io.Reader) error) error {
	var walk func(dir, relative string) error
	walk = func(dir, relative string) error {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			if relative == "" && backupExcluded[name] {
				continue
			}
			path := filepath.Join(dir, name)
			relativePath := name
			if relative != "" {
				relativePath = relative + "/" + name
			}
			info, err := os.Lstat(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			entry := backupConfig + "/" + relativePath
			mtime := time.Unix(info.ModTime().Unix(), 0)
			switch {
			case info.IsDir():
				if err := emit(&tar.Header{
					Name: entry + "/", Mode: fileMode(info), ModTime: mtime,
					Typeflag: tar.TypeDir,
				}, nil); err != nil {
					return err
				}
				if err := walk(path, relativePath); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				file, err := os.Open(path)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				err = emit(&tar.Header{
					Name: entry, Mode: fileMode(info), Size: info.Size(), ModTime: mtime,
					Typeflag: tar.TypeReg,
				}, file)
				_ = file.Close()
				if err != nil {
					return err
				}
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if err := emit(&tar.Header{
					Name: entry, Linkname: target, Mode: 0o777, ModTime: mtime,
					Typeflag: tar.TypeSymlink,
				}, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(configDir, "")
}

func fileMode(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(stat.Mode & 0o7777)
	}
	return int64(info.Mode().Perm())
}

func newBackupName(dir string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	taken := make(map[string]bool, len(entries))
	for _, entry := range entries {
		taken[entry.Name()] = true
	}
	for counter := 1; ; counter++ {
		name := "pigkin-config-" + stamp + ".tar.gz"
		if counter > 1 {
			name = "pigkin-config-" + stamp + "-" + strconv.Itoa(counter) + ".tar.gz"
		}
		if !taken[name] {
			return name, nil
		}
	}
}

func removePartialBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && strings.Contains(name, ".partial-") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// restoreBackup checks the archive into a staging folder beside the config
// folder, waits for every Pi using that folder to exit, then swaps the
// configuration in with renames. A damaged archive changes nothing.
func restoreBackup(ctx *Context) (host.Message, error) {
	name, err := stringParam(ctx.Params, "name")
	if err != nil {
		return nil, err
	}
	archive, err := backupPath(ctx.Env, name)
	if err != nil {
		return nil, err
	}
	release, err := withConfigLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := settleRestores(ctx, true); err != nil {
		return nil, err
	}
	if !exists(archive) {
		return nil, host.NewError("not_found", "This backup no longer exists", &host.Text{Key: "backup_missing"})
	}
	id := strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + strconv.Itoa(os.Getpid())
	staging := ctx.Env.ConfigDir + ".restore-" + id
	old := ctx.Env.ConfigDir + ".old-" + id
	if err := os.MkdirAll(filepath.Dir(ctx.Env.ConfigDir), 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return nil, err
	}
	if err := extractBackup(ctx, archive, staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	if err := os.WriteFile(staging+".complete", nil, 0o644); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	if err := waitForPiExit(ctx, ctx.Env.ConfigDir); err != nil {
		_ = os.RemoveAll(staging)
		_ = os.Remove(staging + ".complete")
		return nil, err
	}
	if err := swapConfig(ctx.Env.ConfigDir, staging, old); err != nil {
		return nil, err
	}
	return host.Message{}, nil
}

type restoreLink struct{ path, target string }

type restoreFolder struct {
	path  string
	mode  os.FileMode
	mtime time.Time
}

func extractBackup(ctx *Context, archive, staging string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	zip, err := gzip.NewReader(file)
	if err != nil {
		return unreadableBackup()
	}
	defer zip.Close()
	reader := tar.NewReader(zip)
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return host.NewError("bad_backup", "The archive is empty", &host.Text{Key: "archive_empty"})
	}
	if err != nil {
		return damagedBackup(err.Error(), "")
	}
	if err := checkBackupManifest(reader, header); err != nil {
		return err
	}
	seen := map[string]bool{}
	links := []restoreLink{}
	linkPaths := map[string]bool{}
	folders := []restoreFolder{}
	progress := &backupProgress{conn: ctx.Conn, verb: "Restored", key: "backup_progress_restored"}
	for {
		if isDone(ctx.done()) {
			return host.NewError("aborted", "The restore was cancelled", nil)
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return damagedBackup(err.Error(), "")
		}
		name := strings.TrimRight(header.Name, "/")
		if name == backupConfig && header.Typeflag == tar.TypeDir {
			continue
		}
		relative, err := configPath(name)
		if err != nil {
			return err
		}
		if seen[relative] {
			return badBackup("backup_path_twice", "The backup holds "+relative+" twice",
				map[string]any{"path": relative})
		}
		parts := strings.Split(relative, "/")
		for index := 1; index < len(parts); index++ {
			if linkPaths[strings.Join(parts[:index], "/")] {
				return badBackup("backup_path_beneath_symlink", "The backup holds "+relative+" beneath a symlink",
					map[string]any{"path": relative})
			}
		}
		seen[relative] = true
		target := filepath.Join(staging, filepath.FromSlash(relative))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			folders = append(folders, restoreFolder{path: target, mode: os.FileMode(header.Mode) & 0o7777, mtime: time.Unix(header.ModTime.Unix(), 0)})
		case tar.TypeSymlink:
			if exists(target) {
				return badBackup("backup_files_beneath_symlink", "The backup holds files beneath the symlink "+relative,
					map[string]any{"path": relative})
			}
			for link := range linkPaths {
				if strings.HasPrefix(link, relative+"/") {
					return badBackup("backup_files_beneath_symlink", "The backup holds files beneath the symlink "+relative,
						map[string]any{"path": relative})
				}
			}
			links = append(links, restoreLink{path: relative, target: header.Linkname})
			linkPaths[relative] = true
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, io.LimitReader(reader, header.Size))
			if closeErr := file.Close(); copyErr == nil {
				copyErr = closeErr
			}
			if copyErr != nil {
				return damagedBackup(copyErr.Error(), "")
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)&0o7777); err != nil {
				return err
			}
			mtime := time.Unix(header.ModTime.Unix(), 0)
			if err := os.Chtimes(target, mtime, mtime); err != nil {
				return err
			}
		default:
			return badBackup("archive_unsupported_entry", "The archive holds an unsupported entry: "+name,
				map[string]any{"path": name})
		}
		progress.count(len(seen))
	}
	var zeros zeroChecker
	if _, err := io.Copy(&zeros, zip); err != nil {
		if errors.Is(err, errTrailingData) {
			return badBackup("backup_damaged", "The backup is damaged: "+err.Error(),
				map[string]any{"reason": err.Error(), "reasonI18n": &host.Text{Key: "archive_trailing_data"}})
		}
		return damagedBackup(err.Error(), "")
	}
	for _, link := range links {
		path := filepath.Join(staging, filepath.FromSlash(link.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(link.target, path); err != nil {
			return err
		}
	}
	for index := len(folders) - 1; index >= 0; index-- {
		folder := folders[index]
		if err := os.Chmod(folder.path, folder.mode); err != nil {
			return err
		}
		if err := os.Chtimes(folder.path, folder.mtime, folder.mtime); err != nil {
			return err
		}
	}
	return nil
}

var errTrailingData = errors.New("The archive has data after its end")

type zeroChecker struct{}

func (zeroChecker) Write(data []byte) (int, error) {
	for _, byte := range data {
		if byte != 0 {
			return 0, errTrailingData
		}
	}
	return len(data), nil
}

func checkBackupManifest(reader *tar.Reader, header *tar.Header) error {
	if header.Typeflag != tar.TypeReg || header.Name != manifestName || header.Size > 64*1024 {
		return notBackup()
	}
	data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
	if err != nil || int64(len(data)) != header.Size {
		return notBackup()
	}
	var manifest backupManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != "pigkin-config-backup" {
		return notBackup()
	}
	if manifest.Version != 1 {
		return host.NewError("backup_version", "This backup is from a newer Pigkin; update Pigkin to restore it",
			&host.Text{Key: "backup_from_newer_pigkin"})
	}
	return nil
}

// configPath checks one entry under config/ and returns its path relative to
// the config folder. Nothing outside it, beneath a link or in an excluded
// folder may be restored.
func configPath(path string) (string, error) {
	if !strings.HasPrefix(path, backupConfig+"/") {
		return "", badBackup("backup_path_outside_config", "The backup holds a file outside the configuration: "+path,
			map[string]any{"path": path})
	}
	relative := strings.TrimPrefix(path, backupConfig+"/")
	segments := strings.Split(relative, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsRune(segment, 0) {
			return "", badBackup("backup_path_unsafe", "The backup holds an unsafe path: "+path,
				map[string]any{"path": path})
		}
	}
	if backupExcluded[segments[0]] {
		return "", badBackup("backup_holds_excluded", "The backup holds "+segments[0]+", which a restore never replaces",
			map[string]any{"name": segments[0]})
	}
	return relative, nil
}

func badBackup(key, message string, args map[string]any) error {
	return host.NewError("bad_backup", message, &host.Text{Key: key, Args: args})
}

func damagedBackup(reason, key string) error {
	args := map[string]any{"reason": reason}
	if key != "" {
		args["reasonI18n"] = &host.Text{Key: key}
	}
	return badBackup("backup_damaged", "The backup is damaged: "+reason, args)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDone(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// swapConfig moves every configuration entry aside, then moves the unpacked
// backup in. The .moved marker lets a later run finish a swap cut short by a
// crash; sessions and the rest of backupExcluded never move.
func swapConfig(configDir, staging, old string) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(old, 0o755); err != nil {
		return err
	}
	// Mark the swap before moving anything, so a crash mid-loop can be finished
	// instead of having old discarded as an abandoned staging.
	if !exists(old + ".moved") {
		if err := os.WriteFile(old+".moved", nil, 0o644); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if backupExcluded[entry.Name()] {
			continue
		}
		// Entries an earlier attempt moved are gone from configDir, so a resumed swap moves only the rest.
		if err := os.Rename(filepath.Join(configDir, entry.Name()), filepath.Join(old, entry.Name())); err != nil {
			return err
		}
	}
	entries, err = os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(configDir, entry.Name())); err != nil {
			return err
		}
	}
	_ = os.RemoveAll(old)
	_ = os.RemoveAll(staging)
	_ = os.Remove(staging + ".complete")
	_ = os.Remove(old + ".moved")
	return nil
}

// settleRestores finishes an interrupted restore whose unpacking completed or
// discards one that never got that far. A backup (wait false) leaves a swap
// alone while a chat's Pi runs; a restore waits for it.
func settleRestores(ctx *Context, wait bool) error {
	parent := filepath.Dir(ctx.Env.ConfigDir)
	prefix := filepath.Base(ctx.Env.ConfigDir) + ".restore-"
	entries, err := os.ReadDir(parent)
	if err != nil {
		entries = nil
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			ids[strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".complete")] = true
		}
	}
	for id := range ids {
		staging := ctx.Env.ConfigDir + ".restore-" + id
		old := ctx.Env.ConfigDir + ".old-" + id
		if exists(staging) && exists(staging+".complete") {
			if wait {
				if err := waitForPiExit(ctx, ctx.Env.ConfigDir); err != nil {
					return err
				}
			} else if piRunning(ctx.Env.ConfigDir) {
				continue
			}
			if err := swapConfig(ctx.Env.ConfigDir, staging, old); err != nil {
				return err
			}
			continue
		}
		_ = os.RemoveAll(staging)
		_ = os.Remove(staging + ".complete")
		_ = os.RemoveAll(old)
		_ = os.Remove(old + ".moved")
	}
	return nil
}
