package modes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

func statLine(pid int, comm string, ppid, session int) string {
	return fmt.Sprintf("%d (%s) S %d %d %d 34816 %d 4194560 0 0 0 0 0 0 0 0 20 0 1 0 1",
		pid, comm, ppid, session, session, session)
}

func TestParseProcStat(t *testing.T) {
	stat, ok := parseProcStat(statLine(200, "bash", 100, 200))
	if !ok || stat != (procStat{pid: 200, comm: "bash", ppid: 100, session: 200}) {
		t.Fatalf("got %v, %v", stat, ok)
	}
	stat, ok = parseProcStat(statLine(300, "my (odd) prog) S 1 1", 200, 200))
	if !ok || stat.comm != "my (odd) prog) S 1 1" || stat.ppid != 200 || stat.session != 200 {
		t.Fatalf("got %v, %v", stat, ok)
	}
	for _, text := range []string{"", "300 bash S 200"} {
		if _, ok := parseProcStat(text); ok {
			t.Fatalf("accepted %q", text)
		}
	}
}

func TestLoginShellComm(t *testing.T) {
	prefix := "/data/data/com.termux/files/usr/bin"
	if got := loginShellComm("HOME=/home\x00SHELL=" + prefix + "/nu\x00TERM=xterm-256color\x00"); got != "nu" {
		t.Fatalf("got %q", got)
	}
	if got := loginShellComm("SHELL=" + prefix + "/a-very-long-shell-name\x00"); got != "a-very-long-she" {
		t.Fatalf("got %q", got)
	}
	for _, environ := range []string{"HOME=/home\x00SHELLS=/etc/shells\x00", ""} {
		if got := loginShellComm(environ); got != "" {
			t.Fatalf("got %q", got)
		}
	}
}

func TestSessionLeader(t *testing.T) {
	script := procStat{pid: 100, comm: "script", ppid: 1, session: 1}
	shell := procStat{pid: 200, comm: "bash", ppid: 100, session: 200}
	if leader, ok := sessionLeader([]procStat{script, shell}, script.pid); !ok || leader != shell {
		t.Fatalf("got %v, %v", leader, ok)
	}
	if _, ok := sessionLeader([]procStat{script}, script.pid); ok {
		t.Fatal("found a leader before the shell started")
	}
}

