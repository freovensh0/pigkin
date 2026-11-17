package modes

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func runRemoteBash(t *testing.T, script string, env ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", remoteScript(script))
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		got := exitCode(err)
		if got == nil {
			t.Fatalf("bash failed without an exit code: %v (%s)", err, stderr.String())
		}
		code = *got
	}
	return stdout.String(), stderr.String(), code
}

func TestRemoteConnectionCodeAndErrors(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"ssh_missing", "ssh_missing"},
		{"remote_not_connected", "remote_not_connected"},
		{"no_access", ""},
		{"bad_params", ""},
	} {
		if got := connectionCode(host.NewError(tc.code, "x", nil)); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.code, got, tc.want)
		}
	}
	r := remote{host: testHost(Host{Name: "box"})}
	failure := asRemoteError(r, errors.New("boom"))
	if failure.Code != "remote_failed" || failure.I18n.Args["host"] != "box" {
		t.Fatalf("asRemoteError = %+v", failure)
	}
	want := host.NewError("remote_unreachable", "down", nil)
	if got := asRemoteError(r, want); got != want {
		t.Fatalf("asRemoteError kept the failure: %v", got)
	}
	if got := asHostError(want); got != want {
		t.Fatalf("asHostError kept the failure: %v", got)
	}
}

func TestRemoteRequested(t *testing.T) {
	got, err := remoteRequested("/home/me/ws", "src/../lib")
	if err != nil || got != "/home/me/ws/lib" {
		t.Fatalf("remoteRequested = %q, %v", got, err)
	}
	for _, raw := range []string{"..", "../outside", "/etc"} {
		if _, err := remoteRequested("/home/me/ws", raw); err == nil {
			t.Fatalf("%s was accepted", raw)
		}
	}
}

func TestRemoteScopeScript(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, outside, root + "\n"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}

	probe := func(target string) (string, int) {
		lines, err := scopeScript(root, target)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, `printf '%s' "$T"`)
		stdout, _, code := runRemoteBash(t, strings.Join(lines, "\n"))
		return stdout, code
	}
	if got, code := probe(filepath.Join(root, "src")); code != 2 {
		t.Fatalf("a missing target exited %d, %q", code, got)
	}
	if got, code := probe(filepath.Join(root, "escape")); code != 3 || got != "" {
		t.Fatalf("a link out of the workspace exited %d, %q", code, got)
	}
	if got, code := probe(root + "\n"); code != 3 || got != "" {
		t.Fatalf("the trailing-newline lookalike exited %d, %q", code, got)
	}
	if got, code := probe(root); code != 0 || got != root {
		t.Fatalf("the workspace exited %d, %q, want %q", code, got, root)
	}
}

func TestRemoteListingScript(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"src", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := listingScript(dir)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runRemoteBash(t, script)
	if code != 0 {
		t.Fatalf("listing exited %d", code)
	}
	entries, err := parseListing([]byte(stdout), dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	if !reflect.DeepEqual(names, []string{".hidden", "main.ts", "src"}) {
		t.Fatalf("entries = %v", names)
	}
}

func TestBrowseListingScript(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home with $x")
	if err := os.MkdirAll(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(home, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	script, err := browseListingScript(home, home)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runRemoteBash(t, script)
	if code != 0 {
		t.Fatalf("listing exited %d", code)
	}
	fields := strings.Split(stdout, "\x00")
	if len(fields) < 6 || fields[0] != home || fields[2] != "ok" {
		t.Fatalf("fields = %q", fields)
	}
	if fields[1] != filepath.Dir(home) || !reflect.DeepEqual(fields[3:len(fields)-1], []string{".hidden", "link", "src"}) {
		t.Fatalf("fields = %q", fields)
	}
	missing, err := browseListingScript(home, filepath.Join(home, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, _ = runRemoteBash(t, missing)
	if !strings.Contains(stdout, "ENOENT\x00") {
		t.Fatalf("missing folder = %q", stdout)
	}
}

func TestRemoteSearchPlanAndRank(t *testing.T) {
	home := "/home/me"
	for _, tc := range []struct{ query, root, term string }{
		{"", "", ""},
		{"  ", "", ""},
		{"~", home, ""},
		{".", home, ""},
		{"api", home, "api"},
		{"~/src/ap", home + "/src", "ap"},
		{"/etc/ss", "/etc", "ss"},
		{"./", home, ""},
	} {
		got := remoteSearchPlan(tc.query, home)
		if tc.query == "" || strings.TrimSpace(tc.query) == "" {
			if got != nil {
				t.Fatalf("%q: got %+v", tc.query, got)
			}
			continue
		}
		if got == nil || got.root != tc.root || got.term != tc.term {
			t.Fatalf("%q: got %+v, want %s/%s", tc.query, got, tc.root, tc.term)
		}
	}
	if got := literalPattern(`a*b?c[d]\e`); got != `a\*b\?c\[d]\\e` {
		t.Fatalf("literalPattern = %q", got)
	}
	if rank := remoteRank("/home/me/API", "/home/me", "api"); rank.tier != 0 || rank.depth != 1 {
		t.Fatalf("exact rank = %+v", rank)
	}
	if rank := remoteRank("/home/me/src/apis", "/home/me", "api"); rank.tier != 1 || rank.depth != 2 {
		t.Fatalf("prefix rank = %+v", rank)
	}
	if rank := remoteRank("/home/me/src/x-api", "/home/me", "api"); rank.tier != 2 {
		t.Fatalf("contains rank = %+v", rank)
	}
}

func TestRemoteGitCommand(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GIT_ARGS\"\nprintf '%s' \"$GIT_INDEX_FILE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	command, err := remoteGitCommand(root, []string{"rev-parse", "--show-toplevel"}, map[string]string{"GIT_INDEX_FILE": "/tmp/i's"})
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runRemoteBash(t, command, "PATH="+bin+":"+os.Getenv("PATH"), "GIT_ARGS="+argsFile)
	if code != 0 || stdout != "/tmp/i's" {
		t.Fatalf("git command exited %d: %q (%s)", code, stdout, stderr)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{"-c", "safe.directory=" + root, "-c", "safe.directory=" + root, "rev-parse", "--show-toplevel"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("git args = %q, want %q", args, want)
	}
}
