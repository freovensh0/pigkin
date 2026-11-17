package modes

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func writeTool(t *testing.T, env host.Env, name, script string) {
	t.Helper()
	path := filepath.Join(env.Prefix, "bin", name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func saveTestHost(t *testing.T, env host.Env, draft host.Message) string {
	t.Helper()
	result := runOp(t, env, "remote_host_save", host.Message{"host": draft})
	data := opData(t, result)
	saved, ok := data["host"].(Host)
	if !ok {
		t.Fatalf("host = %T", data["host"])
	}
	return saved.ID
}

func TestRemoteHostsAndWorkspaceOps(t *testing.T) {
	env := testEnv(t)
	fresh := runOp(t, env, "remote_hosts", host.Message{})
	if fresh["ok"] != true {
		t.Fatalf("fresh hosts: %v", fresh)
	}
	data := opData(t, fresh)
	key, _ := data["publicKey"].(*string)
	if data["sshInstalled"] != false || key != nil {
		t.Fatalf("fresh hosts = %v", data)
	}
	if len(data["hosts"].([]Host)) != 0 || len(data["workspaces"].([]host.Message)) != 0 {
		t.Fatalf("fresh hosts = %v", data)
	}

	draft := host.Message{"name": "devbox", "auth": "key", "address": "me@devbox.lan", "port": float64(22)}
	id := saveTestHost(t, env, draft)
	workspace := opData(t, runOp(t, env, "remote_workspace", host.Message{"hostId": id, "path": "/home/me/src/api"}))
	cwd, ok := workspace["cwd"].(string)
	if !ok {
		t.Fatalf("workspace = %v", workspace)
	}
	if filepath.Dir(filepath.Dir(cwd)) != filepath.Join(filepath.Dir(env.ConfigDir), "remotes") {
		t.Fatalf("stand-in outside remotes: %s", cwd)
	}
	if result := runOp(t, env, "remote_host_remove", host.Message{"id": id}); result["ok"] != true {
		t.Fatalf("remove: %v", result)
	}
	data = opData(t, runOp(t, env, "remote_hosts", host.Message{}))
	if len(data["hosts"].([]Host)) != 0 {
		t.Fatalf("host still saved: %v", data["hosts"])
	}
	workspaces := data["workspaces"].([]host.Message)
	if len(workspaces) != 1 || workspaces[0]["cwd"] != cwd || workspaces[0]["path"] != "/home/me/src/api" {
		t.Fatalf("workspaces = %v", workspaces)
	}
	if result := runOp(t, env, "remote_workspace", host.Message{"hostId": id, "path": "/x"}); opCode(result) != "remote_host_unknown" {
		t.Fatalf("removed host: %v", result)
	}
	if result := runOp(t, env, "remote_key", host.Message{}); opCode(result) != "ssh_missing" {
		t.Fatalf("key without ssh: %v", result)
	}

	invalid := runOp(t, env, "remote_host_save", host.Message{"host": host.Message{"name": "a", "auth": "key", "address": "me@", "port": float64(22)}})
	if opCode(invalid) != "remote_host_invalid" {
		t.Fatalf("invalid host: %v", invalid)
	}
	// The i18n is nested in the failure's body, not in data.
	i18n, _ := invalid["i18n"].(*host.Text)
	if i18n == nil || i18n.Key != "remote_address_invalid" || i18n.Args["address"] != "me@" {
		t.Fatalf("invalid host i18n = %+v", i18n)
	}
}

func TestRemoteHostSaveHome(t *testing.T) {
	env := testEnv(t)
	id := saveTestHost(t, env, host.Message{"name": "a", "auth": "key", "address": "me@a", "port": float64(22)})
	paths := remoteLayoutFor(env)
	hosts, err := loadHosts(paths)
	if err != nil {
		t.Fatal(err)
	}
	home := "/home/me"
	hosts[0].Home = &home
	// Re-save through updateHosts so the on-disk host keeps its home.
	if _, err := updateHosts(paths, func([]Host) ([]Host, any, error) { return hosts, nil, nil }); err != nil {
		t.Fatal(err)
	}
	result := runOp(t, env, "remote_host_save", host.Message{"host": host.Message{"id": id, "name": "b", "auth": "key", "address": "me@a", "port": float64(22)}})
	saved := opData(t, result)["host"].(Host)
	if saved.ID != id || saved.Name != "b" || saved.Home == nil || *saved.Home != home {
		t.Fatalf("renamed host = %+v", saved)
	}
	result = runOp(t, env, "remote_host_save", host.Message{"host": host.Message{"id": id, "name": "b", "auth": "key", "address": "me@a", "port": float64(2222)}})
	moved := opData(t, result)["host"].(Host)
	if moved.Port == nil || *moved.Port != 2222 || moved.Home != nil {
		t.Fatalf("moved host = %+v", moved)
	}
}

func TestRemoteKeyOps(t *testing.T) {
	env := testEnv(t)
	writeTool(t, env, "ssh", "#!/bin/sh\ncase \" $* \" in *\" -G \"*) exit 0 ;; esac\nexit 0\n")
	writeTool(t, env, "ssh-keygen", `#!/bin/sh
y=0
comment=""
file=""
while [ $# -gt 0 ]; do
  case "$1" in
    -y) y=1; shift ;;
    -C) comment="$2"; shift 2 ;;
    -f) file="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ "$y" = 1 ]; then cat "$file.pub" 2>/dev/null || exit 1; exit 0; fi
printf 'PRIVATE KEY\n' > "$file"
key=$(date +%s%N)
printf 'ssh-ed25519 AAAA%s %s\n' "$key" "$comment" > "$file.pub"
exit 0
`)
	first := opData(t, runOp(t, env, "remote_key", host.Message{}))["publicKey"].(string)
	if !strings.HasPrefix(first, "ssh-ed25519 AAAA") || !strings.Contains(first, " pigkin@") {
		t.Fatalf("public key = %q", first)
	}
	if again := opData(t, runOp(t, env, "remote_key", host.Message{}))["publicKey"].(string); again != first {
		t.Fatalf("key changed: %q -> %q", first, again)
	}
	info, err := os.Stat(remoteLayoutFor(env).keyFile())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v (%v)", info, err)
	}
	data := opData(t, runOp(t, env, "remote_hosts", host.Message{}))
	listedKey, _ := data["publicKey"].(*string)
	if data["sshInstalled"] != true || listedKey == nil || *listedKey != first {
		t.Fatalf("hosts after key = %v", data)
	}
	replaced := opData(t, runOp(t, env, "remote_key_new", host.Message{}))["publicKey"].(string)
	if replaced == first {
		t.Fatal("the key was not replaced")
	}
}

func TestRemoteStatusAndDisconnectOps(t *testing.T) {
	env := testEnv(t)
	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -O check "*) exit 0 ;;
  *" -O exit "*) exit 0 ;;
  *" -G "*) exit 0 ;;
