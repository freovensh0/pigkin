package modes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// deviceModel names Pigkin's key in the server's authorized_keys.
func deviceModel() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "getprop", "ro.product.model").Output(); err == nil {
		if model := strings.TrimSpace(string(out)); model != "" {
			return model
		}
	}
	name, _ := os.Hostname()
	return name
}

// publicKey is Pigkin's public key line, or nil before Pigkin made its key.
// A private key on its own is enough to derive the line again.
func publicKey(paths remoteLayout) (*string, error) {
	file := paths.keyFile()
	data, err := os.ReadFile(file + ".pub")
	if err == nil {
		line := strings.TrimSpace(string(data))
		return &line, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out, err := exec.Command(paths.keygenBinary(), "-y", "-f", file).Output()
	if err != nil {
		return nil, nil
	}
	line := strings.TrimSpace(string(out))
	return &line, nil
}

// keyMu keeps two key operations from writing the same key at once.
var keyMu sync.Mutex

func ensureKey(paths remoteLayout) (string, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	return makeKey(paths, false)
}

func replaceKey(paths remoteLayout) (string, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	return makeKey(paths, true)
}

// makeKey writes the key under a temporary name then moves it in, so an
// existing key is never seen half written.
func makeKey(paths remoteLayout, replace bool) (string, error) {
	if !replace {
		if existing, err := publicKey(paths); err != nil {
			return "", err
		} else if existing != nil {
			return *existing, nil
		}
	}
	if err := ensureSshDir(paths); err != nil {
		return "", err
	}
	file := paths.keyFile()
	temporary := fmt.Sprintf("%s.%d.tmp", file, os.Getpid())
	_ = os.Remove(temporary)
	_ = os.Remove(temporary + ".pub")
	args := []string{"-q", "-t", "ed25519", "-N", "", "-C", "pigkin@" + deviceModel(), "-f", temporary}
	if err := exec.Command(paths.keygenBinary(), args...).Run(); err != nil {
		var execErr *exec.Error
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) || (errors.As(err, &execErr) && errors.Is(execErr.Err, fs.ErrNotExist)) {
			return "", sshMissing()
		}
		return "", commandFailed("remote_key_failed", "ssh-keygen", exitCode(err))
	}
	if err := os.Remove(file + ".pub"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(temporary, file); err != nil {
		return "", err
	}
	if err := os.Chmod(file, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(temporary+".pub", file+".pub"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(file + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
