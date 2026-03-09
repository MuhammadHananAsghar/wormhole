package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	_ "github.com/mattn/go-sqlite3"
)

// MaxSubdomainsPerUser is the maximum number of custom subdomains a single user
// can reserve. Applies to both CF (D1) and self-hosted (SQLite) modes.
const MaxSubdomainsPerUser = 3

// Errors returned by SubdomainStore operations.
var (
	ErrSubdomainTaken  = errors.New("subdomain is reserved by another user")
	ErrNotOwner        = errors.New("subdomain is not owned by this user")
	ErrLimitReached    = fmt.Errorf("subdomain limit reached (max %d per user)", MaxSubdomainsPerUser)
)

// SubdomainStore manages subdomain reservations and active tunnel tracking.
// CF mode uses D1 (edge/src/tunnel.ts), self-hosted mode uses this interface
// backed by SQLite.
type SubdomainStore interface {
	// Reserve claims a subdomain for a user. Returns ErrSubdomainTaken if
	// already reserved by another user. Re-reserving by the same user is a no-op.
	Reserve(ctx context.Context, subdomain, userID string) error

	// Release removes a subdomain reservation. Returns ErrNotOwner if the
	// subdomain belongs to a different user. Releasing a nonexistent subdomain
	// is a no-op.
	Release(ctx context.Context, subdomain, userID string) error

	// Owner returns the user ID that reserved the subdomain, or empty string
	// if unreserved.
	Owner(ctx context.Context, subdomain string) (string, error)

	// IsAvailable returns true if the subdomain is neither reserved nor
	// actively used by a tunnel.
	IsAvailable(ctx context.Context, subdomain string) (bool, error)

	// CanClaim returns true if the given user can use this subdomain — either
	// it's free, or it's reserved by the same user, and no other tunnel is
	// active on it.
	CanClaim(ctx context.Context, subdomain, userID string) (bool, error)

	// SetActive marks a subdomain as having an active tunnel connection.
	SetActive(ctx context.Context, subdomain, clientID string) error

	// ClearActive removes the active tunnel marker for a subdomain.
	ClearActive(ctx context.Context, subdomain string) error

	// ListByUser returns all subdomains reserved by a user.
	ListByUser(ctx context.Context, userID string) ([]string, error)

	// CountByUser returns the number of subdomains reserved by a user.
	CountByUser(ctx context.Context, userID string) (int, error)

	// IsSystemReserved returns true if the subdomain is a system-reserved name.
	IsSystemReserved(subdomain string) bool

	// Close releases database resources.
	Close() error
}

var systemReserved = map[string]bool{
	"www": true, "api": true, "relay": true, "admin": true,
	"mail": true, "app": true, "dashboard": true,
}

var subdomainRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

// ValidateSubdomain checks that a subdomain is 3-32 lowercase alphanumeric
// characters or hyphens, not starting or ending with a hyphen.
func ValidateSubdomain(s string) error {
	if len(s) < 3 || len(s) > 32 {
		return fmt.Errorf("subdomain must be 3-32 characters, got %d", len(s))
	}
	if !subdomainRegexp.MatchString(s) {
		return fmt.Errorf("subdomain must be lowercase alphanumeric or hyphens, no leading/trailing hyphens")
	}
	return nil
}

// SQLiteSubdomainStore implements SubdomainStore backed by a local SQLite database.
// Uses the same schema as the D1 migrations in edge/migrations/.
type SQLiteSubdomainStore struct {
	db *sql.DB
}

// NewSQLiteSubdomainStore opens (or creates) a SQLite database at dbPath and
// runs the schema migrations. The schema matches edge/migrations/0001 and 0002.
func NewSQLiteSubdomainStore(dbPath string) (*SQLiteSubdomainStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("opening subdomain database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to subdomain database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating subdomain database: %w", err)
	}

	return &SQLiteSubdomainStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	// Same schema as edge/migrations/0001_create_tunnels.sql and
	// edge/migrations/0002_create_reserved_subdomains.sql
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS tunnels (
			subdomain TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tunnels_client_id ON tunnels(client_id)`,
		`CREATE TABLE IF NOT EXISTS reserved_subdomains (
			subdomain TEXT PRIMARY KEY,
			user_id TEXT,
			reserved_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reserved_user_id ON reserved_subdomains(user_id)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("running migration: %w", err)
		}
	}
	return nil
}