esac
exit 1
`)
	id := saveTestHost(t, env, host.Message{"name": "box", "auth": "key", "address": "me@box", "port": float64(22)})
	data := opData(t, runOp(t, env, "remote_status", host.Message{"ids": []any{id, "ffffffffffff"}}))
	connected, ok := data["connected"].(map[string]bool)
	if !ok || connected[id] != true || connected["ffffffffffff"] != false {
		t.Fatalf("status = %v", data["connected"])
	}
	if result := runOp(t, env, "remote_disconnect", host.Message{"id": id}); result["ok"] != true {
		t.Fatalf("disconnect: %v", result)
	}
	if result := runOp(t, env, "remote_disconnect", host.Message{"id": "ffffffffffff"}); result["ok"] != true {
		t.Fatalf("disconnect of an unknown host: %v", result)
	}
}

func TestRemoteForgetHostKeyOp(t *testing.T) {
	env := testEnv(t)
	paths := remoteLayoutFor(env)
	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -G "*) printf 'hostname 127.0.0.1\nport 2222\nhostkeyalias none\n'; exit 0 ;;
esac
exit 0
`)
	writeTool(t, env, "ssh-keygen", "#!/bin/sh\nexit 0\n")
	id := saveTestHost(t, env, host.Message{"name": "box", "auth": "key", "address": "me@box", "port": float64(2222)})
	_ = os.WriteFile(paths.knownHostsFile()+".old", []byte("old"), 0o600)
	if result := runOp(t, env, "remote_forget_host_key", host.Message{"id": id}); result["ok"] != true {
		t.Fatalf("forget: %v", result)
	}
	if _, err := os.Stat(paths.knownHostsFile() + ".old"); !os.IsNotExist(err) {
		t.Fatalf("the keygen backup stayed: %v", err)
	}
	if result := runOp(t, env, "remote_forget_host_key", host.Message{"id": "ffffffffffff"}); opCode(result) != "remote_host_unknown" {
		t.Fatalf("unknown host: %v", result)
	}
}

