package client

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wormhole-dev/wormhole/internal/transport"
)

const localTimeout = 30 * time.Second

// pathFilterEnabled returns true unless WORMHOLE_NO_PATH_FILTER=1 is set.
// When filtering is enabled, requests to dotfiles and node_modules are
// blocked with 403 before reaching the local server (CWE-441).
func pathFilterEnabled() bool {
	return os.Getenv("WORMHOLE_NO_PATH_FILTER") != "1"
}

// isSensitivePath returns true when the given path should be blocked.
// Blocked patterns:
//   - Any path segment starting with "." at the root (/.env, /.git, /.aws, ...)
//   - Any path containing /node_modules/
func isSensitivePath(path string) bool {
	// Normalise to ensure leading slash.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Block root-level dotfiles: path starts with "/.".
	if strings.HasPrefix(path, "/.") {
		return true
	}
	// Block node_modules anywhere in the path.
	if strings.Contains(path, "/node_modules/") || path == "/node_modules" {
		return true
	}
	return false
}

// blockedResponse returns a 403 HTTP response message for the given request ID.
func blockedResponse(id string) *transport.HTTPResponseMessage {
	return &transport.HTTPResponseMessage{
		Type:    transport.TypeHTTPResponse,
		ID:      id,
		Status:  http.StatusForbidden,
		Headers: map[string]string{"Content-Type": "text/plain"},
		Body:    base64.StdEncoding.EncodeToString([]byte("Forbidden")),
	}
}

// ForwardToLocal takes an HTTP request message from the relay, forwards it
// to the local server, and returns an HTTP response message.
//
// Security properties:
//   - Sensitive paths (dotfiles, node_modules) are blocked with 403 by
//     default (CWE-441). Set WORMHOLE_NO_PATH_FILTER=1 to disable.
//   - When the local server is unreachable, a generic error message is
//     returned to the tunnel; the raw Go error is written to stderr only
//     (CWE-200).
func ForwardToLocal(localAddr string, req *transport.HTTPRequestMessage) (*transport.HTTPResponseMessage, error) {
	// --- Fix 2: Path filtering (CWE-441) ---
	if pathFilterEnabled() && isSensitivePath(req.Path) {
		return blockedResponse(req.ID), nil
	}

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
		// --- Fix 3: Error sanitisation (CWE-200) ---
		// Log the real error locally; send only a generic message through
		// the tunnel so internal network topology is not exposed to remote
		// visitors.
		fmt.Fprintf(os.Stderr, "wormhole: local forward error: %v\n", err)
		return &transport.HTTPResponseMessage{
			Type:    transport.TypeHTTPResponse,
			ID:      req.ID,
			Status:  502,
			Headers: map[string]string{"Content-Type": "text/plain"},
			Body:    base64.StdEncoding.EncodeToString([]byte("Tunnel connected, but the local service is not responding.")),
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
