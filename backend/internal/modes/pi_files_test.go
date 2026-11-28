package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteJSONFileConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	const writers = 32
	var group sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			errs <- writeJSONFile(path, map[string]any{
				"writer":  i,
				"padding": strings.Repeat("x", 4096),
			})
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("final file is corrupt: %v", err)
	}
	if _, ok := value["writer"]; !ok {
		t.Fatalf("final file = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v (%v)", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "settings.json" {
			t.Fatalf("leftover temporary file: %s", entry.Name())
		}
	}
}
