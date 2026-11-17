package modes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	// textLimit keeps an escaped text file well under the app's 16 MiB line.
	textLimit = 2 * 1024 * 1024
	// imageLimit leaves base64 overhead under that line too.
	imageLimit = 8 * 1024 * 1024
	// sniffBytes is enough of an oversized file to tell binary from text.
	sniffBytes = 8 * 1024
	// controlRatio is Paseo's threshold for control characters in text.
	controlRatio = 0.3
)

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
}

// fileMessages is what the app shows for Node's error codes.
var fileMessages = map[string]struct{ text, key string }{
	"EACCES":  {"Termux can't open this.", "files_no_access"},
	"EPERM":   {"Termux can't open this.", "files_no_access"},
	"ENOENT":  {"This file or folder no longer exists.", "files_gone"},
	"ENOTDIR": {"This isn't a folder.", "not_a_folder"},
	"EISDIR":  {"This is a folder.", "files_is_folder"},
	"ELOOP":   {"This link goes round in circles.", "files_link_loop"},
	"EROFS":   {"This storage is read-only.", "files_read_only"},
	"ENOSPC":  {"The device is full.", "files_device_full"},
}

// errnoNames names the errno values the port can name; anything else falls
// back to the unknown-error wording.
var errnoNames = map[syscall.Errno]string{
	syscall.EACCES:       "EACCES",
	syscall.EPERM:        "EPERM",
	syscall.ENOENT:       "ENOENT",
	syscall.ENOTDIR:      "ENOTDIR",
	syscall.EISDIR:       "EISDIR",
	syscall.ELOOP:        "ELOOP",
	syscall.EROFS:        "EROFS",
	syscall.ENOSPC:       "ENOSPC",
	syscall.EIO:          "EIO",
	syscall.ENAMETOOLONG: "ENAMETOOLONG",
	syscall.ENOMEM:       "ENOMEM",
	syscall.EOVERFLOW:    "EOVERFLOW",
	syscall.ENOTEMPTY:    "ENOTEMPTY",
	syscall.EEXIST:       "EEXIST",
	syscall.EBUSY:        "EBUSY",
}

const remoteNotPorted = "Remote workspaces are not ported to the Go host yet"

// files is the long-lived workspace file access for the app's file browser: it
// sends pigkin_files_ready, then answers each request with a reply echoing its
// id until the app hangs up. pigkin_files_list gives a folder's entries and
// pigkin_files_read a file's content, kind and size; pigkin_files_mkdir makes
// a folder. root is absolute and every other path is relative to it,
// `/`-separated, `.` for the root itself, and may not leave it.
//
// Remote workspaces are not ported yet, so their requests are refused.
func files(ctx *Context) (host.Message, error) {
	if _, ok := ctx.Params["hostId"].(string); ok {
		return nil, host.NewError("bad_params", remoteNotPorted, nil)
	}
	ctx.Conn.Send(host.Message{"type": "pigkin_files_ready"})
	ctx.Conn.Listen(func(message host.Message) {
		id, ok := message["id"].(float64)
		if !ok {
			return
		}
		root, ok := message["root"].(string)
		if !ok {
			return
		}
		path, ok := message["path"].(string)
		if !ok {
			return
		}
		switch message["type"] {
		case "pigkin_files_list":
			go answer(ctx.Conn, "pigkin_files_listing", id, listFiles(ctx.Env, root, path))
		case "pigkin_files_read":
			go answer(ctx.Conn, "pigkin_files_file", id, readFile(ctx.Env, root, path))
		case "pigkin_files_mkdir":
			name, ok := message["name"].(string)
			if !ok {
				return
			}
			go answer(ctx.Conn, "pigkin_files_made", id, makeFolder(ctx.Env, root, path, name))
		}
	})
	<-ctx.Conn.Done()
	return host.Message{}, nil
}

func answer(conn *host.Connection, kind string, id float64, result host.Message) {
	result["type"] = kind
	result["id"] = id
	conn.Send(result)
}

// listFiles answers a folder's entries, folders first. A failure is part of the
// listing, so the browser can show it in place.
func listFiles(env host.Env, root, raw string) host.Message {
	path := raw
	if remoteWorkspace(env, root) {
		return host.Message{"path": path, "entries": []host.Message{}, "error": remoteNotPorted, "code": "bad_params"}
	}
	dir, err := scope(root, raw)
	if err != nil {
		return withError(host.Message{"path": path, "entries": []host.Message{}}, err)
	}
	path = relativeTo(root, dir.requested)
	entries, err := listEntries(dir, path)
	if err != nil {
		return withError(host.Message{"path": path, "entries": []host.Message{}}, err)
	}
	sortEntries(entries)
	return host.Message{"path": path, "entries": entries}
}

