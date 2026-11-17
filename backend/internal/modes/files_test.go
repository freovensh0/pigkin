package modes

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

type filesRun struct {
	t       *testing.T
	conn    net.Conn
	scanner *bufio.Scanner
	env     host.Env
	root    string
	nextID  int
}

// startFiles runs the files mode over a pipe and returns its app side, once
// the ready line has arrived.
func startFiles(t *testing.T, params host.Message) *filesRun {
	t.Helper()
	app, hostSide := net.Pipe()
	connection := host.NewConnection(hostSide)
	env := host.Env{ConfigDir: filepath.Join(t.TempDir(), "pi")}
	go func() { _, _ = files(&Context{Env: env, Params: params, Conn: connection}) }()
	run := &filesRun{t: t, conn: app, scanner: host.ScanLines(app), env: env}
	t.Cleanup(func() {
		_ = app.Close()
		_ = hostSide.Close()
	})
	ready := run.next()
	if ready["type"] != "pigkin_files_ready" {
		t.Fatalf("got %v, want pigkin_files_ready", ready)
	}
	return run
}

func newFiles(t *testing.T) (*filesRun, string, string) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := startFiles(t, host.Message{})
	run.root = workspace
	return run, workspace, outside
}

// send writes one line without waiting for a reply.
func (r *filesRun) send(message host.Message) {
	r.t.Helper()
	line, err := host.EncodeLine(message)
	if err != nil {
		r.t.Fatal(err)
	}
	_ = r.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := r.conn.Write(line); err != nil {
		r.t.Fatal(err)
	}
}

// next reads the next line.
func (r *filesRun) next() host.Message {
	r.t.Helper()
	_ = r.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if !r.scanner.Scan() {
		r.t.Fatal("no line from the files mode:", r.scanner.Err())
	}
	var message host.Message
	if err := json.Unmarshal(r.scanner.Bytes(), &message); err != nil {
		r.t.Fatal(err)
	}
	return message
}

// request sends kind with a fresh id and reads until its reply.
func (r *filesRun) request(kind string, fields host.Message) host.Message {
	r.t.Helper()
	r.nextID++
	message := host.Message{"type": kind, "id": r.nextID, "root": r.root}
	for name, value := range fields {
		message[name] = value
	}
	r.send(message)
	for {
		reply := r.next()
		if id, ok := reply["id"].(float64); ok && int(id) == r.nextID {
			return reply
		}
	}
}

func (r *filesRun) list(path string) host.Message {
	return r.request("pigkin_files_list", host.Message{"path": path})
}

func (r *filesRun) read(path string) host.Message {
	return r.request("pigkin_files_read", host.Message{"path": path})
}

func (r *filesRun) mkdir(path, name string) host.Message {
	return r.request("pigkin_files_mkdir", host.Message{"path": path, "name": name})
}

func entryNames(t *testing.T, reply host.Message) []string {
	t.Helper()
	entries, ok := reply["entries"].([]any)
	if !ok {
		t.Fatalf("got %v, want entries", reply)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.(map[string]any)["name"].(string))
	}
	return names
}

func errorKey(reply host.Message) string {
	i18n, _ := reply["errorI18n"].(map[string]any)
	key, _ := i18n["key"].(string)
	return key
}

