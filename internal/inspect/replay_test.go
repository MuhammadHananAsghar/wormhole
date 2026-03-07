package inspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplay_GET(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/api/hello", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"replayed": "true"})
	}))
	defer server.Close()

	entry := Entry{
		Method:         "GET",
		Path:           "/api/hello",
		RequestHeaders: map[string]string{"Accept": "application/json"},
	}

	result, err := Replay(server.Listener.Addr().String(), entry)
	require.NoError(t, err)
	assert.Equal(t, 200, result.Status)
	assert.Contains(t, string(result.Body), "replayed")
	assert.True(t, result.DurationMs >= 0)
}

func TestReplay_POST_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(201)
	}))
	defer server.Close()

	entry := Entry{
		Method:         "POST",
		Path:           "/api/items",
		RequestHeaders: map[string]string{"Content-Type": "application/json"},
		RequestBody:    []byte(`{"name":"test"}`),
	}

	result, err := Replay(server.Listener.Addr().String(), entry)
	require.NoError(t, err)
	assert.Equal(t, 201, result.Status)
}

func TestReplay_ServerDown(t *testing.T) {
	entry := Entry{
		Method: "GET",
		Path:   "/",
	}

	_, err := Replay("127.0.0.1:1", entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replaying request")
}