// readFile answers a file's kind, size and, for text and images within their
// limits, content.
func readFile(env host.Env, root, raw string) host.Message {
	path := raw
	if remoteWorkspace(env, root) {
		return host.Message{"path": path, "error": remoteNotPorted, "code": "bad_params"}
	}
	file, err := scope(root, raw)
	if err != nil {
		return withError(host.Message{"path": path}, err)
	}
	path = relativeTo(root, file.requested)
	info, err := os.Stat(file.real)
	if err != nil {
		return withError(host.Message{"path": path}, err)
	}
	if info.IsDir() {
		return withError(host.Message{"path": path}, isFolder())
	}
	if !info.Mode().IsRegular() {
		return withError(host.Message{"path": path}, notRegular())
	}
	image := isImage(file.requested)
	needed := bytesNeeded(info.Size(), image)
	var data []byte
	if needed > 0 {
		data, err = readStart(file.real, needed)
		if err != nil {
			return withError(host.Message{"path": path}, err)
		}
	}
	return fileReply(path, info.Size(), image, data)
}

// makeFolder answers the new folder's workspace-relative path.
func makeFolder(env host.Env, root, raw, rawName string) host.Message {
	name := strings.TrimSpace(rawName)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return describe(host.NewError("bad_name", "Folder names can't be empty, contain / or be . or ..", &host.Text{Key: "files_folder_name_invalid"}))
	}
	if remoteWorkspace(env, root) {
		return host.Message{"error": remoteNotPorted, "code": "bad_params"}
	}
	parent, err := scope(root, raw)
	if err != nil {
		return describe(err)
	}
	if err := os.Mkdir(filepath.Join(parent.real, name), 0o777); err != nil {
		if errors.Is(err, fs.ErrExist) {
			err = nameTaken(name)
		}
		return describe(err)
	}
	return host.Message{"path": relativeTo(root, filepath.Join(parent.requested, name))}
}

func describe(err error) host.Message {
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		reply := host.Message{"error": hostErr.Message}
		if hostErr.I18n != nil {
			reply["errorI18n"] = *hostErr.I18n
		}
		return reply
	}
	code := errnoName(err)
	if code == "" {
		return host.Message{"error": "Can't read this (unknown error).", "errorI18n": host.Text{Key: "files_unreadable_unknown"}}
	}
	if known, ok := fileMessages[code]; ok {
		return host.Message{"error": known.text, "errorI18n": host.Text{Key: known.key}}
	}
	return host.Message{"error": "Can't read this (" + code + ").", "errorI18n": host.Text{Key: "files_unreadable", Args: map[string]any{"code": code}}}
}

// withError adds a failure's fields to reply.
func withError(reply host.Message, err error) host.Message {
	for name, value := range describe(err) {
		reply[name] = value
	}
	return reply
}

func errnoName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return ""
	}
	return errnoNames[errno]
}

func isMissing(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP))
}

func folderNotFound(path string) error {
	return host.NewError("not_found", "Folder not found: "+path, &host.Text{Key: "folder_not_found", Args: map[string]any{"path": path}})
}

func outsideWorkspace() error {
	return host.NewError("outside_workspace", "That path is outside the workspace.", &host.Text{Key: "files_outside_workspace"})
}

func isFolder() error {
	return host.NewError("is_folder", "This is a folder.", &host.Text{Key: "files_is_folder"})
}

func notRegular() error {
	return host.NewError("not_file", "This isn't a regular file.", &host.Text{Key: "files_not_regular"})
}

func nameTaken(name string) error {
	return host.NewError("name_taken", "A file or folder named "+name+" already exists.", &host.Text{Key: "files_name_taken", Args: map[string]any{"name": name}})
}

// scoped is a workspace path: requested as the app named it, real with links
// resolved, both under the workspace root.
type scoped struct {
	realRoot  string
	requested string
	real      string
}

// scope resolves raw under root, refusing anything that leaves it by `..` or
// through a link.
func scope(root, raw string) (scoped, error) {
	realRoot, err := realFolder(root)
	if err != nil {
		return scoped{}, folderNotFound(root)
	}
	requested := resolvePath(root, raw)
	if !inside(root, requested) {
		return scoped{}, outsideWorkspace()
	}
	real, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return scoped{}, err
	}
	if !inside(realRoot, real) {
		return scoped{}, outsideWorkspace()
	}
	return scoped{realRoot: realRoot, requested: requested, real: real}, nil
}

func realFolder(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("not absolute")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a folder")
	}
	return real, nil
}

// resolvePath is Node's path.resolve for root and raw: an absolute raw wins,
// and `..` segments are normalised.
func resolvePath(root, raw string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Join(root, raw)
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func relativeTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "" {
		return "."
	}
	return rel
}

func childPath(path, name string) string {
	if path == "." {
		return name
	}
	return path + "/" + name
}

// listEntries lists dir without dangling links and links out of the workspace.
func listEntries(dir scoped, path string) ([]host.Message, error) {
	dirents, err := os.ReadDir(dir.real)
	if err != nil {
		return nil, err
	}
	entries := make([]host.Message, 0, len(dirents))
	for _, dirent := range dirents {
		info, ok, err := entryInfo(dir, dirent)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		entries = append(entries, host.Message{
			"name":     dirent.Name(),
			"path":     childPath(path, dirent.Name()),
			"kind":     kind,
			"size":     info.Size(),
			"modified": info.ModTime().UnixMilli(),
		})
	}
	return entries, nil
}

