CREATE TABLE IF NOT EXISTS reserved_subdomains (
  subdomain TEXT PRIMARY KEY,
  user_id TEXT,
  reserved_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_reserved_user_id ON reserved_subdomains(user_id);
