package modes

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestInstalledPigkinExtensions(t *testing.T) {
	env := testEnv(t)
	path := pigkinExtensionPath(env.Root, "pigkin-todo", false)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := []byte("todo\n")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	params := host.Message{"pigkinExtensions": []any{host.Message{
		"name": "pigkin-todo", "sha256": hex.EncodeToString(sum[:]), "usesRuntimePackages": false,
	}}}

	if extensions, refusal := installedPigkinExtensions(&Context{Env: env}); refusal != nil || len(extensions) != 0 {
		t.Fatalf("no fingerprints: %v, %v", extensions, refusal)
	}
	extensions, refusal := installedPigkinExtensions(&Context{Env: env, Params: params})
	if refusal != nil || len(extensions) != 1 || extensions[0].path != path {
		t.Fatalf("installed: %v, %v", extensions, refusal)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, refusal := installedPigkinExtensions(&Context{Env: env, Params: params}); refusal == nil || refusal.Code != "pi_version" {
		t.Fatalf("missing file: %v", refusal)
	}

	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, refusal := installedPigkinExtensions(&Context{Env: env, Params: params}); refusal == nil || refusal.Code != "pi_version" {
		t.Fatalf("changed file: %v", refusal)
	}
}

func TestInstalledNpmPackage(t *testing.T) {
	dir := t.TempDir()
	if installedNpmPackage(dir, "pi-web-access") {
		t.Fatal("package found without settings")
	}
	settings := `{"packages":["npm:pi-web-access","npm:other@1.2.3",{"source":"git:repo"}]}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if !installedNpmPackage(dir, "pi-web-access") {
		t.Fatal("npm-installed package was not detected")
	}
	if installedNpmPackage(dir, "plain") {
		t.Fatal("unrelated package was detected")
	}
}

func TestPackageInstall(t *testing.T) {
	env := testEnv(t)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	runsFile := filepath.Join(dir, "runs")
	node := filepath.Join(dir, "node")
	script := `#!/bin/sh
printf '%s\n' "$*" >> ` + channelQuote(argsFile) + `
printf x >> ` + channelQuote(runsFile) + `
if [ -n "$FAKE_PI_EXIT" ]; then
  printf 'Error: bad source\n' >&2
  exit "$FAKE_PI_EXIT"
fi
printf 'installing %s\n' "$3"
exit 0
`
	if err := os.WriteFile(node, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	packageDir := filepath.Join(env.Root, "pi", "node_modules", piPackage, "dist")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(packageDir, "..", "package.json")
	if err := os.WriteFile(manifest, []byte(`{"bin":{"pi":"dist/pi.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "pi.js"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	params := host.Message{"source": "npm:pi-web-access"}
	if _, err := packageInstall(ensureContext(t, env, params)); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || !strings.Contains(string(args), "install npm:pi-web-access") {
		t.Fatalf("pi args = %q (%v)", args, err)
	}
	if _, err := os.Stat(filepath.Join(env.Root, "config.lock")); !os.IsNotExist(err) {
		t.Fatal("config lock was left behind")
	}
	if runs, _ := os.ReadFile(runsFile); string(runs) != "x" {
		t.Fatalf("pi ran %d times, want 1", len(runs))
	}

	t.Setenv("FAKE_PI_EXIT", "7")
	_, err = packageInstall(ensureContext(t, env, params))
	failure, ok := err.(*host.Error)
	if !ok || failure.Code != "install_failed" || failure.Message != "Error: bad source" {
		t.Fatalf("got %v, want install_failed with Pi's last line", err)
	}
	if runs, _ := os.ReadFile(runsFile); len(runs) != 3 {
		t.Fatalf("pi ran %d times, want 3 after a retry", len(runs))
	}
	if _, err := os.Stat(filepath.Join(env.Root, "config.lock")); !os.IsNotExist(err) {
		t.Fatal("config lock was left behind after a failure")
	}
}
