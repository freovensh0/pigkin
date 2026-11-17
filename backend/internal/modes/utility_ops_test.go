package modes

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func testEnv(t *testing.T) host.Env {
	t.Helper()
	base := t.TempDir()
	env := host.Env{
		Root:      filepath.Join(base, "root"),
		ConfigDir: filepath.Join(base, "config"),
		Home:      filepath.Join(base, "home"),
		Prefix:    filepath.Join(base, "prefix"),
	}
	for _, dir := range []string{env.Root, env.ConfigDir, env.Home, filepath.Join(env.Prefix, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func runOp(t *testing.T, env host.Env, op string, params any) host.Message {
	t.Helper()
	result := answerUtility(&Context{Env: env}, op, params)
	if result["type"] != "pigkin_result" {
		t.Fatalf("%s: result type = %v", op, result["type"])
	}
	return result
}

func opData(t *testing.T, result host.Message) host.Message {
	t.Helper()
	if result["ok"] != true {
		t.Fatalf("operation failed: %v", result)
	}
	data, ok := result["data"].(host.Message)
	if !ok {
		t.Fatalf("data = %T", result["data"])
	}
	return data
}

func opCode(result host.Message) string {
	code, _ := result["code"].(string)
	return code
}

var (
	testUser = host.Message{"role": "user", "content": "hello", "timestamp": 1}
	testAid  = host.Message{
		"role":    "assistant",
		"content": []any{host.Message{"type": "text", "text": "hi there"}},
		"api":     "openai-responses", "provider": "p", "model": "m",
		"usage":      host.Message{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2},
		"stopReason": "stop", "timestamp": 2,
	}
)

// writeSession writes a Pi session with one branch, as the vitest fixtures do.
func writeSession(t *testing.T, env host.Env, cwd, id string, messages []host.Message) string {
	t.Helper()
	name := "--" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-") + "--"
	dir := filepath.Join(env.ConfigDir, "sessions", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-12T00-00-00-000Z_"+id+".jsonl")
	lines := []any{host.Message{"type": "session", "version": 3, "id": id, "timestamp": "2026-10-12T00:00:00.000Z", "cwd": cwd}}
	for index, message := range messages {
		var parent any
		if index > 0 {
			parent = fmt.Sprintf("e%d", index-1)
		}
		lines = append(lines, host.Message{
			"type": "message", "id": fmt.Sprintf("e%d", index), "parentId": parent,
			"timestamp": "2026-10-12T00:00:01.000Z", "message": message,
		})
	}
	writeLines(t, path, lines)
	return path
}

func writeLines(t *testing.T, path string, lines []any) {
	t.Helper()
	var contents strings.Builder
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		contents.Write(data)
		contents.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(contents.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionByID(t *testing.T, data host.Message, id string) host.Message {
	t.Helper()
	for _, item := range data["sessions"].([]host.Message) {
		if item["id"] == id {
			return item
		}
	}
	t.Fatalf("no session %q in %v", id, data)
	return nil
}

func TestSessionsOp(t *testing.T) {
	env := testEnv(t)
	if data := opData(t, runOp(t, env, "sessions", host.Message{})); len(data["sessions"].([]host.Message)) != 0 {
		t.Fatal("a fresh config has no sessions")
	}

	kept, lost := env.Home, filepath.Join(env.Home, "gone")
	writeSession(t, env, kept, "kept", []host.Message{testUser, testAid})
	writeSession(t, env, lost, "lost", []host.Message{testUser, testAid})
	data := opData(t, runOp(t, env, "sessions", host.Message{}))
	if got := sessionByID(t, data, "kept"); got["cwdExists"] != true {
		t.Fatalf("kept = %v", got)
	}
	if got := sessionByID(t, data, "lost"); got["cwdExists"] != false {
		t.Fatalf("lost = %v", got)
	}
}

func TestSessionsOpRemoteStandIn(t *testing.T) {
	env := testEnv(t)
	hostID, remote := "0123456789ab", "/srv/api"
	standIn := filepath.Join(filepath.Dir(env.ConfigDir), "remotes", hostID, pathHash(remote))
	if err := os.MkdirAll(standIn, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("{\"version\":1,\"hostId\":%q,\"path\":%q}\n", hostID, remote)
	if err := os.WriteFile(filepath.Join(standIn, "remote.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSession(t, env, standIn, "remote", []host.Message{testUser, testAid})
	writeSession(t, env, env.Home, "local", []host.Message{testUser, testAid})
	data := opData(t, runOp(t, env, "sessions", host.Message{}))
	remoteSession := sessionByID(t, data, "remote")
	if remoteSession["remote"] == nil || remoteSession["cwdExists"] != true {
		t.Fatalf("remote session = %v", remoteSession)
	}
	want := host.Message{"hostId": hostID, "path": remote}
	if !reflect.DeepEqual(remoteSession["remote"], want) {
		t.Fatalf("remote = %v, want %v", remoteSession["remote"], want)
	}
	if item := sessionByID(t, data, "local"); item["remote"] != nil {
		t.Fatalf("local session is remote: %v", item)
	}
}

func TestSessionsOpCountsSubagentsAndCutsFirstMessage(t *testing.T) {
	env := testEnv(t)
	long := strings.Repeat("é", 205)
	parent := writeSession(t, env, env.Home, "parent", []host.Message{{"role": "user", "content": long}, testAid})
	writeSession(t, env, env.Home, "lonely", []host.Message{testUser, testAid})
	folder := filepath.Join(filepath.Dir(parent), "subagents", "parent")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if err := os.WriteFile(filepath.Join(folder, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := opData(t, runOp(t, env, "sessions", host.Message{}))
	if got := sessionByID(t, data, "parent")["subagents"]; got != 2 {
		t.Fatalf("subagents = %v, want 2", got)
	}
	if got := sessionByID(t, data, "lonely")["subagents"]; got != 0 {
		t.Fatalf("subagents = %v, want 0", got)
	}
	if got := sessionByID(t, data, "parent")["firstMessage"]; got != strings.Repeat("é", 200) {
		t.Fatalf("firstMessage has %d runes", len([]rune(got.(string))))
	}
}

func TestSessionMessagesOp(t *testing.T) {
	env := testEnv(t)
	path := writeSession(t, env, env.Home, "s1", []host.Message{testUser, testAid})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data := opData(t, runOp(t, env, "session_messages", host.Message{"path": path}))
	messages := data["messages"].([]json.RawMessage)
	want := []json.RawMessage{}
	for _, message := range []host.Message{testUser, testAid} {
		encoded, _ := json.Marshal(message)
		want = append(want, encoded)
	}
	if !reflect.DeepEqual(orderedJSON(messages), orderedJSON(want)) {
		t.Fatalf("messages = %s, want %s", data["messages"], want)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("session file changed: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(env.ConfigDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(env.ConfigDir, "settings.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, code := range map[string]string{outside: "outside_sessions", filepath.Join(env.ConfigDir, "sessions", "gone.jsonl"): "not_found"} {
		result := runOp(t, env, "session_messages", host.Message{"path": path})
		if result["ok"] != false || opCode(result) != code {
			t.Fatalf("%s: %v", path, result)
		}
	}
}

func orderedJSON(raw []json.RawMessage) []any {
	result := make([]any, 0, len(raw))
	for _, value := range raw {
		var decoded any
		_ = json.Unmarshal(value, &decoded)
		result = append(result, decoded)
	}
	return result
}

func TestDeleteSessionsOp(t *testing.T) {
	env := testEnv(t)
	sessions := filepath.Join(env.ConfigDir, "sessions", "--project--")
	outside := filepath.Join(env.ConfigDir, "settings.json")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := func(name, contents string) string {
		path := filepath.Join(sessions, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	header := `{"type":"session","version":3,"id":"aaa","timestamp":"","cwd":"/"}` + "\n"

	t.Run("deletes all files and counts a missing one", func(t *testing.T) {
		a := session("a.jsonl", "{}\n")
		b := session("b.jsonl", "{}\n")
		result := runOp(t, env, "delete_sessions", host.Message{"paths": []any{filepath.Join(sessions, "gone.jsonl"), a, b}})
		if result["ok"] != true {
			t.Fatal(result)
		}
		for _, path := range []string{a, b} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("%s still exists: %v", path, err)
			}
		}
	})

	t.Run("checks every path before deleting", func(t *testing.T) {
		a := session("a.jsonl", "{}\n")
		escape := filepath.Join(sessions, "..", "..", "settings.json")
		result := runOp(t, env, "delete_sessions", host.Message{"paths": []any{a, escape}})
		if opCode(result) != "outside_sessions" {
			t.Fatal(result)
		}
		if _, err := os.Stat(a); err != nil {
			t.Fatalf("a was deleted: %v", err)
		}
		if result := runOp(t, env, "delete_sessions", host.Message{"paths": "a.jsonl"}); opCode(result) != "bad_params" {
			t.Fatal(result)
		}
	})

	t.Run("deletes subagent transcripts with their session", func(t *testing.T) {
		file := session("a.jsonl", header)
		folder := filepath.Join(sessions, "subagents", "aaa")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(folder, "child.jsonl")
		if err := os.WriteFile(child, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result := runOp(t, env, "delete_sessions", host.Message{"paths": []any{file}}); result["ok"] != true {
			t.Fatal(result)
		}
		for _, path := range []string{file, child} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("%s still exists: %v", path, err)
			}
		}
	})

	t.Run("refuses a subagent folder that links outside sessions", func(t *testing.T) {
		env := testEnv(t)
		project := filepath.Join(env.ConfigDir, "sessions", "--project--")
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		outsideFolder := filepath.Join(env.ConfigDir, "elsewhere", "aaa")
		if err := os.MkdirAll(outsideFolder, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(outsideFolder, "keep.txt")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(project, "subagents")
		if err := os.Symlink(filepath.Join(env.ConfigDir, "elsewhere"), link); err != nil {
			t.Skip("symlinks unsupported:", err)
		}
		file := filepath.Join(project, "a.jsonl")
		if err := os.WriteFile(file, []byte(header), 0o644); err != nil {
			t.Fatal(err)
		}
		if result := runOp(t, env, "delete_sessions", host.Message{"paths": []any{file}}); opCode(result) != "outside_sessions" {
			t.Fatal(result)
		}
		for _, path := range []string{sentinel, file} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s was deleted: %v", path, err)
			}
		}
	})
}

func TestSaveAttachmentsOp(t *testing.T) {
	env := testEnv(t)
	b64 := func(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }
	file := func(name, text string) any {
		return host.Message{"name": name, "data": b64(text)}
	}

	paths := func(files ...any) []string {
		t.Helper()
		result := runOp(t, env, "save_attachments", host.Message{"files": files})
		if result["ok"] != true {
			t.Fatal(result)
		}
		values := result["data"].(host.Message)["paths"].([]string)
		for _, path := range values {
			if filepath.Dir(filepath.Dir(path)) != filepath.Join(env.Root, "attachments") {
				t.Fatalf("path outside attachments: %s", path)
			}
		}
		return values
	}

	written := paths(file("notes.txt", "hello"), file("data.csv", "a,b"))
	if filepath.Base(written[0]) != "notes.txt" || filepath.Base(written[1]) != "data.csv" {
		t.Fatalf("paths = %v", written)
	}
	for index, want := range []string{"hello", "a,b"} {
		data, err := os.ReadFile(written[index])
		if err != nil || string(data) != want {
			t.Fatalf("file %d = %q (%v)", index, data, err)
		}
	}

	escaped := paths(file("../../escape.txt", "x"), file(`..\win.txt`, "x"), file(".bashrc", "x"), file("..", "x"))
	want := []string{"escape.txt", "win.txt", "bashrc", "file"}
	for index, name := range want {
		if filepath.Base(escaped[index]) != name {
			t.Fatalf("name %d = %q, want %q", index, filepath.Base(escaped[index]), name)
		}
	}

	duplicates := paths(file("a.txt", "1"), file("a.txt", "2"), file("a.txt", "3"))
	if got := []string{filepath.Base(duplicates[0]), filepath.Base(duplicates[1]), filepath.Base(duplicates[2])}; !reflect.DeepEqual(got, []string{"a.txt", "a-2.txt", "a-3.txt"}) {
		t.Fatalf("duplicates = %v", got)
	}
	for index, want := range []string{"1", "2", "3"} {
		data, _ := os.ReadFile(duplicates[index])
		if string(data) != want {
			t.Fatalf("duplicate %d = %q", index, data)
		}
	}

	for _, params := range []host.Message{
		{"files": []any{}},
		{"files": []any{host.Message{"name": "a", "data": "not base64!"}}},
	} {
		if result := runOp(t, env, "save_attachments", params); opCode(result) != "bad_params" {
			t.Fatal(result)
		}
	}
}

func writeBackup(t *testing.T, dir, name, createdAt string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zip := gzip.NewWriter(file)
	writer := tar.NewWriter(zip)
	manifest := fmt.Sprintf(`{"format":"pigkin-config-backup","version":1,"createdAt":%q,"piVersion":null}`, createdAt)
	if err := writer.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(manifest)), ModTime: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackupsOp(t *testing.T) {
	env := testEnv(t)
	dir := backupsDir(env)
	data := opData(t, runOp(t, env, "backups", host.Message{}))
	if data["dir"] != dir || len(data["backups"].([]host.Message)) != 0 {
		t.Fatalf("fresh backups = %v", data)
	}

	old := writeBackup(t, dir, "old.tar.gz", "2026-01-01T00:00:00.000Z")
	newer := writeBackup(t, dir, "new.tar.gz", "2026-02-01T00:00:00.000Z")
	if err := os.WriteFile(filepath.Join(dir, "notes.tar.gz"), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "notes.tar.gz"), future, future); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden.tar.gz", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data = opData(t, runOp(t, env, "backups", host.Message{}))
	backups := data["backups"].([]host.Message)
	if len(backups) != 3 {
		t.Fatalf("backups = %v", backups)
	}
	if backups[0]["name"] != "notes.tar.gz" || backups[0]["error"] == nil {
		t.Fatalf("unreadable backup = %v", backups[0])
	}
	if text, ok := backups[0]["errorI18n"].(*host.Text); !ok || text.Key != "backup_unreadable" {
		t.Fatalf("errorI18n = %v", backups[0]["errorI18n"])
	}
	if backups[1]["name"] != "new.tar.gz" || backups[2]["name"] != "old.tar.gz" {
		t.Fatalf("order = %v, %v", backups[1]["name"], backups[2]["name"])
	}
	if backups[1]["createdAt"] != "2026-02-01T00:00:00.000Z" || backups[1]["piVersion"] != nil || backups[1]["error"] != nil {
		t.Fatalf("backup info = %v", backups[1])
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../config/settings.json", "sub/x.tar.gz", ".hidden.tar.gz", "x.zip"} {
		if result := runOp(t, env, "backup_delete", host.Message{"name": name}); opCode(result) != "bad_name" {
			t.Fatalf("%s: %v", name, result)
		}
	}
	for range 2 {
		if result := runOp(t, env, "backup_delete", host.Message{"name": "old.tar.gz"}); result["ok"] != true {
			t.Fatal(result)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{".hidden.tar.gz", "new.tar.gz", "notes.tar.gz", "readme.txt"}) {
		t.Fatalf("files = %v", names)
	}
}

func TestAnswerUnknownOperationTypes(t *testing.T) {
	env := testEnv(t)
	for _, value := range []any{nil, float64(7), []any{}} {
		result := answerUtility(&Context{Env: env}, value, host.Message{})
		if opCode(result) != "unknown_op" || result["error"] == "" {
			t.Fatalf("%v: %v", value, result)
		}
	}
}
