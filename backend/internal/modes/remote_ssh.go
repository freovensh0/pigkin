package modes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// signIn says how a call may sign in: a background call never asks and refuses
// unknown host keys; an interactive one asks through ssh's askpass; an
// unattended one asks for a password but still refuses unknown or changed keys.
type signIn string

const (
	signBackground  signIn = "background"
	signInteractive signIn = "interactive"
	signUnattended  signIn = "unattended"
)

// remote is one host to run on, and where Pigkin's SSH state is.
type remote struct {
	paths remoteLayout
	host  Host
}

func connectionArgs(r remote) []string {
	return []string{
		"-F", r.paths.configFile(),
		"-o", "ControlPath=" + configWord(r.paths.controlPath(r.host.ID)),
	}
}

func directOptions(r remote, mode signIn) []string {
	strict := "yes"
	if mode == signInteractive {
		strict = "ask"
	}
	args := append(connectionArgs(r),
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=10m",
		"-o", "BatchMode="+strconv.FormatBool(mode == signBackground),
		"-o", "StrictHostKeyChecking="+strict,
	)
	if mode != signBackground {
		args = append(args, "-o", "NumberOfPasswordPrompts=1")
	}
	return append(args, destination(r.host))
}

func masterOptions(r remote) []string {
	return append(connectionArgs(r),
		"-o", "ControlMaster=no",
		"-o", "ProxyCommand=touch \"$PIGKIN_SSH_FALLBACK\"",
		"-o", "LogLevel=QUIET",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		destination(r.host),
	)
}

func sshArgs(r remote, command string, mode signIn) []string {
	return append(directOptions(r, mode), "--", command)
}

// maxSessionsPerHost leaves room under the server's MaxSessions for the other
// processes talking to the same host.
const maxSessionsPerHost = 4

var sessionSlots struct {
	mu     sync.Mutex
	byHost map[string]chan struct{}
}

func acquireSession(hostID string, ctx context.Context) (func(), error) {
	sessionSlots.mu.Lock()
	if sessionSlots.byHost == nil {
		sessionSlots.byHost = make(map[string]chan struct{})
	}
	slot := sessionSlots.byHost[hostID]
	if slot == nil {
		slot = make(chan struct{}, maxSessionsPerHost)
		sessionSlots.byHost[hostID] = slot
	}
	sessionSlots.mu.Unlock()

	select {
	case slot <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slot }) }, nil
	case <-ctxDone(ctx):
		return nil, context.Canceled
	}
}

func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

// sshEnvironment is Termux's environment without the host's own variables,
// plus any variables the call needs.
func sshEnvironment(extra map[string]string) []string {
	drop := map[string]bool{"PIGKIN_TOKEN": true, "PIGKIN_ROOT": true, "PI_CODING_AGENT_DIR": true}
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found && !drop[key] {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

type sshRunOptions struct {
	ctx       context.Context
	stdin     []byte
	stdinOpen bool
	onStdout  func([]byte)
	onStderr  func([]byte)
	timeout   time.Duration
	idle      time.Duration
}

type sshRunResult struct {
	code   *int
	stdout []byte
	stderr string
}

const maxStderr = 64 * 1024

type sshWriterFunc func([]byte) (int, error)

func (write sshWriterFunc) Write(data []byte) (int, error) { return write(data) }

// spawnSsh runs ssh with args, feeding and collecting its standard streams.
func spawnSsh(paths remoteLayout, args []string, opts sshRunOptions, environment []string) (sshRunResult, error) {
	ctx := opts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, paths.sshBinary(), args...)
	cmd.Env = environment

	var stdin io.Reader
	if opts.stdinOpen {
		// os.Pipe is passed to the child as a file descriptor, so exec does not
		// copy from a goroutine that would keep cmd.Wait waiting.
		reader, writer, err := os.Pipe()
		if err != nil {
			return sshRunResult{}, err
		}
		defer reader.Close()
		defer writer.Close()
		stdin = reader
	} else if opts.stdin != nil {
		stdin = bytes.NewReader(opts.stdin)
	}
	cmd.Stdin = stdin

	var stdout bytes.Buffer
	var stderr string
	cmd.Stdout = sshWriterFunc(func(data []byte) (int, error) {
		if opts.onStdout != nil {
			opts.onStdout(data)
		} else {
			stdout.Write(data)
		}
		return len(data), nil
	})
	cmd.Stderr = sshWriterFunc(func(data []byte) (int, error) {
		stderr = (stderr + string(data))
		if len(stderr) > maxStderr {
			stderr = stderr[len(stderr)-maxStderr:]
		}
		if opts.onStderr != nil {
			opts.onStderr(data)
		}
		return len(data), nil
	})

	err := cmd.Run()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, fs.ErrNotExist) {
			return sshRunResult{}, sshMissing()
		}
	}
	return sshRunResult{code: exitCode(err), stdout: stdout.Bytes(), stderr: stderr}, nil
}

