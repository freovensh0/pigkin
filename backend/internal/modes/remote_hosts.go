package modes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

type hostAuth string

const (
	hostAuthKey      hostAuth = "key"
	hostAuthPassword hostAuth = "password"
	hostAuthConfig   hostAuth = "config"
)

// Host is a host the user added, as hosts.json and the app hold it.
type Host struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Auth    hostAuth `json:"auth"`
	Address string   `json:"address"`
	Port    *int     `json:"port"`
	Home    *string  `json:"home"`
}

// HostDraft is the host the app asked to save, without an id for a new one.
type HostDraft struct {
	ID      *string
	Name    string
	Auth    hostAuth
	Address string
	Port    *int
}

var (
	hostIDPattern   = regexp.MustCompile(`^[0-9a-f]{12}$`)
	controlPattern  = regexp.MustCompile(`\p{Cc}`)
	hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	ipv6Pattern     = regexp.MustCompile(`^\[([0-9A-Fa-f.]*:[0-9A-Fa-f:.]*(?:%[A-Za-z0-9_.-]+)?)\]$`)
	userPattern     = regexp.MustCompile(`^[^\s"'#\\@-][^\s"'#]*$`)
	aliasPattern    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// parseAddress splits a key or password host's address at its last @, taking
// an IPv6 address out of its brackets.
func parseAddress(address string) (user *string, name string, ok bool) {
	at := strings.LastIndex(address, "@")
	var rawUser string
	if at < 0 {
		rawUser = ""
	} else {
		rawUser = address[:at]
	}
	name = address[at+1:]
	if at >= 0 && !userPattern.MatchString(rawUser) {
		return nil, "", false
	}
	if match := ipv6Pattern.FindStringSubmatch(name); match != nil {
		if at >= 0 {
			user = &rawUser
		}
		return user, match[1], true
	}
	if !hostNamePattern.MatchString(name) {
		return nil, "", false
	}
	if at >= 0 {
		user = &rawUser
	}
	return user, name, true
}

func hostInvalid(field, key, message string, args map[string]any) *host.Error {
	values := host.Message{"field": field}
	for name, value := range args {
		values[name] = value
	}
	return host.NewError("remote_host_invalid", message, &host.Text{Key: key, Args: values})
}

func asObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// validateHost checks the host the app sent, naming the field at fault in the
// i18n args when one is wrong. Names compare ignoring case, as the sidebar
// tells hosts apart by name.
func validateHost(value any, hosts []Host) (HostDraft, error) {
	object, ok := asObject(value)
	if !ok {
		return HostDraft{}, host.NewError("bad_params", "Missing host", nil)
	}
	var id *string
	if raw, present := object["id"]; present && raw != nil {
		text, ok := raw.(string)
		if !ok || !hostIDPattern.MatchString(text) {
			return HostDraft{}, host.NewError("bad_params", "Bad host id", nil)
		}
		id = &text
	}
	authName, ok := object["auth"].(string)
	if !ok {
		return HostDraft{}, host.NewError("bad_params", "Bad host sign-in method", nil)
	}
	var auth hostAuth
	switch hostAuth(authName) {
	case hostAuthKey, hostAuthPassword, hostAuthConfig:
		auth = hostAuth(authName)
	default:
		return HostDraft{}, host.NewError("bad_params", "Bad host sign-in method", nil)
	}

	name, _ := object["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return HostDraft{}, hostInvalid("name", "remote_name_empty", "Enter a name.", nil)
	}
	if controlPattern.MatchString(name) {
		return HostDraft{}, hostInvalid("name", "remote_name_invalid", "The name can't contain control characters.", nil)
	}
	folded := strings.ToLower(name)
	for _, saved := range hosts {
		if (id == nil || saved.ID != *id) && strings.ToLower(saved.Name) == folded {
			return HostDraft{}, hostInvalid("name", "remote_name_taken", "Another host is named "+name+".",
				map[string]any{"name": name})
		}
	}

	address, _ := object["address"].(string)
	address = strings.TrimSpace(address)
	if address == "" {
		return HostDraft{}, hostInvalid("address", "remote_address_empty", "Enter an address.", nil)
	}
	if auth == hostAuthConfig {
		if !aliasPattern.MatchString(address) {
			return HostDraft{}, hostInvalid("address", "remote_alias_invalid", address+" isn't a Host alias.",
				map[string]any{"alias": address})
		}
		return HostDraft{ID: id, Name: name, Auth: auth, Address: address}, nil
	}
	if _, _, ok := parseAddress(address); !ok {
		return HostDraft{}, hostInvalid("address", "remote_address_invalid", address+" isn't an address like user@host.",
			map[string]any{"address": address})
	}
	portNumber, present := object["port"]
	port, ok := atoi(portNumber)
	if !present || !ok || port < 1 || port > 65535 {
		return HostDraft{}, hostInvalid("port", "remote_port_invalid", "The port must be a number from 1 to 65535.",
			map[string]any{"port": percent(portNumber)})
	}
	return HostDraft{ID: id, Name: name, Auth: auth, Address: address, Port: &port}, nil
}

// parseHost drops a hand-broken entry instead of dropping every host.
func parseHost(value any) (Host, bool) {
	object, ok := asObject(value)
	if !ok {
		return Host{}, false
	}
	id, idOK := object["id"].(string)
	name, nameOK := object["name"].(string)
	address, addressOK := object["address"].(string)
	authName, authOK := object["auth"].(string)
	if !idOK || !hostIDPattern.MatchString(id) || !nameOK || !addressOK || !authOK {
		return Host{}, false
	}
	var auth hostAuth
	switch hostAuth(authName) {
	case hostAuthKey, hostAuthPassword, hostAuthConfig:
		auth = hostAuth(authName)
	default:
		return Host{}, false
	}
	var home *string
	if text, ok := object["home"].(string); ok {
		home = &text
	}
	if auth == hostAuthConfig {
		if !aliasPattern.MatchString(address) {
			return Host{}, false
		}
		return Host{ID: id, Name: name, Auth: auth, Address: address, Home: home}, true
	}
	if _, _, ok := parseAddress(address); !ok {
		return Host{}, false
	}
	port, ok := atoi(object["port"])
	if !ok || port < 1 || port > 65535 {
		return Host{}, false
	}
	return Host{ID: id, Name: name, Auth: auth, Address: address, Port: &port, Home: home}, true
}

// loadHosts reads the saved hosts; none before the first is added.
func loadHosts(paths remoteLayout) ([]Host, error) {
	data, err := os.ReadFile(paths.hostsFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Host{}, nil
		}
		return nil, err
	}
	var parsed struct {
		Hosts []any `json:"hosts"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return []Host{}, nil
	}
	hosts := make([]Host, 0, len(parsed.Hosts))
	for _, value := range parsed.Hosts {
		if host, ok := parseHost(value); ok {
			hosts = append(hosts, host)
		}
	}
	return hosts, nil
}

func findHost(paths remoteLayout, id string) (Host, error) {
	hosts, err := loadHosts(paths)
	if err != nil {
		return Host{}, err
	}
	for _, host := range hosts {
		if host.ID == id {
			return host, nil
		}
	}
	return Host{}, hostUnknown()
}

func newHostId() (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// destination is the name ssh connects to: a key or password host's own Pigkin
// block, or the user's config alias.
func destination(host Host) string {
	if host.Auth == hostAuthConfig {
		return host.Address
	}
	return "pigkin-" + host.ID
}

func configWord(value string) string {
	if strings.IndexFunc(value, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0 {
		return `"` + value + `"`
	}
	return value
}

// sshConfig is the OpenSSH config generated from the saved hosts. Quiet weak
// crypto keeps OpenSSH 10.1's post-quantum warning out of tool output.
func sshConfig(paths remoteLayout, hosts []Host, quietWeakCrypto bool) string {
	lines := []string{
		"# Written by Pigkin from hosts.json; changes here are overwritten.",
		"Host *",
		"  ServerAliveInterval 15",
		"  ServerAliveCountMax 2",
		"  ConnectTimeout 15",
		"  UserKnownHostsFile " + configWord(paths.knownHostsFile()) + " " + configWord(filepath.Join(paths.home, ".ssh", "known_hosts")),
		"  HashKnownHosts no",
	}
	if quietWeakCrypto {
		lines = append(lines, "  WarnWeakCrypto no")
	}
	for _, host := range hosts {
		if host.Auth == hostAuthConfig {
			continue
		}
		user, name, ok := parseAddress(host.Address)
		if !ok {
			continue
		}
		port := 0
		if host.Port != nil {
			port = *host.Port
		}
		lines = append(lines, "", "Host "+destination(host), "  HostName "+name)
		if user != nil {
			lines = append(lines, "  User "+*user)
		}
		preferred := "publickey"
		if host.Auth == hostAuthPassword {
			preferred = "password,keyboard-interactive"
		}
		lines = append(lines,
			"  Port "+strconv.Itoa(port),
			"  IdentityFile "+configWord(paths.keyFile()),
			"  IdentitiesOnly yes",
			"  PreferredAuthentications "+preferred,
		)
	}
	lines = append(lines, "", "Match all", "  Include "+configWord(filepath.Join(paths.home, ".ssh", "config")), "")
	return strings.Join(lines, "\n")
}

// ensureSshDir makes the SSH folder and its sockets folder private to Termux's
// user, as OpenSSH wants.
func ensureSshDir(paths remoteLayout) error {
	if err := os.MkdirAll(paths.socketsDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(paths.ssh, 0o700)
}

// writeAtomically replaces path at once, so a reader never sees half a file.
func writeAtomically(path, text string, mode os.FileMode) error {
	temporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(temporary, []byte(text), mode); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

var (
	warnWeakMu    sync.Mutex
	warnWeakKnown bool
	warnWeakOK    bool
)

// supportsWarnWeakCrypto probes ssh once for WarnWeakCrypto: an older OpenSSH
// would refuse a whole config naming an option it doesn't know.
func supportsWarnWeakCrypto(paths remoteLayout) bool {
	warnWeakMu.Lock()
	defer warnWeakMu.Unlock()
	if warnWeakKnown {
		return warnWeakOK
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, paths.sshBinary(), "-G", "-F", "/dev/null", "-o", "WarnWeakCrypto=no", "pigkin-probe").Run()
	warnWeakOK = err == nil
	warnWeakKnown = true
	return warnWeakOK
}

func writeSshConfig(paths remoteLayout, hosts []Host) error {
	if err := ensureSshDir(paths); err != nil {
		return err
	}
	return writeAtomically(paths.configFile(), sshConfig(paths, hosts, supportsWarnWeakCrypto(paths)), 0o600)
}

func refreshSshConfig(paths remoteLayout) error {
	hosts, err := loadHosts(paths)
	if err != nil || len(hosts) == 0 {
		return err
	}
	return writeSshConfig(paths, hosts)
}

// hostUpdateMu serialises updates in one process so two saves can't drop each
// other's host.
var hostUpdateMu sync.Mutex

// updateHosts applies change to the saved hosts and writes hosts.json and the
// generated ssh config as one step.
func updateHosts(paths remoteLayout, change func([]Host) ([]Host, any, error)) (any, error) {
	hostUpdateMu.Lock()
	defer hostUpdateMu.Unlock()
	hosts, err := loadHosts(paths)
	if err != nil {
		return nil, err
	}
	next, result, err := change(hosts)
	if err != nil {
		return nil, err
	}
	if err := ensureSshDir(paths); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(struct {
		Version int    `json:"version"`
		Hosts   []Host `json:"hosts"`
	}{Version: 1, Hosts: next}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomically(paths.hostsFile(), string(data)+"\n", 0o600); err != nil {
		return nil, err
	}
	if err := writeAtomically(paths.configFile(), sshConfig(paths, next, supportsWarnWeakCrypto(paths)), 0o600); err != nil {
		return nil, err
	}
	return result, nil
}

// recordHome saves a host's remote home, which labels its folders ~/....
func recordHome(paths remoteLayout, id, home string) error {
	hosts, err := loadHosts(paths)
	if err != nil {
		return err
	}
	for _, saved := range hosts {
		if saved.ID == id {
			if saved.Home != nil && *saved.Home == home {
				return nil
			}
			_, err := updateHosts(paths, func(current []Host) ([]Host, any, error) {
				for index := range current {
					if current[index].ID == id {
						current[index].Home = &home
					}
				}
				return current, nil, nil
			})
			return err
		}
	}
	return nil
}
