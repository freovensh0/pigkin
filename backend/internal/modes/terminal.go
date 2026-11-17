package modes

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	// shellCommand runs on the PTY script starts: size it, record its device for
	// later resizes, then become Termux's login shell.
	shellCommand = `stty rows "$PIGKIN_ROWS" cols "$PIGKIN_COLS" && tty > "$PIGKIN_TTY" && unset PIGKIN_ROWS PIGKIN_COLS PIGKIN_TTY && exec login`

	deviceWait     = 50 * time.Millisecond
	deviceAttempts = 40
	busyPoll       = time.Second
	busyPolls      = 2
	idlePolls      = 3
	commLength     = 15
)

// remoteShellCommand is SHELL_COMMAND for a remote workspace. ssh -tt runs the
// remote user's login shell in the remote folder, through sessionScript so a
// refused session is retried; ssh's own messages go to PIGKIN_SSH_ERRORS so a
// failed connection can be told from the shell's output (host/src/modes/terminal.ts).
func remoteShellCommand(r remote, path string) (string, error) {
	quotedPath, err := shellQuote(path)
	if err != nil {
		return "", err
	}
	session, err := sessionScript(r, remoteScript(`cd -- `+quotedPath+` && exec "$SHELL" -l`), []string{"-tt"}, os.Getpid())
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		`stty rows "$PIGKIN_ROWS" cols "$PIGKIN_COLS"`,
		`tty > "$PIGKIN_TTY"`,
		`exec 2>"$PIGKIN_SSH_ERRORS"`,
		"unset PIGKIN_ROWS PIGKIN_COLS PIGKIN_TTY PIGKIN_SSH_ERRORS",
		"{\n" + session + "\n}",
	}, " && "), nil
}

// remoteFailure says why a remote shell's ssh exited with 255, unless the
// remote shell ended it while the connection stayed up.
func remoteFailure(r remote, code any, errors string) *host.Error {
	number, ok := code.(int)
	if !ok || number != 255 {
		return nil
	}
	if failure := sessionFailure(r, &number, errors); failure != nil {
		return failure
	}
	if isConnected(r) {
		return nil
	}
	if failure := classifySshFailure(errors, failureContext{name: r.host.Name, auth: r.host.Auth}); failure != nil {
		return failure
	}
	return unreachable(r.host.Name, "Connection lost")
}

var shells = map[string]bool{
	"sh": true, "login": true, "ash": true, "bash": true, "dash": true,
	"zsh": true, "fish": true, "ksh": true, "mksh": true, "oksh": true,
	"loksh": true, "yash": true,
}

type procStat struct {
	pid     int
	comm    string
	ppid    int
	session int
}

type terminalSize struct{ rows, cols int }

// parseProcStat reads the fields of /proc/<pid>/stat after the command name,
// which may itself hold spaces and parentheses.
func parseProcStat(text string) (procStat, bool) {
	open := strings.IndexByte(text, '(')
	close := strings.LastIndexByte(text, ')')
	if open < 0 || close < open {
		return procStat{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:open]))
	fields := strings.Fields(text[close+1:])
	if err != nil || len(fields) < 4 {
		return procStat{}, false
	}
	ppid, ppidErr := strconv.Atoi(fields[1])
	session, sessionErr := strconv.Atoi(fields[3])
	if ppidErr != nil || sessionErr != nil {
		return procStat{}, false
	}
	return procStat{pid: pid, comm: text[open+1 : close], ppid: ppid, session: session}, true
}

func loginShellComm(environ string) string {
	for _, entry := range strings.Split(environ, "\x00") {
		if strings.HasPrefix(entry, "SHELL=") {
			shell := filepath.Base(entry[len("SHELL="):])
			if len(shell) > commLength {
				shell = shell[:commLength]
			}
			return shell
		}
	}
	return ""
}

func sessionLeader(processes []procStat, scriptPID int) (procStat, bool) {
	for _, proc := range processes {
		if proc.ppid == scriptPID && proc.session == proc.pid {
			return proc, true
		}
	}
	return procStat{}, false
}

func sessionHasWork(processes []procStat, leader procStat, loginShell string) bool {
	if leader.comm != loginShell && !shells[leader.comm] {
		return true
	}
	for _, proc := range processes {
		if proc.session == leader.session && proc.pid != leader.pid {
			return true
		}
	}
	return false
}

