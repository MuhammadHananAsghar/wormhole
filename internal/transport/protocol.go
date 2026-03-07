package transport

import "encoding/json"

// Protocol message types exchanged between client and relay.
const (
	TypeRegister     = "register"
	TypeRegistered   = "registered"
	TypeHTTPRequest  = "http_request"
	TypeHTTPResponse = "http_response"
	TypePing          = "ping"
	TypePong          = "pong"
	TypeRegisterError = "register_error"
	TypeWSOpen        = "ws_open"
	TypeWSOpened      = "ws_opened"
	TypeWSFrame       = "ws_frame"
	TypeWSClose       = "ws_close"
	TypeWSError       = "ws_error"
)

// RegisterMessage is sent by client to request a tunnel.
type RegisterMessage struct {
	Type      string `json:"type"`
	Protocol  string `json:"protocol"`
	Subdomain string `json:"subdomain,omitempty"` // requested subdomain (empty = random)
	Token     string `json:"token,omitempty"`     // auth token for custom subdomains
}

// RegisterErrorMessage is sent by relay when registration fails.
type RegisterErrorMessage struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// RegisteredMessage is sent by relay after successful registration.
type RegisteredMessage struct {
	Type      string `json:"type"`
	Subdomain string `json:"subdomain"`
	URL       string `json:"url"`
}

// HTTPRequestMessage is sent by relay to proxy an HTTP request to the client.
type HTTPRequestMessage struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"` // base64 encoded, nil if no body
}

// HTTPResponseMessage is sent by client back to relay with the local server response.
type HTTPResponseMessage struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"` // base64 encoded
}

// WSOpenMessage is sent by relay when a visitor initiates a WebSocket upgrade.
type WSOpenMessage struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

// WSFrameMessage carries a WebSocket frame between relay and client.
type WSFrameMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Data string `json:"data"` // base64 encoded
}

// WSCloseMessage signals a WebSocket connection was closed.
type WSCloseMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Code int    `json:"code"`
}

// ParseMessage reads the type field from a raw JSON message.
func ParseMessageType(data []byte) (string, error) {
	var msg struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", err
	}
	return msg.Type, nil
}
