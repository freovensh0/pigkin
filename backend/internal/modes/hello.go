package modes

import "github.com/freovensh0/pigkin/backend/internal/host"

// hello answers with nothing: the hello the host sent is the point, so the app
// can re-read what it reports after a settings change.
func hello(*Context) (host.Message, error) {
	return host.Message{}, nil
}
