package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/rs/zerolog"
	"github.com/wormhole-dev/wormhole/internal/inspect"
	"github.com/wormhole-dev/wormhole/internal/transport"
)

// Config holds the client configuration.
type Config struct {
	RelayURL  string
	LocalAddr string
	Subdomain string // requested subdomain (empty = random)
	Token     string // auth token for custom subdomains
	Logger    zerolog.Logger
}

// TunnelInfo holds information about the active tunnel.
type TunnelInfo struct {
	Subdomain string
	URL       string
}

// RequestLog represents a single proxied request for display.
type RequestLog struct {
	Method  string
	Path    string
	Status  int
	Latency time.Duration
}

// Client orchestrates the tunnel lifecycle.
type Client struct {
	config        Config
	transport     transport.Transport
	tunnel        *TunnelInfo
	wsPassthrough *wsPassthrough
	recorder      *inspect.Recorder
	onRequest     func(RequestLog)
	onStatus      func(string)
}

// New creates a new tunnel client.
func New(cfg Config) *Client {
	return &Client{
		config:        cfg,
		transport:     transport.NewWSTransport(cfg.RelayURL),
		wsPassthrough: newWSPassthrough(cfg.Logger),
		recorder:      inspect.NewRecorder(500),
		onRequest:     func(RequestLog) {},
		onStatus:      func(string) {},
	}
}

// Recorder returns the request recorder for the inspector.
func (c *Client) Recorder() *inspect.Recorder {
	return c.recorder
}

// OnRequest sets a callback for each proxied request.
func (c *Client) OnRequest(fn func(RequestLog)) {
	c.onRequest = fn
}

// OnStatus sets a callback for status changes.
func (c *Client) OnStatus(fn func(string)) {
	c.onStatus = fn
}

// Tunnel returns the current tunnel info, or nil if not connected.
func (c *Client) Tunnel() *TunnelInfo {
	return c.tunnel
}

// LocalAddr returns the local address being forwarded to.
func (c *Client) LocalAddr() string {
	return c.config.LocalAddr
}

// Run connects to the relay with auto-reconnect and processes messages until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	for {
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.config.Logger.Warn().Err(err).Msg("disconnected from relay")
		c.tunnel = nil

		// Reconnect with exponential backoff
		if !c.reconnect(ctx) {
			return ctx.Err()
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	c.onStatus("connecting")

	if err := c.transport.Connect(ctx); err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer c.transport.Close()

	// Register tunnel
	tunnel, err := c.register(ctx)
	if err != nil {
		return fmt.Errorf("registering tunnel: %w", err)
	}
	c.tunnel = tunnel
	c.onStatus("online")
	c.config.Logger.Info().Str("url", tunnel.URL).Msg("tunnel established")

	// Process messages with local address in context for WS passthrough
	wsCtx := context.WithValue(ctx, localAddrKey, c.config.LocalAddr)
	return c.messageLoop(wsCtx)
}

func (c *Client) register(ctx context.Context) (*TunnelInfo, error) {
	regMsg := transport.RegisterMessage{
		Type:      transport.TypeRegister,
		Protocol:  "http",
		Subdomain: c.config.Subdomain,
		Token:     c.config.Token,
	}
	data, err := json.Marshal(regMsg)
	if err != nil {
		return nil, fmt.Errorf("marshalling register message: %w", err)
	}

	if err := c.transport.Send(ctx, transport.Message{Data: data}); err != nil {
		return nil, fmt.Errorf("sending register message: %w", err)
	}

	// Wait for registered response
	msg, err := c.transport.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("receiving registration response: %w", err)
	}

	msgType, err := transport.ParseMessageType(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if msgType == transport.TypeRegisterError {
		var regErr transport.RegisterErrorMessage
		if err := json.Unmarshal(msg.Data, &regErr); err != nil {
			return nil, fmt.Errorf("unmarshalling error: %w", err)
		}
		return nil, fmt.Errorf("registration failed: %s", regErr.Error)
	}

	if msgType != transport.TypeRegistered {
		return nil, fmt.Errorf("unexpected response type: %s", msgType)
	}

	var registered transport.RegisteredMessage
	if err := json.Unmarshal(msg.Data, &registered); err != nil {
		return nil, fmt.Errorf("unmarshalling registered message: %w", err)
	}

	return &TunnelInfo{
		Subdomain: registered.Subdomain,
		URL:       registered.URL,
	}, nil
}

