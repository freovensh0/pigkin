package modes

import (
	"path/filepath"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// remoteLayout is Pigkin's SSH state and the OpenSSH it runs in Termux. It is
// built from the host's environment so release and debug builds keep separate
// state under their own Pi config folder.
type remoteLayout struct {
	ssh     string
	remotes string
	bin     string
	home    string
}

func remoteLayoutFor(env host.Env) remoteLayout {
	state := filepath.Dir(env.ConfigDir)
	return remoteLayout{
		ssh:     filepath.Join(state, "ssh"),
		remotes: filepath.Join(state, "remotes"),
		bin:     filepath.Join(env.Prefix, "bin"),
		home:    env.Home,
	}
}

func (p remoteLayout) hostsFile() string      { return filepath.Join(p.ssh, "hosts.json") }
func (p remoteLayout) configFile() string     { return filepath.Join(p.ssh, "config") }
func (p remoteLayout) keyFile() string        { return filepath.Join(p.ssh, "id_ed25519") }
func (p remoteLayout) knownHostsFile() string { return filepath.Join(p.ssh, "known_hosts") }
func (p remoteLayout) socketsDir() string     { return filepath.Join(p.ssh, "s") }
func (p remoteLayout) askpassFile() string    { return filepath.Join(p.ssh, "askpass") }

func (p remoteLayout) controlPath(hostID string) string {
	return filepath.Join(p.socketsDir(), hostID)
}

func (p remoteLayout) sshBinary() string    { return filepath.Join(p.bin, "ssh") }
func (p remoteLayout) shBinary() string     { return filepath.Join(p.bin, "sh") }
func (p remoteLayout) keygenBinary() string { return filepath.Join(p.bin, "ssh-keygen") }
