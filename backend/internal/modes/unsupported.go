package modes

import "github.com/freovensh0/pigkin/backend/internal/host"

// The Go host has no Node runtime: npm packages and Pi's TypeScript extension
// updater have nothing to act on. These modes refuse explicitly so the app
// never reports a package as installed when it isn't.
func packageInstall(*Context) (host.Message, error) { return unsupported("Installing npm packages") }

func updateExtensions(*Context) (host.Message, error) { return unsupported("Updating npm extensions") }

func unsupported(feature string) (host.Message, error) {
	return nil, host.NewError("unsupported", feature+" isn't supported by this host yet",
		&host.Text{Key: "unsupported_mode", Args: map[string]any{"feature": feature}})
}
