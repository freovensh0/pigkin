package modes

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Remote file operations, each one ssh round trip. The scripts run in bash and
// fail through their exit status, as errno names.
type remoteFileError struct {
	code    string
	path    string
	message string
}

func (err *remoteFileError) Error() string { return err.message }

var (
	exitErrno = map[int]string{2: "ENOENT", 13: "EACCES", 20: "ENOTDIR", 21: "EISDIR"}
	errnoDesc = map[string]string{
		"ENOENT":  "no such file or directory",
		"EACCES":  "permission denied",
		"ENOTDIR": "not a directory",
		"EISDIR":  "illegal operation on a directory",
		"EIO":     "i/o error",
	}
)

const statFormat = `if stat -c %s / >/dev/null 2>&1; then fmt=(-L -c '%s %Y'); else fmt=(-L -f '%z %m'); fi`

func runFileScript(r remote, script, syscallName, path string, opts sshRunOptions) ([]byte, error) {
	result, err := runScript(r, script, opts)
	if err != nil {
		return nil, err
	}
	if result.code != nil && *result.code == 0 {
		return result.stdout, nil
	}
	code := "EIO"
	if result.code != nil {
		if errno, ok := exitErrno[*result.code]; ok {
			code = errno
		}
	}
	detail := ""
	if code == "EIO" {
		if trimmed := strings.TrimSpace(result.stderr); trimmed != "" {
			detail = ": " + trimmed
		}
	}
	return nil, &remoteFileError{
		code:    code,
		path:    path,
		message: fmt.Sprintf("%s: %s, %s '%s'%s", code, errnoDesc[code], syscallName, path, detail),
	}
}

func readRemoteFile(r remote, path string, opts sshRunOptions) ([]byte, error) {
	quoted, err := shellQuote(path)
	if err != nil {
		return nil, err
	}
	script := strings.Join([]string{
		"p=" + quoted,
		`[ -d "$p" ] && exit 21`,
		`[ -e "$p" ] || exit 2`,
		`[ -r "$p" ] || exit 13`,
		`exec cat -- "$p"`,
	}, "\n")
	return runFileScript(r, script, "open", path, opts)
}

func accessRemote(r remote, path string, opts sshRunOptions) error {
	quoted, err := shellQuote(path)
	if err != nil {
		return err
	}
	script := strings.Join([]string{
		"p=" + quoted,
		`[ -e "$p" ] || exit 2`,
		`[ -r "$p" ] || exit 13`,
	}, "\n")
	_, err = runFileScript(r, script, "access", path, opts)
	return err
}

func writeRemoteFile(r remote, path string, data []byte, opts sshRunOptions) error {
	quoted, err := shellQuote(path)
	if err != nil {
		return err
	}
	script := strings.Join([]string{
		"p=" + quoted,
		`case $p in */*) d=${p%/*}; d=${d:-/} ;; *) d=. ;; esac`,
		`[ -d "$p" ] && exit 21`,
		`[ -d "$d" ] || exit 2`,
		`if [ -e "$p" ]; then [ -w "$p" ] || exit 13; else [ -w "$d" ] || exit 13; fi`,
		`cat > "$p"`,
	}, "\n")
	opts.stdin = data
	_, err = runFileScript(r, script, "open", path, opts)
	return err
}

func mkdirRemote(r remote, path string, opts sshRunOptions) error {
	quoted, err := shellQuote(path)
	if err != nil {
		return err
	}
	script := strings.Join([]string{
		"p=" + quoted,
		`mkdir -p -- "$p" 2>/dev/null && exit 0`,
		`[ -e "$p" ] && exit 20`,
		"exit 13",
	}, "\n")
	_, err = runFileScript(r, script, "mkdir", path, opts)
	return err
}

