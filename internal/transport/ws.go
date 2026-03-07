package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 30 * time.Second
	pingPeriod = 20 * time.Second
)

// WSTransport implements Transport over WebSocket.
type WSTransport struct {
	url  string
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewWSTransport creates a new WebSocket transport targeting the given relay URL.
func NewWSTransport(relayURL string) *WSTransport {
	return &WSTransport{url: relayURL}
}

func (t *WSTransport) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(ctx, "tcp4", addr)
		},
	}

	conn, _, err := dialer.DialContext(ctx, t.url, nil)
	if err != nil {
		return fmt.Errorf("connecting to relay: %w", err)
	}

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()

	// Start heartbeat
	go t.heartbeat(ctx)

	return nil
}

func (t *WSTransport) Send(ctx context.Context, msg Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn == nil {
		return fmt.Errorf("not connected")
	}

	if err := t.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return fmt.Errorf("setting write deadline: %w", err)
	}

	if err := t.conn.WriteMessage(websocket.TextMessage, msg.Data); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}

	return nil
}

func (t *WSTransport) Receive(ctx context.Context) (Message, error) {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()

	if conn == nil {
		return Message{}, fmt.Errorf("not connected")
	}

	// Use a goroutine so we can respect context cancellation
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		_, data, err := conn.ReadMessage()
		ch <- result{data, err}
	}()

	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return Message{}, fmt.Errorf("reading message: %w", r.err)
		}
		return Message{Data: r.data}, nil
	}
}

func (t *WSTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn == nil {
		return nil
	}

	// Send close frame
	err := t.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(writeWait),
	)
	closeErr := t.conn.Close()
	t.conn = nil

	if err != nil {
		return fmt.Errorf("sending close frame: %w", err)
	}
	return closeErr
}

func (t *WSTransport) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.mu.Lock()
			if t.conn == nil {
				t.mu.Unlock()
				return
			}
			err := t.conn.WriteControl(
				websocket.PingMessage, nil,
				time.Now().Add(writeWait),
			)
			t.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}