func sshInstalled(paths remoteLayout) bool {
	info, err := os.Stat(paths.sshBinary())
	return err == nil && info.Mode()&0o111 != 0
}

// isConnected checks whether the host has a live master connection.
func isConnected(r remote) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(connectionArgs(r), "-O", "check", destination(r.host))
	cmd := exec.CommandContext(ctx, r.paths.sshBinary(), args...)
	cmd.Env = sshEnvironment(nil)
	return cmd.Run() == nil
}

// disconnect ends the master connection; nothing happens when there is none.
func disconnect(r remote) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(connectionArgs(r), "-O", "exit", destination(r.host))
	cmd := exec.CommandContext(ctx, r.paths.sshBinary(), args...)
	cmd.Env = sshEnvironment(nil)
	_ = cmd.Run()
}

// forgetHostKey removes the host's entries from Pigkin's known_hosts. The name
// ssh files the key under comes from ssh -G, which also resolves a config
// alias. The user's own ~/.ssh/known_hosts is never written.
func forgetHostKey(r remote) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.paths.sshBinary(), "-F", r.paths.configFile(), "-G", destination(r.host))
	cmd.Env = sshEnvironment(nil)
	out, err := cmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, fs.ErrNotExist) {
			return sshMissing()
		}
		return err
	}
	options := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, found := strings.Cut(line, " ")
		if found {
			options[key] = strings.TrimSpace(value)
		}
	}
	alias := options["hostkeyalias"]
	hostname := options["hostname"]
	if hostname == "" {
		hostname = r.host.Address
	}
	port := options["port"]
	if port == "" {
		port = "22"
	}
	name := hostname
	if alias != "" && alias != "none" {
		name = alias
	} else if port != "22" {
		name = "[" + hostname + "]:" + port
	}
	file := r.paths.knownHostsFile()
	_ = exec.Command(r.paths.keygenBinary(), "-R", name, "-f", file).Run()
	err = os.Remove(file + ".old")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// readIdleMs is how long a user-facing call waits for a byte before checking
// the connection. Tests shorten it.
func readIdleMs() time.Duration {
	value, err := strconv.Atoi(os.Getenv("PIGKIN_REMOTE_IDLE_MS"))
	if err != nil || value <= 0 {
		return 20 * time.Second
	}
	return time.Duration(value) * time.Millisecond
}

func sessionWait() time.Duration {
	value, err := strconv.Atoi(os.Getenv("PIGKIN_REMOTE_SESSION_WAIT_MS"))
	if err != nil || value <= 0 {
		return 30 * time.Second
	}
	return time.Duration(value) * time.Millisecond
}

var fallbackFiles atomic.Int64

// overMaster runs a command once over the master. fellBack says the master
// didn't carry the session, so nothing ran on the remote.
func overMaster(r remote, command string, opts sshRunOptions) (sshRunResult, bool, error) {
	number := fallbackFiles.Add(1)
	file := filepath.Join(r.paths.socketsDir(), fmt.Sprintf("f%d-%d", os.Getpid(), number))
	environment := sshEnvironment(map[string]string{
		"SHELL":               r.paths.shBinary(),
		"PIGKIN_SSH_FALLBACK": file,
	})
	_ = os.Remove(file)
	result, err := spawnSsh(r.paths, append(masterOptions(r), "--", command), opts, environment)
	if err != nil {
		_ = os.Remove(file)
		return sshRunResult{}, false, err
	}
	_, statErr := os.Stat(file)
	fellBack := statErr == nil
	if fellBack {
		_ = os.Remove(file)
	}
	return result, fellBack, nil
}

