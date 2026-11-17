package modes

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
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
// app hangs up. With a hostId it browses that remote host instead.
func browse(ctx *Context) (host.Message, error) {
	if hostID, ok := ctx.Params["hostId"].(string); ok {
		browseRemote(ctx, hostID)
	} else {
		browseLocal(ctx)
	}
	<-ctx.Conn.Done()
	return host.Message{}, nil
}

func browseLocal(ctx *Context) {
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
	code := ""
	switch {
	case errors.Is(err, fs.ErrPermission):
		code = "EACCES"
	case errors.Is(err, fs.ErrNotExist):
		code = "ENOENT"
	case errors.Is(err, syscall.ENOTDIR):
		code = "ENOTDIR"
	default:
		var errno syscall.Errno
		if errors.As(err, &errno) && errno != 0 {
			code = errnoCode(errno)
		}
	}
	return listingErrorForCode(code)
}

func listingErrorForCode(code string) host.Message {
	switch code {
	case "EACCES", "EPERM":
		return host.Message{"error": "Termux can't open this folder.", "errorI18n": host.Text{Key: "browse_no_access"}}
	case "ENOENT":
		return host.Message{"error": "This folder doesn't exist.", "errorI18n": host.Text{Key: "browse_folder_missing"}}
	case "ENOTDIR":
		return host.Message{"error": "This isn't a folder.", "errorI18n": host.Text{Key: "not_a_folder"}}
	case "":
		return host.Message{"error": "Can't list this folder (unknown error).", "errorI18n": host.Text{Key: "browse_unlistable_unknown"}}
	}
	return host.Message{
		"error":     "Can't list this folder (" + code + ").",
		"errorI18n": host.Text{Key: "browse_unlistable", Args: map[string]any{"code": code}},
	}
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

// A remote host's folders, each request one ssh round trip.

const (
	remoteSearchTimeout = 20 * time.Second
	remoteSearchMatches = 500
)

type openedHost struct {
	remote remote
	home   string
	places []host.Message
}

// remotePlaces is the host's home and root, and the home `~` stands for.
// Reaching it checks the connection, so the browser opens on a host it can
// list or says why it can't.
func remotePlaces(env host.Env, hostID string, ctx context.Context) (*openedHost, *host.Error) {
	paths := remoteLayoutFor(env)
	target, err := findHost(paths, hostID)
	if err != nil {
		return nil, asHostError(err)
	}
	r := remote{paths: paths, host: target}
	probed, err := remoteHome(r, sshRunOptions{ctx: ctx, idle: readIdleMs()})
	if err != nil {
		return nil, asRemoteError(r, err)
	}
	home := probed
	if target.Home != nil {
		home = *target.Home
	}
	if target.Home == nil {
		_ = recordHome(paths, hostID, probed)
	}
	return &openedHost{
		remote: r,
		home:   home,
		places: []host.Message{
			{"kind": "remote_home", "path": home},
			{"kind": "remote_root", "path": "/"},
		},
	}, nil
}

func browseRemote(ctx *Context, hostID string) {
	base := connContext(ctx.Conn)
	opened, failure := remotePlaces(ctx.Env, hostID, base)
	if failure == nil {
		ctx.Conn.Send(host.Message{"type": "pigkin_places", "places": opened.places})
	} else {
		places := host.Message{"type": "pigkin_places", "places": []host.Message{}}
		for name, value := range errorJSON(failure) {
			places[name] = value
		}
		ctx.Conn.Send(places)
	}

	var latest atomic.Int64
	var searchMu sync.Mutex
	var cancelSearch context.CancelFunc
	ctx.Conn.Listen(func(message host.Message) {
		id, ok := message["id"].(float64)
		if !ok {
			return
		}
		switch message["type"] {
		case "pigkin_list":
			raw, ok := message["path"].(string)
			if !ok {
				return
			}
			var listing host.Message
			if failure != nil {
				listing = unreachedListing(raw, failure)
			} else {
				listing = remoteListing(base, opened.remote, raw, opened.home)
			}
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
			searchCtx, cancel := context.WithCancel(base)
			searchMu.Lock()
			if cancelSearch != nil {
				cancelSearch()
			}
			cancelSearch = cancel
			searchMu.Unlock()
			go func() {
				defer cancel()
				var paths []string
				if failure == nil {
					paths = remoteSearch(searchCtx, opened.remote, query, opened.home)
				}
				if paths != nil && latest.Load() == current {
					ctx.Conn.Send(host.Message{"type": "pigkin_search_results", "id": id, "paths": paths})
				}
			}()
		}
	})
}

// remoteListing is a remote folder for the browser, as listFolder gives a
// local one: `~` is the remote home, links are resolved with home keeping its
// spelling, and the parent is offered only when it can be listed.
func remoteListing(ctx context.Context, r remote, raw, home string) host.Message {
	resolved, ok := resolveBrowsePath(raw, home)
	if !ok {
		return host.Message{
			"path": raw, "parent": nil, "dirs": []string{},
			"error": "Paths start with / or ~.", "errorI18n": host.Text{Key: "browse_path_not_absolute"},
		}
	}
	script, err := browseListingScript(home, resolved)
	if err == nil {
		var result sshRunResult
		result, err = runScript(r, script, sshRunOptions{ctx: ctx, idle: readIdleMs()})
		if err == nil {
			fields := strings.Split(string(result.stdout), "\x00")
			if len(fields) > 0 && fields[len(fields)-1] == "" {
				fields = fields[:len(fields)-1]
			}
			status := 0
			if result.code != nil {
				status = *result.code
			}
			if status == 0 && len(fields) >= 3 {
				return remoteListingFields(r, fields)
			}
			detail := strings.TrimSpace(result.stderr)
			if detail == "" {
				detail = "exit " + strconv.Itoa(status)
			}
			err = errors.New(detail)
		}
	}
	return unreachedListing(resolved, asRemoteError(r, err))
}

func browseListingScript(home, resolved string) (string, error) {
	quotedHome, err := shellQuote(home)
	if err != nil {
		return "", err
	}
	quotedPath, err := shellQuote(resolved)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"h=" + quotedHome,
		"p=" + quotedPath,
		`H=$(cd -P -- "$h" 2>/dev/null && pwd -P && echo x); H=${H%x}; H=${H%$'\n'}`,
		`if c=$(cd -P -- "$p" 2>/dev/null && pwd -P && echo x); then`,
		`  c=${c%x}; c=${c%$'\n'}`,
		`  if [ "$c" = "$H" ]; then c=$h; elif [ -n "$H" ] && [ "$H" != / ]; then case $c in "$H"/*) c=$h${c#"$H"} ;; esac; fi`,
		"else c=$p; fi",
		`printf '%s\0' "$c"`,
		`d=${c%/*}; d=${d:-/}`,
		`if [ "$c" != / ] && [ -r "$d" ] && [ -x "$d" ]; then printf '%s\0' "$d"; else printf '\0'; fi`,
		`if [ ! -e "$c" ]; then if [ -d "$d" ] && [ ! -x "$d" ]; then printf 'EACCES\0'; else printf 'ENOENT\0'; fi; exit 0; fi`,
		`[ -d "$c" ] || { printf 'ENOTDIR\0'; exit 0; }`,
		`[ -r "$c" ] && cd -- "$c" 2>/dev/null || { printf 'EACCES\0'; exit 0; }`,
		`printf 'ok\0'`,
		"shopt -s dotglob nullglob",
		`for f in *; do [ -d "$f" ] && printf '%s\0' "$f"; done`,
		"exit 0",
	}, "\n"), nil
}

