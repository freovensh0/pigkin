package modes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// The test binary doubles as ssh's askpass program for the connect tests.
func TestMain(m *testing.M) {
	if os.Getenv("PIGKIN_TEST_ASKPASS") == "1" {
		os.Exit(Askpass(os.Args[len(os.Args)-1]))
	}
	os.Exit(m.Run())
}

func testHost(fields Host) Host {
	result := Host{ID: "0123456789ab", Name: "box", Auth: hostAuthKey, Address: "me@box", Port: intPtr(22)}
	if fields.ID != "" {
		result.ID = fields.ID
	}
	if fields.Name != "" {
		result.Name = fields.Name
	}
	if fields.Auth != "" {
		result.Auth = fields.Auth
	}
	if fields.Address != "" {
		result.Address = fields.Address
	}
	if fields.Port != nil {
		result.Port = fields.Port
	}
	return result
}

func intPtr(value int) *int { return &value }

// invalidHost is the i18n of the remote_host_invalid that draft fails with.
func invalidHost(t *testing.T, draft any, hosts ...Host) host.Text {
	t.Helper()
	_, err := validateHost(draft, hosts)
	failure, ok := err.(*host.Error)
	if !ok || failure.Code != "remote_host_invalid" || failure.I18n == nil {
		t.Fatalf("validateHost(%v) = %v", draft, err)
	}
	return *failure.I18n
}

func TestValidateHost(t *testing.T) {
	draft := func(address any) host.Message {
		return host.Message{"name": "devbox", "auth": "key", "address": address, "port": float64(22)}
	}
	for _, address := range []string{"", " ", "me@", "@host", "host:22", "me@host:22", "my host", "-oProxyCommand=x", "::1", "me@[host]"} {
		i18n := invalidHost(t, draft(address))
		if i18n.Args["field"] != "address" {
			t.Fatalf("%q: %+v", address, i18n)
		}
	}
	for _, address := range []string{"host", "me@host", "me@my-host.lan", "[::1]", "me@[fe80::1%eth0]", "10.0.2.2", "me@domain@host"} {
		parsed, err := validateHost(draft(address), nil)
		if err != nil || parsed.Address != address {
			t.Fatalf("%q: %v, %+v", address, err, parsed)
		}
	}

	user, name, ok := parseAddress("me@[::1]")
	if !ok || user == nil || *user != "me" || name != "::1" {
		t.Fatalf("[::1] split = %v %q %v", user, name, ok)
	}
	user, name, ok = parseAddress("me@domain@host")
	if !ok || user == nil || *user != "me@domain" || name != "host" {
		t.Fatalf("two @ split = %v %q %v", user, name, ok)
	}
	user, name, ok = parseAddress("host")
	if !ok || user != nil || name != "host" {
		t.Fatalf("no user split = %v %q %v", user, name, ok)
	}

	for _, port := range []any{float64(0), float64(65536), "abc", 22.5, nil} {
		i18n := invalidHost(t, host.Message{"name": "devbox", "auth": "key", "address": "me@host", "port": port})
		if i18n.Key != "remote_port_invalid" || i18n.Args["field"] != "port" {
			t.Fatalf("port %v: %+v", port, i18n)
		}
	}
	saved := []Host{testHost(Host{ID: "aaaaaaaaaaaa", Name: "DevBox"})}
	i18n := invalidHost(t, draft("me@devbox.lan"), saved...)
	if i18n.Key != "remote_name_taken" || i18n.Args["name"] != "devbox" {
		t.Fatalf("taken name: %+v", i18n)
	}
	if parsed, err := validateHost(host.Message{"id": "aaaaaaaaaaaa", "name": "devbox", "auth": "key", "address": "me@devbox.lan", "port": float64(22)}, saved); err != nil || parsed.Name != "devbox" {
		t.Fatalf("own name: %v, %+v", err, parsed)
	}
	if i18n := invalidHost(t, host.Message{"name": "  ", "auth": "key", "address": "me@host", "port": float64(22)}); i18n.Key != "remote_name_empty" {
		t.Fatalf("empty name: %+v", i18n)
	}

	config := host.Message{"name": "b", "auth": "config", "address": "box", "port": 22}
	parsed, err := validateHost(config, nil)
	if err != nil || parsed.ID != nil || parsed.Port != nil || parsed.Address != "box" {
		t.Fatalf("config host: %v, %+v", err, parsed)
	}
	for _, alias := range []string{"*.lan", "-F/x"} {
		config["address"] = alias
		if i18n := invalidHost(t, config); i18n.Key != "remote_alias_invalid" {
			t.Fatalf("alias %q: %+v", alias, i18n)
		}
	}
}