func listProcesses() []procStat {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	processes := make([]procStat, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "" {
			continue
		}
		allDigits := true
		for _, c := range name {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", name, "stat"))
		if err != nil {
			continue
		}
		if stat, ok := parseProcStat(string(data)); ok {
			processes = append(processes, stat)
		}
	}
	return processes
}

func hasWork(scriptPID int) bool {
	processes := listProcesses()
	leader, ok := sessionLeader(processes, scriptPID)
	if !ok {
		return false
	}
	environ, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", leader.pid))
	return sessionHasWork(processes, leader, loginShellComm(string(environ)))
}

func watchBusy(ctx *Context, scriptPID int) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(busyPoll)
		defer ticker.Stop()
		busy := false
		streak := 0
		for {
			select {
			case <-stop:
				return
			case <-ctx.Conn.Done():
				return
			case <-ticker.C:
				work := hasWork(scriptPID)
				if work == busy {
					streak = 0
				} else {
					streak++
				}
				needed := busyPolls
				if busy {
					needed = idlePolls
				}
				if streak < needed {
					continue
				}
				busy = !busy
				streak = 0
				ctx.Conn.Send(host.Message{"type": "busy", "busy": busy})
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

// resizer applies the newest size to the shell's PTY with stty, one at a time.
// script cannot do it: the host gives it pipes, not a terminal to follow.
type resizer struct {
	stty       string
	deviceFile string
	mu         sync.Mutex
	pending    *terminalSize
	running    bool
	device     string
}

func (r *resizer) resize(rows, cols int) {
	r.mu.Lock()
	r.pending = &terminalSize{rows, cols}
	started := !r.running
	if started {
		r.running = true
	}
	r.mu.Unlock()
	if started {
		go r.drain()
	}
}

func (r *resizer) drain() {
	for {
		r.mu.Lock()
		if r.pending == nil {
			r.running = false
			r.mu.Unlock()
			return
		}
		size := *r.pending
		r.pending = nil
		r.mu.Unlock()

		device := r.recordedDevice()
		if device == "" {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
			return
		}
		cmd := exec.Command(r.stty, "-F", device, "rows", strconv.Itoa(size.rows), "cols", strconv.Itoa(size.cols))
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "pigkin-host terminal: resize failed: %v\n", err)
		}
	}
}

func (r *resizer) recordedDevice() string {
	r.mu.Lock()
	device := r.device
	r.mu.Unlock()
	for attempt := 0; device == "" && attempt < deviceAttempts; attempt++ {
		data, _ := os.ReadFile(r.deviceFile)
		recorded := strings.TrimSpace(string(data))
		if strings.HasPrefix(recorded, "/dev/") {
			device = recorded
			r.mu.Lock()
			r.device = device
			r.mu.Unlock()
		} else {
			time.Sleep(deviceWait)
		}
	}
	return device
}

func positiveInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		if number > 0 {
			return number, true
		}
	case float64:
		if number > 0 && number <= math.MaxInt32 && number == math.Trunc(number) {
			return int(number), true
		}
	}
	return 0, false
}

func withoutHostVariables() []string {
	hidden := map[string]bool{"PIGKIN_TOKEN": true, "PIGKIN_ROOT": true, "PI_CODING_AGENT_DIR": true}
	env := make([]string, 0)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !hidden[key] {
			env = append(env, entry)
		}
	}
	return env
}