func (c *Client) messageLoop(ctx context.Context) error {
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return fmt.Errorf("receiving message: %w", err)
		}

		msgType, err := transport.ParseMessageType(msg.Data)
		if err != nil {
			c.config.Logger.Warn().Err(err).Msg("invalid message from relay")
			continue
		}

		switch msgType {
		case transport.TypeHTTPRequest:
			go c.handleHTTPRequest(ctx, msg.Data)
		case transport.TypeWSOpen:
			sendFn := func(m transport.Message) error {
				return c.transport.Send(ctx, m)
			}
			go c.wsPassthrough.handleOpen(ctx, msg.Data, sendFn)
		case transport.TypeWSFrame:
			go c.wsPassthrough.handleFrame(msg.Data)
		case transport.TypeWSClose:
			go c.wsPassthrough.handleClose(msg.Data)
		case transport.TypePing:
			c.handlePing(ctx)
		default:
			c.config.Logger.Debug().Str("type", msgType).Msg("unknown message type")
		}
	}
}

func (c *Client) handleHTTPRequest(ctx context.Context, data []byte) {
	start := time.Now()

	var req transport.HTTPRequestMessage
	if err := json.Unmarshal(data, &req); err != nil {
		c.config.Logger.Error().Err(err).Msg("failed to unmarshal HTTP request")
		return
	}

	resp, err := ForwardToLocal(c.config.LocalAddr, &req)
	if err != nil {
		c.config.Logger.Error().Err(err).Str("path", req.Path).Msg("failed to forward request")
		return
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		c.config.Logger.Error().Err(err).Msg("failed to marshal HTTP response")
		return
	}

	if err := c.transport.Send(ctx, transport.Message{Data: respData}); err != nil {
		c.config.Logger.Error().Err(err).Msg("failed to send HTTP response")
		return
	}

	duration := time.Since(start)

	c.onRequest(RequestLog{
		Method:  req.Method,
		Path:    req.Path,
		Status:  resp.Status,
		Latency: duration,
	})

	// Record for inspector
	c.recorder.Record(inspect.Entry{
		ID:              req.ID,
		Method:          req.Method,
		Path:            req.Path,
		RequestHeaders:  req.Headers,
		RequestBody:     decodeBody(req.Body),
		Status:          resp.Status,
		ResponseHeaders: resp.Headers,
		ResponseBody:    decodeBodyStr(resp.Body),
		StartedAt:       start,
		Duration:        duration,
		ContentType:     resp.Headers["Content-Type"],
	})
}

func (c *Client) handlePing(ctx context.Context) {
	pong := map[string]string{"type": transport.TypePong}
	data, _ := json.Marshal(pong)
	c.transport.Send(ctx, transport.Message{Data: data})
}

func decodeBody(b *string) []byte {
	if b == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(*b)
	if err != nil {
		return []byte(*b)
	}
	return data
}

func decodeBodyStr(b string) []byte {
	if b == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(b)
	if err != nil {
		return []byte(b)
	}
	return data
}

func (c *Client) reconnect(ctx context.Context) bool {
	maxBackoff := 30 * time.Second

	for attempt := 0; ; attempt++ {
		// Exponential backoff with jitter
		base := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		if base > maxBackoff {
			base = maxBackoff
		}
		jitter := time.Duration(rand.Int64N(int64(base / 2)))
		wait := base + jitter

		c.onStatus("reconnecting")
		c.config.Logger.Info().Dur("backoff", wait).Int("attempt", attempt+1).Msg("reconnecting")

		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}

		// Try to create a fresh transport and connect
		c.transport = transport.NewWSTransport(c.config.RelayURL)
		if err := c.transport.Connect(ctx); err != nil {
			c.config.Logger.Warn().Err(err).Msg("reconnect failed")
			continue
		}

		// Re-register
		tunnel, err := c.register(ctx)
		if err != nil {
			c.config.Logger.Warn().Err(err).Msg("re-registration failed")
			c.transport.Close()
			continue
		}

		c.tunnel = tunnel
		c.onStatus("online")
		c.config.Logger.Info().Str("url", tunnel.URL).Msg("reconnected")

		// Resume message loop in the caller
		go func() {
			if err := c.messageLoop(ctx); err != nil && ctx.Err() == nil {
				c.config.Logger.Warn().Err(err).Msg("disconnected again")
			}
		}()

		return true
	}
}