func installModeResult(t *testing.T, env host.Env) (host.Message, error, []host.Message) {
	t.Helper()
	client, server := net.Pipe()
	connection := host.NewConnection(server)
	ctx := &Context{Env: env, Conn: connection}
	lines := make(chan host.Message, 32)
	go func() {
		scanner := host.ScanLines(client)
		for scanner.Scan() {
			var message host.Message
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				lines <- message
			}
		}
	}()
	result, err := remoteInstallMode(ctx)
	_ = client.Close()
	progress := []host.Message{}
	for {
		select {
		case line := <-lines:
			progress = append(progress, line)
		case <-time.After(100 * time.Millisecond):
			return result, err, progress
		}
	}
}

func TestRemoteInstallMode(t *testing.T) {
	env := testEnv(t)
	writeTool(t, env, "pkg", `#!/bin/sh
echo "pkg $*"
echo "Setting up openssh (10.2p1) ..."
exit 0
`)
	result, err, progress := installModeResult(t, env)
	if err != nil || result == nil {
		t.Fatalf("install: %v, %v", result, err)
	}
	texts := []string{}
	for _, line := range progress {
		texts = append(texts, line["line"].(string))
	}
	if strings.Join(texts, "|") != "pkg install -y openssh|Setting up openssh (10.2p1) ..." {
		t.Fatalf("progress = %v", texts)
	}

	for _, tc := range []struct{ output, code string }{
		{"E: Could not get lock /var/lib/dpkg/lock-frontend", "remote_install_busy"},
		{"E: Failed to fetch https://packages.termux.dev/openssh.deb  Temporary failure resolving", "remote_install_offline"},
		{"E: Unable to locate package openssh", "install_failed"},
	} {
		writeTool(t, env, "pkg", "#!/bin/sh\necho '"+tc.output+"'\nexit 100\n")
		_, err, _ := installModeResult(t, env)
		failure, ok := err.(*host.Error)
		if !ok || failure.Code != tc.code {
			t.Fatalf("%q: %v", tc.output, err)
		}
	}
}