func TestSSHConfig(t *testing.T) {
	paths := remoteLayoutFor(host.Env{ConfigDir: "/data/home/.pigkin/pi", Prefix: "/data/usr", Home: "/data/home"})
	hosts := []Host{
		testHost(Host{ID: "aaaaaaaaaaaa", Address: "me@devbox.lan", Port: intPtr(2222)}),
		testHost(Host{ID: "bbbbbbbbbbbb", Name: "pw", Auth: hostAuthPassword, Address: "[::1]", Port: intPtr(22)}),
		testHost(Host{ID: "cccccccccccc", Name: "cfg", Auth: hostAuthConfig, Address: "box", Port: nil}),
	}
	want := `# Written by Pigkin from hosts.json; changes here are overwritten.
Host *
  ServerAliveInterval 15
  ServerAliveCountMax 2
  ConnectTimeout 15
  UserKnownHostsFile /data/home/.pigkin/ssh/known_hosts /data/home/.ssh/known_hosts
  HashKnownHosts no
  WarnWeakCrypto no

Host pigkin-aaaaaaaaaaaa
  HostName devbox.lan
  User me
  Port 2222
  IdentityFile /data/home/.pigkin/ssh/id_ed25519
  IdentitiesOnly yes
  PreferredAuthentications publickey

Host pigkin-bbbbbbbbbbbb
  HostName ::1
  Port 22
  IdentityFile /data/home/.pigkin/ssh/id_ed25519
  IdentitiesOnly yes
  PreferredAuthentications password,keyboard-interactive

Match all
  Include /data/home/.ssh/config
`
	if got := sshConfig(paths, hosts, true); got != want {
		t.Fatalf("sshConfig:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(sshConfig(paths, hosts, false), "WarnWeakCrypto") {
		t.Fatal("an old ssh would refuse WarnWeakCrypto")
	}
	spaced := remoteLayoutFor(host.Env{ConfigDir: "/data/home/.pigkin/pi", Prefix: "/data/usr", Home: "/my home"})
	if !strings.Contains(sshConfig(spaced, nil, false), `Include "/my home/.ssh/config"`) {
		t.Fatal("a home with a space must be quoted")
	}
}

func TestHostsStore(t *testing.T) {
	env := testEnv(t)
	paths := remoteLayoutFor(env)
	var firstErr, secondErr error
	done := make(chan struct{}, 2)
	go func() {
		_, err := updateHosts(paths, func(hosts []Host) ([]Host, any, error) {
			return append(hosts, testHost(Host{ID: "aaaaaaaaaaaa", Name: "a"})), nil, nil
		})
		firstErr = err
		done <- struct{}{}
	}()
	go func() {
		_, err := updateHosts(paths, func(hosts []Host) ([]Host, any, error) {
			return append(hosts, testHost(Host{ID: "bbbbbbbbbbbb", Name: "b"})), nil, nil
		})
		secondErr = err
		done <- struct{}{}
	}()
	<-done
	<-done
	if firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent saves: %v, %v", firstErr, secondErr)
	}
	hosts, err := loadHosts(paths)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, saved := range hosts {
		names = append(names, saved.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("names = %v", names)
	}
	info, err := os.Stat(paths.hostsFile())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("hosts.json mode = %v (%v)", info, err)
	}
	info, err = os.Stat(paths.ssh)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("ssh dir mode = %v (%v)", info, err)
	}
	config, err := os.ReadFile(paths.configFile())
	if err != nil || !strings.Contains(string(config), "Host pigkin-bbbbbbbbbbbb") {
		t.Fatalf("config = %s (%v)", config, err)
	}

	broken := `{"version":1,"hosts":[
		{"id":"aaaaaaaaaaaa","name":"a","auth":"key","address":"me@a","port":22,"home":null},
		{"id":"zz","name":"bad"},
		{"id":"cccccccccccc","name":"c","auth":"key","address":"me@c","port":0,"home":null}
	]}`
	if err := os.WriteFile(paths.hostsFile(), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err = loadHosts(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "a" {
		t.Fatalf("broken entry kept: %+v", hosts)
	}
}

func TestStandIns(t *testing.T) {
	remotes := filepath.Join(t.TempDir(), "remotes")
	want := filepath.Join(remotes, "0123456789ab", pathHash("/home/me/src/api"))
	if got := standInFor(remotes, "0123456789ab", "/home/me/src/api"); got != want {
		t.Fatalf("standInFor = %s, want %s", got, want)
	}
	if standInFor(remotes, "0123456789ab", "/home/me/src/api/") != want || standInFor(remotes, "0123456789ab", "/home/me//src/api") != want {
		t.Fatal("normalisation changed the hash")
	}
	if _, err := normalizeRemotePath("src/api"); err == nil {
		t.Fatal("a relative remote path was accepted")
	}
	cwd, err := createStandIn(remotes, "0123456789ab", "/srv/app")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := createStandIn(remotes, "0123456789ab", "/srv/app"); err != nil || again != cwd {
		t.Fatalf("second stand-in = %s (%v)", again, err)
	}
	saved, err := readStandIn(remotes, cwd)
	if err != nil || saved == nil || saved["hostId"] != "0123456789ab" || saved["path"] != "/srv/app" {
		t.Fatalf("readStandIn = %v (%v)", saved, err)
	}
	if _, err := createStandIn(remotes, "ba9876543210", "/"); err != nil {
		t.Fatal(err)
	}
	workspaces, err := listStandIns(remotes)
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspaces = %v (%v)", workspaces, err)
	}

	local := filepath.Join(filepath.Dir(remotes), "project")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "remote.json"), []byte(`{"version":1,"hostId":"0123456789ab","path":"/srv/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if saved, err := readStandIn(remotes, local); err != nil || saved != nil {
		t.Fatalf("a local project became a stand-in: %v (%v)", saved, err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "remote.json"), []byte(`{"version":1,"hostId":"0123456789ab","path":"/srv/other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if saved, err := readStandIn(remotes, cwd); err != nil || saved != nil {
		t.Fatalf("a mismatched hash became a stand-in: %v (%v)", saved, err)
	}
}