// runSession runs command over the master, asking again while the server
// refuses it a session, or makes a fresh connection when there is no master.
func runSession(r remote, command string, opts sshRunOptions) (sshRunResult, error) {
	var deadline time.Time
	wait := 250 * time.Millisecond
	for {
		result, fellBack, err := overMaster(r, command, opts)
		if err != nil {
			return sshRunResult{}, err
		}
		if !fellBack {
			return result, nil
		}
		if !isConnected(r) {
			return spawnSsh(r.paths, sshArgs(r, command, signBackground), opts, sshEnvironment(nil))
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(sessionWait())
		}
		now := time.Now()
		if !now.Before(deadline) {
			return sshRunResult{}, sessionsFull(r.host.Name)
		}
		pause := wait
		if remaining := deadline.Sub(now); pause > remaining {
			pause = remaining
		}
		select {
		case <-time.After(pause):
		case <-ctxDone(opts.ctx):
			return sshRunResult{}, context.Canceled
		}
		if wait < 2*time.Second {
			wait *= 2
		}
	}
}

// idleWatch cancels ctx after d without a touch. A live connection that simply
// runs a slow command touches stdout and stderr, so it stays alive.
type idleWatch struct {
	ctx    context.Context
	cancel context.CancelFunc
	touch  func()
	stop   func()
	fired  func() bool
}

func newIdleWatch(base context.Context, d time.Duration) *idleWatch {
	if base == nil {
		base = context.Background()
	}
	if d <= 0 {
		return &idleWatch{ctx: base, cancel: func() {}, touch: func() {}, stop: func() {}, fired: func() bool { return false }}
	}
	ctx, cancel := context.WithCancel(base)
	timer := time.NewTimer(d)
	var mu sync.Mutex
	stopped, didFire := false, false
	go func() {
		select {
		case <-timer.C:
			mu.Lock()
			if !stopped {
				didFire = true
				cancel()
			}
			mu.Unlock()
		case <-ctx.Done():
		}
	}()
	return &idleWatch{
		ctx:    ctx,
		cancel: cancel,
		touch: func() {
			mu.Lock()
			defer mu.Unlock()
			if stopped {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
		},
		stop: func() {
			mu.Lock()
			defer mu.Unlock()
			stopped = true
			timer.Stop()
		},
		fired: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return didFire
		},
	}
}

// runScript runs a bash script on r through the command wrapper, sharing the
// master connection and a session slot.
func runScript(r remote, script string, opts sshRunOptions) (sshRunResult, error) {
	ctx := opts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}
	release, err := acquireSession(r.host.ID, ctx)
	if err != nil {
		return sshRunResult{}, err
	}
	defer release()

	idle := newIdleWatch(ctx, opts.idle)
	defer idle.stop()
	defer idle.cancel()

	var stdout bytes.Buffer
	run := opts
	run.ctx = idle.ctx
	run.idle = 0
	run.onStdout = func(data []byte) {
		idle.touch()
		if opts.onStdout != nil {
			opts.onStdout(data)
		} else {
			stdout.Write(data)
		}
	}
	run.onStderr = func(data []byte) {
		idle.touch()
		if opts.onStderr != nil {
			opts.onStderr(data)
		}
	}

	result, err := runSession(r, remoteScript(script), run)
	if err != nil {
		if idle.fired() && ctx.Err() == nil {
			disconnect(r)
			return sshRunResult{}, unreachable(r.host.Name, "Connection timed out")
		}
		return sshRunResult{}, err
	}
	if opts.onStdout == nil {
		result.stdout = stdout.Bytes()
	}
	if result.code != nil && *result.code == 255 && !isConnected(r) {
		if failure := classifySshFailure(result.stderr, failureContext{name: r.host.Name, auth: r.host.Auth}); failure != nil {
			return sshRunResult{}, failure
		}
		return sshRunResult{}, unreachable(r.host.Name, "Connection lost")
	}
	if isBashMissing(result.code, result.stderr) {
		return sshRunResult{}, bashMissing(r.host.Name)
	}
	return result, nil
}

