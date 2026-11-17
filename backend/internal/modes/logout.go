package modes

import (
	"errors"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func logoutOp(ctx *Context) (host.Message, error) {
	providerID, err := stringParam(ctx.Params, "providerId")
	if err != nil {
		return nil, err
	}
	runtime, err := createRuntime(ctx, false)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	if err := runtime.Logout(piContext(ctx), providerID); err != nil {
		return nil, err
	}

	settings, err := readSettings(ctx.Env.ConfigDir)
	if err != nil {
		if errors.Is(err, errSettingsInvalid) {
			return host.Message{}, nil
		}
		return nil, err
	}
	patterns, err := enabledModels(settings)
	if err != nil || len(patterns) == 0 {
		return host.Message{}, nil
	}
	universe, err := scopeUniverse(runtime)
	if err != nil {
		return nil, err
	}
	kept := withoutMisdirected(patterns, universe)
	if !jsEqual(kept, patterns) {
		setEnabledModels(settings, kept)
		if err := writeSettings(ctx.Env.ConfigDir, settings); err != nil {
			return nil, err
		}
	}
	return host.Message{}, nil
}
