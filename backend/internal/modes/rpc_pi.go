package modes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	piPackage       = "@earendil-works/pi-coding-agent"
	remoteExtension = "pigkin-remote"
)

var extensionNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type pigkinExtension struct {
	name string
	path string
}

type lockedTail struct {
	mu  sync.Mutex
	buf tailBuffer
}

func (t *lockedTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Write(p)
}

func (t *lockedTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func piEntry(root string) (string, error) {
	dir := filepath.Join(root, "pi", "node_modules", filepath.FromSlash(piPackage))
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	var entry string
	if err := json.Unmarshal(manifest.Bin, &entry); err != nil {
		var bin map[string]string
		if err := json.Unmarshal(manifest.Bin, &bin); err != nil {
			return "", errors.New("agent package.json has no bin.pi")
		}
		entry = bin["pi"]
	}
	if entry == "" {
		return "", errors.New("agent package.json has no bin.pi")
	}
	return filepath.Join(dir, entry), nil
}

// installedPigkinExtensions resolves the paths host/src/pigkin-extensions.ts
// uses and checks each installed file against the app's fingerprint. A missing
// or changed file means the app must fall back to the extension-less backend.
func installedPigkinExtensions(ctx *Context) []pigkinExtension {
	items, ok := ctx.Params["pigkinExtensions"].([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	extensions := make([]pigkinExtension, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		name, _ := obj["name"].(string)
		fingerprint, _ := obj["sha256"].(string)
		inRuntime, _ := obj["usesRuntimePackages"].(bool)
		if !extensionNameRE.MatchString(name) || fingerprint == "" {
			return nil
		}
		path := filepath.Join(ctx.Env.Root, "extensions", name, "index.js")
		if inRuntime {
			path = filepath.Join(ctx.Env.Root, "pi", "extensions", name, "index.js")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != fingerprint {
			return nil
		}
		extensions = append(extensions, pigkinExtension{name: name, path: path})
	}
	return extensions
}

type remoteChat struct {
	extension string
	prompt    string
}

// remoteChatFor resolves the remote workspace a stand-in cwd names. A local
// folder needs no remote chat; a remote one needs pigkin-remote installed.
func remoteChatFor(ctx *Context, cwd string, extensions []pigkinExtension) (*remoteChat, error) {
	paths := remoteLayoutFor(ctx.Env)
	standIn, err := readStandIn(paths.remotes, cwd)
	if err != nil || standIn == nil {
		return nil, err
	}
	var extension string
	for _, candidate := range extensions {
		if candidate.name == remoteExtension {
			extension = candidate.path
			break
		}
	}
	if extension == "" {
		return nil, host.NewError("pi_version", "The agent's bundled extensions are out of date", &host.Text{Key: "agent_extensions_outdated"})
	}
	hostID, _ := standIn["hostId"].(string)
	remotePath, _ := standIn["path"].(string)
	target, err := findHost(paths, hostID)
	if err != nil {
		return nil, err
	}
	return &remoteChat{extension: extension, prompt: remoteSystemAppend(target, remotePath)}, nil
}

// rpcWithPi runs the Node Pi the app bundles, with the same fd-3 channel and
// --extension paths as host/src/modes/rpc.ts. PiG's own extension host spawns
// Node with no ExtraFiles (coding/extension/host/subprocess/host.go:3192-3269),
// so its extensions never see fd 3 and their pigkinChannel() stays inert.
func rpcWithPi(ctx *Context, cwd, sessionPath string, extensions []pigkinExtension) (host.Message, error) {
	entry, err := piEntry(ctx.Env.Root)
	if err != nil {
		return nil, host.NewError("pi_version", "The agent's install is incomplete", &host.Text{Key: "agent_install_incomplete"})
	}
	node, err := exec.LookPath("node")
	if err != nil {
		node = filepath.Join(ctx.Env.Prefix, "bin", "node")
		if _, statErr := os.Stat(node); statErr != nil {
			return nil, err
		}
	}
	remote, err := remoteChatFor(ctx, cwd, extensions)
	if err != nil {
		return nil, err
	}
	args := []string{entry, "--mode", "rpc"}
	if sessionPath != "" {
		args = append(args, "--session", sessionPath)
	}
	for _, extension := range extensions {
		if remote != nil && extension.name == remoteExtension {
			continue
		}
		args = append(args, "--extension", extension.path)
	}
	prompt := systemAppend
	if remote != nil {
		// pigkin-remote replaces the local tools, so Pi must load it last.
		args = append(args, "--extension", remote.extension)
		prompt = remote.prompt
	}
	args = append(args, "--append-system-prompt", prompt)

	cmd := exec.Command(node, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"PIGKIN_ROOT="+ctx.Env.Root,
		"PI_CODING_AGENT_DIR="+ctx.Env.ConfigDir,
		"HOME="+ctx.Env.Home,
		"PREFIX="+ctx.Env.Prefix,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr lockedTail
	cmd.Stderr = &stderr
	channel, childChannel, err := newChannelPair()
	if err != nil {
		return nil, err
	}
	cmd.ExtraFiles = []*os.File{childChannel}
	if err := cmd.Start(); err != nil {
		_ = childChannel.Close()
		_ = channel.Close()
		return nil, err
	}
	_ = childChannel.Close()
	socket := &channelSocket{conn: channel}
	_ = socket.send(channelMessage{Topic: helloTopic})

	done := make(chan struct{})
	stop := func() {
		_ = stdin.Close()
		select {
		case <-done:
		case <-time.After(killAfter):
			_ = cmd.Process.Kill()
		}
	}
	merger := &outputMerger{write: ctx.Conn.SendBytes}
	note := func(reason string) {
		_, _ = stderr.Write([]byte(reason + "\n"))
	}
	go readChannel(channel, merger.channel, func(reason string) {
		note("dropped a channel line from pi: " + reason)
	})
	appLines := ctx.Conn.Raw()
	go func() {
		for {
			select {
			case line, open := <-appLines:
				if !open {
					stop()
					return
				}
				if lineType(line) == channelType {
					message, ok := parseChannelLine(line, channelType)
					if !ok || len(line) > maxChannelLine {
						note("dropped a channel line from the app")
						continue
					}
					if socket.send(message) != nil {
						return
					}
					continue
				}
				if writeAll(stdin, line) != nil {
					return
				}
			case <-ctx.Conn.Done():
				stop()
				return
			}
		}
	}()
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		scanner := host.ScanLines(stdout)
		for scanner.Scan() {
			merger.flush()
			ctx.Conn.SendBytes(append(append([]byte(nil), scanner.Bytes()...), '\n'))
		}
		merger.flush()
	}()
	<-outputDone
	waitErr := cmd.Wait()
	close(done)
	_ = channel.Close()

	var code any = 0
	var signal any
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			code = nil
			signal = status.Signal().String()
		} else {
			code = exitErr.ExitCode()
		}
	} else if waitErr != nil {
		code = nil
	}
	ctx.Conn.Send(host.Message{
		"type":   "pigkin_exit",
		"code":   code,
		"signal": signal,
		"stderr": stderr.String(),
	})
	return host.Message{}, nil
}
