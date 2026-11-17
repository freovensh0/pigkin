package host

import (
	"encoding/json"
	"net"
	"sync"
	"time"
)

// Message is one JSON object on the wire.
type Message = map[string]any

// FirstStatus says how waiting for the request ended.
type FirstStatus int

const (
	FirstMessage FirstStatus = iota
	FirstTimeout
	FirstClosed
)

// Connection is the connect-back socket to the app. Messages that arrive
// before a listener is set are queued and delivered to it, so a mode can send
// its ready line before it reads the next request.
type Connection struct {
	conn    net.Conn
	mu      sync.Mutex
	writeMu sync.Mutex

	queue    []Message
	listener func(Message)

	// rawMode hands the socket to a byte-oriented mode after its first request.
	raw     chan []byte
	rawMode bool
	pending [][]byte

	done     chan struct{}
	doneOnce sync.Once
}

func NewConnection(conn net.Conn) *Connection {
	c := &Connection{conn: conn, done: make(chan struct{})}
	go c.read()
	return c
}

func (c *Connection) read() {
	defer c.closeDone()
	scanner := ScanLines(c.conn)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		c.mu.Lock()
		if c.rawMode {
			raw := c.raw
			c.mu.Unlock()
			select {
			case raw <- append(line, '\n'):
			case <-c.done:
			}
			continue
		}
		c.mu.Unlock()
		var message Message
		if err := json.Unmarshal(line, &message); err != nil || message == nil {
			continue
		}
		c.deliver(message)
	}
	c.mu.Lock()
	raw := c.raw
	c.mu.Unlock()
	if raw != nil {
		close(raw)
	}
}

// Done is closed once the app hung up or the socket failed.
func (c *Connection) Done() <-chan struct{} { return c.done }

func (c *Connection) closeDone() {
	c.doneOnce.Do(func() { close(c.done) })
}

func (c *Connection) deliver(message Message) {
	c.mu.Lock()
	listener := c.listener
	if listener == nil {
		c.queue = append(c.queue, message)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	listener(message)
}

// Listen delivers queued and future messages to listener.
func (c *Connection) Listen(listener func(Message)) {
	c.mu.Lock()
	c.listener = listener
	pending := c.queue
	c.queue = nil
	c.mu.Unlock()
	for _, message := range pending {
		listener(message)
	}
}

// First waits for the first message, or for timeout or a hang-up.
func (c *Connection) First(timeout time.Duration) (Message, FirstStatus) {
	incoming := make(chan Message, 256)
	wait := func(message Message) {
		select {
		case incoming <- message:
		default:
		}
	}
	c.Listen(wait)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case message := <-incoming:
		c.mu.Lock()
		c.listener = nil
		// Lines that arrived with the request belong to the mode, not to the
		// app's next turn; keep them for a mode that takes the socket raw.
		for {
			select {
			case extra := <-incoming:
				if line, err := EncodeLine(extra); err == nil {
					c.pending = append(c.pending, line)
				}
			default:
				c.mu.Unlock()
				return message, FirstMessage
			}
		}
	case <-timer.C:
		return nil, FirstTimeout
	case <-c.done:
		select {
		case message := <-incoming:
			return message, FirstMessage
		default:
			return nil, FirstClosed
		}
	}
}

// Raw hands the socket to a byte-oriented mode: every later line from the app
// arrives on the returned channel with its newline, and Send still writes
// lines back. Lines read before the switch come first.
func (c *Connection) Raw() <-chan []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw = make(chan []byte, 256)
	c.rawMode = true
	for _, line := range c.pending {
		c.raw <- line
	}
	for _, message := range c.queue {
		if line, err := EncodeLine(message); err == nil {
			c.raw <- line
		}
	}
	c.pending = nil
	c.queue = nil
	return c.raw
}

// SendBytes writes one already-framed line to the app.
func (c *Connection) SendBytes(line []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.conn.Write(line)
}

// Send writes one line. A client that hung up drops it; every caller is about
// to exit or hang up too.
func (c *Connection) Send(message Message) {
	line, err := EncodeLine(message)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, _ = c.conn.Write(line)
}

// Close flushes and closes the socket, which ends the host's process.
func (c *Connection) Close() error {
	return c.conn.Close()
}
