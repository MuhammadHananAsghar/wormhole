package client

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wormhole-dev/wormhole/internal/transport"
)

func TestForwardToLocal_GET(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/api/hello", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
	}))
	defer server.Close()

	// Strip http:// prefix to get host:port
	addr := server.Listener.Addr().String()

	req := &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      "req_1",
		Method:  "GET",
		Path:    "/api/hello",
		Headers: map[string]string{},
		Body:    nil,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	assert.Equal(t, "req_1", resp.ID)
	assert.Equal(t, transport.TypeHTTPResponse, resp.Type)

	bodyBytes, err := base64.StdEncoding.DecodeString(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(bodyBytes), "hello")
}

func TestForwardToLocal_POST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(201)
		w.Write([]byte("created"))
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	body := base64.StdEncoding.EncodeToString([]byte(`{"name":"test"}`))

	req := &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      "req_2",
		Method:  "POST",
		Path:    "/items",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    &body,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.Status)
}

func TestForwardToLocal_ServerDown(t *testing.T) {
	req := &transport.HTTPRequestMessage{
		Type:   transport.TypeHTTPRequest,
		ID:     "req_3",
		Method: "GET",
		Path:   "/",
		Body:   nil,
	}

	// Use an address that's not listening
	resp, err := ForwardToLocal("127.0.0.1:1", req)
	require.NoError(t, err) // Should not error — returns 502 response
	assert.Equal(t, 502, resp.Status)
}

func TestForwardToLocal_HostHeaderRewriting(t *testing.T) {
	var receivedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(200)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()

	req := &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      "req_host",
		Method:  "GET",
		Path:    "/",
		Headers: map[string]string{"Host": "myapp.wormhole.bar"},
		Body:    nil,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	assert.Equal(t, addr, receivedHost)
}

func TestForwardToLocal_ForwardedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// X-Forwarded headers should be passed through from the relay
		assert.Equal(t, "1.2.3.4", r.Header.Get("X-Forwarded-For"))
		assert.Equal(t, "https", r.Header.Get("X-Forwarded-Proto"))
		assert.Equal(t, "myapp.wormhole.bar", r.Header.Get("X-Forwarded-Host"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()

	req := &transport.HTTPRequestMessage{
		Type:   transport.TypeHTTPRequest,
		ID:     "req_fwd",
		Method: "GET",
		Path:   "/",
		Headers: map[string]string{
			"X-Forwarded-For":   "1.2.3.4",
			"X-Forwarded-Proto": "https",
			"X-Forwarded-Host":  "myapp.wormhole.bar",
		},
		Body: nil,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
}

func TestForwardToLocal_SkipsHopByHopHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Connection and Upgrade headers should be stripped
		assert.Empty(t, r.Header.Get("Connection"))
		assert.Empty(t, r.Header.Get("Upgrade"))
		assert.Empty(t, r.Header.Get("Transfer-Encoding"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()

	req := &transport.HTTPRequestMessage{
		Type:   transport.TypeHTTPRequest,
		ID:     "req_hop",
		Method: "GET",
		Path:   "/",
		Headers: map[string]string{
			"Connection":        "keep-alive",
			"Upgrade":           "websocket",
			"Transfer-Encoding": "chunked",
			"X-Custom":          "value",
		},
		Body: nil,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
}

func TestForwardToLocal_PreservesHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-value", r.Header.Get("X-Custom"))
		w.Header().Set("X-Response", "from-local")
		w.WriteHeader(200)
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()

	req := &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      "req_4",
		Method:  "GET",
		Path:    "/",
		Headers: map[string]string{"X-Custom": "test-value"},
		Body:    nil,
	}

	resp, err := ForwardToLocal(addr, req)
	require.NoError(t, err)
	assert.Equal(t, "from-local", resp.Headers["X-Response"])
}

func TestForwardToLocal_DoesNotInjectAcceptEncoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Accept-Encoding"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      "req_accept_encoding",
		Method:  "GET",
		Path:    "/static/app.css",
		Headers: map[string]string{},
		Body:    nil,
	}

	resp, err := ForwardToLocal(server.Listener.Addr().String(), req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.Status)
}

func TestForwardToLocal_PreservesGzipResponse(t *testing.T) {
	payload := []byte("body { color: red; }")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.Header.Get("Accept-Encoding"), "gzip")
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		w.WriteHeader(http.StatusOK)
		_, writeErr := w.Write(compressed.Bytes())
		require.NoError(t, writeErr)
	}))
	defer server.Close()

	req := &transport.HTTPRequestMessage{
		Type:   transport.TypeHTTPRequest,
		ID:     "req_gzip",
		Method: "GET",
		Path:   "/static/app.css",
		Headers: map[string]string{
			"Accept-Encoding": "gzip, br",
		},
		Body: nil,
	}

	resp, err := ForwardToLocal(server.Listener.Addr().String(), req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.Status)
	assert.Equal(t, "gzip", resp.Headers["Content-Encoding"])
	_, hasContentLength := resp.Headers["Content-Length"]
	assert.False(t, hasContentLength)

	body, err := base64.StdEncoding.DecodeString(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, compressed.Bytes(), body)
}

func TestForwardToLocal_PreservesBrotliResponseBytes(t *testing.T) {
	encoded := []byte{0x1b, 0x06, 0x00, 0x01, 0x8b, 0x13, 0x00}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.Header.Get("Accept-Encoding"), "br")
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		w.WriteHeader(http.StatusOK)
		_, writeErr := w.Write(encoded)
		require.NoError(t, writeErr)
	}))
	defer server.Close()

	req := &transport.HTTPRequestMessage{
		Type:   transport.TypeHTTPRequest,
		ID:     "req_br",
		Method: "GET",
		Path:   "/static/app.js",
		Headers: map[string]string{
			"Accept-Encoding": "gzip, br",
		},
		Body: nil,
	}

	resp, err := ForwardToLocal(server.Listener.Addr().String(), req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.Status)
	assert.Equal(t, "br", resp.Headers["Content-Encoding"])
	_, hasContentLength := resp.Headers["Content-Length"]
	assert.False(t, hasContentLength)

	body, err := base64.StdEncoding.DecodeString(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, encoded, body)
}
