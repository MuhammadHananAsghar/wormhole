# Wormhole — Development Guidelines

## Project
Open-source ngrok alternative. One command (`wormhole http 3000`) gives a public URL via Cloudflare's edge network.

## Architecture
- **Go CLI client** (`cmd/`, `internal/`, `pkg/`) — single binary, ~12MB
- **Cloudflare Edge relay** (`edge/`) — Workers + Durable Objects + D1 + R2
- **Self-hosted relay** (`internal/server/`) — Go binary with QUIC + TCP tunnels

## Development Philosophy

### TDD — Test-Driven Development
1. **Red**: Write a failing test first
2. **Green**: Write minimal code to make it pass
3. **Refactor**: Clean up while keeping tests green

### Testing Standards
- **Go**: Use `testing` package + `testify` for assertions. Table-driven tests for all functions with multiple inputs.
- **Edge (TypeScript)**: Use `vitest` with Cloudflare Workers test bindings (`@cloudflare/vitest-pool-workers`).
- **Test naming**: `Test<Function>_<Scenario>` (Go), `describe/it` blocks (TS)
- **Coverage targets**: Core tunnel/transport logic must have >90% coverage
- **Integration tests**: Use `_test.go` files alongside source. For edge, `__tests__/` directory.

### Code Quality
- **Error handling**: Never swallow errors. Wrap with context (`fmt.Errorf("connecting to relay: %w", err)`)
- **Logging**: Use `zerolog` (Go), structured logging with levels. No `fmt.Println` in production code.
- **Security**: Validate all external input. No command injection, XSS, or SQL injection. Use parameterized queries for D1.
- **Performance**: Stay within targets (see `Wormhole_Project_Plan.md`). Profile hot paths. Minimize allocations in proxy path.

### Go Conventions
- Follow standard Go project layout
- Use `internal/` for non-exported packages
- `context.Context` as first parameter for cancellable operations
- Graceful shutdown via `context.WithCancel` + signal handling
- Interfaces at consumer, implementations at provider

### Cloudflare Edge Conventions
- TypeScript strict mode
- Use `wrangler.toml` for all configuration
- Durable Object classes export from `edge/src/`
- D1 migrations in `edge/migrations/`
- Deploy with `npx wrangler deploy`

## Task Tracking
- All task status lives in [memory/global.md](memory link — Claude auto-memory)
- Phase task details in `phases/<phase_name>/task_<n>.md`

## Commands
```bash
# Go
go test ./...                        # Run all tests
go test -race ./...                  # Race detector
go test -cover ./...                 # Coverage
go build -o wormhole ./cmd/wormhole  # Build binary

# Edge (Cloudflare)
cd edge && npm test                  # Run edge tests
cd edge && npx wrangler dev          # Local dev
cd edge && npx wrangler deploy       # Deploy

# Linting
golangci-lint run                    # Go linting
cd edge && npx eslint .              # TS linting
```

## File Structure Reference
See `Wormhole_Project_Plan.md` for full project structure.