func TestCommandWrapper(t *testing.T) {
	nasty := "it's \"$HOME\" `whoami` $(id) * ~ \\ ü\n\ttab"
	word, err := shellQuote(nasty)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`printf '%%s|' %s "$0"; printf '%%s' "${BASH_VERSION:+bash}"`, word)
	out, err := exec.Command("bash", "-c", remoteScript(script)).Output()
	if err != nil || string(out) != nasty+"|bash|bash" {
		t.Fatalf("wrapper = %q (%v)", out, err)
	}
	if _, err := shellQuote("a\x00b"); err == nil {
		t.Fatal("a NUL was accepted")
	}

	command, err := watched("pwd; echo oops >&2; sleep 0.2; exit 7", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", remoteScript(command))
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	go func() {
		time.Sleep(400 * time.Millisecond)
		writer.Close()
	}()
	cmd.Stdin = reader
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if exit := exitCode(err); exit == nil || *exit != 7 {
		t.Fatalf("watched exit = %v (%v)", exit, err)
	}
	if !strings.HasSuffix(stdout.String(), "\n") || stderr.String() != "oops\n" {
		t.Fatalf("watched streams = %q, %q", stdout.String(), stderr.String())
	}
}

func TestClassifySshFailure(t *testing.T) {
	batch := failureContext{name: "devbox", auth: hostAuthKey}
	pq := "** WARNING: connection is not using a post-quantum key exchange algorithm.\n** This session may be vulnerable.\n"
	cases := []struct{ stderr, code string }{
		{"No ED25519 host key is known for [127.0.0.1]:42222 and you have requested strict checking.\nHost key verification failed.\n", "remote_host_key_unknown"},
		{"Host key for [127.0.0.1]:42222 has changed and you have requested strict checking.\nHost key verification failed.\n", "remote_host_key_changed"},
		{"jeph@127.0.0.1: Permission denied (publickey,password,keyboard-interactive).\n", "remote_auth_failed"},
		{"Received disconnect from 10.0.2.2 port 2222:2: Too many authentication failures\n", "remote_auth_failed"},
		{"ssh: connect to host 127.0.0.1 port 42222: Connection refused\n", "remote_unreachable"},
		{"ssh: Could not resolve hostname box: Name or service not known\n", "remote_unreachable"},
		{"ssh: connect to host 10.0.2.2 port 2222: Connection timed out\n", "remote_unreachable"},
		{"Connection timed out during banner exchange\n", "remote_unreachable"},
		{"kex_exchange_identification: read: Connection reset by peer\n", "remote_unreachable"},
		{pq + "Timeout, server 10.0.2.2 not responding.\n", "remote_unreachable"},
		{"client_loop: send disconnect: Broken pipe\n", "remote_unreachable"},
		{"Bad owner or permissions on /data/home/.ssh/config\n", "remote_failed"},
		{"/data/home/.ssh/config line 3: Bad configuration option: foo\n", "remote_failed"},
	}
	for _, tc := range cases {
		failure := classifySshFailure(tc.stderr, batch)
		if failure == nil || failure.Code != tc.code {
			t.Fatalf("%q: %v", tc.stderr, failure)
		}
	}
	denied := "pigkin@10.0.2.2: Permission denied (password,keyboard-interactive).\n"
	for _, auth := range []hostAuth{hostAuthPassword, hostAuthConfig} {
		if failure := classifySshFailure(denied, failureContext{name: "devbox", auth: auth}); failure == nil || failure.Code != "remote_not_connected" {
			t.Fatalf("%s: %v", auth, failure)
		}
	}
	if failure := classifySshFailure(denied, failureContext{name: "devbox", auth: hostAuthPassword, interactive: true}); failure == nil || failure.Code != "remote_auth_failed" {
		t.Fatalf("interactive: %v", failure)
	}
	failure := classifySshFailure("Warning: Permanently added '[h]:22' (ED25519) to the list of known hosts.\n"+pq+"ssh: connect to host h port 22: No route to host\n", batch)
	if failure == nil || failure.I18n == nil || failure.I18n.Args["detail"] != "ssh: connect to host h port 22: No route to host" {
		t.Fatalf("detail = %v", failure)
	}
	for _, stderr := range []string{"fatal: could not read from remote repository\n", ""} {
		if classifySshFailure(stderr, batch) != nil {
			t.Fatalf("command output was classified: %q", stderr)
		}
	}
	for _, tc := range []struct {
		stderr  string
		missing bool
	}{
		{"sh: 1: exec: bash: not found\n", true},
		{"zsh:1: command not found: bash\n", true},
		{"fish: Unknown command: bash\n", true},
		{"sh: bash: not found\n", true},
		{"bash: line 1: foo: command not found\n", false},
	} {
		code := 127
		if isBashMissing(&code, tc.stderr) != tc.missing {
			t.Fatalf("bashMissing(%q) = %v", tc.stderr, !tc.missing)
		}
		if isBashMissing(nil, tc.stderr) {
			t.Fatal("no exit code means no missing bash")
		}
	}
}

