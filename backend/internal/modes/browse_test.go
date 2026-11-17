package modes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func TestResolveBrowsePath(t *testing.T) {
	home := "/data/data/com.termux/files/home"
	cases := map[string]string{"~": home, "~/src": home + "/src", "~//src": home + "/src", "/etc/../usr": "/usr"}
	for raw, want := range cases {
		got, ok := resolveBrowsePath(raw, home)
		if !ok || got != want {
			t.Fatalf("%q: got %q, %v; want %q", raw, got, ok, want)
		}
	}
	if _, ok := resolveBrowsePath("src", home); ok {
		t.Fatal("a relative path must be refused")
	}
}

func TestListFolderSortsDotFoldersLastAndNumbersByValue(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b", "A", "10", "2", ".hidden", "app-2", "app-10"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing := listFolder(dir, dir)
	dirs, ok := listing["dirs"].([]string)
	if !ok {
		t.Fatalf("got %T", listing["dirs"])
	}
	want := []string{"2", "10", "A", "app-2", "app-10", "b", ".hidden"}
	if !reflect.DeepEqual(dirs, want) {
		t.Fatalf("got %q, want %q", dirs, want)
	}
	if listing["parent"] != filepath.Dir(dir) {
		t.Fatalf("parent %v, want %q", listing["parent"], filepath.Dir(dir))
	}
}

func TestListFolderFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	listing := listFolder(dir, dir)
	if !reflect.DeepEqual(listing["dirs"], []string{"link", "real"}) {
		t.Fatalf("got %v", listing["dirs"])
	}
}

func TestListFolderKeepsUnreadablePathsInTheListing(t *testing.T) {
	listing := listFolder("/definitely/not/here", "/")
	if listing["error"] == nil {
		t.Fatalf("got %v, want an error in the listing", listing)
	}
	if listing["errorI18n"].(host.Text).Key != "browse_folder_missing" {
		t.Fatalf("got %v", listing["errorI18n"])
	}
}

func TestListFolderRefusesRelativePaths(t *testing.T) {
	listing := listFolder("src", "/home")
	if listing["errorI18n"].(host.Text).Key != "browse_path_not_absolute" {
		t.Fatalf("got %v", listing)
	}
}

func TestSearchFindsFoldersAndSkipsNoise(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"src/app", "src/lib", "docs", "node_modules/dep", ".cache"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	places := []host.Message{{"kind": "home", "path": home}}

	paths := searchFolders("ap", places, home, func() bool { return false })
	want := []string{filepath.Join(home, "src", "app")}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("got %q, want %q", paths, want)
	}

	children := searchFolders("~", places, home, func() bool { return false })
	wantChildren := []string{filepath.Join(home, "docs"), filepath.Join(home, "src")}
	if !reflect.DeepEqual(children, wantChildren) {
		t.Fatalf("got %q, want home's own folders %q", children, wantChildren)
	}
}

func TestSearchStopsWhenCancelled(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := searchFolders("src", []host.Message{{"path": home}}, home, func() bool { return true })
	if paths != nil {
		t.Fatalf("got %q, want no answer", paths)
	}
}

func TestNaturalCompare(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"2", "10", -1},
		{"10", "2", 1},
		{"A", "b", -1},
		{"app-2", "app-10", -1},
		{"same", "same", 0},
	}
	for _, c := range cases {
		got := naturalCompare(c.left, c.right)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Fatalf("%q vs %q: got %d, want %d", c.left, c.right, got, c.want)
		}
	}
}
