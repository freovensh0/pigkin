// Package modes implements the host modes the app starts the host with.
package modes

import (
	"errors"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// Context is what a mode needs: the Termux environment, its request's params,
// and the connection to the app.
type Context struct {
	Env    host.Env
	Params host.Message
	Conn   *host.Connection
}

// Mode answers one connection. A mode that streams keeps listening on the
// connection and returns once the app hangs up.
type Mode func(*Context) (host.Message, error)

// All is the modes ported so far. Every other mode the TypeScript host has is
// still there; see plans/go-backend.md for the port order.
var All = map[string]Mode{
	"hello":    hello,
	"browse":   browse,
	"files":    files,
	"login":    login,
	"utility":  utility,
	"git":      git,
	"rpc":      rpc,
	"terminal": terminal,
}

// Failure is the pigkin_result that reports err as a refusal.
func Failure(err error) host.Message {
	var hostErr *host.Error
	if errors.As(err, &hostErr) {
		result := host.Message{
			"type":  "pigkin_result",
			"ok":    false,
			"error": hostErr.Message,
			"code":  hostErr.Code,
		}
		if hostErr.I18n != nil {
			result["i18n"] = hostErr.I18n
		}
		return result
	}
	return host.Message{
		"type":  "pigkin_result",
		"ok":    false,
		"error": err.Error(),
		"code":  "failed",
	}
}