func TestPromptsAndProbe(t *testing.T) {
	line := "ED25519 key fingerprint is SHA256:8zo3KGaAUG/xVChQGSB0LEPUQa0w1gti6htTrkbqQ88."
	text := "The authenticity of host '[10.0.2.2]:2222' can't be established.\n" + line + "\nAre you sure you want to continue connecting (yes/no)? "
	prompt := describePrompt(text)
	if prompt.Kind != "host_key" || prompt.KeyType != "ED25519" || prompt.Fingerprint != "SHA256:8zo3KGaAUG/xVChQGSB0LEPUQa0w1gti6htTrkbqQ88" {
		t.Fatalf("host key prompt = %+v", prompt)
	}
	line = "ED25519 key fingerprint is: SHA256:8zo3KGaAUG/xVChQGSB0LEPUQa0w1gti6htTrkbqQ88"
	prompt = describePrompt("continue connecting\n" + line)
	if prompt.Fingerprint != "SHA256:8zo3KGaAUG/xVChQGSB0LEPUQa0w1gti6htTrkbqQ88" {
		t.Fatalf("OpenSSH 10 prompt = %+v", prompt)
	}
	for _, tc := range []struct{ text, kind string }{
		{"pigkin@10.0.2.2's password: ", "password"},
		{"(pigkin@10.0.2.2) Password: ", "password"},
		{"Enter passphrase for key '/data/home/.ssh/id_ed25519': ", "passphrase"},
		{"(pigkin@10.0.2.2) Verification code: ", "other"},
		{"The host's key is new.\nAre you sure you want to continue connecting (yes/no)? ", "host_key"},
	} {
		if got := describePrompt(tc.text).Kind; got != tc.kind {
			t.Fatalf("%q = %s, want %s", tc.text, got, tc.kind)
		}
	}
	probe := parseProbe("/home/me\n/usr/bin/bash\n/usr/local/bin/rg\n")
	if probe == nil || probe.Home != "/home/me" || !probe.Tools.Bash || probe.Tools.Git || !probe.Tools.Rg {
		t.Fatalf("probe = %+v", probe)
	}
	if parseProbe("Welcome!\n") != nil {
		t.Fatal("a banner became a probe")
	}
}

func TestSessionCap(t *testing.T) {
	held := make([]func(), 0, maxSessionsPerHost)
	for range maxSessionsPerHost {
		release, err := acquireSession("cap-test", context.Background())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, release)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() {
		release, err := acquireSession("cap-test", ctx)
		if err == nil {
			release()
		}
		waiter <- err
	}()
	select {
	case err := <-waiter:
		t.Fatalf("a fifth session was admitted: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-waiter:
		if err != context.Canceled {
			t.Fatalf("waiter = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a cancelled waiter stayed")
	}
	release, err := acquireSession("other-host", context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, release := range held {
		release()
	}
}
