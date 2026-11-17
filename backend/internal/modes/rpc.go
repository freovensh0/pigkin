package modes

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// systemAppend is Pigkin's identity and Termux guidance, appended to the
// agent's system prompt for every local chat (host/src/prompt.ts).
const systemAppend = `The harness described above is Pi. You are Pigkin: an AI coding assistant that runs on Pi inside Termux on the user's Android device. The user talks to you through the Pigkin app's chat UI. Call yourself Pigkin; Pi's docs describe how you work.

Termux is not a standard Linux system:
- Software can be installed with ` + "`pkg`" + `, Termux's package manager. The ` + "`proot`" + ` package, which provides ` + "`termux-chroot`" + `, and ` + "`proot-distro`" + ` are both available through ` + "`pkg`" + `.
- Termux does not use the standard Linux folder layout; its packages use their own folders instead of ` + "`/bin`" + `, ` + "`/etc`" + `, ` + "`/usr`" + `, and ` + "`/tmp`" + `. Termux's equivalents:
  - ` + "`$PREFIX`" + ` takes the place of ` + "`/usr`" + `. Programs installed with ` + "`pkg`" + ` are in ` + "`$PREFIX/bin`" + `, their configuration in ` + "`$PREFIX/etc`" + `, and their libraries in ` + "`$PREFIX/lib`" + `.
  - ` + "`$TMPDIR`" + ` takes the place of ` + "`/tmp`" + `. It is normally wiped when Termux's background service stops.
  - ` + "`$HOME`" + ` is the Termux home folder.
- Programs that hard-code standard paths can be run with ` + "`termux-chroot <command>`" + `, which makes Termux's folders visible at ` + "`/usr`" + `, ` + "`/bin`" + `, ` + "`/etc`" + `, ` + "`/tmp`" + `, and the other standard paths.
- Termux uses Android's C library, so Linux programs built for other systems may not run natively.
- Such programs can often be run with ` + "`proot-distro`" + `, which provides the environment of a Linux distribution such as Debian or Ubuntu inside Termux. The distribution's programs run directly on the device, but its file system and root user are simulated. The distribution is never booted, so there is no systemd. It has its own package manager, such as ` + "`apt`" + ` on Debian or Ubuntu. Run a command in it with ` + "`proot-distro login <distro> -- <command>`" + `. ` + "`proot-distro`" + ` is slower than native Termux and is not a sandbox: the Termux home folder and Android shared storage are visible inside it, and its root user grants no special privileges.

Termux docs: https://wiki.termux.com/wiki/Differences_from_Linux (includes ` + "`termux-chroot`" + `)
Termux env docs: https://github.com/termux/termux-packages/wiki/Termux-execution-environment
PRoot docs: https://wiki.termux.com/wiki/PRoot
proot-distro docs: https://github.com/termux/proot-distro`

const (
	stderrTail = 16 * 1024
	killAfter  = 5 * time.Second
)

// HiddenRPCFlag selects the child process that runs PiG's RPC server.
const HiddenRPCFlag = "--pig-rpc"

// pigRPCBinary is the executable that serves PiG's RPC protocol. Tests point
// it at a freshly built host binary instead of the test binary.
var pigRPCBinary = os.Executable

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > stderrTail {
		b.data = append([]byte(nil), b.data[len(b.data)-stderrTail:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.data) }

// rpc runs PiG's own RPC mode as a child process and bridges its standard
// streams to the app's socket. PiG emits the same event lines as the Node host,
// so no translation is needed; the host only frames the final exit record.
func rpc(ctx *Context) (host.Message, error) {
	cwd, ok := ctx.Params["cwd"].(string)
	if !ok || cwd == "" {
		return nil, host.NewError("bad_params", "Expected a workspace folder", nil)
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, host.NewError("cwd_missing", "Folder not found: "+cwd, &host.Text{Key: "folder_not_found", Args: map[string]any{"path": cwd}})
	}
	if remoteWorkspace(ctx.Env, cwd) {
		return nil, host.NewError("bad_params", remoteNotPorted, nil)
	}
	sessionPath, _ := ctx.Params["sessionPath"].(string)

	if extensions := installedPigkinExtensions(ctx); len(extensions) > 0 {
		return rpcWithPi(ctx, cwd, sessionPath, extensions)
	}

	exe, err := pigRPCBinary()
	if err != nil {
		return nil, err
	}
	args := []string{
		HiddenRPCFlag,
		"--mode", "rpc",
		"--no-extensions",
		"--session-dir", filepath.Join(ctx.Env.ConfigDir, "sessions"),
		"--append-system-prompt", systemAppend,
	}
	if sessionPath != "" {
		args = append(args, "--session", sessionPath)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"PIG_USE_PI_DIRS=1",
		"PIG_HOME="+ctx.Env.ConfigDir,
		"PI_CODING_AGENT_DIR="+ctx.Env.ConfigDir,
	)
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
		return nil, err
	}

	done := make(chan struct{})
	stop := func() {
		_ = stdin.Close()
		select {
		case <-done:
		case <-time.After(killAfter):
			_ = cmd.Process.Kill()
		}
	}
	appLines := ctx.Conn.Raw()
	go func() {
		for {
			select {
			case line, open := <-appLines:
				if !open {
					stop()
					return
				}
				if writeAll(stdin, line) != nil {
					return
				}
			case <-ctx.Conn.Done():
				stop()
				return
			}
		}
	}()
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		scanner := host.ScanLines(stdout)
		for scanner.Scan() {
			ctx.Conn.SendBytes(append(append([]byte(nil), scanner.Bytes()...), '\n'))
		}
	}()
	<-outputDone
	waitErr := cmd.Wait()
	close(done)

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
	ctx.Conn.Send(host.Message{
		"type":   "pigkin_exit",
		"code":   code,
		"signal": signal,
		"stderr": stderr.String(),
	})
	return host.Message{}, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
