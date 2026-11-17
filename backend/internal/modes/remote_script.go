package modes

import (
	"encoding/base64"
	"strings"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// shellQuote renders value as one single-quoted word for bash or any POSIX
// shell. A NUL would end the word early, so it is refused.
func shellQuote(value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", host.NewError("bad_params", "A path or command can't contain a NUL character", nil)
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'", nil
}

// remoteScript runs script in bash whatever the user's login shell is: base64
// has no character any shell treats specially, so the script arrives intact.
func remoteScript(script string) string {
	return `exec bash -c 'eval "$(printf %s ` + base64.StdEncoding.EncodeToString([]byte(script)) + ` | base64 -d)"'`
}

// watched runs command in cwd under a watchdog: killing the local ssh closes
// standard input, and the watcher then kills the remote process group too.
func watched(command, cwd string) (string, error) {
	quotedCommand, err := shellQuote(command)
	if err != nil {
		return "", err
	}
	quotedCwd, err := shellQuote(cwd)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"set -m",
		"cd -- " + quotedCwd + " || exit 1",
		"exec 3>&2 2>/dev/null",
		"bash -c " + quotedCommand + " </dev/null 2>&3 3>&- &",
		"child=$!",
		`{ cat >/dev/null; kill -TERM -- "-$child" 2>/dev/null; } >/dev/null 2>&1 3>&- &`,
		"watcher=$!",
		`wait "$child"`,
		"status=$?",
		`kill "$watcher" 2>/dev/null`,
		`exit "$status"`,
		"",
	}, "\n"), nil
}