func TestSessionHasWork(t *testing.T) {
	shell := procStat{pid: 200, comm: "bash", ppid: 100, session: 200}
	foreground := procStat{pid: 300, comm: "vim", ppid: 200, session: 200}
	orphan := procStat{pid: 400, comm: "node", ppid: 1, session: 200}
	detached := procStat{pid: 500, comm: "sleep", ppid: 200, session: 500}
	other := procStat{pid: 600, comm: "bash", ppid: 1, session: 600}
	cases := []struct {
		name      string
		processes []procStat
		leader    procStat
		login     string
		want      bool
	}{
		{"idle prompt", []procStat{shell}, shell, "bash", false},
		{"foreground command", []procStat{shell, foreground}, shell, "bash", true},
		{"orphaned job", []procStat{shell, orphan}, shell, "bash", true},
		{"other sessions", []procStat{shell, detached, other}, shell, "bash", false},
		{"exec in place", []procStat{foreground}, foreground, "bash", true},
		{"exec with no SHELL", []procStat{{pid: 200, comm: "ssh", ppid: 100, session: 200}}, procStat{pid: 200, comm: "ssh", ppid: 100, session: 200}, "", true},
		{"login shell from SHELL", []procStat{{pid: 200, comm: "nu", ppid: 100, session: 200}}, procStat{pid: 200, comm: "nu", ppid: 100, session: 200}, "nu", false},
		{"sh running login", []procStat{{pid: 200, comm: "sh", ppid: 100, session: 200}}, procStat{pid: 200, comm: "sh", ppid: 100, session: 200}, "", false},
		{"login", []procStat{{pid: 200, comm: "login", ppid: 100, session: 200}}, procStat{pid: 200, comm: "login", ppid: 100, session: 200}, "", false},
		{"rc exec'd shell", []procStat{{pid: 200, comm: "zsh", ppid: 100, session: 200}}, procStat{pid: 200, comm: "zsh", ppid: 100, session: 200}, "bash", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionHasWork(test.processes, test.leader, test.login); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestTerminalRefusesBadRequests(t *testing.T) {
	missing := func(params host.Message, code string) {
		t.Helper()
		_, err := terminal(&Context{Params: params})
		var refusal *host.Error
		if !errors.As(err, &refusal) || refusal.Code != code {
			t.Fatalf("%v: got %v, want %s", params, err, code)
		}
	}
	missing(host.Message{}, "bad_params")
	missing(host.Message{"cwd": "x", "rows": 24, "cols": 80}, "cwd_missing")
	folder := t.TempDir()
	missing(host.Message{"cwd": folder, "rows": 0, "cols": 80}, "bad_params")
	missing(host.Message{"cwd": folder, "rows": 24, "cols": "80"}, "bad_params")

	_, err := terminal(&Context{Params: host.Message{"cwd": filepath.Join(folder, "gone"), "rows": 24, "cols": 80}})
	var refusal *host.Error
	if !errors.As(err, &refusal) || refusal.I18n == nil || refusal.I18n.Key != "folder_not_found" {
		t.Fatalf("got %v, want folder_not_found i18n", err)
	}
}

func TestTerminalRefusesRemoteWorkspace(t *testing.T) {
	root := t.TempDir()
	env := host.Env{ConfigDir: filepath.Join(root, "config")}
	remote := "/home/me/project"
	sum := sha256.Sum256([]byte(remote))
	standIn := filepath.Join(filepath.Dir(env.ConfigDir), "remotes", "devbox", hex.EncodeToString(sum[:])[:16])
	if err := os.MkdirAll(standIn, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"hostId":"devbox","path":"` + remote + `"}`
	if err := os.WriteFile(filepath.Join(standIn, "remote.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := terminal(&Context{Env: env, Params: host.Message{"cwd": standIn, "rows": 24, "cols": 80}})
	var refusal *host.Error
	if !errors.As(err, &refusal) || refusal.Code != "bad_params" || refusal.Message != remoteNotPorted {
		t.Fatalf("got %v, want a remote refusal", err)
	}
}

func TestTerminalProtocol(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '/dev/pts/99' > "$PIGKIN_TTY"
printf 'HELLO'
while IFS= read -r line; do
  case "$line" in
    exit) exit 7 ;;
    *) printf 'ECHO:%s\n' "$line" ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(bin, "script"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "stty.log")
	t.Setenv("PIGKIN_STTY_LOG", log)
	if err := os.WriteFile(filepath.Join(bin, "stty"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$PIGKIN_STTY_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	hostSide, appConn := net.Pipe()
	defer appConn.Close()
	conn := host.NewConnection(hostSide)
	lines := make(chan host.Message, 64)
	go func() {
		defer close(lines)
		scanner := host.ScanLines(appConn)
		for scanner.Scan() {
			var message host.Message
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				lines <- message
			}
		}
	}()
	write := func(message host.Message) {
		t.Helper()
		line, err := host.EncodeLine(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := appConn.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	write(host.Message{"type": "pigkin_request", "params": host.Message{"cwd": cwd, "rows": 24, "cols": 80}})
	request, status := conn.First(time.Second)
	if status != host.FirstMessage {
		t.Fatalf("request: %v", status)
	}
	done := make(chan error, 1)
	go func() {
		_, err := terminal(&Context{
			Env:    host.Env{Root: root, Prefix: root, ConfigDir: filepath.Join(root, "config")},
			Params: request["params"].(map[string]any),
			Conn:   conn,
		})
		done <- err
	}()

	readUntil := func(want string) host.Message {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case message, open := <-lines:
				if !open {
					t.Fatal("host closed before " + want)
				}
				if message["type"] == "output" {
					data, _ := base64.StdEncoding.DecodeString(message["data"].(string))
					if strings.Contains(string(data), want) {
						return message
					}
				}
				if message["type"] == "pigkin_exit" && want != "exit" {
					t.Fatalf("exited early: %v", message)
				}
			case <-deadline:
				t.Fatal("timed out waiting for " + want)
			}
		}
	}
	readUntil("HELLO")
	write(host.Message{"type": "input", "data": base64.StdEncoding.EncodeToString([]byte("ping\n"))})
	readUntil("ECHO:ping")
	write(host.Message{"type": "resize", "rows": 40, "cols": 120})

	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), "-F /dev/pts/99 rows 40 cols 120") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resize never reached stty: %q", data)
		}
		time.Sleep(10 * time.Millisecond)
	}

	write(host.Message{"type": "input", "data": base64.StdEncoding.EncodeToString([]byte("exit\n"))})
	select {
	case message := <-lines:
		if message["type"] != "pigkin_exit" || message["code"] != float64(7) {
			t.Fatalf("got %v", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no exit line")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, fmt.Sprintf("terminal-tty-%d", os.Getpid()))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("device file left behind: %v", err)
	}
}
