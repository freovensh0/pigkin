package modes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sync"
	"syscall"

	"github.com/freovensh0/pigkin/backend/internal/host"
)

const (
	channelFD      = 3
	channelType    = "pigkin_channel"
	helloTopic     = "pigkin"
	maxChannelLine = 1024 * 1024
	maxSocketLine  = host.MaxLine - 1024
)

var channelTopicRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type channelMessage struct {
	Topic string
	Data  any
}

func lineType(line []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	kind, _ := obj["type"].(string)
	return kind
}

func parseChannelLine(line []byte, wantType string) (channelMessage, bool) {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil || obj == nil {
		return channelMessage{}, false
	}
	if kind, _ := obj["type"].(string); kind != wantType {
		return channelMessage{}, false
	}
	topic, ok := obj["topic"].(string)
	if !ok || !channelTopicRE.MatchString(topic) {
		return channelMessage{}, false
	}
	return channelMessage{Topic: topic, Data: obj["data"]}, true
}

func encodeChannelMessage(message channelMessage) ([]byte, error) {
	return json.Marshal(struct {
		Topic string `json:"topic"`
		Data  any    `json:"data"`
	}{Topic: message.Topic, Data: message.Data})
}

var errChannelLineTooLong = errors.New("channel line too long")

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		piece, err := reader.ReadSlice('\n')
		if !tooLong {
			line = append(line, piece...)
			if len(line) > limit {
				line = nil
				tooLong = true
			}
		}
		if err == nil || !errors.Is(err, bufio.ErrBufferFull) {
			if tooLong {
				return nil, errChannelLineTooLong
			}
			if err != nil {
				return line, err
			}
			return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r")), nil
		}
	}
}

func readChannel(socket io.Reader, onMessage func(channelMessage), onDropped func(string)) {
	reader := bufio.NewReader(socket)
	for {
		line, err := readBoundedLine(reader, maxSocketLine)
		if errors.Is(err, errChannelLineTooLong) {
			onDropped(fmt.Sprintf("line exceeds %d bytes", maxSocketLine))
			continue
		}
		if len(line) > 0 {
			if message, ok := parseChannelLine(line, ""); ok {
				onMessage(message)
			} else {
				onDropped("not a channel message")
			}
		}
		if err != nil {
			if err != io.EOF {
				onDropped(err.Error())
			}
			return
		}
	}
}

type outputMerger struct {
	mu      sync.Mutex
	pending []channelMessage
	write   func([]byte)
}

func (m *outputMerger) channel(message channelMessage) {
	m.mu.Lock()
	m.pending = append(m.pending, message)
	m.mu.Unlock()
	m.flush()
}

func (m *outputMerger) flush() {
	m.mu.Lock()
	pending := m.pending
	m.pending = nil
	m.mu.Unlock()
	for _, message := range pending {
		line, err := host.EncodeLine(host.Message{
			"type": channelType, "topic": message.Topic, "data": message.Data,
		})
		if err == nil {
			m.write(line)
		}
	}
}

type channelSocket struct {
	conn net.Conn
	mu   sync.Mutex
}

func (s *channelSocket) write(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(line) > 0 {
		n, err := s.conn.Write(line)
		if err != nil {
			return err
		}
		line = line[n:]
	}
	return nil
}

func (s *channelSocket) send(message channelMessage) error {
	line, err := encodeChannelMessage(message)
	if err != nil {
		return err
	}
	return s.write(append(line, '\n'))
}

func newChannelPair() (net.Conn, *os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "pigkin-channel-parent")
	child := os.NewFile(uintptr(fds[1]), "pigkin-channel-child")
	conn, err := net.FileConn(parent)
	if err != nil {
		_ = parent.Close()
		_ = child.Close()
		return nil, nil, err
	}
	_ = parent.Close()
	return conn, child, nil
}
