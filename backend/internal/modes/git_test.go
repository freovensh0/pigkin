package modes

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

type gitRun struct {
	t       *testing.T
	conn    net.Conn
	scanner *bufio.Scanner
	env     host.Env
	root    string
	nextID  int
}

func startGit(t *testing.T, params host.Message) *gitRun {
	t.Helper()
	system, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	prefix := filepath.Join(base, "prefix")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(system, filepath.Join(prefix, "bin", "git")); err != nil {
		t.Fatal(err)
	}
	app, hostSide := net.Pipe()
	connection := host.NewConnection(hostSide)
	env := host.Env{Prefix: prefix, ConfigDir: filepath.Join(base, "pi"), Home: base}
	go func() { _, _ = git(&Context{Env: env, Params: params, Conn: connection}) }()
	run := &gitRun{t: t, conn: app, scanner: host.ScanLines(app), env: env}
	t.Cleanup(func() {
		_ = app.Close()
		_ = hostSide.Close()
	})
	ready := run.next()
	if ready["type"] != "pigkin_git_ready" {
		t.Fatalf("got %v, want pigkin_git_ready", ready)
	}
	return run
}

func (r *gitRun) send(message host.Message) {
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

func (r *gitRun) next() host.Message {
	r.t.Helper()
	_ = r.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if !r.scanner.Scan() {
		r.t.Fatal("no line from the git mode:", r.scanner.Err())
	}
	var message host.Message
	if err := json.Unmarshal(r.scanner.Bytes(), &message); err != nil {
		r.t.Fatal(err)
	}
	return message
}

func (r *gitRun) request(kind string, fields host.Message) host.Message {
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

type gitFixture struct {
	t         *testing.T
	run       *gitRun
	workspace string
	tmp       string
	clock     int64
}

func newGit(t *testing.T) *gitFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	run := startGit(t, host.Message{})
	workspace := filepath.Join(base, "workspace")
	run.root = workspace
	return &gitFixture{t: t, run: run, workspace: workspace, tmp: tmp, clock: 1_700_000_000}
}

func (g *gitFixture) gitWith(extra []string, cwd string, args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (g *gitFixture) git(cwd string, args ...string) string {
	return g.gitWith(nil, cwd, args...)
}

func (g *gitFixture) commitAll(cwd, message string) string {
	g.clock += 60
	date := strconv.FormatInt(g.clock, 10) + " +0000"
	env := []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
	g.gitWith(env, cwd, "add", "-A")
	g.gitWith(env, cwd, "commit", "-q", "--allow-empty", "-m", message)
	return strings.TrimSpace(g.git(cwd, "rev-parse", "HEAD"))
}

func (g *gitFixture) write(relative, content, root string) {
	g.t.Helper()
	if root == "" {
		root = g.workspace
	}
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gitFixture) initRepo(files map[string]string, root string) string {
	g.t.Helper()
	if root == "" {
		root = g.workspace
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.git(root, "init", "-q", "-b", "main")
	for name, content := range files {
		g.write(name, content, root)
	}
	return g.commitAll(root, "initial")
}

func (g *gitFixture) changes(fields host.Message) host.Message {
	message := host.Message{"mode": "uncommitted", "ignoreWhitespace": false}
	for name, value := range fields {
		message[name] = value
	}
	return g.run.request("pigkin_git_changes", message)
}

func (g *gitFixture) detect(trusted bool, root string) host.Message {
	if root == "" {
		root = g.workspace
	}
	return g.run.request("pigkin_git_detect", host.Message{"trusted": trusted, "root": root})
}

func (g *gitFixture) commits(fields host.Message) host.Message {
	return g.run.request("pigkin_git_commits", fields)
}

func (g *gitFixture) commitDiff(sha string, ignore bool) host.Message {
	return g.run.request("pigkin_git_commit_diff", host.Message{"sha": sha, "ignoreWhitespace": ignore, "root": g.workspace})
}

func filesOf(t *testing.T, reply host.Message) []host.Message {
	t.Helper()
	items, ok := reply["files"].([]any)
	if !ok {
		t.Fatalf("got %v, want files", reply)
	}
	result := make([]host.Message, 0, len(items))
	for _, item := range items {
		result = append(result, item.(map[string]any))
	}
	return result
}

func filePaths(files []host.Message) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file["path"].(string))
	}
	return paths
}

