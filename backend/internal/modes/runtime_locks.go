package modes

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// procDir is overridable so tests can supply a fake process table.
func procDir() string {
	if dir := os.Getenv("PIGKIN_PROC_DIR"); dir != "" {
		return dir
	}
	return "/proc"
}

// lockHolders are the host executables that may hold a config lock. Both are
// checked so a Go host never disturbs a TypeScript host left from an older
// build.
var lockHolders = [][]byte{[]byte("host.mjs"), []byte("pigkin-host")}

func livePid(path string) int {
	target, err := os.Readlink(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(target)
	if err != nil || pid <= 0 || !processAlive(pid) {
		return 0
	}
	return pid
}

func processAlive(pid int) bool {
	if pid == os.Getpid() {
		return true
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	command, err := os.ReadFile(filepath.Join(procDir(), strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	for _, holder := range lockHolders {
		if bytes.Contains(command, holder) {
			return true
		}
	}
	return false
}

// clearStaleLock removes a lock whose holder is gone. A rename has one winner,
// so two hosts that both found it stale cannot both remove a fresh lock.
func clearStaleLock(path string) {
	stale := path + ".stale." + strconv.Itoa(os.Getpid())
	if err := os.Rename(path, stale); err != nil {
		return
	}
	if holder := livePid(stale); holder != 0 {
		_ = os.Symlink(strconv.Itoa(holder), path)
	}
	_ = os.Remove(stale)
}

// withConfigLock serialises backups, restores, deletes and extension updates
// across host processes. The returned function releases the lock.
func withConfigLock(ctx *Context) (func(), error) {
	path := filepath.Join(ctx.Env.Root, "config.lock")
	pid := strconv.Itoa(os.Getpid())
	for attempt := 0; ; attempt++ {
		if err := os.Symlink(pid, path); err == nil {
			return func() { _ = os.Remove(path) }, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if livePid(path) != 0 || attempt == 2 {
			return nil, host.NewError("busy", "Another backup, restore or extension update is running.", &host.Text{Key: "config_lock_busy"})
		}
		clearStaleLock(path)
	}
}
