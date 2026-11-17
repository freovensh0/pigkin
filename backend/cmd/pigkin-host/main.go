// Command pigkin-host is the Go replacement for the TypeScript host that
// connects back to the Pigkin app from Termux. It speaks the same JSONL
// protocol; the app still starts host.mjs, so this is not wired in yet.
package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	_ "github.com/MichaelKinsy/PiG/ai/all"
	// Linking the Go port makes its version part of the binary the app talks to.
	_ "github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/cli"
	"github.com/freovensh0/pigkin/backend/internal/host"
	"github.com/freovensh0/pigkin/backend/internal/modes"
)

const (
	protocol = 1
	// requestTimeout is how long the host waits for the app's request after
	// its hello.
	requestTimeout = 10 * time.Second

	exitUsage     = 64
	exitNoRequest = 2
)

func main() {
	// The rpc mode starts this binary as a child so PiG's RPC server owns its
	// own stdin/stdout and exit status.
	if len(os.Args) > 1 && os.Args[1] == modes.HiddenRPCFlag {
		cli.Main(os.Args[2:], nil)
		return
	}
	// ssh runs the host as its askpass program with the question as an
	// argument; that role never connects back to the app.
	if len(os.Args) >= 2 && os.Args[1] == "askpass" {
		prompt := ""
		if len(os.Args) >= 3 {
			prompt = os.Args[2]
		}
		os.Exit(modes.Askpass(prompt))
	}

	mode, port, ok := arguments()
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: PIGKIN_TOKEN=… PIGKIN_ROOT=… PI_CODING_AGENT_DIR=… PREFIX=… pigkin-host <mode> <port>")
		os.Exit(exitUsage)
	}
	env, ok := host.ReadEnv()
	if !ok {
		fmt.Fprintln(os.Stderr, "pigkin-host: one of PIGKIN_TOKEN, PIGKIN_ROOT, PI_CODING_AGENT_DIR, PREFIX is missing")
		os.Exit(exitUsage)
	}

	socket, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "pigkin-host: %v\n", err)
		os.Exit(1)
	}
	conn := host.NewConnection(socket)
	conn.Send(host.Message{
		"type":          "pigkin_hello",
		"protocol":      protocol,
		"token":         env.Token,
		"mode":          mode,
		"piVersion":     piVersion(),
		"nodeVersion":   runtime.Version(),
		"configDir":     env.ConfigDir,
		"home":          env.Home,
		"sharedStorage": host.GrantedSharedStorage(env) != "",
	})

	request, status := conn.First(requestTimeout)
	switch status {
	case host.FirstTimeout:
		os.Exit(exitNoRequest)
	case host.FirstClosed:
		os.Exit(1)
	}
	if request["type"] != "pigkin_request" {
		conn.Send(host.Message{"type": "pigkin_result", "ok": false, "error": "Expected a pigkin_request", "code": "bad_request"})
		_ = conn.Close()
		return
	}
	params, _ := request["params"].(map[string]any)
	if params == nil {
		conn.Send(host.Message{"type": "pigkin_result", "ok": false, "error": "Expected a pigkin_request", "code": "bad_request"})
		_ = conn.Close()
		return
	}

	run, known := modes.All[mode]
	if !known {
		conn.Send(host.Message{"type": "pigkin_result", "ok": false, "error": "Unknown mode " + mode, "code": "unknown_mode"})
		_ = conn.Close()
		return
	}
	ctx := &modes.Context{Env: env, Params: params, Conn: conn}
	data, err := run(ctx)
	result := host.Message{"type": "pigkin_result", "ok": true, "data": data}
	if err != nil {
		result = modes.Failure(err)
	}
	conn.Send(result)
	_ = conn.Close()
}

// piVersion is the Go Pi port's version from the build, or nil when the binary
// was built without module info.
func piVersion() any {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	for _, dependency := range info.Deps {
		if dependency.Path == "github.com/MichaelKinsy/PiG" {
			return dependency.Version
		}
	}
	return nil
}

// arguments reads the mode and connect-back port.
func arguments() (string, int, bool) {
	args := os.Args[1:]
	if len(args) != 2 {
		return "", 0, false
	}
	port, err := strconv.Atoi(args[1])
	if err != nil || port <= 0 || port > 65_535 {
		return "", 0, false
	}
	return args[0], port, true
}