func fileByPath(t *testing.T, reply host.Message, path string) host.Message {
	t.Helper()
	for _, file := range filesOf(t, reply) {
		if file["path"] == path {
			return file
		}
	}
	t.Fatalf("no file %q in %v", path, reply)
	return nil
}

func hunkLines(t *testing.T, file host.Message, index int) []string {
	t.Helper()
	hunks := file["hunks"].([]any)
	lines := hunks[index].(map[string]any)["lines"].([]any)
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, line.(string))
	}
	return result
}

func gitErrorKey(reply host.Message) string {
	i18n, _ := reply["errorI18n"].(map[string]any)
	key, _ := i18n["key"].(string)
	return key
}

func TestGitStartsReady(t *testing.T) {
	startGit(t, host.Message{})
}

func TestGitDetect(t *testing.T) {
	g := newGit(t)
	if err := os.Mkdir(g.workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	reply := g.detect(true, "")
	if reply["repo"] != false || reply["git"] != true {
		t.Fatalf("got %v", reply)
	}
	g.initRepo(map[string]string{"sub/file.txt": "x\n"}, filepath.Join(g.workspace, "nested"))
	if reply := g.detect(true, ""); reply["repo"] != false {
		t.Fatalf("got %v", reply)
	}
	nested := filepath.Join(g.workspace, "nested")
	if reply := g.detect(false, nested); reply["repo"] != true || reply["git"] != true {
		t.Fatalf("got %v", reply)
	}
	sub := filepath.Join(nested, "sub")
	if reply := g.detect(true, sub); reply["repo"] != false {
		t.Fatalf("got %v", reply)
	}
}

func TestGitDetectWithoutGit(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"a.txt": "a\n"}, "")
	if err := os.Remove(filepath.Join(g.run.env.Prefix, "bin", "git")); err != nil {
		t.Fatal(err)
	}
	if reply := g.detect(true, ""); reply["repo"] != true || reply["git"] != false {
		t.Fatalf("got %v", reply)
	}
	reply := g.changes(nil)
	if reply["code"] != "no_git" || reply["error"] != "git isn't installed in Termux." || gitErrorKey(reply) != "git_missing" {
		t.Fatalf("got %v", reply)
	}
	if reply := g.commits(nil); reply["code"] != "no_git" {
		t.Fatalf("got %v", reply)
	}
}

