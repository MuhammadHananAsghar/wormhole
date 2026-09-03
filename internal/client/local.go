package client

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/wormhole-dev/wormhole/internal/transport"
)

const localTimeout = 30 * time.Second

// localTransport disables automatic compression so the tunnel forwards the
// exact response body bytes returned by the local server.
var localTransport = func() *http.Transport {
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{DisableCompression: true}
	}
	clone := baseTransport.Clone()
	clone.DisableCompression = true
	return clone
}()

// pathFilterEnabled returns true unless WORMHOLE_NO_PATH_FILTER=1 is set.
// When filtering is enabled, requests to dotfiles and node_modules are
// blocked with 403 before reaching the local server (CWE-441).
func pathFilterEnabled() bool {
	return os.Getenv("WORMHOLE_NO_PATH_FILTER") != "1"
}

// isSensitivePath reports whether rawPath should be blocked before forwarding.
// Dot segments and node_modules are matched after URL unescaping and path
// cleaning so encoded or traversal variants cannot bypass the filter.
func isSensitivePath(rawPath string) bool {
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	unescaped, err := url.PathUnescape(rawPath)
	if err != nil {
		return true
	}
	cleaned := path.Clean(unescaped)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ".") || segment == "node_modules" {
			return true
		}
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

// isSkippedResponseHeader reports whether key describes HTTP connection
// framing that must not be forwarded through the tunnel.
func isSkippedResponseHeader(key string) bool {
	switch strings.ToLower(key) {
	case "content-length", "transfer-encoding", "connection", "keep-alive", "te", "trailer", "upgrade":
		return true
	default:
		return false
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
	client := &http.Client{Timeout: localTimeout, Transport: localTransport}
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
		if isSkippedResponseHeader(key) {
			continue
		}
		headers[key] = resp.Header.Get(key)
	}
	if resp.Uncompressed {
		delete(headers, "Content-Encoding")
	}

	return &transport.HTTPResponseMessage{
		Type:    transport.TypeHTTPResponse,
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(body),
	}, nil
}