func remoteHome(r remote, opts sshRunOptions) (string, error) {
	data, err := runFileScript(r, `printf '%s' "$HOME"`, "home", "~", opts)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func realRemoteDir(r remote, path string, opts sshRunOptions) (string, error) {
	quoted, err := shellQuote(path)
	if err != nil {
		return "", err
	}
	script := strings.Join([]string{
		"p=" + quoted,
		`[ -e "$p" ] || exit 2`,
		`[ -d "$p" ] || exit 20`,
		`cd -P -- "$p" 2>/dev/null || exit 13`,
		`printf '%s' "$PWD"`,
	}, "\n")
	data, err := runFileScript(r, script, "realpath", path, opts)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type remoteKind string

const (
	remoteFile      remoteKind = "file"
	remoteDirectory remoteKind = "directory"
	remoteOther     remoteKind = "other"
)

type remoteStat struct {
	kind    remoteKind
	size    int64
	mtimeMs int64
}

// statRemote reports what path is, following links; nil when nothing is there.
func statRemote(r remote, path string, opts sshRunOptions) (*remoteStat, error) {
	quoted, err := shellQuote(path)
	if err != nil {
		return nil, err
	}
	script := strings.Join([]string{
		statFormat,
		"p=" + quoted,
		`[ -e "$p" ] || exit 2`,
		`if [ -d "$p" ]; then k=directory; elif [ -f "$p" ]; then k=file; else k=other; fi`,
		`s=$(stat "${fmt[@]}" -- "$p") || exit 13`,
		`printf '%s %s\n' "$k" "$s"`,
	}, "\n")
	data, err := runFileScript(r, script, "stat", path, opts)
	if err != nil {
		var fileErr *remoteFileError
		if asRemoteFileError(err, &fileErr) && fileErr.code == "ENOENT" {
			return nil, nil
		}
		return nil, err
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) != 3 || (fields[0] != string(remoteDirectory) && fields[0] != string(remoteFile) && fields[0] != string(remoteOther)) {
		return nil, &remoteFileError{code: "EIO", path: path, message: "EIO: unexpected stat output, stat '" + path + "'"}
	}
	size, _ := strconv.ParseInt(fields[1], 10, 64)
	mtime, _ := strconv.ParseInt(fields[2], 10, 64)
	return &remoteStat{kind: remoteKind(fields[0]), size: size, mtimeMs: mtime * 1000}, nil
}

func asRemoteFileError(err error, target **remoteFileError) bool {
	fileErr, ok := err.(*remoteFileError)
	if ok {
		*target = fileErr
	}
	return ok
}

type remoteEntry struct {
	name    string
	kind    remoteKind
	size    int64
	mtimeMs int64
	symlink bool
	target  string
}

func listingScript(path string) (string, error) {
	quoted, err := shellQuote(path)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		statFormat,
		"d=" + quoted,
		`[ -e "$d" ] || exit 2`,
		`[ -d "$d" ] || exit 20`,
		`[ -r "$d" ] && [ -x "$d" ] || exit 13`,
		`cd -- "$d" || exit 13`,
		"shopt -s dotglob nullglob",
		"names=()",
		"for f in *; do",
		`  [ -e "$f" ] || continue`,
		`  if [ -d "$f" ]; then k=d; elif [ -f "$f" ]; then k=f; else k=o; fi`,
		"  l=0; t=",
		`  if [ -L "$f" ]; then l=1; t=$({ realpath "./$f" 2>/dev/null || readlink -f "./$f" 2>/dev/null; } && echo x); t=${t%x}; t=${t%$'\n'}; fi`,
		`  printf '%s\0%s\0%s\0%s\0' "$k" "$l" "$t" "$f"`,
		`  names+=("./$f")`,
		"done",
		`printf 'end\0'`,
		`[ "${#names[@]}" -eq 0 ] && exit 0`,
		`printf '%s\0' "${names[@]}" | xargs -0 stat "${fmt[@]}" -- || exit 13`,
	}, "\n"), nil
}

func listRemoteDir(r remote, path string, opts sshRunOptions) ([]remoteEntry, error) {
	script, err := listingScript(path)
	if err != nil {
		return nil, err
	}
	data, err := runFileScript(r, script, "scandir", path, opts)
	if err != nil {
		return nil, err
	}
	return parseListing(data, path)
}

var listingKinds = map[string]remoteKind{"d": remoteDirectory, "f": remoteFile, "o": remoteOther}

// parseListing reads NUL-separated records up to `end`, then one size mtime
// line per entry.
func parseListing(data []byte, path string) ([]remoteEntry, error) {
	type record struct {
		kind    remoteKind
		symlink bool
		target  string
		name    string
	}
	start := 0
	field := func() (string, error) {
		end := bytes.IndexByte(data[start:], 0)
		if end < 0 {
			return "", &remoteFileError{code: "EIO", path: path, message: "EIO: truncated listing, scandir '" + path + "'"}
		}
		value := string(data[start : start+end])
		start += end + 1
		return value, nil
	}
	var records []record
	for {
		kind, err := field()
		if err != nil {
			return nil, err
		}
		if kind == "end" {
			break
		}
		symlinkValue, err := field()
		if err != nil {
			return nil, err
		}
		target, err := field()
		if err != nil {
			return nil, err
		}
		name, err := field()
		if err != nil {
			return nil, err
		}
		entries := listingKinds[kind]
		if entries == "" {
			entries = remoteOther
		}
		records = append(records, record{kind: entries, symlink: symlinkValue == "1", target: target, name: name})
	}
	var sizes []string
	for _, line := range strings.Split(string(data[start:]), "\n") {
		if line != "" {
			sizes = append(sizes, line)
		}
	}
	if len(sizes) != len(records) {
		return nil, &remoteFileError{code: "EIO", path: path, message: "EIO: entries changed while listing, scandir '" + path + "'"}
	}
	entries := make([]remoteEntry, 0, len(records))
	for index, item := range records {
		fields := strings.Fields(sizes[index])
		var size, mtime int64
		if len(fields) > 0 {
			size, _ = strconv.ParseInt(fields[0], 10, 64)
		}
		if len(fields) > 1 {
			mtime, _ = strconv.ParseInt(fields[1], 10, 64)
		}
		target := ""
		if item.symlink && item.target != "" {
			target = item.target
		}
		entries = append(entries, remoteEntry{name: item.name, kind: item.kind, size: size, mtimeMs: mtime * 1000, symlink: item.symlink, target: target})
	}
	return entries, nil
}
