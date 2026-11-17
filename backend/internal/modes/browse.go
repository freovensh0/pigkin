package modes

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// Where Android mounts shared storage volumes; emulated and self are not
// volumes themselves.
const storageRoot = "/storage"

// Limits the folder search shares with the TypeScript host.
const (
	searchLimit   = 30
	searchMatches = 500
	maxDepth      = 12
)

// Folders a search never enters or offers, besides hidden ones.
var ignoredFolders = map[string]bool{
	"node_modules": true, "venv": true, "env": true, "virtualenv": true,
	"dist": true, "build": true, "target": true, "out": true,
	"coverage": true, "vendor": true, "__pycache__": true, ".git": true,
}

// browse is the long-lived folder listing for the app's folder browser: it
// sends pigkin_places once, then answers each pigkin_list with a
// pigkin_listing and each pigkin_search with pigkin_search_results until the
// app hangs up.
//
// Remote hosts are not ported yet; the TypeScript host still browses them.
func browse(ctx *Context) (host.Message, error) {
	if _, ok := ctx.Params["hostId"].(string); ok {
		return nil, host.NewError("bad_params", "Remote hosts are not ported to the Go host yet", nil)
	}

	places := localPlaces(ctx.Env)
	ctx.Conn.Send(host.Message{"type": "pigkin_places", "places": places})

	var latest atomic.Int64
	ctx.Conn.Listen(func(message host.Message) {
		id, ok := message["id"].(float64)
		if !ok {
			return
		}
		switch message["type"] {
		case "pigkin_list":
			path, ok := message["path"].(string)
			if !ok {
				return
			}
			listing := listFolder(path, ctx.Env.Home)
			listing["type"] = "pigkin_listing"
			listing["id"] = id
			ctx.Conn.Send(listing)
		case "pigkin_search":
			query, ok := message["query"].(string)
			if !ok {
				return
			}
			current := int64(id)
			latest.Store(current)
			go func() {
				paths := searchFolders(query, places, ctx.Env.Home, func() bool {
					return latest.Load() != current
				})
				if paths != nil && latest.Load() == current {
					ctx.Conn.Send(host.Message{"type": "pigkin_search_results", "id": id, "paths": paths})
				}
			}()
		}
	})

	<-ctx.Conn.Done()
	return host.Message{}, nil
}

// localPlaces is Termux home, phone storage when Termux may write it, and any
// other mounted volume.
func localPlaces(env host.Env) []host.Message {
	places := []host.Message{{"kind": "home", "path": env.Home}}
	if shared := host.GrantedSharedStorage(env); shared != "" {
		places = append(places, host.Message{"kind": "shared", "path": shared})
	}
	entries, err := os.ReadDir(storageRoot)
	if err != nil {
		return places
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(storageRoot, name)
		if name == "emulated" || name == "self" || !isDirectory(path) {
			continue
		}
		places = append(places, host.Message{"kind": "volume", "path": path, "name": name})
	}
	return places
}

// listFolder answers one pigkin_list: the canonical path, an openable parent,
// and its subfolders. A failure is part of the listing, so the browser can show
// it in place.
func listFolder(raw, home string) host.Message {
	resolved, ok := resolveBrowsePath(raw, home)
	if !ok {
		return host.Message{
			"path":      raw,
			"parent":    nil,
			"dirs":      []string{},
			"error":     "Paths start with / or ~.",
			"errorI18n": host.Text{Key: "browse_path_not_absolute"},
		}
	}
	path := canonical(resolved, home)
	var parent any
	if path != "/" {
		parent = openableParent(filepath.Dir(path))
	}
	dirs, err := subfolders(path)
	if err != nil {
		listing := listingError(err)
		listing["path"] = path
		listing["parent"] = parent
		listing["dirs"] = []string{}
		return listing
	}
	return host.Message{"path": path, "parent": parent, "dirs": dirs}
}

