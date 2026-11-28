package modes

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// probeCommand runs in the login shell itself: POSIX, so it works without
// bash and reports its absence. It prints the home folder, then tool paths.
const probeCommand = `printf '%s\n' "$HOME"; command -v bash; command -v git; command -v rg`

type probeResult struct {
	Home  string
	Tools struct {
		Bash bool
		Git  bool
		Rg   bool
	}
}

// parseProbe reads the home folder and tool names from the probe's output.
func parseProbe(output string) *probeResult {
	lines := strings.Split(output, "\n")
	home := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(home, "/") {
		return nil
	}
	result := &probeResult{Home: home}
	for _, line := range lines[1:] {
		name := path.Base(strings.TrimSpace(line))
		switch name {
		case "bash":
			result.Tools.Bash = true
		case "git":
			result.Tools.Git = true
		case "rg":
			result.Tools.Rg = true
		}
	}
	return result
}

type connectOptions struct {
	askpassCommand []string
	prompt         func(remotePrompt) (string, bool)
	ctx            context.Context
	unattended     bool
}

var bridgeNumbers atomic.Int64

// connect leaves a master connection running: ssh asks about the host key,
// password or passphrase through Pigkin's askpass, and each question goes to
// options.prompt. An unattended connect never asks about the host key.
func connect(r remote, opts connectOptions) (*probeResult, error) {
	hosts, err := loadHosts(r.paths)
	if err != nil {
		return nil, err
	}
	if err := writeSshConfig(r.paths, hosts); err != nil {
		return nil, err
	}
	commandWords := make([]string, 0, len(opts.askpassCommand))
	for _, word := range opts.askpassCommand {
		quoted, err := shellQuote(word)
		if err != nil {
			return nil, err
		}
		commandWords = append(commandWords, quoted)
	}
	script := "#!" + r.paths.shBinary() + "\nexec " + strings.Join(commandWords, " ") + ` "$@"` + "\n"
	if err := writeAtomically(r.paths.askpassFile(), script, 0o700); err != nil {
		return nil, err
	}

	ctx := opts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stateMu sync.Mutex
	var aborted *host.Error
	passwordAsked := false
	passphrases := make(map[string]bool)
	answer := func(text string) (string, bool) {
		prompt := describePrompt(text)
		stateMu.Lock()
		repeated := false
		if prompt.Kind == "password" {
			repeated = passwordAsked
		} else if prompt.Kind == "passphrase" {
			repeated = passphrases[text]
		}
		if repeated {
			aborted = authFailed(r.host.Name)
			stateMu.Unlock()
			cancel()
			return "", false
		}
		if prompt.Kind == "password" {
			passwordAsked = true
		}
		if prompt.Kind == "passphrase" {
			passphrases[text] = true
		}
		stateMu.Unlock()
		reply, ok := opts.prompt(prompt)
		if !ok {
			stateMu.Lock()
			aborted = connectCancelled()
			stateMu.Unlock()
			cancel()
			return "", false
		}
		if prompt.Kind == "host_key" {
			return "yes", true
		}
		return reply, true
	}

	// In the SSH folder, whose path is short enough for a Unix socket's sun_path.
	socketPath := filepath.Join(r.paths.ssh, fmt.Sprintf("a%d-%d", os.Getpid(), bridgeNumbers.Add(1)))
	environment := sshEnvironment(map[string]string{
		"SSH_ASKPASS":         r.paths.askpassFile(),
		"SSH_ASKPASS_REQUIRE": "force",
		askpassSocketVariable: socketPath,
	})
	bridge, err := startAskpassBridge(socketPath, func(prompt string, confirm bool) (string, bool) {
		return answer(prompt)
	})
	if err != nil {
		return nil, err
	}
	defer bridge.close()

	release, err := acquireSession(r.host.ID, ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	mode := signInteractive
	if opts.unattended {
		mode = signUnattended
	}
	exit, err := spawnSsh(r.paths, sshArgs(r, probeCommand, mode), sshRunOptions{ctx: ctx}, environment)
	if err != nil {
		return nil, err
	}
	stateMu.Lock()
	stopped := aborted
	stateMu.Unlock()
	if stopped != nil {
		return nil, stopped
	}
	if exit.code != nil && *exit.code == 255 {
		context := failureContext{name: r.host.Name, auth: r.host.Auth, interactive: true}
		if failure := classifySshFailure(exit.stderr, context); failure != nil {
			return nil, failure
		}
		return nil, remoteFailed(r.host.Name, lastLine(exit.stderr))
	}
	probe := parseProbe(string(exit.stdout))
	if probe == nil {
		detail := lastLine(exit.stderr)
		if detail == "" {
			detail = "no home folder"
		}
		return nil, remoteFailed(r.host.Name, detail)
	}
	if !probe.Tools.Bash {
		return nil, bashMissing(r.host.Name)
	}
	if err := recordHome(r.paths, r.host.ID, probe.Home); err != nil {
		return nil, err
	}
	return probe, nil
}

// connContext is cancelled when the app hangs up on the mode's connection.
func connContext(conn *host.Connection) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-conn.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
