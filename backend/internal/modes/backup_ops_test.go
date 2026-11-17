package modes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

type backupFixture struct {
	env      host.Env
	config   string
	backups  string
	original map[string]string
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	env := testEnv(t)
	f := &backupFixture{env: env, config: env.ConfigDir, backups: backupsDir(env)}
	long := "long-skill-name-" + strings.Repeat("x", 140)
	for _, dir := range []string{
		filepath.Join(f.config, "extensions/empty"),
		filepath.Join(f.config, "npm/node_modules/tool/bin"),
		filepath.Join(f.config, "npm/node_modules/.bin"),
		filepath.Join(f.config, "skills", long),
		filepath.Join(f.config, "sessions/home"),
		filepath.Join(f.config, "bin"),
		filepath.Join(f.config, "tmp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]struct {
		data string
		mode os.FileMode
	}{
		"settings.json":                     {`{"theme":"dark"}`, 0o644},
		"auth.json":                         {`{"anthropic":{}}`, 0o600},
		"AGENTS.md":                         {"# Rules\n", 0o644},
		"npm/node_modules/tool/bin/tool.js": {"#!/usr/bin/env node\n", 0o755},
		"skills/" + long + "/SKILL.md":      {"skill\n", 0o644},
		"sessions/home/a.jsonl":             {"session a\n", 0o644},
		"bin/rg":                            {"binary", 0o644},
		"tmp/scratch":                       {"scratch", 0o644},
		"pi-debug.log":                      {"log", 0o644},
		"crashes.json":                      {"[]", 0o644},
	}
	for name, file := range files {
		if err := os.WriteFile(filepath.Join(f.config, filepath.FromSlash(name)), []byte(file.data), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../tool/bin/tool.js", filepath.Join(f.config, "npm/node_modules/.bin/tool")); err != nil {
		t.Fatal(err)
	}
	f.original = f.snapshot(t)
	return f
}

func (f *backupFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	var walk func(dir, relative string)
	walk = func(dir, relative string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			name := entry.Name()
			if relative != "" {
				name = relative + "/" + name
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			mode := fmt.Sprintf("%04o", info.Mode().Perm())
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
				result[name] = "link -> " + target
			case info.IsDir():
				result[name] = "dir " + mode
				walk(path, name)
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				result[name] = "file " + mode + " " + string(data)
			}
		}
	}
	walk(f.config, "")
	return result
}

func (f *backupFixture) create(t *testing.T) string {
	t.Helper()
	data, err := createBackup(&Context{Env: f.env})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := data["backup"].(host.Message)
	if !ok {
		t.Fatalf("backup = %v", data["backup"])
	}
	name, ok := info["name"].(string)
	if !ok || !strings.HasPrefix(name, "pigkin-config-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("backup name = %v", name)
	}
	return name
}

func (f *backupFixture) restore(t *testing.T, name string) error {
	t.Helper()
	_, err := restoreBackup(&Context{Env: f.env, Params: host.Message{"name": name}})
	return err
}

func TestBackupCreateRestoreRoundTrip(t *testing.T) {
	f := newBackupFixture(t)
	name := f.create(t)

	path := filepath.Join(f.backups, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zip, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(zip)
	first, err := reader.Next()
	if err != nil || first.Name != manifestName {
		t.Fatalf("first tar entry = %v (%v)", first, err)
	}
	manifestBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || manifest["format"] != "pigkin-config-backup" || manifest["version"] != float64(1) || manifest["piVersion"] != nil {
		t.Fatalf("manifest = %s (%v)", manifestBytes, err)
	}
	seenExcluded := false
	longSeen := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(header.Name, "sessions/") || strings.HasPrefix(header.Name, "config/bin") || strings.HasPrefix(header.Name, "config/tmp") {
			seenExcluded = true
		}
		if strings.Contains(header.Name, "long-skill-name-") {
			longSeen = true
		}
	}
	if seenExcluded || !longSeen {
		t.Fatalf("excluded = %v, long name = %v", seenExcluded, longSeen)
	}

	if err := os.WriteFile(filepath.Join(f.config, "settings.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.config, "npm")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.config, "added-later.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sessions/home/a.jsonl", "bin/rg", "pi-debug.log"} {
		if err := os.WriteFile(filepath.Join(f.config, filepath.FromSlash(name)), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.restore(t, name); err != nil {
		t.Fatal(err)
	}
	got := f.snapshot(t)
	for path, want := range f.original {
		if strings.HasPrefix(path, "sessions/") || strings.HasPrefix(path, "bin/") || path == "pi-debug.log" {
			continue
		}
		if got[path] != want {
			t.Errorf("%s = %q, want %q", path, got[path], want)
		}
	}
	if _, ok := got["added-later.json"]; ok {
		t.Error("restore left a file added after the backup")
	}
	if got["sessions/home/a.jsonl"] != "file 0644 changed" || got["bin/rg"] != "file 0644 changed" || got["pi-debug.log"] != "file 0644 changed" {
		t.Errorf("restore changed excluded state: %v", got)
	}
}

type tarItem struct {
	name, link string
	mode       int64
	kind       byte
	data       string
}

func writeCraftedBackup(t *testing.T, dir, name string, items []tarItem) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zip)
	for _, item := range items {
		header := &tar.Header{
			Name: item.name, Linkname: item.link, Mode: item.mode,
			Typeflag: item.kind, Size: int64(len(item.data)),
			ModTime: time.Unix(0, 0),
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if item.data != "" {
			if _, err := writer.Write([]byte(item.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func manifestItem() tarItem {
	return tarItem{name: manifestName, mode: 0o644, kind: tar.TypeReg,
		data: `{"format":"pigkin-config-backup","version":1,"createdAt":"2026-01-01T00:00:00.000Z","piVersion":null}`}
}

func TestBackupRestoreRefusesUnsafeArchives(t *testing.T) {
	for name, items := range map[string][]tarItem{
		"parent escape":   {{name: "config/../escape.json", mode: 0o644, kind: tar.TypeReg, data: "{}"}},
		"absolute path":   {{name: "/etc/escape.json", mode: 0o644, kind: tar.TypeReg, data: "{}"}},
		"outside config":  {{name: "other/settings.json", mode: 0o644, kind: tar.TypeReg, data: "{}"}},
		"sessions":        {{name: "config/sessions/home/a.jsonl", mode: 0o644, kind: tar.TypeReg, data: "replaced"}},
		"downloaded tool": {{name: "config/bin/rg", mode: 0o644, kind: tar.TypeReg, data: "replaced"}},
		"duplicate": {
			{name: "config/settings.json", mode: 0o644, kind: tar.TypeReg, data: "1"},
			{name: "config/settings.json", mode: 0o644, kind: tar.TypeReg, data: "2"},
		},
		"beneath symlink": {
			{name: "config/link", link: "/tmp", mode: 0o777, kind: tar.TypeSymlink},
			{name: "config/link/escape.json", mode: 0o644, kind: tar.TypeReg, data: "{}"},
		},
		"symlink over files": {
			{name: "config/folder/a.json", mode: 0o644, kind: tar.TypeReg, data: "{}"},
			{name: "config/folder", link: "/tmp", mode: 0o777, kind: tar.TypeSymlink},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBackupFixture(t)
			writeCraftedBackup(t, f.backups, "crafted.tar.gz", append([]tarItem{manifestItem()}, items...))
			before := f.snapshot(t)
			err := f.restore(t, "crafted.tar.gz")
			hostErr, ok := err.(*host.Error)
			if !ok || hostErr.Code != "bad_backup" {
				t.Fatalf("got %v, want bad_backup", err)
			}
			if got := f.snapshot(t); !equalSnapshot(before, got) {
				t.Fatal("a refused backup changed the config folder")
			}
			entries, _ := os.ReadDir(filepath.Dir(f.config))
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "config.restore-") || strings.HasPrefix(entry.Name(), "config.old-") {
					t.Fatalf("left staging folders: %v", entries)
				}
			}
		})
	}
}

func equalSnapshot(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func TestBackupRestoreRejectsNonBackupsAndBadNames(t *testing.T) {
	f := newBackupFixture(t)
	writeCraftedBackup(t, f.backups, "other.tar.gz", []tarItem{{name: "config/settings.json", mode: 0o644, kind: tar.TypeReg, data: "{}"}})
	if err := f.restore(t, "other.tar.gz"); !codeIs(err, "not_backup") {
		t.Fatalf("got %v, want not_backup", err)
	}
	writeCraftedBackup(t, f.backups, "newer.tar.gz", []tarItem{{name: manifestName, mode: 0o644, kind: tar.TypeReg, data: `{"format":"pigkin-config-backup","version":2}`}})
	if err := f.restore(t, "newer.tar.gz"); !codeIs(err, "backup_version") {
		t.Fatalf("got %v, want backup_version", err)
	}
	for _, name := range []string{"../config/settings.json", "sub/x.tar.gz", ".hidden.tar.gz", "x.zip"} {
		if err := f.restore(t, name); !codeIs(err, "bad_name") {
			t.Fatalf("%s: got %v, want bad_name", name, err)
		}
	}
	if err := f.restore(t, "missing.tar.gz"); !codeIs(err, "not_found") {
		t.Fatalf("got %v, want not_found", err)
	}
}

func codeIs(err error, code string) bool {
	hostErr, ok := err.(*host.Error)
	return ok && hostErr.Code == code
}

func fakeProc(t *testing.T, entries map[string][2]string) string {
	t.Helper()
	dir := t.TempDir()
	for pid, files := range entries {
		process := filepath.Join(dir, pid)
		if err := os.MkdirAll(process, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(process, "cmdline"), []byte(files[0]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(process, "environ"), []byte(files[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIGKIN_PROC_DIR", dir)
	return dir
}

func TestBackupCreateLockAndStaleTakeover(t *testing.T) {
	f := newBackupFixture(t)
	lock := filepath.Join(f.env.Root, "config.lock")
	if err := os.Symlink(strconv.Itoa(os.Getpid()), lock); err != nil {
		t.Fatal(err)
	}
	if _, err := createBackup(&Context{Env: f.env}); !codeIs(err, "busy") {
		t.Fatalf("got %v, want busy", err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	// A holder whose pid no longer exists is stale and taken over.
	if err := os.Symlink("999999", lock); err != nil {
		t.Fatal(err)
	}
	if _, err := createBackup(&Context{Env: f.env}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("config lock left behind: %v", err)
	}
}

func TestBackupRestoreWaitsForPiAndTimesOut(t *testing.T) {
	f := newBackupFixture(t)
	name := f.create(t)
	if err := os.WriteFile(filepath.Join(f.config, "settings.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	proc := fakeProc(t, map[string][2]string{
		"4242": {"node pi-coding-agent/dist/cli.js", "\x00PI_CODING_AGENT_DIR=" + f.config + "\x00"},
	})
	previous := piExitGrace
	piExitGrace = 400 * time.Millisecond
	t.Cleanup(func() { piExitGrace = previous })
	err := f.restore(t, name)
	if !codeIs(err, "pi_running") {
		t.Fatalf("got %v, want pi_running", err)
	}
	hostErr, _ := err.(*host.Error)
	if hostErr.I18n == nil || hostErr.I18n.Key != "pi_running" {
		t.Fatalf("i18n = %v", hostErr.I18n)
	}
	if got, _ := os.ReadFile(filepath.Join(f.config, "settings.json")); string(got) != "{broken" {
		t.Fatalf("config changed while Pi ran: %q", got)
	}
	if err := os.RemoveAll(proc); err != nil {
		t.Fatal(err)
	}
	if err := f.restore(t, name); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.config, "settings.json")); string(got) != `{"theme":"dark"}` {
		t.Fatalf("restored settings = %q", got)
	}
}

func TestBackupRestoreReadsPaxArchive(t *testing.T) {
	f := newBackupFixture(t)
	long := "config/skills/" + strings.Repeat("long-skill-name-", 10) + "/SKILL.md"
	path := filepath.Join(f.backups, "pax.tar.gz")
	if err := os.MkdirAll(f.backups, 0o755); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	writeTSEntry(t, zip, manifestItem(), "")
	writeTSEntry(t, zip, tarItem{name: long, mode: 0o644, kind: tar.TypeReg, data: "pax skill\n"}, "")
	writeTSEntry(t, zip, tarItem{name: "config/settings.json", mode: 0o644, kind: tar.TypeReg, data: `{"from":"pax"}`}, "")
	writeTSZeroBlocks(t, zip)
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.restore(t, "pax.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(f.config, filepath.FromSlash(strings.TrimPrefix(long, "config/")))); err != nil || string(got) != "pax skill\n" {
		t.Fatalf("pax file = %q (%v)", got, err)
	}
}

// writeTSEntry writes exactly the blocks the TypeScript tar writer writes:
// ASCII ustar fields, a preceding pax header for names that don't fit, and
// zero padding.
func writeTSEntry(t *testing.T, out io.Writer, item tarItem, paxPath string) {
	t.Helper()
	name := item.name
	if item.kind == tar.TypeDir {
		name += "/"
	}
	headerName, linkName := name, item.link
	var records string
	if paxPath != "" || !fitsUstar(name, 100) {
		records += tsPaxRecord("path", name)
		headerName = ""
	}
	if !fitsUstar(linkName, 100) {
		records += tsPaxRecord("linkpath", linkName)
		linkName = ""
	}
	if records != "" {
		if _, err := out.Write(tsHeader("PaxHeader", 'x', int64(len(records)), 0o644, 0, "")); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(out, records); err != nil {
			t.Fatal(err)
		}
		writeTSPadding(t, out, int64(len(records)))
	}
	kind := byte('0')
	switch item.kind {
	case tar.TypeDir:
		kind = '5'
	case tar.TypeSymlink:
		kind = '2'
	}
	if _, err := out.Write(tsHeader(headerName, kind, int64(len(item.data)), item.mode, 0, linkName)); err != nil {
		t.Fatal(err)
	}
	if len(item.data) > 0 {
		if _, err := io.WriteString(out, item.data); err != nil {
			t.Fatal(err)
		}
		writeTSPadding(t, out, int64(len(item.data)))
	}
}

func writeTSPadding(t *testing.T, out io.Writer, size int64) {
	t.Helper()
	padding := make([]byte, (512-size%512)%512)
	if _, err := out.Write(padding); err != nil {
		t.Fatal(err)
	}
}

func writeTSZeroBlocks(t *testing.T, out io.Writer) {
	t.Helper()
	if _, err := out.Write(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
}

func fitsUstar(value string, length int) bool {
	if len(value) >= length {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return false
		}
	}
	return true
}

func tsPaxRecord(key, value string) string {
	body := " " + key + "=" + value + "\n"
	length := len(body)
	for len(fmt.Sprint(length))+len(body) != length {
		length = len(fmt.Sprint(length)) + len(body)
	}
	return fmt.Sprint(length) + body
}

func tsHeader(name string, kind byte, size, mode, mtime int64, link string) []byte {
	header := make([]byte, 512)
	copy(header[0:100], name)
	copy(header[100:108], []byte(fmt.Sprintf("%07o\x00", mode&0o7777)))
	copy(header[124:136], []byte(fmt.Sprintf("%011o\x00", size)))
	copy(header[136:148], []byte(fmt.Sprintf("%011o\x00", mtime)))
	copy(header[148:156], "        ")
	header[156] = kind
	copy(header[157:257], link)
	copy(header[257:265], "ustar\x0000")
	sum := 0
	for _, byte := range header {
		sum += int(byte)
	}
	copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	return header
}

func TestBackupProgressLines(t *testing.T) {
	f := newBackupFixture(t)
	client, server := net.Pipe()
	defer client.Close()
	connection := host.NewConnection(server)
	lines := make(chan host.Message, 16)
	go func() {
		scanner := host.ScanLines(client)
		for scanner.Scan() {
			var message host.Message
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				lines <- message
			}
		}
		close(lines)
	}()
	if _, err := createBackup(&Context{Env: f.env, Conn: connection}); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	seen := 0
	for line := range lines {
		if line["type"] != "pigkin_progress" {
			continue
		}
		seen++
		if !strings.HasPrefix(line["line"].(string), "Backed up ") {
			t.Fatalf("progress line = %v", line["line"])
		}
		i18n, ok := line["lineI18n"].(map[string]any)
		if !ok || i18n["key"] != "backup_progress_backed_up" {
			t.Fatalf("progress i18n = %v", line["lineI18n"])
		}
	}
	if seen == 0 {
		t.Fatal("no progress lines")
	}
}

func TestSettleRestoresCleansUnfinishedAndSwapsFinished(t *testing.T) {
	f := newBackupFixture(t)
	staging := f.config + ".restore-1"
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "settings.json"), []byte("{half"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.create(t)
	if got, _ := os.ReadFile(filepath.Join(f.config, "settings.json")); string(got) != `{"theme":"dark"}` {
		t.Fatalf("unfinished restore changed config: %q", got)
	}

	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging+".complete", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "settings.json"), []byte(`{"restored":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.create(t)
	if got, _ := os.ReadFile(filepath.Join(f.config, "settings.json")); string(got) != `{"restored":true}` {
		t.Fatalf("finished restore not swapped: %q", got)
	}
	names := []string{}
	entries, _ := os.ReadDir(filepath.Dir(f.config))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"backups", "config", "home", "prefix", "root"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("leftover folders = %v", names)
	}
}