// terminal runs the user's login shell on a PTY made by util-linux's script.
// After the request the connection stays JSONL: the app sends input (base64
// bytes) and resize (rows, cols); the host sends output (base64 bytes), busy,
// and one pigkin_exit once the shell is gone.
func terminal(ctx *Context) (host.Message, error) {
	cwd, ok := ctx.Params["cwd"].(string)
	if !ok {
		return nil, host.NewError("bad_params", `Missing string parameter "cwd"`, nil)
	}
	rows, ok := positiveInt(ctx.Params["rows"])
	if !ok {
		return nil, host.NewError("bad_params", `Parameter "rows" must be a positive integer`, nil)
	}
	cols, ok := positiveInt(ctx.Params["cols"])
	if !ok {
		return nil, host.NewError("bad_params", `Parameter "cols" must be a positive integer`, nil)
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, host.NewError("cwd_missing", "Folder not found: "+cwd, &host.Text{Key: "folder_not_found", Args: map[string]any{"path": cwd}})
	}
	relay, remotePath, err := remoteFolderFor(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if relay != nil && !sshInstalled(relay.paths) {
		return nil, sshMissing()
	}

	deviceFile := filepath.Join(ctx.Env.Root, fmt.Sprintf("terminal-tty-%d", os.Getpid()))
	env := append(withoutHostVariables(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"PIGKIN_ROWS="+strconv.Itoa(rows),
		"PIGKIN_COLS="+strconv.Itoa(cols),
		"PIGKIN_TTY="+deviceFile,
	)
	command := shellCommand
	errorsFile := ""
	if relay != nil {
		errorsFile = filepath.Join(ctx.Env.Root, fmt.Sprintf("terminal-ssh-%d", os.Getpid()))
		env = append(env, "SHELL="+filepath.Join(ctx.Env.Prefix, "bin", "sh"), "PIGKIN_SSH_ERRORS="+errorsFile)
		if command, err = remoteShellCommand(*relay, remotePath); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(filepath.Join(ctx.Env.Prefix, "bin", "script"), "-qfec", command, "/dev/null")
	cmd.Dir = cwd
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		ctx.Conn.Send(host.Message{"type": "pigkin_exit", "code": nil, "signal": nil, "stderr": "\n" + err.Error() + "\n"})
		return host.Message{}, nil
	}

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buffer)
			if n > 0 {
				// The blocking socket write is the backpressure: a slow app
				// stops these reads, which stops the shell's output pipe.
				ctx.Conn.Send(host.Message{"type": "output", "data": base64.StdEncoding.EncodeToString(buffer[:n])})
			}
			if err != nil {
				return
			}
		}
	}()

	res := &resizer{stty: filepath.Join(ctx.Env.Prefix, "bin", "stty"), deviceFile: deviceFile}
	go func() {
		for line := range ctx.Conn.Raw() {
			var message host.Message
			if json.Unmarshal(line, &message) != nil {
				continue
			}
			switch message["type"] {
			case "input":
				data, ok := message["data"].(string)
				if !ok {
					continue
				}
				decoded, err := base64.StdEncoding.DecodeString(data)
				if err == nil && writeAll(stdin, decoded) != nil {
					return
				}
			case "resize":
				rows, rowsOK := positiveInt(message["rows"])
				cols, colsOK := positiveInt(message["cols"])
				if rowsOK && colsOK {
					res.resize(rows, cols)
				}
			}
		}
	}()

	stopWatching := func() {}
	if relay == nil {
		// The remote's processes can't be seen from here, so its busy flag stays idle.
		stopWatching = watchBusy(ctx, cmd.Process.Pid)
	}
	childDone := make(chan struct{})
	go func() {
		// script only forwards SIGTERM, which the shell ignores; killing it
		// makes the kernel hang up the shell and its jobs.
		select {
		case <-ctx.Conn.Done():
			_ = cmd.Process.Kill()
		case <-childDone:
		}
	}()

	<-outputDone
	waitErr := cmd.Wait()
	_ = stdin.Close()
	close(childDone)
	stopWatching()
	_ = os.Remove(deviceFile)

	var code any = 0
	var signal any
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			code = nil
			signal = status.Signal().String()
		} else {
			code = exitErr.ExitCode()
		}
	} else if waitErr != nil {
		code = nil
	}
	result := host.Message{"type": "pigkin_exit", "code": code, "signal": signal, "stderr": stderr.String()}
	if relay != nil {
		errors, _ := os.ReadFile(errorsFile)
		_ = os.Remove(errorsFile)
		if failure := remoteFailure(*relay, code, string(errors)); failure != nil {
			ctx.Conn.Send(host.Message{"type": "output", "data": base64.StdEncoding.EncodeToString([]byte("\r\n" + failure.Message + "\r\n"))})
			result["error"] = errorJSON(failure)
		}
	}
	ctx.Conn.Send(result)
	return host.Message{}, nil
}