// connectFixture writes a fake ssh that asks SSH_ASKPASS once for a password,
// then asks again when it was wrong, as OpenSSH does for a password host.
func connectFixture(t *testing.T, env host.Env) {
	t.Helper()
	t.Setenv("PIGKIN_TEST_ASKPASS", "1")
	if err := os.Symlink("/bin/sh", filepath.Join(env.Prefix, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -G "*) echo "hostname 127.0.0.1"; exit 0 ;;
esac
answer=$("$SSH_ASKPASS" "me@box's password: ") || exit 255
if [ "$answer" = right ]; then printf '%s\n' /home/me /usr/bin/bash /usr/bin/git; exit 0; fi
"$SSH_ASKPASS" "(me@box) Password: " >/dev/null
echo "me@box: Permission denied (password,keyboard-interactive)." >&2
exit 255
`)
}

func testConnect(t *testing.T, env host.Env, id string, reply func(remotePrompt) (string, bool)) (*probeResult, error, []remotePrompt) {
	t.Helper()
	paths := remoteLayoutFor(env)
	hosts, err := loadHosts(paths)
	if err != nil {
		t.Fatal(err)
	}
	var target Host
	for _, saved := range hosts {
		if saved.ID == id {
			target = saved
		}
	}
	prompts := []remotePrompt{}
	probe, err := connect(remote{paths: paths, host: target}, connectOptions{
		askpassCommand: []string{os.Args[0]},
		prompt: func(asked remotePrompt) (string, bool) {
			prompts = append(prompts, asked)
			return reply(asked)
		},
	})
	return probe, err, prompts
}

func TestRemoteConnectWithAskpass(t *testing.T) {
	env := testEnv(t)
	connectFixture(t, env)
	id := saveTestHost(t, env, host.Message{"name": "box", "auth": "password", "address": "me@box", "port": float64(22)})

	probe, err, prompts := testConnect(t, env, id, func(remotePrompt) (string, bool) { return "right", true })
	if err != nil {
		t.Fatal(err)
	}
	if probe.Home != "/home/me" || !probe.Tools.Bash || !probe.Tools.Git || probe.Tools.Rg {
		t.Fatalf("probe = %+v", probe)
	}
	if len(prompts) != 1 || prompts[0].Kind != "password" || prompts[0].Text != "me@box's password: " {
		t.Fatalf("prompts = %+v", prompts)
	}
	hosts, err := loadHosts(remoteLayoutFor(env))
	if err != nil || hosts[0].Home == nil || *hosts[0].Home != "/home/me" {
		t.Fatalf("home not saved: %+v (%v)", hosts, err)
	}

	_, err, prompts = testConnect(t, env, id, func(remotePrompt) (string, bool) { return "wrong", true })
	if failure, ok := err.(*host.Error); !ok || failure.Code != "remote_auth_failed" || len(prompts) != 1 {
		t.Fatalf("wrong password: %v, prompts %+v", err, prompts)
	}

	_, err, _ = testConnect(t, env, id, func(remotePrompt) (string, bool) { return "", false })
	if failure, ok := err.(*host.Error); !ok || failure.Code != "remote_connect_cancelled" {
		t.Fatalf("cancel: %v", err)
	}
}

func TestRemoteConnectHostKey(t *testing.T) {
	env := testEnv(t)
	t.Setenv("PIGKIN_TEST_ASKPASS", "1")
	if err := os.Symlink("/bin/sh", filepath.Join(env.Prefix, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -G "*) echo "hostname 127.0.0.1"; exit 0 ;;
esac
answer=$("$SSH_ASKPASS" "ED25519 key fingerprint is SHA256:AAAA.\nAre you sure you want to continue connecting (yes/no)? ") || exit 255
if [ "$answer" != yes ]; then exit 255; fi
printf '%s\n' /home/me /usr/bin/bash
`)
	id := saveTestHost(t, env, host.Message{"name": "box", "auth": "key", "address": "me@box", "port": float64(22)})
	probe, err, prompts := testConnect(t, env, id, func(remotePrompt) (string, bool) { return "not-yes", true })
	if err != nil || probe == nil {
		t.Fatalf("host key connect: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Kind != "host_key" {
		t.Fatalf("prompts = %+v", prompts)
	}
}

func TestRemoteConnectFailures(t *testing.T) {
	env := testEnv(t)
	t.Setenv("PIGKIN_TEST_ASKPASS", "1")
	if err := os.Symlink("/bin/sh", filepath.Join(env.Prefix, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	id := saveTestHost(t, env, host.Message{"name": "box", "auth": "key", "address": "me@box", "port": float64(22)})

	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -G "*) echo "hostname 127.0.0.1"; exit 0 ;;
esac
echo "No ED25519 host key is known for box and you have requested strict checking." >&2
echo "Host key verification failed." >&2
exit 255
`)
	_, err, prompts := testConnect(t, env, id, func(remotePrompt) (string, bool) { return "yes", true })
	if failure, ok := err.(*host.Error); !ok || failure.Code != "remote_host_key_unknown" || len(prompts) != 0 {
		t.Fatalf("unknown host key: %v, prompts %+v", err, prompts)
	}

	writeTool(t, env, "ssh", `#!/bin/sh
case " $* " in
  *" -G "*) echo "hostname 127.0.0.1"; exit 0 ;;
esac
printf '%s\n' /home/me
`)
	_, err, _ = testConnect(t, env, id, func(remotePrompt) (string, bool) { return "yes", true })
	if failure, ok := err.(*host.Error); !ok || failure.Code != "remote_bash_missing" {
		t.Fatalf("no bash: %v", err)
	}
}

func TestAskpassBridge(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "askpass.sock")
	bridge, err := startAskpassBridge(socket, func(prompt string, confirm bool) (string, bool) {
		if confirm {
			return "", false
		}
		return "answer:" + prompt, true
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.close()
	t.Setenv(askpassSocketVariable, socket)

	output := captureStdout(t, func() {
		if code := Askpass("hello"); code != 0 {
			t.Fatalf("askpass = %d", code)
		}
	})
	if output != "answer:hello\n" {
		t.Fatalf("answer = %q", output)
	}
	t.Setenv("SSH_ASKPASS_PROMPT", "confirm")
	if code := Askpass("hello"); code != 1 {
		t.Fatalf("cancelled askpass = %d", code)
	}
	if code := Askpass("hello"); code != 1 {
		t.Fatalf("second cancelled askpass = %d", code)
	}
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = original }()
	run()
	writer.Close()
	data := make([]byte, 4096)
	count, _ := reader.Read(data)
	reader.Close()
	return string(data[:count])
}