// resolveBrowsePath turns what the browser typed into a path: ~ and ~/… are
// Termux home, anything else must be absolute. .. segments are normalised.
func resolveBrowsePath(raw, home string) (string, bool) {
	if raw == "~" {
		return home, true
	}
	if rest, found := strings.CutPrefix(raw, "~/"); found {
		return filepath.Join(home, rest), true
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), true
	}
	return "", false
}

// canonical resolves links, so a folder reached through one (Termux's
// ~/storage/*) has a single spelling. Home keeps the spelling Termux reports,
// since on Android it lies under the /data/data link. Unreadable paths stay as
// given; their listing says why.
func canonical(path, home string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return real
	}
	if real == realHome {
		return home
	}
	if rest, found := strings.CutPrefix(real, realHome+"/"); found {
		return home + "/" + rest
	}
	return real
}

// openableParent is parent when Termux can list it, so the browser never
// offers a step up into a refusal.
func openableParent(parent string) any {
	handle, err := os.Open(parent)
	if err != nil {
		return nil
	}
	_ = handle.Close()
	return parent
}

// subfolders lists subfolder names, following symlinks (Termux's ~/storage/*
// are links), dot folders last.
func subfolders(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		full := filepath.Join(path, entry.Name())
		if entry.IsDir() || (entry.Type()&fs.ModeSymlink != 0 && isDirectory(full)) {
			names = append(names, entry.Name())
		}
	}
	sortFolders(names)
	return names, nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sortFolders orders names the way the browser lists them: dot folders last,
// numbers by value.
func sortFolders(names []string) {
	sort.SliceStable(names, func(i, j int) bool {
		left, right := names[i], names[j]
		leftHidden, rightHidden := strings.HasPrefix(left, "."), strings.HasPrefix(right, ".")
		if leftHidden != rightHidden {
			return !leftHidden
		}
		if order := naturalCompare(left, right); order != 0 {
			return order < 0
		}
		return left < right
	})
}

// naturalCompare compares case-insensitively, with digit runs by value, as
// Intl.Collator does for the names the browser shows.
func naturalCompare(left, right string) int {
	a, b := []rune(left), []rune(right)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if unicode.IsDigit(a[i]) && unicode.IsDigit(b[j]) {
			startA, startB := i, j
			for i < len(a) && unicode.IsDigit(a[i]) {
				i++
			}
			for j < len(b) && unicode.IsDigit(b[j]) {
				j++
			}
			digitsA := strings.TrimLeft(string(a[startA:i]), "0")
			digitsB := strings.TrimLeft(string(b[startB:j]), "0")
			if len(digitsA) != len(digitsB) {
				return len(digitsA) - len(digitsB)
			}
			if order := strings.Compare(digitsA, digitsB); order != 0 {
				return order
			}
			continue
		}
		lowerA, lowerB := unicode.ToLower(a[i]), unicode.ToLower(b[j])
		if lowerA != lowerB {
			return int(lowerA) - int(lowerB)
		}
		i++
		j++
	}
	return len(a) - len(b)
}

// listingError is a listing's error and errorI18n for a failed read.
func listingError(err error) host.Message {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return host.Message{"error": "Termux can't open this folder.", "errorI18n": host.Text{Key: "browse_no_access"}}
	case errors.Is(err, fs.ErrNotExist):
		return host.Message{"error": "This folder doesn't exist.", "errorI18n": host.Text{Key: "browse_folder_missing"}}
	case errors.Is(err, syscall.ENOTDIR):
		return host.Message{"error": "This isn't a folder.", "errorI18n": host.Text{Key: "not_a_folder"}}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		code := errnoCode(errno)
		return host.Message{
			"error":     "Can't list this folder (" + code + ").",
			"errorI18n": host.Text{Key: "browse_unlistable", Args: map[string]any{"code": code}},
		}
	}
	return host.Message{"error": "Can't list this folder (unknown error).", "errorI18n": host.Text{Key: "browse_unlistable_unknown"}}
}