func remoteListingFields(r remote, fields []string) host.Message {
	var parent any
	if fields[1] != "" {
		parent = fields[1]
	}
	listing := host.Message{"path": fields[0], "parent": parent, "dirs": []string{}}
	switch fields[2] {
	case "ok":
		dirs := append([]string{}, fields[3:]...)
		sortFolders(dirs)
		listing["dirs"] = dirs
	case "EACCES":
		name := r.host.Name
		listing["error"] = name + " doesn't let you open this folder."
		listing["errorI18n"] = host.Text{Key: "remote_browse_no_access", Args: map[string]any{"host": name}}
	default:
		for name, value := range listingErrorForCode(fields[2]) {
			listing[name] = value
		}
	}
	return listing
}

func unreachedListing(raw string, failure *host.Error) host.Message {
	listing := host.Message{"path": raw, "parent": nil, "dirs": []string{}, "error": failure.Message}
	if failure.I18n != nil {
		listing["errorI18n"] = *failure.I18n
	}
	if code := connectionCode(failure); code != "" {
		listing["errorCode"] = code
	}
	return listing
}

var findEscapes = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)

func literalPattern(value string) string { return findEscapes.Replace(value) }

type searchTarget struct{ root, term string }

// remoteSearchPlan is where a remote search looks, and for what: a typed path's
// folder, else all of home.
func remoteSearchPlan(query, home string) *searchTarget {
	typed := strings.TrimSpace(query)
	switch typed {
	case "":
		return nil
	case "~", ".":
		return &searchTarget{root: home}
	}
	anchored := ""
	switch {
	case strings.HasPrefix(typed, "~/"), strings.HasPrefix(typed, "./"):
		anchored = home + typed[1:]
	case strings.HasPrefix(typed, "/"):
		anchored = typed
	default:
		return &searchTarget{root: home, term: typed}
	}
	slash := strings.LastIndex(anchored, "/")
	root := anchored[:slash]
	if root == "" {
		root = "/"
	}
	return &searchTarget{root: path.Clean(root), term: anchored[slash+1:]}
}

