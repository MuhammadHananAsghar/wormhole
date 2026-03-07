package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/wormhole-dev/wormhole/internal/transport"
)

// wsPassthrough manages WebSocket connections between visitors and local server.
type wsPassthrough struct {
	conns  map[string]*websocket.Conn
	mu     sync.Mutex
	logger zerolog.Logger
}

func newWSPassthrough(logger zerolog.Logger) *wsPassthrough {
	return &wsPassthrough{
		conns:  make(map[string]*websocket.Conn),
		logger: logger,
	}
}

func (wp *wsPassthrough) handleOpen(ctx context.Context, data []byte, sendFn func(transport.Message) error) {
	var msg transport.WSOpenMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		wp.logger.Error().Err(err).Msg("failed to unmarshal ws_open")
		return
	}

	// Connect to local WebSocket server
	localURL := fmt.Sprintf("ws://%s%s", wp.localAddr(ctx), msg.Path)
	reqHeader := http.Header{}
	for k, v := range msg.Headers {
		reqHeader.Set(k, v)
	}
	// Remove headers that would conflict
	reqHeader.Del("Upgrade")
	reqHeader.Del("Connection")
	reqHeader.Del("Sec-Websocket-Key")
	reqHeader.Del("Sec-Websocket-Version")
	reqHeader.Del("Sec-Websocket-Extensions")

	conn, _, err := websocket.DefaultDialer.Dial(localURL, reqHeader)
	if err != nil {
		wp.logger.Warn().Err(err).Str("id", msg.ID).Msg("failed to connect local WS")
		errMsg, _ := json.Marshal(map[string]interface{}{
			"type":  transport.TypeWSClose,
			"id":    msg.ID,
			"code":  1002,
		})
		sendFn(transport.Message{Data: errMsg})
		return
	}

	wp.mu.Lock()
	wp.conns[msg.ID] = conn
	wp.mu.Unlock()

	// Read frames from local and send to relay
	go func() {
		defer func() {
			wp.mu.Lock()
			delete(wp.conns, msg.ID)
			wp.mu.Unlock()
			conn.Close()
		}()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				closeMsg, _ := json.Marshal(map[string]interface{}{
					"type": transport.TypeWSClose,
					"id":   msg.ID,
					"code": 1000,
				})
				sendFn(transport.Message{Data: closeMsg})
				return
			}

			frame, _ := json.Marshal(transport.WSFrameMessage{
				Type: transport.TypeWSFrame,
				ID:   msg.ID,
				Data: base64.StdEncoding.EncodeToString(data),
			})
			if err := sendFn(transport.Message{Data: frame}); err != nil {
				return
			}
		}
	}()
}

func (wp *wsPassthrough) handleFrame(data []byte) {
	var msg transport.WSFrameMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	wp.mu.Lock()
	conn := wp.conns[msg.ID]
	wp.mu.Unlock()

	if conn == nil {
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return
	}

	conn.WriteMessage(websocket.TextMessage, decoded)
}

func (wp *wsPassthrough) handleClose(data []byte) {
	var msg transport.WSCloseMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	wp.mu.Lock()
	conn := wp.conns[msg.ID]
	delete(wp.conns, msg.ID)
	wp.mu.Unlock()

	if conn != nil {
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		conn.Close()
	}
}

func (wp *wsPassthrough) closeAll() {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	for id, conn := range wp.conns {
		conn.Close()
		delete(wp.conns, id)
	}
}

// localAddr is extracted from context — set by the client before passing
func (wp *wsPassthrough) localAddr(ctx context.Context) string {
	if addr, ok := ctx.Value(localAddrKey).(string); ok {
		return addr
	}
	return "localhost:8080"
}

type contextKey string

const localAddrKey contextKey = "localAddr"
