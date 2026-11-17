// Package host holds the wire protocol between Pigkin's app and the host that
// runs in Termux: JSONL framing, the connect-back connection, and the
// environment the Termux bootstrap exports.
package host

import (
	"os"
	"path/filepath"
	"syscall"
)

// Env is what the Termux bootstrap exports before starting the host.
type Env struct {
	Token string
	// Root is Pigkin's managed state, including the pinned agent runtime.
	Root string
	// ConfigDir is PI_CODING_AGENT_DIR: Pi's config, sessions and packages.
	ConfigDir string
	Home      string
	// Prefix is Termux's $PREFIX, the root of its packages.
	Prefix string
	// SharedStorage is Android's shared storage, /sdcard unless set.
	SharedStorage string
}

// Text is a message the app shows, named by a string resource key it knows.
type Text struct {
	Key  string         `json:"key"`
	Args map[string]any `json:"args,omitempty"`
}

// Error is a failure with a machine-readable code, reported as a
// pigkin_result with ok:false.
type Error struct {
	Code    string
	Message string
	I18n    *Text
}

func (e *Error) Error() string { return e.Message }

func NewError(code, message string, i18n *Text) *Error {
	return &Error{Code: code, Message: message, I18n: i18n}
}

// ReadEnv reads the bootstrap's variables, reporting false when one is
// missing: the host can't run outside Termux.
func ReadEnv() (Env, bool) {
	env := Env{
		Token:         os.Getenv("PIGKIN_TOKEN"),
		Root:          os.Getenv("PIGKIN_ROOT"),
		ConfigDir:     os.Getenv("PI_CODING_AGENT_DIR"),
		Home:          os.Getenv("HOME"),
		Prefix:        os.Getenv("PREFIX"),
		SharedStorage: os.Getenv("EXTERNAL_STORAGE"),
	}
	if env.Token == "" || env.Root == "" || env.ConfigDir == "" || env.Prefix == "" {
		return Env{}, false
	}
	if env.Home == "" {
		env.Home, _ = os.UserHomeDir()
	}
	if env.SharedStorage == "" {
		env.SharedStorage = "/sdcard"
	}
	return env, true
}

// GrantedSharedStorage is shared storage's real folder when Termux can write
// to it, else empty. Android lets an app list the top level without access, so
// this asks for write permission.
func GrantedSharedStorage(env Env) string {
	path, err := filepath.EvalSymlinks(env.SharedStorage)
	if err != nil {
		return ""
	}
	if err := syscall.Access(path, wOK); err != nil {
		return ""
	}
	return path
}

// wOK is the access(2) "writable" bit, which syscall doesn't name.
const wOK = 2