func TestGitUncommittedChanges(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{
		"a.txt": "one\ntwo\nthree\n", "gone.txt": "bye\n", ".gitignore": "*.log\n",
	}, "")
	g.write("a.txt", "ONE\ntwo\nthree\n", "")
	g.git(g.workspace, "add", "a.txt")
	g.write("a.txt", "ONE\ntwo\nTHREE\n", "")
	if err := os.Remove(filepath.Join(g.workspace, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	g.write("new/b.txt", "fresh\n", "")
	g.write("debug.log", "ignored\n", "")
	index, err := os.ReadFile(filepath.Join(g.workspace, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	reply := g.changes(nil)
	if reply["branch"] != "main" || reply["baseRef"] != "main" || reply["type"] != "pigkin_git_changes_result" {
		t.Fatalf("got %v", reply)
	}
	files := filesOf(t, reply)
	if got := strings.Join(filePaths(files), ","); got != "a.txt,gone.txt,new/b.txt" {
		t.Fatalf("got %q", got)
	}
	a := files[0]
	if a["change"] != "modified" || a["additions"].(float64) != 2 || a["deletions"].(float64) != 2 || a["kind"] != "text" {
		t.Fatalf("got %v", a)
	}
	if got := strings.Join(hunkLines(t, a, 0), ","); got != "-one,+ONE, two,-three,+THREE" {
		t.Fatalf("got %q", got)
	}
	header := a["hunks"].([]any)[0].(map[string]any)["header"]
	if header != "@@ -1,3 +1,3 @@" {
		t.Fatalf("got %v", header)
	}
	after, err := os.ReadFile(filepath.Join(g.workspace, ".git", "index"))
	if err != nil || string(after) != string(index) {
		t.Fatalf("the repo index changed: %v", err)
	}
	if status := g.git(g.workspace, "status", "--porcelain"); !strings.Contains(status, "?? new/") {
		t.Fatalf("got %q", status)
	}
	if entries, err := os.ReadDir(g.tmp); err != nil || len(entries) != 0 {
		t.Fatalf("scratch left behind: %v %v", entries, err)
	}
}

func TestGitRenameAndWhitespace(t *testing.T) {
	g := newGit(t)
	lines := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n"
	g.initRepo(map[string]string{"old name.txt": lines}, "")
	if err := os.Remove(filepath.Join(g.workspace, "old name.txt")); err != nil {
		t.Fatal(err)
	}
	g.write("new name.txt", strings.Replace(lines, "l8\n", "l8 changed\n", 1), "")
	renamed := fileByPath(t, g.changes(nil), "new name.txt")
	if renamed["oldPath"] != "old name.txt" || renamed["change"] != "renamed" || renamed["additions"].(float64) != 1 {
		t.Fatalf("got %v", renamed)
	}

	g2 := newGit(t)
	g2.initRepo(map[string]string{"spaces.txt": "a b\n", "mixed.txt": "x\ny\n"}, "")
	g2.write("spaces.txt", "a  b\n", "")
	g2.write("mixed.txt", "x \nY\n", "")
	if got := strings.Join(filePaths(filesOf(t, g2.changes(nil))), ","); got != "mixed.txt,spaces.txt" {
		t.Fatalf("got %q", got)
	}
	files := filesOf(t, g2.changes(host.Message{"ignoreWhitespace": true}))
	if len(files) != 1 || files[0]["path"] != "mixed.txt" || files[0]["additions"].(float64) != 1 {
		t.Fatalf("got %v", files)
	}
	if got := strings.Join(hunkLines(t, files[0], 0), ","); got != " x ,-y,+Y" {
		t.Fatalf("got %q", got)
	}
}

func TestGitUnbornAndDetached(t *testing.T) {
	g := newGit(t)
	if err := os.MkdirAll(g.workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	g.git(g.workspace, "init", "-q", "-b", "trunk")
	g.write("staged.txt", "s\n", "")
	g.git(g.workspace, "add", "staged.txt")
	g.write("loose.txt", "l\n", "")
	reply := g.changes(nil)
	if reply["branch"] != "trunk" || reply["baseRef"] != nil {
		t.Fatalf("got %v", reply)
	}
	if got := strings.Join(filePaths(filesOf(t, reply)), ","); got != "loose.txt,staged.txt" {
		t.Fatalf("got %q", got)
	}

	detached := filepath.Join(filepath.Dir(g.workspace), "detached")
	g.run.root = detached
	first := g.initRepo(map[string]string{"README.md": "hello\n"}, detached)
	g.commitAll(detached, "second")
	g.git(detached, "checkout", "-q", "--detach", first)
	if err := os.Remove(filepath.Join(detached, ".git", "index")); err != nil {
		t.Fatal(err)
	}
	g.write("README.md", "changed\n", detached)
	reply = g.changes(nil)
	if reply["branch"] != nil || reply["baseRef"] != "main" {
		t.Fatalf("got %v", reply)
	}
	files := filesOf(t, reply)
	if len(files) != 1 || files[0]["path"] != "README.md" || files[0]["change"] != "modified" {
		t.Fatalf("got %v", files)
	}
}

func TestGitFingerprint(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"README.md": "hello\n"}, "")
	g.write("README.md", "edited\n", "")
	first := g.changes(nil)
	fingerprint := first["fingerprint"].(string)
	unchanged := g.changes(host.Message{"known": fingerprint})
	if unchanged["fingerprint"] != fingerprint || unchanged["unchanged"] != true || unchanged["type"] != "pigkin_git_changes_result" {
		t.Fatalf("got %v", unchanged)
	}
	g.write("README.md", "edited again\n", "")
	changed := g.changes(host.Message{"known": fingerprint})
	if changed["fingerprint"] == fingerprint {
		t.Fatalf("fingerprint did not change: %v", changed)
	}
	if _, ok := changed["unchanged"]; ok {
		t.Fatalf("got unchanged in %v", changed)
	}
	if got := strings.Join(hunkLines(t, filesOf(t, changed)[0], 0), ","); got != "-hello,+edited again" {
		t.Fatalf("got %q", got)
	}
}

func TestGitNotRepoAndMissingFolder(t *testing.T) {
	g := newGit(t)
	if err := os.Mkdir(g.workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	reply := g.changes(nil)
	if reply["code"] != "not_repo" || !strings.Contains(reply["error"].(string), "not a git repository") {
		t.Fatalf("got %v", reply)
	}
	nowhere := filepath.Join(filepath.Dir(g.workspace), "nowhere")
	reply = g.run.request("pigkin_git_changes", host.Message{"root": nowhere, "mode": "uncommitted", "ignoreWhitespace": false})
	if reply["code"] != "not_repo" || gitErrorKey(reply) != "folder_not_found" {
		t.Fatalf("got %v", reply)
	}
	i18n := reply["errorI18n"].(map[string]any)
	args := i18n["args"].(map[string]any)
	if args["path"] != nowhere {
		t.Fatalf("got %v", reply)
	}
}

func TestGitDiffLimits(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"README.md": "hello\n"}, "")
	text := strings.Repeat(strings.Repeat("x", 99)+"\n", 11_000)
	g.write("big.txt", text, "")
	g.write("small.txt", "small\n", "")
	files := fileByPath(t, g.changes(nil), "big.txt")
	if files["kind"] != "too_large" || files["additions"].(float64) != 11_000 {
		t.Fatalf("got %v", files)
	}
	small := fileByPath(t, g.changes(nil), "small.txt")
	if got := strings.Join(hunkLines(t, small, 0), ","); got != "+small" {
		t.Fatalf("got %q", got)
	}

	g2 := newGit(t)
	g2.initRepo(map[string]string{"README.md": "hello\n"}, "")
	big := strings.Repeat(strings.Repeat("x", 99)+"\n", 7000)
	medium := strings.Repeat(strings.Repeat("x", 99)+"\n", 3000)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "e.txt"} {
		g2.write(name, big, "")
	}
	g2.write("d.txt", medium, "")
	g2.write("f.txt", "small\n", "")
	got := filesOf(t, g2.changes(nil))
	want := []string{"a.txt:text", "b.txt:text", "c.txt:too_large", "d.txt:text", "e.txt:too_large", "f.txt:text"}
	parts := make([]string, 0, len(got))
	for _, file := range got {
		parts = append(parts, file["path"].(string)+":"+file["kind"].(string))
	}
	if strings.Join(parts, ",") != strings.Join(want, ",") {
		t.Fatalf("got %q", parts)
	}
}

