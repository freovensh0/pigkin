package modes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

// askpassSocketVariable names the bridge socket in the environment ssh passes
// to its askpass program.
const askpassSocketVariable = "PIGKIN_ASKPASS_SOCKET"

type remotePrompt struct {
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	KeyType     string `json:"keyType,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

var hostKeyFingerprint = regexp.MustCompile(`(?m)^(\S+) key fingerprint is:? (SHA256:[A-Za-z0-9+/]+)`)

// describePrompt says what kind of question ssh is asking: the host key is
// known by its "continue connecting", so a rewording still gets that dialog.
func describePrompt(text string) remotePrompt {
	if strings.Contains(text, "continue connecting") {
		match := hostKeyFingerprint.FindStringSubmatch(text)
		if len(match) == 3 {
			return remotePrompt{Kind: "host_key", Text: text, KeyType: match[1], Fingerprint: match[2]}
		}
		return remotePrompt{Kind: "host_key", Text: text}
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "passphrase"):
		return remotePrompt{Kind: "passphrase", Text: text}
	case strings.Contains(lower, "password"):
		return remotePrompt{Kind: "password", Text: text}
	default:
		return remotePrompt{Kind: "other", Text: text}
	}
}

type askpassBridge struct {
	listener net.Listener
	path     string
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// startAskpassBridge listens for askpass questions on a Unix socket and
// answers each with answer; ok false cancels the question.
func startAskpassBridge(path string, answer func(prompt string, confirm bool) (string, bool)) (*askpassBridge, error) {
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	bridge := &askpassBridge{listener: listener, path: path, conns: make(map[net.Conn]struct{})}
	go bridge.serve(answer)
	return bridge, nil
}

func (b *askpassBridge) serve(answer func(prompt string, confirm bool) (string, bool)) {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = conn.Close()
			return
		}
		b.conns[conn] = struct{}{}
		b.mu.Unlock()
		go b.answer(conn, answer)
	}
}

func (b *askpassBridge) answer(conn net.Conn, answer func(prompt string, confirm bool) (string, bool)) {
	defer func() {
		_ = conn.Close()
		b.mu.Lock()
		delete(b.conns, conn)
		b.mu.Unlock()
	}()
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	var question struct {
		Prompt  string `json:"prompt"`
		Confirm bool   `json:"confirm"`
	}
	if json.Unmarshal(scanner.Bytes(), &question) != nil || question.Prompt == "" {
		return
	}
	reply, ok := answer(question.Prompt, question.Confirm)
	var answerValue any
	if ok {
		answerValue = reply
	}
	line, err := json.Marshal(host.Message{"answer": answerValue})
	if err != nil {
		return
	}
	_, _ = conn.Write(append(line, '\n'))
}

func (b *askpassBridge) close() {
	b.mu.Lock()
	b.closed = true
	_ = b.listener.Close()
	for conn := range b.conns {
		_ = conn.Close()
	}
	b.mu.Unlock()
	_ = os.Remove(b.path)
}

// Askpass is the host's askpass role: ssh runs it with its question as the
// argument, and it prints the bridge's answer for ssh to use.
func Askpass(prompt string) int {
	path := os.Getenv(askpassSocketVariable)
	if path == "" {
		return 1
	}
	if os.Getenv("SSH_ASKPASS_PROMPT") == "none" {
		return 0
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return 1
	}
	defer conn.Close()
	line, err := json.Marshal(host.Message{"prompt": prompt, "confirm": os.Getenv("SSH_ASKPASS_PROMPT") == "confirm"})
	if err != nil {
		return 1
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return 1
	}
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return 1
	}
	var reply struct {
		Answer *string `json:"answer"`
	}
	if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.Answer == nil {
		return 1
	}
	fmt.Println(*reply.Answer)
	return 0
}
