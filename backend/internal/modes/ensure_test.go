package modes

import (
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

func TestEnsureInstallsAndRefreshesHostBinary(t *testing.T) {
	env := testEnv(t)
	binary := filepath.Join(t.TempDir(), "bundled-host")
	if err := os.WriteFile(binary, []byte("first build"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, binary)
	target := filepath.Join(env.Root, "pigkin-host")

	data, err := ensure(&Context{Env: env, Params: ensureParams()})
	if err != nil {
		t.Fatal(err)
	}
	if data["piVersion"] != RuntimeVersion() {
		t.Fatalf("piVersion = %v, want %v", data["piVersion"], RuntimeVersion())
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "first build" {
		t.Fatalf("installed binary = %q (%v)", got, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %v (%v)", info, err)
	}

	if _, err := ensure(&Context{Env: env, Params: ensureParams()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "first build" {
		t.Fatalf("identical binary was rewritten: %q", got)
	}

	if err := os.WriteFile(binary, []byte("newer build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(&Context{Env: env, Params: ensureParams()}); err != nil {
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
		if entry.Name() != "pigkin-host" {
			t.Fatalf("leftover in root: %s", entry.Name())
		}
	}
}

func TestEnsureLeavesBinaryAloneWhenItIsTheRunningOne(t *testing.T) {
	env := testEnv(t)
	target := filepath.Join(env.Root, "pigkin-host")
	if err := os.WriteFile(target, []byte("running"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, target)
	if _, err := ensure(&Context{Env: env, Params: ensureParams()}); err != nil {
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
	for _, mode := range []Mode{packageInstall, updateExtensions} {
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