// runWatched runs command in cwd under the remote watchdog so a lost local ssh
// ends the remote command too.
func runWatched(r remote, command, cwd string, opts sshRunOptions) (sshRunResult, error) {
	script, err := watched(command, cwd)
	if err != nil {
		return sshRunResult{}, err
	}
	opts.stdinOpen = true
	return runScript(r, script, opts)
}

// checkConnection checks that r can be reached now, without signing in when
// connect is false.
func checkConnection(r remote, connect bool) error {
	if isConnected(r) {
		if _, err := runScript(r, "exit 0", sshRunOptions{timeout: 5 * time.Second}); err == nil {
			return nil
		}
		if isConnected(r) {
			return unreachable(r.host.Name, "Connection timed out")
		}
	}
	if !connect {
		return notConnected(r.host.Name)
	}
	_, err := runScript(r, "true", sshRunOptions{timeout: 30 * time.Second})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return unreachable(r.host.Name, "Connection timed out")
		}
		return err
	}
	return nil
}

// sessionScript is a sh script that runs a long-lived command over the master,
// asking again while the server refuses it a session.
func sessionScript(r remote, command string, flags []string, owner int) (string, error) {
	quotedCommand, err := shellQuote(command)
	if err != nil {
		return "", err
	}
	quotedSSH, err := shellQuote(r.paths.sshBinary())
	if err != nil {
		return "", err
	}
	check := strings.Join(words(append(append([]string{}, connectionArgs(r)...), "-O", "check", destination(r.host))), " ")
	quotedShell := shellQuoteOrEmpty(r.paths.shBinary())
	direct := strings.Join(words(append(append([]string{}, flags...), directOptions(r, signBackground)...)), " ")
	master := strings.Join(words(append(append([]string{}, flags...), masterOptions(r)...)), " ")
	fallback := filepath.Join(r.paths.socketsDir(), "f")
	full := sessionsFull(r.host.Name).Message
	quotedFull, err := shellQuote(full)
	if err != nil {
		return "", err
	}
	waitSeconds := strconv.FormatInt(int64((sessionWait()+time.Second-1)/time.Second), 10)
	return strings.Join([]string{
		"c=" + quotedCommand,
		"f=" + shellQuoteOrEmpty(fallback) + "$$",
		"full() { printf '%s\\n' " + quotedFull + " >&2; exit 255; }",
		"d=",
		"while :; do",
		`  rm -f -- "$f"`,
		"  SHELL=" + quotedShell + " PIGKIN_SSH_FALLBACK=\"$f\" " + quotedSSH + " " + master + ` -- "$c"`,
		"  code=$?",
		`  [ -e "$f" ] || exit "$code"`,
		`  rm -f -- "$f"`,
		fmt.Sprintf("  kill -0 %d 2>/dev/null || exit 255", owner),
		"  " + quotedSSH + " " + check + " 2>/dev/null || exec " + quotedSSH + " " + direct + ` -- "$c"`,
		"  [ -n \"$d\" ] || d=$(($(date +%s) + " + waitSeconds + "))",
		`  [ "$(date +%s)" -lt "$d" ] || full`,
		"  sleep 1",
		fmt.Sprintf("  kill -0 %d 2>/dev/null || exit 255", owner),
		`  [ "$(date +%s)" -lt "$d" ] || full`,
		"done",
	}, "\n"), nil
}

func words(args []string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		quoted, err := shellQuote(arg)
		if err != nil {
			quoted = configWord(arg)
		}
		result = append(result, quoted)
	}
	return result
}

func shellQuoteOrEmpty(value string) string {
	quoted, err := shellQuote(value)
	if err != nil {
		return "''"
	}
	return quoted
}

// sessionFailure reports a session script that ended with the sessions-full
// message after waiting too long.
func sessionFailure(r remote, code *int, stderr string) *host.Error {
	full := sessionsFull(r.host.Name)
	if code != nil && *code == 255 && strings.Contains(stderr, full.Message) {
		return full
	}
	return nil
}
