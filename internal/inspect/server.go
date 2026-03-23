package inspect

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// Server runs the traffic inspector HTTP server on localhost.
type Server struct {
	recorder *Recorder
	localAddr string
	logger   zerolog.Logger
	listener net.Listener
	upgrader websocket.Upgrader
}

// NewServer creates an inspector server.
func NewServer(recorder *Recorder, localAddr string, logger zerolog.Logger) *Server {
	s := &Server{
		recorder:  recorder,
		localAddr: localAddr,
		logger:    logger,
	}
	s.upgrader = websocket.Upgrader{
		// CheckOrigin validates that the WebSocket upgrade request originates
		// from the inspector itself (same host). This prevents cross-origin
		// WebSocket hijacking (CWE-942).
		CheckOrigin: func(r *http.Request) bool {
			return s.isAllowedOrigin(r)
		},
	}
	return s
}

// isAllowedOrigin returns true when the request's Origin header is either
// absent (e.g., curl / same-origin browser fetch) or matches the inspector's
// own address. This is the gating predicate for both CORS and WebSocket
// origin validation (CWE-942).
func (s *Server) isAllowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header — direct browser navigation or non-browser client.
		return true
	}
	if s.listener == nil {
		// Listener not yet bound; conservatively deny cross-origin requests.
		return false
	}
	// The inspector binds to a local address (e.g., "127.0.0.1:4040").
	// Acceptable origins are http:// and https:// variants of that address.
	inspectorAddr := s.listener.Addr().String()
	return origin == "http://"+inspectorAddr || origin == "https://"+inspectorAddr
}

// Start binds to the given address and serves the inspector.
func (s *Server) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("binding inspector: %w", err)
	}
	s.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/requests/", s.handleRequestByID)
	mux.HandleFunc("/api/requests/stream", s.handleStream)
	mux.HandleFunc("/api/replay/", s.handleReplay)
	mux.HandleFunc("/api/har", s.handleHAR)
	mux.HandleFunc("/api/clear", s.handleClear)
	mux.HandleFunc("/", s.handleDashboard)

	server := &http.Server{Handler: corsMiddleware(s, mux)}
	go server.Serve(listener)
	s.logger.Info().Str("addr", listener.Addr().String()).Msg("inspector started")
	return nil
}

