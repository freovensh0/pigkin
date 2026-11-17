package modes

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// The utility operations that manage remote hosts, and the two standalone
// modes that need a connection of their own.
func remoteHostsOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	installed := sshInstalled(paths)
	key, err := publicKey(paths)
	if err != nil {
		return nil, err
	}
	hosts, err := loadHosts(paths)
	if err != nil {
		return nil, err
	}
	workspaces, err := listStandIns(paths.remotes)
	if err != nil {
		return nil, err
	}
	return host.Message{
		"sshInstalled": installed,
		"publicKey":    key,
		"hosts":        hosts,
		"workspaces":   workspaces,
	}, nil
}

type savedHostResult struct {
	saved    Host
	previous *Host
}

func remoteHostSaveOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	result, err := updateHosts(paths, func(hosts []Host) ([]Host, any, error) {
		draft, err := validateHost(ctx.Params["host"], hosts)
		if err != nil {
			return nil, nil, err
		}
		var existing *Host
		if draft.ID != nil {
			for index := range hosts {
				if hosts[index].ID == *draft.ID {
					copy := hosts[index]
					existing = &copy
					break
				}
			}
			if existing == nil {
				return nil, nil, hostUnknown()
			}
		}
		moved := false
		if existing != nil {
			samePort := (existing.Port == nil) == (draft.Port == nil)
			if samePort && existing.Port != nil {
				samePort = *existing.Port == *draft.Port
			}
			moved = existing.Address != draft.Address || !samePort || existing.Auth != draft.Auth
		}
		saved := Host{
			Name:    draft.Name,
			Auth:    draft.Auth,
			Address: draft.Address,
			Port:    draft.Port,
		}
		if draft.ID != nil {
			saved.ID = *draft.ID
		} else {
			id, err := newHostId()
			if err != nil {
				return nil, nil, err
			}
			saved.ID = id
		}
		if !moved && existing != nil {
			saved.Home = existing.Home
		}
		next := make([]Host, 0, len(hosts)+1)
		replaced := false
		for _, old := range hosts {
			if old.ID == saved.ID {
				next = append(next, saved)
				replaced = true
			} else {
				next = append(next, old)
			}
		}
		if !replaced {
			next = append(next, saved)
		}
		var previous *Host
		if moved {
			previous = existing
		}
		return next, savedHostResult{saved: saved, previous: previous}, nil
	})
	if err != nil {
		return nil, err
	}
	outcome := result.(savedHostResult)
	if outcome.previous != nil {
		disconnect(remote{paths: paths, host: *outcome.previous})
	}
	return host.Message{"host": outcome.saved}, nil
}

func remoteHostRemoveOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	id, err := stringParam(ctx.Params, "id")
	if err != nil {
		return nil, err
	}
	result, err := updateHosts(paths, func(hosts []Host) ([]Host, any, error) {
		next := make([]Host, 0, len(hosts))
		var removed *Host
		for _, host := range hosts {
			if host.ID == id {
				copy := host
				removed = &copy
				continue
			}
			next = append(next, host)
		}
		return next, removed, nil
	})
	if err != nil {
		return nil, err
	}
	if removed, ok := result.(*Host); ok && removed != nil {
		disconnect(remote{paths: paths, host: *removed})
	}
	return host.Message{}, nil
}

func remoteKeyOp(ctx *Context) (host.Message, error) {
	line, err := ensureKey(remoteLayoutFor(ctx.Env))
	if err != nil {
		return nil, err
	}
	return host.Message{"publicKey": line}, nil
}

func remoteKeyNewOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	line, err := replaceKey(paths)
	if err != nil {
		return nil, err
	}
	hosts, err := loadHosts(paths)
	if err != nil {
		return nil, err
	}
	for _, host := range hosts {
		if host.Auth == hostAuthKey {
			disconnect(remote{paths: paths, host: host})
		}
	}
	return host.Message{"publicKey": line}, nil
}

func remoteStatusOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	ids, err := stringArrayParam(ctx.Params, "ids")
	if err != nil {
		return nil, err
	}
	hosts, err := loadHosts(paths)
	if err != nil {
		return nil, err
	}
	connected := make(map[string]bool, len(ids))
	for _, id := range ids {
		state := false
		for _, host := range hosts {
			if host.ID == id {
				state = isConnected(remote{paths: paths, host: host})
				break
			}
		}
		connected[id] = state
	}
	return host.Message{"connected": connected}, nil
}

func remoteDisconnectOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	id, err := stringParam(ctx.Params, "id")
	if err != nil {
		return nil, err
	}
	hosts, err := loadHosts(paths)
	if err != nil {
		return nil, err
	}
	for _, host := range hosts {
		if host.ID == id {
			disconnect(remote{paths: paths, host: host})
			break
		}
	}
	return host.Message{}, nil
}

func remoteForgetHostKeyOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	id, err := stringParam(ctx.Params, "id")
	if err != nil {
		return nil, err
	}
	target, err := findHost(paths, id)
	if err != nil {
		return nil, err
	}
	if err := forgetHostKey(remote{paths: paths, host: target}); err != nil {
		return nil, err
	}
	return host.Message{}, nil
}

