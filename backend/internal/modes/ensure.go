package modes

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// RuntimeVersion is the PiG version the host is built against. The hello and
// ensure report the same value, so the app's last hello stays in step with a
// completed setup.
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

var hostExecutable = os.Executable

var extensionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ensure refreshes the bundled host binary in Pigkin's managed folder and
// reports the runtime version. The Node runtime and its extensions have no Go
// equivalent; package_install and update_extensions say so rather than
// pretending to install them.
func ensure(ctx *Context) (host.Message, error) {
	for _, key := range []string{"packageJson", "packageLock", "expectedVersion"} {
		if _, err := stringParam(ctx.Params, key); err != nil {
			return nil, err
		}
	}
	if err := validateExtensions(ctx.Params["pigkinExtensions"]); err != nil {
		return nil, err
	}
	if err := installHostBinary(ctx.Env.Root); err != nil {
		return nil, err
	}
	return host.Message{"piVersion": RuntimeVersion()}, nil
}

func validateExtensions(value any) error {
	items, ok := value.([]any)
	if !ok {
		return host.NewError("bad_params", `Parameter "pigkinExtensions" must be an array of { name, source, usesRuntimePackages } with plain names`, nil)
	}
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			return badExtension()
		}
		name, ok := fields["name"].(string)
		if !ok || !extensionName.MatchString(name) {
			return badExtension()
		}
		if _, ok := fields["source"].(string); !ok {
			return badExtension()
		}
		if _, ok := fields["usesRuntimePackages"].(bool); !ok {
			return badExtension()
		}
	}
	return nil
}

func badExtension() error {
	return host.NewError("bad_params", `Parameter "pigkinExtensions" must be an array of { name, source, usesRuntimePackages } with plain names`, nil)
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