// Addr returns the listener address, or empty if not started.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Close stops the inspector server.
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// corsMiddleware enforces same-origin access on the inspector API.
// It only reflects the CORS allow-origin header when the request's Origin
// matches the inspector's own address, preventing any other website from
// reading tunnel traffic via cross-origin requests (CWE-942).
func corsMiddleware(srv *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if srv.isAllowedOrigin(r) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				// Echo the exact origin back — no wildcard.
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// Origin present but not the inspector's own address — reject.
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// GET /api/requests — list all captured entries
func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}

	entries := s.recorder.Entries()

	// Apply query filters
	method := r.URL.Query().Get("method")
	path := r.URL.Query().Get("path")
	if method != "" || path != "" {
		entries = s.recorder.Filter(method, path, 0, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// GET /api/requests/{id}
func (s *Server) handleRequestByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/requests/")
	if id == "" || id == "stream" {
		return
	}

	entry, ok := s.recorder.Get(id)
	if !ok {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

// GET /api/requests/stream — WebSocket live stream
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn().Err(err).Msg("inspector ws upgrade failed")
		return
	}
	defer conn.Close()

	ch := s.recorder.Subscribe()
	defer s.recorder.Unsubscribe(ch)

	// Read pump (just to detect close)
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for entry := range ch {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// POST /api/replay/{id} — replay a captured request to localhost
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/replay/")
	entry, ok := s.recorder.Get(id)
	if !ok {
		http.Error(w, `{"error":"not found"}`, 404)
		return
	}

	result, err := Replay(s.localAddr, entry)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GET /api/har — export as HAR format
func (s *Server) handleHAR(w http.ResponseWriter, r *http.Request) {
	entries := s.recorder.Entries()
	har := BuildHAR(entries)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=wormhole_%s.har", time.Now().Format("20060102_150405")))
	json.NewEncoder(w).Encode(har)
}

// POST /api/clear — clear all entries
func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	s.recorder.Clear()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}

// handleDashboard serves the embedded React SPA
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// For now, serve a minimal HTML page. The full React SPA will be embedded via go:embed later.
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, dashboardHTML)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Wormhole Inspector</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0a0a0a;color:#e0e0e0}
.header{background:#111;border-bottom:1px solid #222;padding:12px 20px;display:flex;align-items:center;justify-content:space-between}
.header h1{font-size:16px;color:#a78bfa;font-weight:600}
.header .actions{display:flex;gap:8px}
.btn{background:#1a1a2e;border:1px solid #333;color:#ccc;padding:6px 12px;border-radius:4px;cursor:pointer;font-size:12px}
.btn:hover{background:#252540;border-color:#555}
.btn.primary{background:#4c1d95;border-color:#6d28d9;color:#fff}
.btn.primary:hover{background:#5b21b6}
.toolbar{background:#111;border-bottom:1px solid #222;padding:8px 20px;display:flex;gap:8px;align-items:center}
.toolbar select,.toolbar input{background:#1a1a2e;border:1px solid #333;color:#ccc;padding:4px 8px;border-radius:4px;font-size:12px}
.toolbar input{flex:1;max-width:300px}
.container{display:flex;height:calc(100vh - 90px)}
.list{width:50%;border-right:1px solid #222;overflow-y:auto}
.detail{width:50%;overflow-y:auto;padding:16px}
.entry{display:grid;grid-template-columns:60px 1fr 50px 60px;gap:8px;padding:8px 16px;border-bottom:1px solid #1a1a1a;cursor:pointer;align-items:center;font-size:13px}
.entry:hover{background:#111}
.entry.selected{background:#1a1a2e;border-left:2px solid #7c3aed}
.method{font-weight:700;font-size:12px}
.method.GET{color:#34d399}.method.POST{color:#60a5fa}.method.PUT,.method.PATCH{color:#fbbf24}.method.DELETE{color:#f87171}
.path{color:#999;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.status{font-weight:600;text-align:right}
.status.s2xx{color:#34d399}.status.s3xx{color:#fbbf24}.status.s4xx{color:#fb923c}.status.s5xx{color:#f87171}
.latency{color:#666;text-align:right;font-size:12px}
.detail h3{color:#a78bfa;font-size:13px;margin:12px 0 6px;text-transform:uppercase;letter-spacing:0.5px}
.detail h3:first-child{margin-top:0}
.detail-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px}
.detail-header .method-path{font-size:14px;font-weight:600}
.kv{font-size:12px;font-family:'SF Mono',Monaco,monospace;margin:2px 0;color:#999}
.kv .key{color:#7c3aed}.kv .val{color:#ccc}
.body-preview{background:#111;border:1px solid #222;border-radius:4px;padding:12px;font-family:'SF Mono',Monaco,monospace;font-size:12px;white-space:pre-wrap;word-break:break-all;max-height:300px;overflow-y:auto;color:#ccc}
.empty{text-align:center;color:#555;padding:60px 20px;font-size:14px}
.badge{display:inline-block;padding:2px 6px;border-radius:3px;font-size:11px;font-weight:600}
#connection-status{font-size:11px;color:#666}
#connection-status.connected{color:#34d399}
</style>
</head>
<body>
<div class="header">
  <h1>Wormhole Inspector</h1>
  <div class="actions">
    <span id="connection-status">connecting...</span>
    <button class="btn" onclick="clearAll()">Clear</button>
    <button class="btn" onclick="exportHAR()">Export HAR</button>
  </div>
</div>
<div class="toolbar">
  <select id="method-filter" onchange="applyFilter()">
    <option value="">All Methods</option>
    <option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option>
  </select>
  <input id="path-filter" placeholder="Filter by path..." oninput="applyFilter()">
  <select id="status-filter" onchange="applyFilter()">
    <option value="">All Status</option>
    <option value="2xx">2xx</option><option value="3xx">3xx</option><option value="4xx">4xx</option><option value="5xx">5xx</option>
  </select>
</div>
<div class="container">
  <div class="list" id="request-list"><div class="empty">Waiting for requests...</div></div>
  <div class="detail" id="request-detail"><div class="empty">Select a request to view details</div></div>
</div>

<script>
let entries = [];
let selectedId = null;
let ws;

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(proto + '://' + location.host + '/api/requests/stream');
  ws.onopen = () => {
    document.getElementById('connection-status').textContent = 'live';
    document.getElementById('connection-status').className = 'connected';
  };
  ws.onmessage = (e) => {
    const entry = JSON.parse(e.data);
    entries.unshift(entry);
    if (entries.length > 500) entries.pop();
    render();
  };
  ws.onclose = () => {
    document.getElementById('connection-status').textContent = 'disconnected';
    document.getElementById('connection-status').className = '';
    setTimeout(connectWS, 2000);
  };
}

async function loadInitial() {
  try {
    const resp = await fetch('/api/requests');
    entries = (await resp.json()).reverse();
    render();
  } catch(e) {}
}

function statusClass(s) {
  if (s >= 500) return 's5xx';
  if (s >= 400) return 's4xx';
  if (s >= 300) return 's3xx';
  return 's2xx';
}

function applyFilter() {
  render();
}

function getFiltered() {
  const m = document.getElementById('method-filter').value;
  const p = document.getElementById('path-filter').value.toLowerCase();
  const s = document.getElementById('status-filter').value;
  return entries.filter(e => {
    if (m && e.method !== m) return false;
    if (p && !e.path.toLowerCase().includes(p)) return false;
    if (s === '2xx' && (e.status < 200 || e.status >= 300)) return false;
    if (s === '3xx' && (e.status < 300 || e.status >= 400)) return false;
    if (s === '4xx' && (e.status < 400 || e.status >= 500)) return false;
    if (s === '5xx' && e.status < 500) return false;
    return true;
  });
}

function render() {
  const filtered = getFiltered();
  const list = document.getElementById('request-list');
  if (!filtered.length) {
    list.innerHTML = '<div class="empty">No matching requests</div>';
    return;
  }
  list.innerHTML = filtered.map(e => {
    const dur = e.duration_ns ? (e.duration_ns / 1e6).toFixed(0) + 'ms' : '-';
    return '<div class="entry' + (e.id === selectedId ? ' selected' : '') + '" onclick="selectEntry(\'' + e.id + '\')">'
      + '<span class="method ' + e.method + '">' + e.method + '</span>'
      + '<span class="path">' + escHtml(e.path) + '</span>'
      + '<span class="status ' + statusClass(e.status) + '">' + e.status + '</span>'
      + '<span class="latency">' + dur + '</span></div>';
  }).join('');
}

function selectEntry(id) {
  selectedId = id;
  render();
  const entry = entries.find(e => e.id === id);
  if (!entry) return;
  const detail = document.getElementById('request-detail');
  const dur = entry.duration_ns ? (entry.duration_ns / 1e6).toFixed(1) + 'ms' : '-';

  let html = '<div class="detail-header"><span class="method-path"><span class="method ' + entry.method + '">'
    + entry.method + '</span> ' + escHtml(entry.path) + '</span>'
    + '<button class="btn primary" onclick="replayRequest(\'' + entry.id + '\')">Replay</button></div>'
    + '<div style="color:#666;font-size:12px;margin-bottom:12px">' + new Date(entry.started_at).toLocaleString() + ' | ' + dur + '</div>';

  html += '<h3>Request Headers</h3>';
  if (entry.request_headers) {
    html += Object.entries(entry.request_headers).map(([k,v]) =>
      '<div class="kv"><span class="key">' + escHtml(k) + '</span>: <span class="val">' + escHtml(v) + '</span></div>'
    ).join('');
  }

  if (entry.request_body && entry.request_body.length) {
    html += '<h3>Request Body</h3><div class="body-preview">' + tryFormat(entry.request_body) + '</div>';
  }

  html += '<h3>Response ' + entry.status + '</h3>';
  if (entry.response_headers) {
    html += Object.entries(entry.response_headers).map(([k,v]) =>
      '<div class="kv"><span class="key">' + escHtml(k) + '</span>: <span class="val">' + escHtml(v) + '</span></div>'
    ).join('');
  }

  if (entry.response_body && entry.response_body.length) {
    html += '<h3>Response Body</h3><div class="body-preview">' + tryFormat(entry.response_body) + '</div>';
  }

  detail.innerHTML = html;
}

async function replayRequest(id) {
  try {
    const resp = await fetch('/api/replay/' + id, { method: 'POST' });
    const result = await resp.json();
    if (result.error) { alert('Replay error: ' + result.error); return; }
    alert('Replay: ' + result.status + ' ' + (result.duration_ms || 0).toFixed(0) + 'ms');
  } catch(e) { alert('Replay failed: ' + e.message); }
}

async function clearAll() {
  await fetch('/api/clear', { method: 'POST' });
  entries = [];
  selectedId = null;
  render();
  document.getElementById('request-detail').innerHTML = '<div class="empty">Select a request to view details</div>';
}

async function exportHAR() {
  const resp = await fetch('/api/har');
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a'); a.href = url; a.download = 'wormhole.har'; a.click();
  URL.revokeObjectURL(url);
}

function tryFormat(data) {
  if (!data) return '';
  // data could be base64 or a byte array from JSON
  let str;
  if (typeof data === 'string') {
    try { str = atob(data); } catch(e) { str = data; }
  } else if (Array.isArray(data)) {
    str = String.fromCharCode(...data);
  } else {
    str = String(data);
  }
  try { return escHtml(JSON.stringify(JSON.parse(str), null, 2)); } catch(e) {}
  return escHtml(str);
}

function escHtml(s) {
  if (!s) return '';
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

loadInitial();
connectWS();
</script>
</body>
</html>`