func TestGitOddFiles(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"run.sh": "echo hi\n", "typed.txt": "plain\n"}, "")
	odd := `dir with space/naïve "quoted" ✓.txt`
	g.write(odd, "odd\n", "")
	g.write("crlf.txt", "first\r\nsecond\r\n", "")
	if err := os.WriteFile(filepath.Join(g.workspace, "image.bin"), []byte{0x89, 0x50, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	g.write("empty.txt", "", "")
	if err := os.Chmod(filepath.Join(g.workspace, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("run.sh", filepath.Join(g.workspace, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := os.Remove(filepath.Join(g.workspace, "typed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("run.sh", filepath.Join(g.workspace, "typed.txt")); err != nil {
		t.Fatal(err)
	}

	files := filesOf(t, g.changes(nil))
	want := []string{"crlf.txt", odd, "empty.txt", "image.bin", "link", "run.sh", "typed.txt"}
	if got := strings.Join(filePaths(files), ","); got != strings.Join(want, ",") {
		t.Fatalf("got %q", got)
	}
	byName := make(map[string]host.Message)
	for _, file := range files {
		byName[file["path"].(string)] = file
	}
	if got := strings.Join(hunkLines(t, byName[odd], 0), ","); got != "+odd" {
		t.Fatalf("odd name: got %q", got)
	}
	crlf := hunkLines(t, byName["crlf.txt"], 0)
	if len(crlf) != 2 || crlf[0] != "+first\r" || crlf[1] != "+second\r" {
		t.Fatalf("crlf: got %q", crlf)
	}
	if byName["image.bin"]["kind"] != "binary" || byName["image.bin"]["additions"].(float64) != 0 {
		t.Fatalf("binary: got %v", byName["image.bin"])
	}
	if byName["empty.txt"]["kind"] != "text" || len(byName["empty.txt"]["hunks"].([]any)) != 0 {
		t.Fatalf("empty: got %v", byName["empty.txt"])
	}
	if got := strings.Join(hunkLines(t, byName["link"], 0), ","); got != `+run.sh,\ No newline at end of file` {
		t.Fatalf("symlink: got %q", got)
	}
	typed := byName["typed.txt"]["hunks"].([]any)
	if len(typed) != 2 || strings.Join(hunkLines(t, byName["typed.txt"], 0), ",") != "-plain" {
		t.Fatalf("file to symlink: got %v", byName["typed.txt"])
	}
}

func TestGitDoesNotWriteTheObjectStore(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"a.txt": "a\n", "b.txt": "b\n"}, "")
	g.git(g.workspace, "repack", "-q", "-a", "-d")
	g.write("b.txt", "loose\n", "")
	g.git(g.workspace, "add", "b.txt")
	g.write("a.txt", "changed\n", "")
	g.write("new.txt", "n\n", "")
	before := objectStore(t, g.workspace)
	if got := strings.Join(filePaths(filesOf(t, g.changes(nil))), ","); got != "a.txt,b.txt,new.txt" {
		t.Fatalf("got %q", got)
	}
	if after := objectStore(t, g.workspace); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("object store changed: %v -> %v", before, after)
	}
	cmd := exec.Command("git", "cat-file", "-e", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391")
	cmd.Dir = g.workspace
	if err := cmd.Run(); err == nil {
		t.Fatal("the empty blob was written to the repo")
	}
	if entries, err := os.ReadDir(g.tmp); err != nil || len(entries) != 0 {
		t.Fatalf("scratch left behind: %v %v", entries, err)
	}
}

func objectStore(t *testing.T, root string) []string {
	t.Helper()
	objects := filepath.Join(root, ".git", "objects")
	entries := []string{}
	for _, name := range readNames(t, objects) {
		if len(name) != 2 || !strings.ContainsAny(name[:1], "0123456789abcdef") {
			continue
		}
		for _, file := range readNames(t, filepath.Join(objects, name)) {
			entries = append(entries, name+"/"+file)
		}
	}
	for _, pack := range readNames(t, filepath.Join(objects, "pack")) {
		entries = append(entries, "pack/"+pack)
	}
	sort.Strings(entries)
	return entries
}

func readNames(t *testing.T, dir string) []string {
	t.Helper()
	dirents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(dirents))
	for _, dirent := range dirents {
		names = append(names, dirent.Name())
	}
	return names
}

func TestGitSlowCommandTimesOut(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"README.md": "hello\n"}, "")
	system, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(g.run.env.Prefix, "bin", "git")
	if err := os.Remove(wrapper); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \" $* \" in *\" log \"*) sleep 5;; esac\nexec " + system + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIGKIN_GIT_TIMEOUT_MS", "500")
	reply := g.commits(nil)
	if reply["code"] != "failed" {
		t.Fatalf("got %v", reply)
	}
	if gitErrorKey(reply) != "git_timed_out" || !strings.Contains(reply["error"].(string), "took longer than") {
		t.Fatalf("got %v", reply)
	}
	if _, ok := reply["commits"]; ok {
		t.Fatalf("got commits in %v", reply)
	}
}

func TestGitCommittedChanges(t *testing.T) {
	g := newGit(t)
	upstream := filepath.Join(filepath.Dir(g.workspace), "upstream")
	g.initRepo(map[string]string{"base.txt": "base\n"}, upstream)
	g.git(filepath.Dir(g.workspace), "clone", "-q", upstream, g.workspace)
	g.git(g.workspace, "checkout", "-q", "-b", "feature")
	g.write("feature.txt", "f\n", "")
	g.commitAll(g.workspace, "feature work")
	g.write("uncommitted.txt", "u\n", "")
	g.write("base.txt", "upstream moved on\n", upstream)
	g.commitAll(upstream, "upstream work")
	g.git(g.workspace, "fetch", "-q")
	reply := g.changes(host.Message{"mode": "base"})
	if reply["branch"] != "feature" || reply["baseRef"] != "main" {
		t.Fatalf("got %v", reply)
	}
	if got := strings.Join(filePaths(filesOf(t, reply)), ","); got != "feature.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestGitBaseBranchFallbacks(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"base.txt": "base\n"}, "")
	g.git(g.workspace, "checkout", "-q", "-b", "feature")
	g.write("feature.txt", "f\n", "")
	g.commitAll(g.workspace, "feature work")
	if reply := g.changes(host.Message{"mode": "base"}); reply["baseRef"] != "main" {
		t.Fatalf("got %v", reply)
	}
	g.git(g.workspace, "branch", "-q", "-m", "main", "master")
	if reply := g.changes(host.Message{"mode": "base"}); reply["baseRef"] != "master" {
		t.Fatalf("got %v", reply)
	}
	g.git(g.workspace, "branch", "-q", "-m", "master", "trunk")
	reply := g.changes(host.Message{"mode": "base"})
	if reply["baseRef"] != nil || len(filesOf(t, reply)) != 0 {
		t.Fatalf("got %v", reply)
	}
	if reply := g.commits(nil); reply["baseRef"] != nil || reply["total"].(float64) != 0 {
		t.Fatalf("got %v", reply)
	}
}

func TestGitCommits(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"README.md": "hello\n"}, "")
	g.git(g.workspace, "checkout", "-q", "-b", "feature")
	first := g.commitAll(g.workspace, `first: with "quotes" and ünïcode`)
	firstTime := g.clock
	second := g.commitAll(g.workspace, "second")
	reply := g.commits(nil)
	if reply["type"] != "pigkin_git_commits_result" || reply["baseRef"] != "main" || reply["total"].(float64) != 2 {
		t.Fatalf("got %v", reply)
	}
	commits := reply["commits"].([]any)
	if len(commits) != 2 {
		t.Fatalf("got %v", commits)
	}
	one := commits[0].(map[string]any)
	if one["sha"] != second || one["subject"] != "second" || one["authorTime"].(float64) != float64(g.clock) {
		t.Fatalf("got %v", one)
	}
	two := commits[1].(map[string]any)
	if two["sha"] != first || two["subject"] != `first: with "quotes" and ünïcode` || two["authorTime"].(float64) != float64(firstTime) {
		t.Fatalf("got %v", two)
	}
	fingerprint := reply["fingerprint"].(string)
	unchanged := g.commits(host.Message{"known": fingerprint})
	if unchanged["unchanged"] != true || unchanged["fingerprint"] != fingerprint {
		t.Fatalf("got %v", unchanged)
	}
	g.git(g.workspace, "checkout", "-q", "--detach")
	if reply := g.commits(nil); reply["baseRef"] != nil || reply["total"].(float64) != 0 {
		t.Fatalf("got %v", reply)
	}
}

