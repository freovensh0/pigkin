package modes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	installLockName  = "install.lock"
	installingMarker = ".pigkin-installing"
	maxProgressLine  = 4096
	nodeOKScript     = `const [a, b] = process.versions.node.split(".").map(Number); process.exit(a > 22 || (a === 22 && b >= 19) ? 0 : 1)`
)

// RuntimeVersion is the PiG version the host is built against. The hello and
// the ensure report the same value when the bundled Node runtime is absent.
func RuntimeVersion() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/MichaelKinsy/PiG" {
				return dependency.Version
			}
		}
	}
	return runtime.Version()
}

// installedPiVersion is the version of the pinned Node Pi under root, or ""
// when that runtime hasn't been installed.
func installedPiVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "pi", "node_modules", piPackage, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Version
}

// PiVersion is what the app checks the agent against: the installed Node Pi
// when the bundled runtime is there, else the PiG version this host carries.
func PiVersion(root string) string {
	if version := installedPiVersion(root); version != "" {
		return version
	}
	return RuntimeVersion()
}

var hostExecutable = os.Executable

var extensionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ensure refreshes the bundled host binary in Pigkin's managed folder, then
// installs the pinned Node Pi runtime when the app ships bundled extensions.
// The Node runtime is what runs fd-3 extension chats and remote chats; the
// Go-only PiG path needs none of it.
func ensure(ctx *Context) (host.Message, error) {
	for _, key := range []string{"packageJson", "packageLock", "expectedVersion"} {
		if _, err := stringParam(ctx.Params, key); err != nil {
			return nil, err
		}
	}
	extensions, err := validateExtensions(ctx.Params["pigkinExtensions"])
	if err != nil {
		return nil, err
	}
	if err := installHostBinary(ctx.Env.Root); err != nil {
		return nil, err
	}
	if len(extensions) == 0 {
		return host.Message{"piVersion": PiVersion(ctx.Env.Root)}, nil
	}
	version, err := ensureNodeRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return host.Message{"piVersion": version}, nil
}

func validateExtensions(value any) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, badExtension()
	}
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, badExtension()
		}
		name, ok := fields["name"].(string)
		if !ok || !extensionName.MatchString(name) {
			return nil, badExtension()
		}
		if _, ok := fields["source"].(string); !ok {
			return nil, badExtension()
		}
		if _, ok := fields["usesRuntimePackages"].(bool); !ok {
			return nil, badExtension()
		}
	}
	return items, nil
}

func badExtension() error {
	return host.NewError("bad_params", `Parameter "pigkinExtensions" must be an array of { name, source, usesRuntimePackages } with plain names`, nil)
}

