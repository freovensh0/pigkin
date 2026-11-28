package modes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWebToolsToEager(t *testing.T) {
	for _, test := range []struct {
		name    string
		initial string
		want    string
		wantErr bool
	}{
		{"missing", "", "{\n  \"toolActivation\": \"eager\"\n}\n", false},
		{"object", `{"readOnly":true}`, "{\n  \"readOnly\": true,\n  \"toolActivation\": \"eager\"\n}\n", false},
		{"already set", `{"toolActivation":"dynamic"}`, `{"toolActivation":"dynamic"}`, false},
		{"deferred", `{"toolExposure":"deferred"}`, `{"toolExposure":"deferred"}`, false},
		{"invalid", `nope`, `nope`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "web-search.json")
			if test.initial != "" {
				if err := os.WriteFile(path, []byte(test.initial), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := defaultWebToolsToEager(dir)
			if (err != nil) != test.wantErr {
				t.Fatalf("defaultWebToolsToEager = %v, wantErr %v", err, test.wantErr)
			}
			got, err := os.ReadFile(path)
			if test.name == "missing" && os.IsNotExist(err) {
				got = nil
			} else if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("web-search.json = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDefaultWebToolsToEagerReportsWriteFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing-config")
	if err := defaultWebToolsToEager(dir); err == nil {
		t.Fatal("a failed config write was ignored")
	}
}
