package modes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// The failures a remote operation reports to the app. Their codes are the ones
// the app's RemoteFailure recognises; their i18n keys are in HostText.kt.
func sshMissing() *host.Error {
	return host.NewError("ssh_missing", "OpenSSH isn't installed in Termux.", &host.Text{Key: "remote_ssh_missing"})
}

func hostUnknown() *host.Error {
	return host.NewError("remote_host_unknown", "This workspace's host was removed.", &host.Text{Key: "remote_host_unknown"})
}

func bashMissing(name string) *host.Error {
	return host.NewError("remote_bash_missing", name+" has no bash, which Pigkin needs to work there.",
		&host.Text{Key: "remote_bash_missing", Args: map[string]any{"host": name}})
}

func connectCancelled() *host.Error {
	return host.NewError("remote_connect_cancelled", "Connecting was cancelled.", &host.Text{Key: "remote_connect_cancelled"})
}

func authFailed(name string) *host.Error {
	return host.NewError("remote_auth_failed", name+" refused the sign-in.",
		&host.Text{Key: "remote_auth_failed", Args: map[string]any{"host": name}})
}

func notConnected(name string) *host.Error {
	return host.NewError("remote_not_connected", "Not connected to "+name+". Connect to sign in.",
		&host.Text{Key: "remote_not_connected", Args: map[string]any{"host": name}})
}

func unreachable(name, detail string) *host.Error {
	return host.NewError("remote_unreachable", "Couldn't reach "+name+": "+detail,
		&host.Text{Key: "remote_unreachable", Args: map[string]any{"host": name, "detail": detail}})
}

func sessionsFull(name string) *host.Error {
	return host.NewError("remote_sessions_full", name+" has no free sessions. Close a terminal or stop a background task, then try again.",
		&host.Text{Key: "remote_sessions_full", Args: map[string]any{"host": name}})
}

func remoteFailed(name, detail string) *host.Error {
	return host.NewError("remote_failed", "SSH to "+name+" failed: "+detail,
		&host.Text{Key: "remote_failed", Args: map[string]any{"host": name, "detail": detail}})
}

// commandFailed reports a local command that exited with code, worded like the
// TypeScript host's ensure command failures.
func commandFailed(code, command string, exitCode *int) *host.Error {
	if exitCode == nil {
		return host.NewError(code, command+" failed (exit signal)",
			&host.Text{Key: "command_killed", Args: map[string]any{"command": command}})
	}
	return host.NewError(code, fmt.Sprintf("%s failed (exit %d)", command, *exitCode),
		&host.Text{Key: "command_failed", Args: map[string]any{"command": command, "exit": *exitCode}})
}

// errorJSON is the failure shape on pigkin_places and the channel's remote
// topic: the app shows it as is.
func errorJSON(err *host.Error) host.Message {
	result := host.Message{"code": err.Code, "message": err.Message}
	if err.I18n != nil {
		result["i18n"] = *err.I18n
	}
	return result
}

// failureContext is what an ssh failure's classification depends on.
type failureContext struct {
	name        string
	auth        hostAuth
	interactive bool
}

var (
	hostKeyChangedLine = regexp.MustCompile(`^Host key for .* has changed`)
	hostKeyUnknownLine = regexp.MustCompile(`^No \S+ host key is known for |^Host key verification failed\.`)
	authRefusedLine    = regexp.MustCompile(`: Permission denied \(|^Too many authentication failures|^Received disconnect from .*: Too many authentication failures`)
	unreachableLine    = regexp.MustCompile(`Connection refused|Connection timed out|Operation timed out|No route to host|Network is unreachable|Network is down|Host is down|Could not resolve hostname|Name or service not known|Temporary failure in name resolution|nodename nor servname|No address associated with hostname|Timeout, server .* not responding|Connection reset|Connection closed by|closed by remote host|Broken pipe|banner exchange|Software caused connection abort`)
	sshOwnLine         = regexp.MustCompile(`^(ssh: |ssh_\w+|kex_\w+|mux_client_\w+|muxclient|Control ?[Ss]ocket|client_loop: |Bad (owner|configuration)|Connection |Host key |No \S+ host key |Too many authentication|Received disconnect|Load key |\S+: Permission denied \(|\S+ line \d+: |Unable to negotiate|command-line line \d+: |Timeout, server)`)
	sshNoiseLine       = regexp.MustCompile(`^(Warning: |\*\* |@|$)`)
	noBashLine         = regexp.MustCompile(`(?m)(^|[\s:])bash: ([Cc]ommand )?not found|command not found: bash|Unknown command:? '?bash\b|exec: bash: No such file`)
)

// lastLine is stderr's last line that says something, which is how ssh ends a
// failure.
func lastLine(stderr string) string {
	lines := strings.Split(stderr, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !sshNoiseLine.MatchString(line) {
			return line
		}
	}
	return ""
}

// classifySshFailure reads an ssh run's exit-255 stderr. It returns nil when
// the 255 came from the remote command, which ssh passes through.
func classifySshFailure(stderr string, context failureContext) *host.Error {
	var own []string
	for _, line := range strings.Split(stderr, "\n") {
		if sshOwnLine.MatchString(line) {
			own = append(own, line)
		}
	}
	if len(own) == 0 {
		return nil
	}
	for _, line := range own {
		if hostKeyChangedLine.MatchString(line) {
			return host.NewError("remote_host_key_changed", context.name+"'s host key has changed. Someone may be intercepting the connection.",
				&host.Text{Key: "remote_host_key_changed", Args: map[string]any{"host": context.name}})
		}
	}
	for _, line := range own {
		if hostKeyUnknownLine.MatchString(line) {
			return host.NewError("remote_host_key_unknown", context.name+"'s host key isn't known yet. Connect to check it.",
				&host.Text{Key: "remote_host_key_unknown", Args: map[string]any{"host": context.name}})
		}
	}
	for _, line := range own {
		if authRefusedLine.MatchString(line) {
			if context.interactive || context.auth == hostAuthKey {
				return authFailed(context.name)
			}
			return notConnected(context.name)
		}
	}
	for _, line := range own {
		if unreachableLine.MatchString(line) {
			return unreachable(context.name, strings.TrimSpace(line))
		}
	}
	return remoteFailed(context.name, lastLine(strings.Join(own, "\n")))
}

// isBashMissing tells a remote without bash from a command bash couldn't find.
func isBashMissing(code *int, stderr string) bool {
	return code != nil && *code == 127 && noBashLine.MatchString(stderr)
}

// exitCode reads a process's exit status, or nil when it was killed by a signal.
func exitCode(err error) *int {
	if err == nil {
		code := 0
		return &code
	}
	type status interface{ ExitCode() int }
	if coded, ok := err.(status); ok {
		code := coded.ExitCode()
		if code < 0 {
			return nil
		}
		return &code
	}
	return nil
}

// atoi is a JSON number that is an integer, as the app sends a port.
func atoi(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

// percent formats value the way String(port) does in the host's error text.
func percent(value any) string {
	if value == nil {
		return "null"
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'g', -1, 64)
	}
	return fmt.Sprint(value)
}