// entryInfo stats dirent, following links; false for dangling links and links
// out of the workspace.
func entryInfo(dir scoped, dirent os.DirEntry) (fs.FileInfo, bool, error) {
	target := filepath.Join(dir.real, dirent.Name())
	if dirent.Type()&fs.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(target)
		if isMissing(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if !inside(dir.realRoot, real) {
			return nil, false, nil
		}
		target = real
	}
	info, err := os.Stat(target)
	if isMissing(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return info, true, nil
}

// sortEntries orders entries folders first, then by name ignoring case and
// reading numbers.
func sortEntries(entries []host.Message) {
	sort.SliceStable(entries, func(i, j int) bool {
		leftDir, rightDir := entries[i]["kind"] == "directory", entries[j]["kind"] == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		left, right := entries[i]["name"].(string), entries[j]["name"].(string)
		if order := naturalCompare(left, right); order != 0 {
			return order < 0
		}
		return left < right
	})
}

// readStart reads up to length bytes from the start of path.
func readStart(path string, length int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, length)
	filled, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buffer[:filled], nil
}

// asText renders data as text, or refuses when it looks binary: a NUL, invalid
// UTF-8, or too many control characters. A partial read may end mid-character,
// which isn't held against it.
func asText(data []byte, partial bool) (string, bool) {
	control := 0
	for _, b := range data {
		if b == 0 {
			return "", false
		}
		if (b < 32 && b != '\t' && b != '\n' && b != '\r') || b == 127 {
			control++
		}
	}
	if len(data) > 0 && float64(control)/float64(len(data)) > controlRatio {
		return "", false
	}
	if partial {
		data = trimIncomplete(data)
	}
	if !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}

// trimIncomplete drops a UTF-8 sequence cut off at the end of data.
func trimIncomplete(data []byte) []byte {
	for n := 1; n <= 3 && n <= len(data); n++ {
		start := data[len(data)-n]
		runeLength := leadingLength(start)
		if runeLength <= n {
			continue
		}
		continuations := true
		for _, b := range data[len(data)-n+1:] {
			if b&0xC0 != 0x80 {
				continuations = false
				break
			}
		}
		if continuations {
			return data[:len(data)-n]
		}
	}
	return data
}

// leadingLength is the byte length a UTF-8 sequence starting with b needs, or
// 0 when b can't start one.
func leadingLength(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// bytesNeeded how much of a file of size bytes its reply needs: all of an image
// or text file within its limit, none of a larger image, and the first sniff of
// a larger file, to tell binary from text.
func bytesNeeded(size int64, image bool) int64 {
	if image {
		if size > imageLimit {
			return 0
		}
		return size
	}
	if size > textLimit {
		return sniffBytes
	}
	return size
}

// fileReply is the reply for file path of size bytes, from the bytes
// bytesNeeded asked for.
func fileReply(path string, size int64, image bool, data []byte) host.Message {
	if image {
		if size > imageLimit {
			return host.Message{"path": path, "size": size, "kind": "too_large"}
		}
		return host.Message{"path": path, "size": len(data), "kind": "image", "content": base64.StdEncoding.EncodeToString(data)}
	}
	if size > textLimit {
		kind := "binary"
		if _, ok := asText(data, true); ok {
			kind = "too_large"
		}
		return host.Message{"path": path, "size": size, "kind": kind}
	}
	content, ok := asText(data, false)
	if !ok {
		return host.Message{"path": path, "size": len(data), "kind": "binary"}
	}
	return host.Message{"path": path, "size": len(data), "kind": "text", "content": content}
}

func isImage(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// remoteWorkspace reports whether root is a remote workspace's stand-in folder:
// a folder under the remotes beside Pi's config whose manifest names the same
// remote folder its name hashes.
func remoteWorkspace(env host.Env, root string) bool {
	remotes := filepath.Join(filepath.Dir(env.ConfigDir), "remotes")
	folder := filepath.Clean(root)
	parent := filepath.Dir(filepath.Dir(folder))
	if parent != remotes {
		real, err := filepath.EvalSymlinks(remotes)
		if err != nil || parent != real {
			return false
		}
	}
	data, err := os.ReadFile(filepath.Join(folder, "remote.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Version int    `json:"version"`
		HostID  string `json:"hostId"`
		Path    string `json:"path"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 {
		return false
	}
	if manifest.HostID == "" || !strings.HasPrefix(manifest.Path, "/") {
		return false
	}
	sum := sha256.Sum256([]byte(manifest.Path))
	return filepath.Base(filepath.Dir(folder)) == manifest.HostID && filepath.Base(folder) == hex.EncodeToString(sum[:])[:16]
}