func remoteWorkspaceOp(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	hostID, err := stringParam(ctx.Params, "hostId")
	if err != nil {
		return nil, err
	}
	target, err := findHost(paths, hostID)
	if err != nil {
		return nil, err
	}
	remotePath, err := stringParam(ctx.Params, "path")
	if err != nil {
		return nil, err
	}
	cwd, err := createStandIn(paths.remotes, target.ID, remotePath)
	if err != nil {
		return nil, err
	}
	return host.Message{"cwd": cwd}, nil
}

// remoteConnectMode connects a host, passing ssh's questions to the app as
// pigkin_remote_prompt and waiting for pigkin_remote_answer.
func remoteConnectMode(ctx *Context) (host.Message, error) {
	paths := remoteLayoutFor(ctx.Env)
	id, err := stringParam(ctx.Params, "hostId")
	if err != nil {
		return nil, err
	}
	target, err := findHost(paths, id)
	if err != nil {
		return nil, err
	}

	var pendingMu sync.Mutex
	pending := make(map[int64]chan *string)
	var nextID atomic.Int64
	ctx.Conn.Listen(func(message host.Message) {
		if message["type"] != "pigkin_remote_answer" {
			return
		}
		number, ok := message["id"].(float64)
		if !ok {
			return
		}
		key := int64(number)
		pendingMu.Lock()
		answer := pending[key]
		delete(pending, key)
		pendingMu.Unlock()
		if answer == nil {
			return
		}
		var value *string
		if text, ok := message["answer"].(string); ok {
			value = &text
		}
		select {
		case answer <- value:
		default:
		}
	})

	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	probe, err := connect(remote{paths: paths, host: target}, connectOptions{
		askpassCommand: []string{self, "askpass"},
		ctx:            connContext(ctx.Conn),
		unattended:     ctx.Params["unattended"] == true,
		prompt: func(asked remotePrompt) (string, bool) {
			key := nextID.Add(1)
			answer := make(chan *string, 1)
			pendingMu.Lock()
			pending[key] = answer
			pendingMu.Unlock()
			message := host.Message{
				"type": "pigkin_remote_prompt", "id": key,
				"kind": asked.Kind, "text": asked.Text,
			}
			if asked.KeyType != "" {
				message["keyType"] = asked.KeyType
			}
			if asked.Fingerprint != "" {
				message["fingerprint"] = asked.Fingerprint
			}
			ctx.Conn.Send(message)
			select {
			case value := <-answer:
				if value == nil {
					return "", false
				}
				return *value, true
			case <-ctx.Conn.Done():
				pendingMu.Lock()
				delete(pending, key)
				pendingMu.Unlock()
				return "", false
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return host.Message{
		"home": probe.Home,
		"tools": host.Message{
			"bash": probe.Tools.Bash, "git": probe.Tools.Git, "rg": probe.Tools.Rg,
		},
	}, nil
}

var (
	installBusyLine    = regexp.MustCompile(`(?i)Could not get lock|Unable to acquire the dpkg frontend lock|Unable to lock the administration directory|dpkg frontend lock|locked by another process`)
	installOfflineLine = regexp.MustCompile(`(?i)Failed to fetch|Unable to fetch|Temporary failure resolving|Could not resolve|Could not connect to|Connection timed out|Network is unreachable|No address associated`)
)

type progressLines struct {
	mu    sync.Mutex
	carry string
	send  func(string)
	note  func(string)
}

func (lines *progressLines) write(data []byte) (int, error) {
	lines.mu.Lock()
	defer lines.mu.Unlock()
	parts := strings.Split(lines.carry+string(data), "\n")
	lines.carry = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		lines.emit(line)
	}
	return len(data), nil
}

func (lines *progressLines) flush() {
	lines.mu.Lock()
	defer lines.mu.Unlock()
	if lines.carry != "" {
		lines.emit(lines.carry)
		lines.carry = ""
	}
}

func (lines *progressLines) emit(line string) {
	line = strings.TrimSuffix(line, "\r")
	lines.note(line)
	lines.send(line)
}

// remoteInstallMode installs Termux's OpenSSH, forwarding pkg's output as
// progress lines.
func remoteInstallMode(ctx *Context) (host.Message, error) {
	base := connContext(ctx.Conn)
	cmd := exec.CommandContext(base, filepath.Join(ctx.Env.Prefix, "bin", "pkg"), "install", "-y", "openssh")
	cmd.Dir = ctx.Env.Home
	cmd.Env = sshEnvironment(nil)
	busy, offline := false, false
	lines := &progressLines{
		send: func(line string) { ctx.Conn.Send(host.Message{"type": "pigkin_progress", "line": line}) },
		note: func(line string) {
			busy = busy || installBusyLine.MatchString(line)
			offline = offline || installOfflineLine.MatchString(line)
		},
	}
	cmd.Stdout = sshWriterFunc(lines.write)
	cmd.Stderr = sshWriterFunc(lines.write)
	err := cmd.Run()
	lines.flush()
	if err == nil {
		return host.Message{}, nil
	}
	if base.Err() != nil {
		return nil, base.Err()
	}
	if busy {
		return nil, host.NewError("remote_install_busy", "Termux's package manager is busy with another install. Try again once it's done.",
			&host.Text{Key: "remote_install_busy"})
	}
	if offline {
		return nil, host.NewError("remote_install_offline", "Couldn't download OpenSSH. Check your internet connection, then try again.",
			&host.Text{Key: "remote_install_offline"})
	}
	return nil, commandFailed("install_failed", "pkg install openssh", exitCode(err))
}
