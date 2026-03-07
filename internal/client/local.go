package client

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wormhole-dev/wormhole/internal/transport"
)

const localTimeout = 30 * time.Second

// ForwardToLocal takes an HTTP request message from the relay, forwards it
// to the local server, and returns an HTTP response message.
func ForwardToLocal(localAddr string, req *transport.HTTPRequestMessage) (*transport.HTTPResponseMessage, error) {
	// Build the local URL
	url := fmt.Sprintf("http://%s%s", localAddr, req.Path)

	// Decode body if present
	var bodyReader io.Reader
	if req.Body != nil {
		decoded, err := base64.StdEncoding.DecodeString(*req.Body)
		if err != nil {
			return nil, fmt.Errorf("decoding request body: %w", err)
		}
		bodyReader = strings.NewReader(string(decoded))
	}

	// Create the HTTP request
	httpReq, err := http.NewRequest(req.Method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set Host header to local address (Task 7: Host header rewriting)
	httpReq.Host = localAddr

	// Copy headers, skip hop-by-hop headers
	for key, value := range req.Headers {
		lower := strings.ToLower(key)
		if lower == "host" || lower == "connection" || lower == "upgrade" ||
			lower == "transfer-encoding" {
			continue
		}
		httpReq.Header.Set(key, value)
	}

	// Make the request to local server
	client := &http.Client{Timeout: localTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return &transport.HTTPResponseMessage{
			Type:    transport.TypeHTTPResponse,
			ID:      req.ID,
			Status:  502,
			Headers: map[string]string{"Content-Type": "text/plain"},
			Body:    base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("Failed to reach local server: %s", err))),
		}, nil
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Collect response headers
	headers := make(map[string]string)
	for key := range resp.Header {
		headers[key] = resp.Header.Get(key)
	}

	return &transport.HTTPResponseMessage{
		Type:    transport.TypeHTTPResponse,
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(body),
	}, nil
}
