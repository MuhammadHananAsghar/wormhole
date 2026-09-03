package inspect

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() zerolog.Logger {
	return zerolog.New(os.Stderr).Level(zerolog.Disabled)
}

func TestServer_ListRequests(t *testing.T) {
	rec := NewRecorder(100)
	rec.Record(makeEntry("1", "GET", "/api/users", 200))
	rec.Record(makeEntry("2", "POST", "/api/items", 201))

	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", srv.handleRequests)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/requests")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)

	var entries []Entry
	json.NewDecoder(resp.Body).Decode(&entries)
	assert.Len(t, entries, 2)
}

func TestServer_ListRequests_FilterByMethod(t *testing.T) {
	rec := NewRecorder(100)
	rec.Record(makeEntry("1", "GET", "/a", 200))
	rec.Record(makeEntry("2", "POST", "/b", 201))

	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", srv.handleRequests)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/requests?method=GET")
	require.NoError(t, err)
	defer resp.Body.Close()

	var entries []Entry
	json.NewDecoder(resp.Body).Decode(&entries)
	assert.Len(t, entries, 1)
	assert.Equal(t, "GET", entries[0].Method)
}

func TestServer_GetRequestByID(t *testing.T) {
	rec := NewRecorder(100)
	rec.Record(makeEntry("abc123", "DELETE", "/api/items/5", 204))

	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests/", srv.handleRequestByID)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/requests/abc123")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)

	var entry Entry
	json.NewDecoder(resp.Body).Decode(&entry)
	assert.Equal(t, "DELETE", entry.Method)
}

func TestServer_GetRequestByID_NotFound(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests/", srv.handleRequestByID)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/requests/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 404, resp.StatusCode)
}

func TestServer_WebSocketStream(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests/stream", srv.handleStream)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/requests/stream"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Record an entry — should stream to WS client
	go func() {
		time.Sleep(50 * time.Millisecond)
		rec.Record(makeEntry("streamed", "GET", "/live", 200))
	}()

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := ws.ReadMessage()
	require.NoError(t, err)

	var entry Entry
	json.Unmarshal(data, &entry)
	assert.Equal(t, "streamed", entry.ID)
}

func TestServer_Clear(t *testing.T) {
	rec := NewRecorder(100)
	rec.Record(makeEntry("1", "GET", "/", 200))

	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/clear", srv.handleClear)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/clear", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 0, rec.Len())
}

func TestServer_Dashboard(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleDashboard)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestServer_CORSAllowsSameOrigin(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	require.NoError(t, srv.Start("127.0.0.1:0"))
	defer srv.Close()

	origin := "http://" + srv.Addr()
	req, err := http.NewRequest(http.MethodGet, origin+"/api/requests", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, origin, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.NotEqual(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestServer_CORSAllowsLocalhostOrigin(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	require.NoError(t, srv.Start("localhost:0"))
	defer srv.Close()

	_, port, err := net.SplitHostPort(srv.Addr())
	require.NoError(t, err)
	origin := "http://localhost:" + port
	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/api/requests", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, origin, resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestServer_CORSRejectsCrossOrigin(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	require.NoError(t, srv.Start("127.0.0.1:0"))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/api/requests", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.example")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestServer_WebSocketOriginPolicy(t *testing.T) {
	rec := NewRecorder(100)
	srv := NewServer(rec, "localhost:3000", testLogger())
	require.NoError(t, srv.Start("127.0.0.1:0"))
	defer srv.Close()

	allowedHeaders := http.Header{}
	allowedHeaders.Set("Origin", "http://"+srv.Addr())
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+srv.Addr()+"/api/requests/stream", allowedHeaders)
	require.NoError(t, err)
	ws.Close()

	deniedHeaders := http.Header{}
	deniedHeaders.Set("Origin", "https://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial("ws://"+srv.Addr()+"/api/requests/stream", deniedHeaders)
	require.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp.Body.Close()
	}
}