func TestGitCommitDiff(t *testing.T) {
	g := newGit(t)
	root := g.initRepo(map[string]string{"a.txt": "one\n"}, "")
	g.write("a.txt", "one  \ntwo\n", "")
	second := g.commitAll(g.workspace, "second")
	reply := g.commitDiff(second, false)
	if reply["type"] != "pigkin_git_commit_diff_result" {
		t.Fatalf("got %v", reply)
	}
	file := filesOf(t, reply)[0]
	if file["path"] != "a.txt" || file["change"] != "modified" || file["additions"].(float64) != 2 || file["deletions"].(float64) != 1 {
		t.Fatalf("got %v", file)
	}
	if got := strings.Join(hunkLines(t, file, 0), ","); got != "-one,+one  ,+two" {
		t.Fatalf("got %q", got)
	}
	rootDiff := filesOf(t, g.commitDiff(root, false))[0]
	if rootDiff["change"] != "added" || strings.Join(hunkLines(t, rootDiff, 0), ",") != "+one" {
		t.Fatalf("got %v", rootDiff)
	}
	missing := g.commitDiff("0123456789abcdef0123456789abcdef01234567", false)
	if missing["code"] != "commit_not_found" || missing["error"] != "Commit 0123456789abcdef0123456789abcdef01234567 not found." {
		t.Fatalf("got %v", missing)
	}
	if reply := g.commitDiff("HEAD", false); reply["code"] != "commit_not_found" {
		t.Fatalf("got %v", reply)
	}
}