type remoteRankResult struct{ tier, depth int }

// remoteRank is how well folder path under root matches term: name exactly,
// then by prefix, then anywhere.
func remoteRank(pathValue, root, term string) remoteRankResult {
	name := strings.ToLower(path.Base(pathValue))
	lower := strings.ToLower(term)
	tier := 3
	switch {
	case name == lower:
		tier = 0
	case strings.HasPrefix(name, lower):
		tier = 1
	case strings.Contains(name, lower):
		tier = 2
	}
	rel, err := filepath.Rel(root, pathValue)
	if err != nil {
		rel = pathValue
	}
	return remoteRankResult{tier: tier, depth: strings.Count(rel, "/") + 1}
}

// remoteSearch is query's matches on the remote, best first. It runs under the
// watchdog, so a newer search and the time limit stop find on the remote too.
func remoteSearch(ctx context.Context, r remote, query, home string) []string {
	target := remoteSearchPlan(query, home)
	if target == nil || strings.ContainsRune(query, 0) {
		return []string{}
	}
	test := "-iname"
	if strings.Contains(target.term, "/") {
		test = "-ipath"
	}
	names := make([]string, 0, len(ignoredFolders))
	for name := range ignoredFolders {
		names = append(names, name)
	}
	sort.Strings(names)
	prune := `-name '.*'`
	for _, name := range names {
		quoted, err := shellQuote(name)
		if err != nil {
			return []string{}
		}
		prune += " -o -name " + quoted
	}
	depth := maxDepth
	if target.term == "" {
		depth = 1
	}
	quotedRoot, err := shellQuote(target.root)
	if err != nil {
		return []string{}
	}
	quotedPattern, err := shellQuote("*" + literalPattern(target.term) + "*")
	if err != nil {
		return []string{}
	}
	command := "find " + quotedRoot + " -mindepth 1 -maxdepth " + strconv.Itoa(depth) +
		" \\( " + prune + " \\) -prune -o -type d " + test + " " + quotedPattern + " -print0 2>/dev/null"

	enoughCtx, enoughCancel := context.WithCancel(ctx)
	defer enoughCancel()
	var chunks [][]byte
	matches := 0
	_, _ = runWatched(r, command, "/", sshRunOptions{
		ctx:     enoughCtx,
		timeout: remoteSearchTimeout,
		onStdout: func(chunk []byte) {
			chunks = append(chunks, chunk)
			for _, b := range chunk {
				if b == 0 {
					matches++
				}
			}
			if matches >= remoteSearchMatches {
				enoughCancel()
			}
		},
	})
	if ctx.Err() != nil {
		return nil
	}
	type ranked struct {
		value string
		rank  remoteRankResult
	}
	list := make([]ranked, 0)
	for _, value := range bytes.Split(bytes.Join(chunks, nil), []byte{0}) {
		if len(value) > 0 {
			pathValue := string(value)
			list = append(list, ranked{pathValue, remoteRank(pathValue, target.root, target.term)})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		left, right := list[i], list[j]
		if left.rank.tier != right.rank.tier {
			return left.rank.tier < right.rank.tier
		}
		if left.rank.depth != right.rank.depth {
			return left.rank.depth < right.rank.depth
		}
		return left.value < right.value
	})
	paths := make([]string, 0, searchLimit)
	seen := map[string]bool{}
	for _, entry := range list {
		if seen[entry.value] {
			continue
		}
		seen[entry.value] = true
		paths = append(paths, entry.value)
		if len(paths) == searchLimit {
			break
		}
	}
	return paths
}
