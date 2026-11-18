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
	}{
		{"missing", "", "{\n  \"toolActivation\": \"eager\"\n}\n"},
		{"object", `{"readOnly":true}`, "{\n  \"readOnly\": true,\n  \"toolActivation\": \"eager\"\n}\n"},
		{"already set", `{"toolActivation":"dynamic"}`, `{"toolActivation":"dynamic"}`},
		{"deferred", `{"toolExposure":"deferred"}`, `{"toolExposure":"deferred"}`},
		{"invalid", `nope`, `nope`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "web-search.json")
			if test.initial != "" {
				if err := os.WriteFile(path, []byte(test.initial), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			defaultWebToolsToEager(dir)
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