func TestFilesListsFoldersFirstThenByNaturalName(t *testing.T) {
	run, workspace, _ := newFiles(t)
	for _, name := range []string{"project10", "Project2", ".config"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"b.txt", "A.txt", "file10", "file9", ".env"} {
		if err := os.WriteFile(filepath.Join(workspace, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := entryNames(t, run.list("."))
	want := []string{".config", "Project2", "project10", "src", ".env", "A.txt", "b.txt", "file9", "file10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilesDescribesEntriesWithRelativePaths(t *testing.T) {
	run, workspace, _ := newFiles(t)
	file := filepath.Join(workspace, "src", "main.ts")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	reply := run.list("src")
	if reply["path"] != "src" {
		t.Fatalf("got path %v", reply["path"])
	}
	entries := reply["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("got %v", entries)
	}
	lib := entries[0].(map[string]any)
	if lib["name"] != "lib" || lib["path"] != "src/lib" || lib["kind"] != "directory" {
		t.Fatalf("got %v", lib)
	}
	main := entries[1].(map[string]any)
	if main["name"] != "main.ts" || main["path"] != "src/main.ts" || main["kind"] != "file" {
		t.Fatalf("got %v", main)
	}
	if main["size"].(float64) != 5 || int64(main["modified"].(float64)) != info.ModTime().UnixMilli() {
		t.Fatalf("got %v", main)
	}

	if got := run.list("./src/"); got["path"] != "src" {
		t.Fatalf("got path %v", got["path"])
	}
	root := run.list(".")
	if root["path"] != "." {
		t.Fatalf("got path %v", root["path"])
	}
	names := root["entries"].([]any)
	if len(names) != 1 || names[0].(map[string]any)["path"] != "src" {
		t.Fatalf("got %v", names)
	}
}

func TestFilesFollowsLinksAndSkipsDanglingAndEscapingOnes(t *testing.T) {
	run, workspace, outside := newFiles(t)
	pairs := [][2]string{
		{filepath.Join(workspace, "src"), "src-link"},
		{filepath.Join(workspace, "gone.md"), "dangling.md"},
		{outside, "escape"},
		{filepath.Join(outside, "secret.txt"), "secret.txt"},
	}
	for _, pair := range pairs {
		if err := os.Symlink(pair[0], filepath.Join(workspace, pair[1])); err != nil {
			t.Skip("symlinks unsupported:", err)
		}
	}
	got := entryNames(t, run.list("."))
	if len(got) != 2 || got[0] != "src" || got[1] != "src-link" {
		t.Fatalf("got %q, want [src src-link]", got)
	}
}

func TestFilesReportsListingFailures(t *testing.T) {
	run, workspace, outside := newFiles(t)
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	missing := run.list("missing")
	if missing["path"] != "missing" || len(missing["entries"].([]any)) != 0 || missing["error"] != "This file or folder no longer exists." || errorKey(missing) != "files_gone" {
		t.Fatalf("got %v", missing)
	}
	if reply := run.list("notes.txt"); reply["error"] != "This isn't a folder." || errorKey(reply) != "not_a_folder" {
		t.Fatalf("got %v", reply)
	}
	if reply := run.list("../outside"); reply["error"] != "That path is outside the workspace." || errorKey(reply) != "files_outside_workspace" {
		t.Fatalf("got %v", reply)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if reply := run.list("escape"); reply["error"] != "That path is outside the workspace." {
		t.Fatalf("got %v", reply)
	}

	nowhere := filepath.Join(filepath.Dir(workspace), "nowhere")
	missingRoot := run.request("pigkin_files_list", host.Message{"root": nowhere, "path": "."})
	if missingRoot["error"] != "Folder not found: "+nowhere || errorKey(missingRoot) != "folder_not_found" {
		t.Fatalf("got %v", missingRoot)
	}
	if reply := run.request("pigkin_files_list", host.Message{"root": "relative", "path": "."}); reply["error"] != "Folder not found: relative" {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesIgnoresMalformedRequests(t *testing.T) {
	run, _, _ := newFiles(t)
	run.send(host.Message{"type": "pigkin_files_list", "id": "1", "root": run.root, "path": "."})
	run.send(host.Message{"type": "pigkin_files_list", "id": 1000, "path": "."})
	run.send(host.Message{"type": "pigkin_files_mkdir", "id": 1001, "root": run.root, "path": "."})
	reply := run.list(".")
	if reply["id"].(float64) != float64(run.nextID) || reply["type"] != "pigkin_files_listing" {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesReadsText(t *testing.T) {
	run, workspace, _ := newFiles(t)
	text := "héllo 👋\n"
	if err := os.WriteFile(filepath.Join(workspace, "src", "hello.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "empty.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "icon.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	hello := run.read("src/hello.md")
	if hello["type"] != "pigkin_files_file" || hello["path"] != "src/hello.md" || hello["kind"] != "text" || hello["content"] != text || hello["size"].(float64) != float64(len(text)) {
		t.Fatalf("got %v", hello)
	}
	if empty := run.read("empty.txt"); empty["size"].(float64) != 0 || empty["kind"] != "text" || empty["content"] != "" {
		t.Fatalf("got %v", empty)
	}
	if svg := run.read("icon.svg"); svg["kind"] != "text" || svg["content"] != "<svg/>" {
		t.Fatalf("got %v", svg)
	}
}

func TestFilesReadsImagesAsBase64(t *testing.T) {
	run, workspace, _ := newFiles(t)
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0xff}
	if err := os.WriteFile(filepath.Join(workspace, "pic.PNG"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	reply := run.read("pic.PNG")
	if reply["path"] != "pic.PNG" || reply["kind"] != "image" || reply["size"].(float64) != float64(len(png)) {
		t.Fatalf("got %v", reply)
	}
	decoded, err := base64.StdEncoding.DecodeString(reply["content"].(string))
	if err != nil || !bytes.Equal(decoded, png) {
		t.Fatalf("got %q, want %v", reply["content"], png)
	}
}

func TestFilesCallsBinaryWithoutContent(t *testing.T) {
	run, workspace, _ := newFiles(t)
	if err := os.WriteFile(filepath.Join(workspace, "nul.dat"), []byte("abc\x00def"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "latin1.txt"), []byte{0x63, 0x61, 0x66, 0xe9}, 0o600); err != nil {
		t.Fatal(err)
	}

	nul := run.read("nul.dat")
	if nul["size"].(float64) != 7 || nul["kind"] != "binary" {
		t.Fatalf("got %v", nul)
	}
	if _, ok := nul["content"]; ok {
		t.Fatalf("got content in %v", nul)
	}
	if latin := run.read("latin1.txt"); latin["size"].(float64) != 4 || latin["kind"] != "binary" {
		t.Fatalf("got %v", latin)
	}
}

func TestFilesCallsTextOverTheLimitTooLargeWithoutContent(t *testing.T) {
	run, workspace, _ := newFiles(t)
	size := textLimit + 1
	if err := os.WriteFile(filepath.Join(workspace, "big.log"), bytes.Repeat([]byte("a"), size), 0o600); err != nil {
		t.Fatal(err)
	}
	reply := run.read("big.log")
	if reply["path"] != "big.log" || reply["size"].(float64) != float64(size) || reply["kind"] != "too_large" {
		t.Fatalf("got %v", reply)
	}
	if _, ok := reply["content"]; ok {
		t.Fatalf("got content in %v", reply)
	}
}

func TestFilesCallsALargeFileWithABinarySniffBinary(t *testing.T) {
	run, workspace, _ := newFiles(t)
	content := append(bytes.Repeat([]byte("a"), sniffBytes-1), 0)
	content = append(content, bytes.Repeat([]byte("a"), textLimit-sniffBytes+2)...)
	if err := os.WriteFile(filepath.Join(workspace, "big.dat"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if reply := run.read("big.dat"); reply["kind"] != "binary" {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesSniffToleratesATruncatedRuneAtTheCut(t *testing.T) {
	run, workspace, _ := newFiles(t)
	content := append(bytes.Repeat([]byte("a"), sniffBytes-1), 0xE2)
	content = append(content, bytes.Repeat([]byte("a"), textLimit-sniffBytes+2)...)
	if err := os.WriteFile(filepath.Join(workspace, "big.log"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if reply := run.read("big.log"); reply["kind"] != "too_large" {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesCallsAnImageOverTheLimitTooLarge(t *testing.T) {
	run, workspace, _ := newFiles(t)
	file, err := os.Create(filepath.Join(workspace, "big.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(imageLimit + 1); err != nil {
		t.Fatal(err)
	}
	reply := run.read("big.png")
	if reply["kind"] != "too_large" || reply["size"].(float64) != float64(imageLimit+1) {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesRefusesFoldersAndPathsOutOfTheWorkspace(t *testing.T) {
	run, workspace, outside := newFiles(t)
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(workspace, "secret.txt")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if reply := run.read("src"); reply["path"] != "src" || reply["error"] != "This is a folder." || errorKey(reply) != "files_is_folder" {
		t.Fatalf("got %v", reply)
	}
	if reply := run.read("secret.txt"); reply["error"] != "That path is outside the workspace." || errorKey(reply) != "files_outside_workspace" {
		t.Fatalf("got %v", reply)
	}
	if reply := run.read("../outside/secret.txt"); reply["error"] != "That path is outside the workspace." {
		t.Fatalf("got %v", reply)
	}
	if reply := run.read("missing.txt"); reply["error"] != "This file or folder no longer exists." || errorKey(reply) != "files_gone" {
		t.Fatalf("got %v", reply)
	}
}

func TestFilesMakesAFolderAndNamesItRelativeToTheWorkspace(t *testing.T) {
	run, workspace, _ := newFiles(t)
	reply := run.mkdir("src", " lib ")
	if reply["type"] != "pigkin_files_made" || reply["path"] != "src/lib" {
		t.Fatalf("got %v", reply)
	}
	info, err := os.Stat(filepath.Join(workspace, "src", "lib"))
	if err != nil || !info.IsDir() {
		t.Fatalf("got %v, %v, want a folder", info, err)
	}
	if docs := run.mkdir(".", "docs"); docs["path"] != "docs" {
		t.Fatalf("got %v", docs)
	}
}

func TestFilesRefusesTakenAndInvalidFolderNames(t *testing.T) {
	run, workspace, _ := newFiles(t)
	if err := os.WriteFile(filepath.Join(workspace, "taken"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src", "taken"} {
		reply := run.mkdir(".", name)
		if reply["error"] != "A file or folder named "+name+" already exists." || errorKey(reply) != "files_name_taken" {
			t.Fatalf("%q: got %v", name, reply)
		}
	}
	for _, name := range []string{"", "  ", ".", "..", "a/b", "a\x00b"} {
		reply := run.mkdir(".", name)
		if error, _ := reply["error"].(string); !strings.Contains(error, "Folder names") || errorKey(reply) != "files_folder_name_invalid" {
			t.Fatalf("%q: got %v", name, reply)
		}
		if _, ok := reply["path"]; ok {
			t.Fatalf("%q: got a path in %v", name, reply)
		}
	}
	if reply := run.mkdir("..", "sneaky"); reply["error"] != "That path is outside the workspace." {
		t.Fatalf("got %v", reply)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(workspace), "sneaky")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want no folder outside the workspace", err)
	}
}

func TestFilesRefusesRemoteWorkspaces(t *testing.T) {
	_, err := files(&Context{Env: host.Env{}, Params: host.Message{"hostId": "devbox"}, Conn: nil})
	var refusal *host.Error
	if !errors.As(err, &refusal) || refusal.Code != "bad_params" || refusal.I18n != nil {
		t.Fatalf("got %v, want a bad_params refusal", err)
	}

	run, _, _ := newFiles(t)
	remote := "/home/me/project"
	sum := sha256.Sum256([]byte(remote))
	standIn := filepath.Join(filepath.Dir(run.env.ConfigDir), "remotes", "devbox", hex.EncodeToString(sum[:])[:16])
	if err := os.MkdirAll(standIn, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"hostId":"devbox","path":"` + remote + `"}`
	if err := os.WriteFile(filepath.Join(standIn, "remote.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	listing := run.request("pigkin_files_list", host.Message{"root": standIn, "path": "."})
	if listing["error"] != remoteNotPorted || listing["code"] != "bad_params" || errorKey(listing) != "" {
		t.Fatalf("got %v", listing)
	}
	if entries := listing["entries"].([]any); len(entries) != 0 {
		t.Fatalf("got %v", entries)
	}
	read := run.request("pigkin_files_read", host.Message{"root": standIn, "path": "x.txt"})
	if read["error"] != remoteNotPorted || read["code"] != "bad_params" {
		t.Fatalf("got %v", read)
	}
	made := run.request("pigkin_files_mkdir", host.Message{"root": standIn, "path": ".", "name": "x"})
	if made["error"] != remoteNotPorted || made["code"] != "bad_params" {
		t.Fatalf("got %v", made)
	}
	if _, err := os.Stat(filepath.Join(standIn, "x")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want no folder made in the stand-in", err)
	}
}