// ensureNodeRuntime installs the pinned manifest from the app into $PIGKIN_ROOT/pi
// when it isn't there in full, mirroring host/src/modes/ensure.ts. The marker
// makes an install cut off after the manifests were written count as unfinished.
func ensureNodeRuntime(ctx *Context) (string, error) {
	piDir := filepath.Join(ctx.Env.Root, "pi")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(ctx.Env.ConfigDir, 0o755); err != nil {
		return "", err
	}
	release, err := withInstallLock(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	packageJSON, _ := stringParam(ctx.Params, "packageJson")
	packageLock, _ := stringParam(ctx.Params, "packageLock")
	expected, _ := stringParam(ctx.Params, "expectedVersion")
	packagePath := filepath.Join(piDir, "package.json")
	lockPath := filepath.Join(piDir, "package-lock.json")
	if ctx.Params["reinstall"] != true &&
		installedPiVersion(ctx.Env.Root) == expected &&
		sameContents(packagePath, packageJSON) &&
		sameContents(lockPath, packageLock) &&
		piInstallComplete(ctx.Env.Root) {
		return installedPiVersion(ctx.Env.Root), nil
	}
	marker := filepath.Join(piDir, installingMarker)
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(packagePath, []byte(packageJSON), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(lockPath, []byte(packageLock), 0o644); err != nil {
		return "", err
	}
	if err := npmCi(ctx); err != nil {
		return "", err
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return installedPiVersion(ctx.Env.Root), nil
}

func sameContents(path, want string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == want
}

func piInstallComplete(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "pi", installingMarker)); err == nil {
		return false
	}
	entry, err := piEntry(root)
	if err != nil {
		return false
	}
	_, err = os.Stat(entry)
	return err == nil
}

// withInstallLock keeps two hosts from running npm ci in the same folder at
// once. A lock whose holder is gone is taken over; a live one is waited out
// until the app hangs up.
func withInstallLock(ctx *Context) (func(), error) {
	path := filepath.Join(ctx.Env.Root, installLockName)
	pid := strconv.Itoa(os.Getpid())
	base := connContext(ctx.Conn)
	for {
		if err := os.Symlink(pid, path); err == nil {
			return func() { _ = os.Remove(path) }, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if livePid(path) == 0 {
			clearStaleLock(path)
			continue
		}
		select {
		case <-base.Done():
			return nil, base.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func npmCi(ctx *Context) error {
	npmNetworkError := regexp.MustCompile(`^npm error (network|code E(AI_AGAIN|NOTFOUND|CONNREFUSED|CONNRESET|TIMEDOUT|NETUNREACH))\b`)
	if err := ensureNode(ctx); err != nil {
		return err
	}
	base := connContext(ctx.Conn)
	cmd := exec.CommandContext(base, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--no-update-notifier", "--loglevel=error")
	cmd.Dir = filepath.Join(ctx.Env.Root, "pi")
	offline := false
	lines := &progressLines{
		send: func(line string) { sendProgress(ctx, line) },
		note: func(line string) { offline = offline || npmNetworkError.MatchString(line) },
	}
	cmd.Stdout = sshWriterFunc(lines.write)
	cmd.Stderr = sshWriterFunc(lines.write)
	sendProgress(ctx, "npm ci")
	err := cmd.Run()
	lines.flush()
	if base.Err() != nil {
		return base.Err()
	}
	if err == nil {
		return nil
	}
	if offline {
		return host.NewError("npm_offline", "Couldn't download the agent. Check your internet connection, then retry.", &host.Text{Key: "agent_download_offline"})
	}
	return commandFailed("npm_failed", "npm ci", exitCode(err))
}

// ensureNode makes Node.js available for npm ci. The Play helper's setup
// installs it for pairing; an F-Droid install reaches ensure first and has no
// other path to it, so the runtime install brings it in here. A fresh F-Droid
// bootstrap needs its packages upgraded before nodejs links against openssl.
func ensureNode(ctx *Context) error {
	if exec.Command("node", "-e", nodeOKScript).Run() == nil {
		return nil
	}
	sendProgress(ctx, "Installing Node.js. This can take a few minutes.")
	base := connContext(ctx.Conn)
	_ = exec.CommandContext(base, "dpkg", "--configure", "-a").Run()
	pkg := filepath.Join(ctx.Env.Prefix, "bin", "pkg")
	if err := runPkg(ctx, base, pkg, "upgrade", "-y"); err != nil {
		return err
	}
	if err := runPkg(ctx, base, pkg, "install", "-y", "nodejs"); err != nil {
		return err
	}
	if exec.Command("node", "-e", nodeOKScript).Run() == nil {
		return nil
	}
	code := 1
	return commandFailed("npm_failed", "pkg install nodejs", &code)
}

func runPkg(ctx *Context, base context.Context, pkg string, args ...string) error {
	cmd := exec.CommandContext(base, pkg, args...)
	cmd.Dir = ctx.Env.Home
	cmd.Stdin = strings.NewReader(strings.Repeat("y\n", 1024))
	offline := false
	lines := &progressLines{
		send: func(line string) { sendProgress(ctx, line) },
		note: func(line string) { offline = offline || installOfflineLine.MatchString(line) },
	}
	cmd.Stdout = sshWriterFunc(lines.write)
	cmd.Stderr = sshWriterFunc(lines.write)
	err := cmd.Run()
	lines.flush()
	if base.Err() != nil {
		return base.Err()
	}
	if err == nil {
		return nil
	}
	if offline {
		return host.NewError("npm_offline", "Couldn't download the agent. Check your internet connection, then retry.", &host.Text{Key: "agent_download_offline"})
	}
	return commandFailed("npm_failed", "pkg "+args[0], exitCode(err))
}

func sendProgress(ctx *Context, line string) {
	if len(line) > maxProgressLine {
		line = line[:maxProgressLine]
	}
	ctx.Conn.Send(host.Message{"type": "pigkin_progress", "line": strings.TrimSuffix(line, "\r")})
}

// installHostBinary puts the running binary at $PIGKIN_ROOT/pigkin-host when
// that copy is missing or an older build. Writing a temporary file and
// renaming it keeps a chat that starts at the same time from loading half a
// binary.
func installHostBinary(root string) error {
	executable, err := hostExecutable()
	if err != nil {
		return err
	}
	target := filepath.Join(root, "pigkin-host")
	same, err := samePath(executable, target)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	identical, err := sameBytes(executable, target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if identical {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary := target + ".tmp-" + strconv.Itoa(os.Getpid())
	defer os.Remove(temporary)
	in, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}

func samePath(left, right string) (bool, error) {
	first, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	second, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	return first == second, nil
}

func sameBytes(left, right string) (bool, error) {
	first, err := fileHash(left)
	if err != nil {
		return false, err
	}
	second, err := fileHash(right)
	if err != nil {
		return false, err
	}
	return first == second, nil
}

func fileHash(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}
