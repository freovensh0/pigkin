package modes

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

var base64Pattern = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

type attachedFile struct {
	name  string
	bytes []byte
}

// attachedFiles validates the request and gives each pick a name unique among
// the picks, so two files with one name both survive.
func attachedFiles(params host.Message) ([]attachedFile, error) {
	files, ok := params["files"].([]any)
	if !ok || len(files) == 0 {
		return nil, host.NewError("bad_params", `Parameter "files" must be a non-empty array`, nil)
	}
	taken := map[string]bool{}
	result := make([]attachedFile, 0, len(files))
	for _, item := range files {
		file, ok := item.(map[string]any)
		name, hasName := file["name"].(string)
		data, hasData := file["data"].(string)
		if !ok || !hasName || !hasData || !base64Pattern.MatchString(data) {
			return nil, host.NewError("bad_params", "Each file needs a name and base64 data", nil)
		}
		bytes, err := decodeBase64(data)
		if err != nil {
			return nil, host.NewError("bad_params", "Each file needs a name and base64 data", nil)
		}
		name = uniqueName(safeName(name), taken)
		taken[name] = true
		result = append(result, attachedFile{name: name, bytes: bytes})
	}
	return result, nil
}

func decodeBase64(data string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(data)
}

// safeName keeps a plain file name, never a path or a hidden file.
func safeName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.TrimLeft(name, ".")
	if name == "" {
		return "file"
	}
	return name
}

func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	stem, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot > 0 {
		stem, ext = name[:dot], name[dot:]
	}
	for n := 2; ; n++ {
		candidate := stem + "-" + strconv.Itoa(n) + ext
		if !taken[candidate] {
			return candidate
		}
	}
}

func folderName() (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405")
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return stamp + "-" + hex.EncodeToString(random), nil
}

// saveAttachmentsOp writes the app's picked files where Pi's tools can read
// them, in a fresh folder under $PIGKIN_ROOT/attachments.
func saveAttachmentsOp(ctx *Context) (host.Message, error) {
	files, err := attachedFiles(ctx.Params)
	if err != nil {
		return nil, err
	}
	cwd, hasCwd, err := nullableStringParam(ctx.Params, "cwd")
	if err != nil {
		return nil, err
	}
	if hasCwd {
		paths := remotePathsFor(ctx.Env)
		standIn, err := readStandIn(paths.remotes, cwd)
		if err != nil {
			return nil, err
		}
		if standIn != nil {
			return nil, host.NewError("remote_not_ported", "Remote attachments are not ported to the Go host yet", nil)
		}
	}
	name, err := folderName()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(ctx.Env.Root, "attachments", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	saved := make([]string, 0, len(files))
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := handle.Write(file.bytes); err != nil {
			_ = handle.Close()
			return nil, err
		}
		if err := handle.Close(); err != nil {
			return nil, err
		}
		saved = append(saved, path)
	}
	return host.Message{"paths": saved}, nil
}
