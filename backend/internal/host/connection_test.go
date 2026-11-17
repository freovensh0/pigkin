package host

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// appSide is the Android end of a test connection. Polling in a goroutine keeps
// net.Pipe, which has no buffer, from blocking the host's writes.
type appSide struct {
	incoming chan Message
	conn     net.Conn
}

func newAppSide(conn net.Conn) *appSide {
	app := &appSide{incoming: make(chan Message, 8), conn: conn}
	scanner := ScanLines(conn)
	go func() {
		defer close(app.incoming)
		for scanner.Scan() {
			var message Message
			if err := json.Unmarshal(scanner.Bytes(), &message); err == nil {
				app.incoming <- message
			}
		}
	}()
	return app
}

func (a *appSide) read(t *testing.T) Message {
	t.Helper()
	select {
	case message, ok := <-a.incoming:
		if !ok {
			t.Fatal("no line from the host")
		}
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a line from the host")
	}
	return nil
}

func (a *appSide) write(t *testing.T, message Message) {
	t.Helper()
	line, err := EncodeLine(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.conn.Write(line); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionQueuesMessagesBeforeListen(t *testing.T) {
	hostSide, appConn := net.Pipe()
	app := newAppSide(appConn)
	defer appConn.Close()
	conn := NewConnection(hostSide)

	conn.Send(Message{"type": "pigkin_hello"})
	if hello := app.read(t); hello["type"] != "pigkin_hello" {
		t.Fatalf("got %v", hello)
	}

	// The request arrives before the mode listens: it must be queued, not lost.
	app.write(t, Message{"type": "pigkin_request", "params": Message{"op": "sessions"}})
	request, status := conn.First(time.Second)
	if status != FirstMessage || request["type"] != "pigkin_request" {
		t.Fatalf("got %v, %v", request, status)
	}

	delivered := make(chan Message, 1)
	conn.Listen(func(message Message) { delivered <- message })
	app.write(t, Message{"type": "pigkin_list", "id": 7.0})
	select {
	case message := <-delivered:
		if message["type"] != "pigkin_list" {
			t.Fatalf("got %v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not receive the message")
	}
}

func TestConnectionFirstTimesOut(t *testing.T) {
	hostSide, appConn := net.Pipe()
	defer appConn.Close()
	defer hostSide.Close()
	conn := NewConnection(hostSide)
	if _, status := conn.First(10 * time.Millisecond); status != FirstTimeout {
		t.Fatalf("got %v, want a timeout", status)
	}
}

func TestConnectionFirstSeesHangUp(t *testing.T) {
	hostSide, appConn := net.Pipe()
	conn := NewConnection(hostSide)
	_ = appConn.Close()
	if _, status := conn.First(time.Second); status != FirstClosed {
		t.Fatalf("got %v, want closed", status)
	}
}