func errnoCode(errno syscall.Errno) string {
	names := map[syscall.Errno]string{
		syscall.EIO:          "EIO",
		syscall.ELOOP:        "ELOOP",
		syscall.ENAMETOOLONG: "ENAMETOOLONG",
		syscall.ENOMEM:       "ENOMEM",
		syscall.EOVERFLOW:    "EOVERFLOW",
	}
	if name, ok := names[errno]; ok {
		return name
	}
	return "E" + strings.ToUpper(strings.ReplaceAll(errno.Error(), " ", "_"))
}

// searchFolders finds folders under the places whose path holds the query.
//
// shortcut: matches names by substring and ranks exact, prefix, then contains;
// Paseo's path/fuzzy tiers arrive with the browse port.
func searchFolders(query string, places []host.Message, home string, cancelled func() bool) []string {
	term, roots, limit := searchPlan(query, places, home)
	if len(roots) == 0 {
		return []string{}
	}
	type match struct {
		path  string
		tier  int
		depth int
	}
	var found []match
	seen := map[string]bool{}
	for _, root := range roots {
		walkFolders(root, "", 0, limit, func(visible, relative string, depth int) bool {
			if cancelled() {
				return false
			}
			tier := matchTier(relative, term)
			if tier < 0 || seen[visible] {
				return true
			}
			seen[visible] = true
			found = append(found, match{path: visible, tier: tier, depth: depth})
			return len(found) < searchMatches
		})
	}
	if cancelled() {
		return nil
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].tier != found[j].tier {
			return found[i].tier < found[j].tier
		}
		if found[i].depth != found[j].depth {
			return found[i].depth < found[j].depth
		}
		return found[i].path < found[j].path
	})
	paths := make([]string, 0, searchLimit)
	for _, entry := range found {
		paths = append(paths, entry.path)
		if len(paths) == searchLimit {
			break
		}
	}
	return paths
}

// searchPlan splits a query into the term to match, the roots to walk, and how
// deep: a typed path anchors the walk at that path's folder, and ~ lists home's
// own folders.
func searchPlan(query string, places []host.Message, home string) (string, []string, int) {
	trimmed := strings.TrimSpace(query)
	switch {
	case trimmed == "":
		return "", nil, 0
	case trimmed == "~" || trimmed == "." || trimmed == "~/":
		return "", []string{home}, 1
	case strings.HasPrefix(trimmed, "~/"):
		term, roots := pathQuery(filepath.Join(home, trimmed[2:]))
		return term, roots, maxDepth
	case strings.HasPrefix(trimmed, "/"):
		term, roots := pathQuery(trimmed)
		return term, roots, maxDepth
	}
	roots := make([]string, 0, len(places))
	for _, place := range places {
		if path, ok := place["path"].(string); ok {
			roots = append(roots, path)
		}
	}
	return strings.ToLower(trimmed), roots, maxDepth
}

func pathQuery(path string) (string, []string) {
	if strings.HasSuffix(path, "/") {
		return "", []string{filepath.Clean(path)}
	}
	cleaned := filepath.Clean(path)
	return strings.ToLower(filepath.Base(cleaned)), []string{filepath.Dir(cleaned)}
}

// walkFolders visits root's folders, skipping hidden and ignored names; visit
// returns false to stop the walk.
func walkFolders(root, relative string, depth, limit int, visit func(visible, relative string, depth int) bool) bool {
	if depth >= limit {
		return true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return true
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || ignoredFolders[name] {
			continue
		}
		visible := filepath.Join(root, name)
		if !isDirectory(visible) {
			continue
		}
		childRelative := filepath.Join(relative, name)
		if !visit(visible, childRelative, depth+1) {
			return false
		}
		if !walkFolders(visible, childRelative, depth+1, limit, visit) {
			return false
		}
	}
	return true
}

// matchTier ranks relative against term: -1 for no match, 0 exact name, 1
// prefix, 2 anywhere in the path.
func matchTier(relative, term string) int {
	name := strings.ToLower(filepath.Base(relative))
	switch {
	case term == "":
		return 2
	case name == term:
		return 0
	case strings.HasPrefix(name, term):
		return 1
	case strings.Contains(strings.ToLower(relative), term):
		return 2
	}
	return -1
}
