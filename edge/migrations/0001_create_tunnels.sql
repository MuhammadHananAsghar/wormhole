CREATE TABLE IF NOT EXISTS tunnels (
  subdomain TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_tunnels_client_id ON tunnels(client_id);
