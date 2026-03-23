package client

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wormhole-dev/wormhole/internal/transport"
)

// makeSecReq is a convenience helper for security tests.
func makeSecReq(id, path string) *transport.HTTPRequestMessage {
	return &transport.HTTPRequestMessage{
		Type:    transport.TypeHTTPRequest,
		ID:      id,
		Method:  "GET",
		Path:    path,
		Headers: map[string]string{},
		Body:    nil,
	}
}

// TestPathFilter_DotfileAtRoot verifies that /.env is blocked (CWE-441).
func TestPathFilter_DotfileAtRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should have been blocked before reaching local server")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := ForwardToLocal(srv.Listener.Addr().String(), makeSecReq("pf1", "/.env"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.Status, "/.env must be blocked")
}

// TestPathFilter_DotGit verifies that /.git/config is blocked (CWE-441).
func TestPathFilter_DotGit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should have been blocked before reaching local server")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := ForwardToLocal(srv.Listener.Addr().String(), makeSecReq("pf2", "/.git/config"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.Status, "/.git/config must be blocked")
}

// TestPathFilter_NodeModules verifies that /node_modules/package.json is blocked (CWE-441).
func TestPathFilter_NodeModules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should have been blocked before reaching local server")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := ForwardToLocal(srv.Listener.Addr().String(), makeSecReq("pf3", "/node_modules/package.json"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.Status, "/node_modules/package.json must be blocked")
}

// TestPathFilter_NormalPath verifies that a normal API path passes through.
func TestPathFilter_NormalPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := ForwardToLocal(srv.Listener.Addr().String(), makeSecReq("pf4", "/api/hello"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.Status, "/api/hello must pass through")
}

// TestPathFilter_SubdirDotfile verifies that /public/.hidden is NOT blocked —
// only root-level dotfiles (paths starting with "/.") are blocked.
func TestPathFilter_SubdirDotfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := ForwardToLocal(srv.Listener.Addr().String(), makeSecReq("pf5", "/public/.hidden"))
	require.NoError(t, err)
	// /public/.hidden does NOT start with "/." so it should pass through.
	assert.Equal(t, http.StatusOK, resp.Status, "/public/.hidden should pass through (not a root dotfile)")
}

// TestErrorSanitization verifies that 502 error body is generic (CWE-200).
func TestErrorSanitization(t *testing.T) {
	req := makeSecReq("san1", "/api/data")

	resp, err := ForwardToLocal("127.0.0.1:1", req)
	require.NoError(t, err)
	assert.Equal(t, 502, resp.Status)

	body, decErr := base64.StdEncoding.DecodeString(resp.Body)
	require.NoError(t, decErr)
	bodyStr := string(body)

	// Generic message must be present.
	assert.Contains(t, bodyStr, "local service is not responding")
	// Internal network details must NOT be in the response body.
	assert.NotContains(t, bodyStr, "localhost")
	assert.NotContains(t, bodyStr, "127.0.0.1")
	assert.NotContains(t, bodyStr, "connect:")
	assert.NotContains(t, bodyStr, "dial ")
}