func (s *SQLiteSubdomainStore) Reserve(ctx context.Context, subdomain, userID string) error {
	// Check if already reserved
	var existingUser string
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM reserved_subdomains WHERE subdomain = ?", subdomain,
	).Scan(&existingUser)

	if err == nil {
		// Row exists — same user is fine, different user is an error
		if existingUser == userID {
			return nil
		}
		return ErrSubdomainTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("checking reservation: %w", err)
	}

	// Enforce per-user limit before inserting
	count, err := s.CountByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("counting user subdomains: %w", err)
	}
	if count >= MaxSubdomainsPerUser {
		return ErrLimitReached
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO reserved_subdomains (subdomain, user_id) VALUES (?, ?)",
		subdomain, userID,
	)
	if err != nil {
		return fmt.Errorf("reserving subdomain: %w", err)
	}
	return nil
}

func (s *SQLiteSubdomainStore) Release(ctx context.Context, subdomain, userID string) error {
	var existingUser string
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM reserved_subdomains WHERE subdomain = ?", subdomain,
	).Scan(&existingUser)

	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to release
	}
	if err != nil {
		return fmt.Errorf("checking reservation: %w", err)
	}

	if existingUser != userID {
		return ErrNotOwner
	}

	_, err = s.db.ExecContext(ctx,
		"DELETE FROM reserved_subdomains WHERE subdomain = ? AND user_id = ?",
		subdomain, userID,
	)
	if err != nil {
		return fmt.Errorf("releasing subdomain: %w", err)
	}
	return nil
}

func (s *SQLiteSubdomainStore) Owner(ctx context.Context, subdomain string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM reserved_subdomains WHERE subdomain = ?", subdomain,
	).Scan(&userID)

	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying owner: %w", err)
	}
	return userID, nil
}

func (s *SQLiteSubdomainStore) IsAvailable(ctx context.Context, subdomain string) (bool, error) {
	// Check reservations
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM reserved_subdomains WHERE subdomain = ?", subdomain,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking reservation: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	// Check active tunnels
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tunnels WHERE subdomain = ?", subdomain,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking active tunnels: %w", err)
	}
	return count == 0, nil
}

func (s *SQLiteSubdomainStore) CanClaim(ctx context.Context, subdomain, userID string) (bool, error) {
	// Check active tunnels first
	var activeCount int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tunnels WHERE subdomain = ?", subdomain,
	).Scan(&activeCount)
	if err != nil {
		return false, fmt.Errorf("checking active tunnels: %w", err)
	}
	if activeCount > 0 {
		return false, nil
	}

	// Check reservations
	var existingUser string
	err = s.db.QueryRowContext(ctx,
		"SELECT user_id FROM reserved_subdomains WHERE subdomain = ?", subdomain,
	).Scan(&existingUser)

	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // not reserved, not active
	}
	if err != nil {
		return false, fmt.Errorf("checking reservation: %w", err)
	}

	return existingUser == userID, nil
}

func (s *SQLiteSubdomainStore) SetActive(ctx context.Context, subdomain, clientID string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO tunnels (subdomain, client_id) VALUES (?, ?)",
		subdomain, clientID,
	)
	if err != nil {
		return fmt.Errorf("setting active tunnel: %w", err)
	}
	return nil
}

func (s *SQLiteSubdomainStore) ClearActive(ctx context.Context, subdomain string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM tunnels WHERE subdomain = ?", subdomain,
	)
	if err != nil {
		return fmt.Errorf("clearing active tunnel: %w", err)
	}
	return nil
}

func (s *SQLiteSubdomainStore) ListByUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT subdomain FROM reserved_subdomains WHERE user_id = ? ORDER BY subdomain", userID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing subdomains: %w", err)
	}
	defer rows.Close()

	var subs []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, fmt.Errorf("scanning subdomain: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *SQLiteSubdomainStore) CountByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM reserved_subdomains WHERE user_id = ?", userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting user subdomains: %w", err)
	}
	return count, nil
}

func (s *SQLiteSubdomainStore) IsSystemReserved(subdomain string) bool {
	return systemReserved[subdomain]
}

func (s *SQLiteSubdomainStore) Close() error {
	return s.db.Close()
}
