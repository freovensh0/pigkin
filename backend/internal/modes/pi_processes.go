package modes

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// piExitGrace is how long a restore waits for an open chat's Pi to exit.
var piExitGrace = 20 * time.Second

func isPiCommand(command []byte) bool {
	return bytes.Contains(command, []byte("pi-coding-agent")) ||
		(bytes.Contains(command, []byte("pigkin-host")) && bytes.Contains(command, []byte(HiddenRPCFlag)))
}

// runningPi lists the pids of Pi processes using configDir. It reads /proc
// rather than a process table: a chat may have started Pi from a terminal, and
// a host that died may have left one behind.
func runningPi(configDir string) []int {
	entries, err := os.ReadDir(procDir())
	if err != nil {
		return nil
	}
	needle := []byte("\x00PI_CODING_AGENT_DIR=" + configDir + "\x00")
	var found []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join(procDir(), entry.Name())
		command, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || !isPiCommand(command) {
			continue
		}
		environment, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err == nil && bytes.Contains(append([]byte{0}, environment...), needle) {
			found = append(found, pid)
		}
	}
	return found
}

func waitForPiExit(ctx *Context, configDir string) error {
	deadline := time.Now().Add(piExitGrace)
	for {
		running := runningPi(configDir)
		if len(running) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			ids := make([]string, 0, len(running))
			for _, pid := range running {
				ids = append(ids, strconv.Itoa(pid))
			}
			processes := strings.Join(ids, ", ")
			return host.NewError("pi_running", "Pi is still running (process "+processes+
				"). Stop it, for example by quitting pi in a terminal, and try again.",
				&host.Text{Key: "pi_running", Args: map[string]any{"processes": processes}})
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.done():
			return host.NewError("aborted", "The restore was cancelled", nil)
		}
	}
}

func piRunning(configDir string) bool { return len(runningPi(configDir)) > 0 }

// done is closed when the app hangs up; modes that wait use it to unwind.
func (c *Context) done() <-chan struct{} {
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Done()
}
