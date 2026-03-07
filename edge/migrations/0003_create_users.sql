CREATE TABLE IF NOT EXISTS users (
  github_id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  token TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_token ON users(token);
