package modes

import "github.com/freovensh0/pigkin/backend/internal/host"

// The Go host still has no port of Pi's TypeScript extension updater, so that
// mode refuses explicitly instead of reporting a success that never happened.
func updateExtensions(*Context) (host.Message, error) { return unsupported("Updating npm extensions") }

func unsupported(feature string) (host.Message, error) {
	return nil, host.NewError("unsupported", feature+" isn't supported by this host yet",
		&host.Text{Key: "unsupported_mode", Args: map[string]any{"feature": feature}})
}
