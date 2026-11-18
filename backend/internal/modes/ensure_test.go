package modes

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func stubExecutable(t *testing.T, path string) {
	t.Helper()
	previous := hostExecutable
	hostExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { hostExecutable = previous })
}

func ensureParams() host.Message {
	return host.Message{
		"packageJson":     `{"dependencies":{}}`,
		"packageLock":     `{"lockfileVersion":3}`,
		"expectedVersion": "1.5.2",
		"pigkinExtensions": []any{
			host.Message{"name": "pigkin-todo", "source": "todo\n", "usesRuntimePackages": false},
		},
	}
}

func writeFakeTool(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeToolchain puts a node that passes the version check and an npm that
// writes a believable pinned install on PATH. npm counts its runs and can be
// told to fail, so no test touches the network.
func fakeToolchain(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeFakeTool(t, filepath.Join(dir, "node"), "#!/bin/sh\nexit 0\n")
	t.Setenv("FAKE_NPM_COUNTER", filepath.Join(dir, "npm-runs"))
	t.Setenv("FAKE_PI_VERSION", "1.5.2")
	writeFakeTool(t, filepath.Join(dir, "npm"), `#!/bin/sh
printf x >> "$FAKE_NPM_COUNTER"
printf 'added 1 package\n'
if [ -n "$FAKE_NPM_OFFLINE" ]; then
  printf 'npm error code ENOTFOUND registry.npmjs.org\n' >&2
  exit 1
fi
if [ -n "$FAKE_NPM_EXIT" ]; then
  exit "$FAKE_NPM_EXIT"
fi
mkdir -p node_modules/@earendil-works/pi-coding-agent/dist
cat > node_modules/@earendil-works/pi-coding-agent/package.json <<EOF
{"version":"$FAKE_PI_VERSION","bin":{"pi":"dist/pi.js"}}
EOF
: > node_modules/@earendil-works/pi-coding-agent/dist/pi.js
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func npmRuns(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FAKE_NPM_COUNTER"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(data)
}

func ensureContext(t *testing.T, env host.Env, params host.Message) *Context {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, err := client.Read(buffer); err != nil {
				return
			}
		}
	}()
	return &Context{Env: env, Params: params, Conn: host.NewConnection(server)}
}

func stubBundledBinary(t *testing.T, root, content string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "bundled-host")
	if err := os.WriteFile(binary, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, binary)
	return binary
}

func TestEnsureInstallsAndRefreshesHostBinary(t *testing.T) {
	env := testEnv(t)
	fakeToolchain(t)
	binary := stubBundledBinary(t, env.Root, "first build")
	target := filepath.Join(env.Root, "pigkin-host")

	data, err := ensure(ensureContext(t, env, ensureParams()))
	if err != nil {
		t.Fatal(err)
	}
	if data["piVersion"] != "1.5.2" {
		t.Fatalf("piVersion = %v, want the installed Node Pi version", data["piVersion"])
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "first build" {
		t.Fatalf("installed binary = %q (%v)", got, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %v (%v)", info, err)
	}
	if runs := npmRuns(t); runs != 1 {
		t.Fatalf("npm ran %d times, want 1", runs)
	}
	if !sameContents(filepath.Join(env.Root, "pi", "package.json"), `{"dependencies":{}}`) {
		t.Fatal("pinned package.json was not written")
	}
	if !sameContents(filepath.Join(env.Root, "pi", "package-lock.json"), `{"lockfileVersion":3}`) {
		t.Fatal("pinned package-lock.json was not written")
	}
	if _, err := os.Stat(filepath.Join(env.Root, "pi", installingMarker)); !os.IsNotExist(err) {
		t.Fatal("install marker was left behind")
	}
	if _, err := os.Stat(filepath.Join(env.Root, installLockName)); !os.IsNotExist(err) {
		t.Fatal("install lock was left behind")
	}
	if got, err := os.ReadFile(filepath.Join(env.Root, "extensions", "pigkin-todo", "index.js")); err != nil || string(got) != "todo\n" {
		t.Fatalf("bundled extension = %q (%v)", got, err)
	}

	if _, err := ensure(ensureContext(t, env, ensureParams())); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "first build" {
		t.Fatalf("identical binary was rewritten: %q", got)
	}
	if runs := npmRuns(t); runs != 1 {
		t.Fatalf("a current runtime was reinstalled: %d npm runs", runs)
	}

	if err := os.WriteFile(binary, []byte("newer build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(ensureContext(t, env, ensureParams())); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "newer build" {
		t.Fatalf("binary was not refreshed: %q", got)
	}
	entries, err := os.ReadDir(env.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "pigkin-host" && entry.Name() != "pi" && entry.Name() != "extensions" {
			t.Fatalf("leftover in root: %s", entry.Name())
		}
	}
}

func TestEnsureInstallsBundledExtensions(t *testing.T) {
	env := testEnv(t)
	fakeToolchain(t)
	stubBundledBinary(t, env.Root, "first build")
	params := ensureParams()
	params["pigkinExtensions"] = []any{
		host.Message{"name": "pigkin-todo", "source": "todo\n", "usesRuntimePackages": false},
		host.Message{"name": "pigkin-web-access", "source": "web\n", "usesRuntimePackages": true},
	}
	if _, err := ensure(ensureContext(t, env, params)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want string
	}{
		{filepath.Join(env.Root, "extensions", "pigkin-todo", "index.js"), "todo\n"},
		{filepath.Join(env.Root, "pi", "extensions", "pigkin-web-access", "index.js"), "web\n"},
	} {
		got, err := os.ReadFile(test.path)
		if err != nil || string(got) != test.want {
			t.Fatalf("%s = %q (%v), want %q", test.path, got, err, test.want)
		}
	}

	// A tampered file is restored by the next ensure; one that already matches
	// is left alone.
	path := filepath.Join(env.Root, "extensions", "pigkin-todo", "index.js")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(ensureContext(t, env, params)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "todo\n" {
		t.Fatalf("tampered extension was not restored: %q", got)
	}
}

func TestEnsureSkipsNodeRuntimeWithoutExtensions(t *testing.T) {
	env := testEnv(t)
	fakeToolchain(t)
	stubBundledBinary(t, env.Root, "first build")
	params := ensureParams()
	delete(params, "pigkinExtensions")

	data, err := ensure(ensureContext(t, env, params))
	if err != nil {
		t.Fatal(err)
	}
	if data["piVersion"] != RuntimeVersion() {
		t.Fatalf("piVersion = %v, want the PiG version", data["piVersion"])
	}
	if _, err := os.Stat(filepath.Join(env.Root, "pi")); !os.IsNotExist(err) {
		t.Fatal("Node runtime was installed without bundled extensions")
	}
	if runs := npmRuns(t); runs != 0 {
		t.Fatalf("npm ran %d times, want 0", runs)
	}
	if got, _ := os.ReadFile(filepath.Join(env.Root, "pigkin-host")); string(got) != "first build" {
		t.Fatalf("binary was not installed: %q", got)
	}
}

func TestEnsureRefreshesStaleNodeRuntime(t *testing.T) {
	env := testEnv(t)
	fakeToolchain(t)
	stubBundledBinary(t, env.Root, "first build")
	if _, err := ensure(ensureContext(t, env, ensureParams())); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(env.Root, "pi", "package.json")
	if err := os.WriteFile(manifest, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(ensureContext(t, env, ensureParams())); err != nil {
		t.Fatal(err)
	}
	if runs := npmRuns(t); runs != 2 {
		t.Fatalf("npm ran %d times, want 2", runs)
	}
	if !sameContents(manifest, `{"dependencies":{}}`) {
		t.Fatal("stale manifest was not rewritten")
	}
}

func TestEnsureNpmFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
		code string
		i18n string
	}{
		{"offline", "FAKE_NPM_OFFLINE", "npm_offline", "agent_download_offline"},
		{"failed", "FAKE_NPM_EXIT", "npm_failed", "command_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testEnv(t)
			fakeToolchain(t)
			value := "1"
			if test.name == "failed" {
				value = "7"
			}
			t.Setenv(test.key, value)
			stubBundledBinary(t, env.Root, "first build")

			_, err := ensure(ensureContext(t, env, ensureParams()))
			hostErr, ok := err.(*host.Error)
			if !ok || hostErr.Code != test.code {
				t.Fatalf("got %v, want %s", err, test.code)
			}
			if hostErr.I18n == nil || hostErr.I18n.Key != test.i18n {
				t.Fatalf("i18n = %v, want %s", hostErr.I18n, test.i18n)
			}
		})
	}
}

func TestPiVersion(t *testing.T) {
	env := testEnv(t)
	if got := PiVersion(env.Root); got != RuntimeVersion() {
		t.Fatalf("PiVersion without runtime = %q, want %q", got, RuntimeVersion())
	}
	dir := filepath.Join(env.Root, "pi", "node_modules", piPackage)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"9.9.9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PiVersion(env.Root); got != "9.9.9" {
		t.Fatalf("PiVersion with runtime = %q, want 9.9.9", got)
	}
}

func TestEnsureLeavesBinaryAloneWhenItIsTheRunningOne(t *testing.T) {
	env := testEnv(t)
	fakeToolchain(t)
	target := filepath.Join(env.Root, "pigkin-host")
	if err := os.WriteFile(target, []byte("running"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, target)
	if _, err := ensure(ensureContext(t, env, ensureParams())); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "running" {
		t.Fatalf("running binary was replaced: %q", got)
	}
}

func TestEnsureValidatesParams(t *testing.T) {
	env := testEnv(t)
	for name, params := range map[string]host.Message{
		"missing packageJson": {"packageLock": "x", "expectedVersion": "x", "pigkinExtensions": []any{}},
		"bad extension name": {
			"packageJson": "x", "packageLock": "x", "expectedVersion": "x",
			"pigkinExtensions": []any{host.Message{"name": "../escape", "source": "x", "usesRuntimePackages": false}},
		},
		"bad extension array": {
			"packageJson": "x", "packageLock": "x", "expectedVersion": "x", "pigkinExtensions": "nope",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ensure(&Context{Env: env, Params: params})
			hostErr, ok := err.(*host.Error)
			if !ok || hostErr.Code != "bad_params" {
				t.Fatalf("got %v, want bad_params", err)
			}
		})
	}
}

func TestUnsupportedSetupModes(t *testing.T) {
	for _, mode := range []Mode{updateExtensions} {
		_, err := mode(&Context{})
		hostErr, ok := err.(*host.Error)
		if !ok || hostErr.Code != "unsupported" {
			t.Fatalf("got %v, want unsupported", err)
		}
		if hostErr.I18n == nil || hostErr.I18n.Key != "unsupported_mode" {
			t.Fatalf("i18n = %v", hostErr.I18n)
		}
	}
}
