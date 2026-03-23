
<p align="center">
  <pre align="center">
  █   █ █▀▀█ █▀▀█ █▀▄▀█ █  █ █▀▀█ █   █▀▀
  █▄█▄█ █  █ █▄▄▀ █ █ █ █▀▀█ █  █ █   █▀▀
  ▀ ▀ ▀ ▀▀▀▀ ▀ ▀▀ ▀   ▀ ▀  ▀ ▀▀▀▀ ▀▀▀ ▀▀▀
  </pre>
  <br>
  <strong>Expose your localhost to the internet. Instantly.</strong>
  <br><br>
  <a href="https://github.com/MuhammadHananAsghar/wormhole/releases"><img src="https://img.shields.io/github/v/release/MuhammadHananAsghar/wormhole?style=flat-square" alt="Release"></a>
  <a href="https://github.com/MuhammadHananAsghar/wormhole/blob/main/LICENSE"><img src="https://img.shields.io/github/license/MuhammadHananAsghar/wormhole?style=flat-square" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/MuhammadHananAsghar/wormhole"><img src="https://goreportcard.com/badge/github.com/MuhammadHananAsghar/wormhole?style=flat-square" alt="Go Report"></a>
</p>

---

**Wormhole** is an open-source [ngrok](https://ngrok.com) alternative that gives your local server a public HTTPS URL with a single command. No signup required. No config files. Just works.

```bash
wormhole http 3000
```

```
  █   █ █▀▀█ █▀▀█ █▀▄▀█ █  █ █▀▀█ █   █▀▀
  █▄█▄█ █  █ █▄▄▀ █ █ █ █▀▀█ █  █ █   █▀▀
  ▀ ▀ ▀ ▀▀▀▀ ▀ ▀▀ ▀   ▀ ▀  ▀ ▀▀▀▀ ▀▀▀ ▀▀▀
  v0.1.0

  ╭──────────────────────────────────────────────────╮
  │       Status  ● connected                        │
  │   Forwarding  https://k7x9m2.wormhole.bar → ...  │
  │    Inspector  http://localhost:4040               │
  ╰──────────────────────────────────────────────────╯

  Requests
  --------------------------------------------------------------
  GET     /                          200    12ms
  POST    /webhooks/stripe           200     8ms
  GET     /api/users                 200    34ms
```

## Features

- **One command** — `wormhole http 3000` and you're live
- **HTTPS by default** — TLS handled automatically by Cloudflare
- **Custom subdomains** — `wormhole http 3000 --subdomain myapp` (free with GitHub login)
- **Traffic inspector** — Built-in dashboard at `localhost:4040` with live request stream
- **Request replay** — Re-send any captured request with one click
- **HAR export** — Export captured traffic in standard HAR format
- **Color-coded terminal** — Live request log with method + status code colors
- **Auto-reconnect** — Exponential backoff, seamless recovery
- **WebSocket passthrough** — Full WebSocket support through the tunnel
- **Zero config** — No signup, no config file, no server to deploy
- **Open source** — Fully open source, MIT licensed

## Install

### Quick install (macOS / Linux)

```bash
curl -fsSL https://wormhole.bar/install.sh | sh
```

### Homebrew (macOS)

```bash
brew install MuhammadHananAsghar/tap/wormhole
```

### Go install

```bash
go install github.com/MuhammadHananAsghar/wormhole/cmd/wormhole@latest
```

### Build from source

```bash
git clone https://github.com/MuhammadHananAsghar/wormhole.git
cd wormhole
make build
# Binary: ./wormhole
```

## Quick Start

### Expose a local HTTP server

```bash
# Start your local server on any port
wormhole http 3000
# => https://k7x9m2.wormhole.bar -> http://localhost:3000
```

### Custom subdomain (free)

```bash
# One-time login via GitHub
wormhole login

# Use your own subdomain
wormhole http 3000 --subdomain myapp
# => https://myapp.wormhole.bar -> http://localhost:3000
```

### Traffic inspector

Every tunnel automatically starts a traffic inspector at `http://localhost:4040`:

- Live request/response stream via WebSocket
- Request detail view with headers and body
- One-click request replay
- Filter by method, status code, path
- Export as HAR file

```bash
# Custom inspector port
wormhole http 3000 --inspect localhost:5050

# Disable inspector
wormhole http 3000 --no-inspect
```

## CLI Reference

```bash
wormhole http <port>                    # Expose local HTTP server
wormhole http <port> --subdomain NAME   # Custom subdomain
wormhole http <port> --headless         # No TUI, plain log output
wormhole http <port> --inspect ADDR     # Custom inspector address
wormhole http <port> --no-inspect       # Disable inspector

wormhole login                          # Authenticate via GitHub
wormhole logout                         # Remove stored credentials
wormhole status                         # Show auth status
wormhole uninstall                      # Remove wormhole from system
wormhole uninstall --purge              # Also remove config (~/.wormhole/)
wormhole version                        # Print version
```

## How It Works

```
YOUR LAPTOP                       CLOUDFLARE EDGE (300+ cities)
┌──────────────┐                 ┌─────────────────────────────┐
│              │   WebSocket     │                             │
│  wormhole    │◄───────────────►│  Worker (request router)    │
│  client      │  (encrypted)    │         ↕                   │
│              │                 │  Durable Object (tunnel)    │
│  localhost   │                 │  • Holds your WebSocket     │
│  :3000       │                 │  • Proxies HTTP to you      │
└──────────────┘                 │  • Hibernates when idle     │
                                 └──────────────┬──────────────┘
                                                │
                                   *.wormhole.bar (Cloudflare DNS)
                                                │
                                        Public Internet
```

1. Client opens WebSocket to nearest Cloudflare edge
2. Durable Object assigns a subdomain
3. HTTP requests to `*.wormhole.bar` hit the Worker
4. Worker routes to the correct Durable Object
5. DO serializes the request over WebSocket to your client
6. Client forwards to `localhost:3000`
7. Response flows back the same path

**Latency:** client <-> nearest CF edge (~5-20ms) + localhost (~0ms) = fast.

## Architecture

| Component | Technology |
|---|---|
| CLI Client | Go, Cobra, Bubbletea, Lipgloss |
| Transport | WebSocket (gorilla/websocket) |
| Edge Relay | Cloudflare Workers + Durable Objects |
| Database | Cloudflare D1 (SQLite) |
| DNS | Cloudflare DNS (wildcard `*.wormhole.bar`) |
| TLS | Cloudflare automatic SSL |
| Auth | GitHub OAuth |

## Project Structure

```
wormhole/
├── cmd/wormhole/          # CLI entry point
├── internal/
│   ├── client/            # Tunnel client (connect, forward, display)
│   ├── transport/         # WebSocket transport layer
│   └── inspect/           # Traffic inspector (recorder, server, replay, HAR)
├── edge/                  # Cloudflare Worker + Durable Object relay
│   ├── src/
│   │   ├── index.ts       # Worker router + auth
│   │   └── tunnel.ts      # Durable Object tunnel proxy
│   └── migrations/        # D1 schema migrations
├── pkg/config/            # User config (~/.wormhole/config.json)
├── deployments/           # install.sh, Docker, etc.
├── Makefile
└── .goreleaser.yml
```

## Security

Wormhole includes hardening measures to protect users who inadvertently expose sensitive local files or infrastructure details through the tunnel.

### Sensitive path blocking (CWE-441)

By default, wormhole blocks requests to dotfiles and `node_modules` before they reach your local server:

- `/.env`, `/.git`, `/.aws`, `/.ssh`, `/.docker`, and any other root-level dotfile — return **403 Forbidden**
- `/node_modules/` anywhere in the path — return **403 Forbidden**

This prevents credentials and source control history from being served to the internet even if your local server would normally serve them.

To disable (for users who genuinely need to serve these paths):

```bash
WORMHOLE_NO_PATH_FILTER=1 wormhole http 3000
```

### Inspector CORS hardening (CWE-942)

The traffic inspector (`localhost:4040`) no longer sets `Access-Control-Allow-Origin: *`. CORS headers are only returned when the request's `Origin` matches the inspector's own address. This prevents malicious websites visited in the same browser session from reading tunnel traffic via cross-origin requests.

The WebSocket upgrader's `CheckOrigin` is also locked down to the same policy.

### Error message sanitization (CWE-200)

When the local server is unreachable, wormhole returns a generic message (`Tunnel connected, but the local service is not responding.`) to the remote caller instead of the raw Go error string. Internal network topology details (host names, port numbers, error codes) are logged locally only and never sent through the tunnel.

## Development

```bash
# Run all Go tests
go test ./... -race

# Run edge tests
cd edge && npm test

# Build binary
make build

# Cross-compile all platforms
make dist
```

### TDD Workflow

This project follows test-driven development. Write failing tests first, then implement.

```bash
# Run tests in watch mode
go test ./internal/inspect/ -v -count=1

# Coverage
go test ./... -cover
```

## Comparison

| Feature | ngrok (free) | Cloudflare Tunnel | **Wormhole** |
|---|---|---|---|
| One-command setup | Needs signup | Needs CF account | **Just works** |
| Custom subdomains | Paid ($8/mo) | Yes (complex) | **Free** |
| HTTPS | Yes | Yes | **Yes** |
| Traffic inspector | Basic | No | **Full (replay, HAR)** |
| WebSocket support | Yes | Yes | **Yes** |
| Open source | No | Client only | **Fully open source** |
| Cost | $0-$120/yr | $0 (complex) | **$0** |

## Roadmap

Wormhole is built on a **dual-track architecture**: Cloudflare Workers (free, no setup) + self-hosted Go relay (full control, unlimited scale).

- [x] **Phase 1** — Core tunnel (`wormhole http 3000` → public URL, WebSocket passthrough)
- [x] **Phase 2** — HTTPS, custom subdomains (auto-reserve, 3/user limit), GitHub OAuth
- [x] **Phase 3** — Traffic inspector, request replay, HAR export, curl generation
- [ ] **Phase 4** — Self-hosted Go relay (`wormhole server`, QUIC transport, SQLite persistence)
- [ ] **Phase 5** — Auth & multi-user (API keys, team tokens, CF + self-hosted middleware)
- [ ] **Phase 6** — Stream multiplexing (virtual streams over single WebSocket, backpressure)
- [ ] **Phase 7** — Plugin system (request/response pipeline, custom auth, transforms)
- [ ] **Phase 8** — Observability (Prometheus metrics, structured logs, health endpoints)
- [ ] **Phase 9** — Protocol evolution (version negotiation, TLS pinning, binary framing)
- [ ] **Phase 10** — Enterprise hardening (connection limits, mTLS, audit logs, RBAC)
- [ ] **Phase 11** — P2P mode (`wormhole share`, WebRTC direct connections, no relay)
- [ ] **Phase 12** — Polish & ship (homepage, docs site, video demos, package registries)

## Author

**Muhammad Hanan Asghar**

- GitHub: [@MuhammadHananAsghar](https://github.com/MuhammadHananAsghar)
- LinkedIn: [muhammadhananasghar](https://www.linkedin.com/in/muhammadhananasghar/)

## License

MIT License. See [LICENSE](LICENSE) for details.

---

<p align="center">
  <sub>Built with Go + Cloudflare Workers. Runs on the edge.</sub>
</p>
