package modes

import (
	"context"
	"os"
	"os/exec"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// piInstallAttempts is how many times pi install runs: a dropped connection or
// a stale npm cache entry often clears on the second try.
const piInstallAttempts = 2

// packageInstall runs `pi install <source>` in Pigkin's Pi, adding the package
// to Pi's global settings so the next chat loads it. A config lock keeps a
// backup or restore from losing the settings Pi writes. Mirrors
// host/src/modes/package-install.ts.
func packageInstall(ctx *Context) (host.Message, error) {
	source, err := stringParam(ctx.Params, "source")
	if err != nil {
		return nil, err
	}
	release, err := withConfigLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	entry, err := piEntry(ctx.Env.Root)
	if err != nil {
		return nil, host.NewError("pi_version", "The agent's install is incomplete", &host.Text{Key: "agent_install_incomplete"})
	}
	node, err := piNode(ctx.Env)
	if err != nil {
		return nil, err
	}
	base := connContext(ctx.Conn)
	var lastLine string
	var code *int
	for attempt := 0; attempt < piInstallAttempts; attempt++ {
		lastLine = ""
		code, lastLine, err = runPiInstall(base, ctx, node, entry, source)
		if err != nil {
			return nil, err
		}
		if code != nil && *code == 0 {
			return host.Message{}, nil
		}
	}
	if lastLine != "" {
		return nil, host.NewError("install_failed", lastLine, nil)
	}
	return nil, commandFailed("install_failed", "pi install", code)
}

// runPiInstall runs one pi install, forwarding Pi's output as progress lines
// and returning its exit code and last non-blank line.
func runPiInstall(base context.Context, ctx *Context, node, entry, source string) (*int, string, error) {
	cmd := exec.CommandContext(base, node, entry, "install", source)
	cmd.Dir = ctx.Env.Home
	cmd.Env = append(os.Environ(),
		"PIGKIN_ROOT="+ctx.Env.Root,
		"PI_CODING_AGENT_DIR="+ctx.Env.ConfigDir,
		"HOME="+ctx.Env.Home,
		"PREFIX="+ctx.Env.Prefix,
	)
	lastLine := ""
	lines := &progressLines{
		send: func(line string) { sendProgress(ctx, line) },
		note: func(line string) {
			if line != "" {
				lastLine = line
			}
		},
	}
	cmd.Stdout = sshWriterFunc(lines.write)
	cmd.Stderr = sshWriterFunc(lines.write)
	err := cmd.Run()
	lines.flush()
	if base.Err() != nil {
		return nil, "", base.Err()
	}
	return exitCode(err), lastLine, nil
}