func TestGitIgnoresMalformedRequests(t *testing.T) {
	g := newGit(t)
	g.initRepo(map[string]string{"README.md": "hello\n"}, "")
	g.run.send(host.Message{"type": "pigkin_git_changes", "id": "1", "root": g.workspace, "mode": "uncommitted", "ignoreWhitespace": false})
	g.run.send(host.Message{"type": "pigkin_git_changes", "id": 1000, "root": g.workspace, "mode": "staged", "ignoreWhitespace": false})
	g.run.send(host.Message{"type": "pigkin_git_detect", "id": 1001, "root": g.workspace})
	g.run.send(host.Message{"type": "pigkin_git_commit_diff", "id": 1002, "root": g.workspace, "sha": "abc"})
	g.run.nextID = 1002
	reply := g.detect(false, g.workspace)
	if reply["id"].(float64) != 1003 || reply["type"] != "pigkin_git_detected" {
		t.Fatalf("got %v", reply)
	}
}

func TestGitReportsRemoteFailures(t *testing.T) {
	g := newGit(t)
	standIn, err := createStandIn(remoteLayoutFor(g.run.env).remotes, "devbox", "/home/me/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"pigkin_git_changes", "pigkin_git_commits"} {
		fields := host.Message{"root": standIn}
		if kind == "pigkin_git_changes" {
			fields["mode"] = "uncommitted"
			fields["ignoreWhitespace"] = false
		}
		reply := g.run.request(kind, fields)
		if reply["code"] != "remote_host_unknown" || reply["error"] != "This workspace's host was removed." {
			t.Fatalf("%s: got %v", kind, reply)
		}
	}
	detected := g.run.request("pigkin_git_detect", host.Message{"root": standIn, "trusted": true})
	if detected["code"] != "remote_host_unknown" {
		t.Fatalf("got %v", detected)
	}
}
