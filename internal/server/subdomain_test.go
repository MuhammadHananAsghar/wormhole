package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteSubdomainStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteSubdomainStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestNewSQLiteSubdomainStore_CreatesDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteSubdomainStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "database file should exist")
}

func TestNewSQLiteSubdomainStore_InvalidPath(t *testing.T) {
	_, err := NewSQLiteSubdomainStore("/nonexistent/dir/test.db")
	assert.Error(t, err)
}

func TestReserve_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Reserve(ctx, "myapp", "user1")
	assert.NoError(t, err)

	owner, err := store.Owner(ctx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, "user1", owner)
}

func TestReserve_DuplicateSameUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "myapp", "user1"))
	err := store.Reserve(ctx, "myapp", "user1")
	assert.NoError(t, err, "same user re-reserving should succeed")
}

func TestReserve_DuplicateDifferentUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "myapp", "user1"))
	err := store.Reserve(ctx, "myapp", "user2")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSubdomainTaken)
}

func TestRelease_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "myapp", "user1"))
	err := store.Release(ctx, "myapp", "user1")
	assert.NoError(t, err)

	owner, err := store.Owner(ctx, "myapp")
	require.NoError(t, err)
	assert.Empty(t, owner, "owner should be empty after release")
}

func TestRelease_WrongUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "myapp", "user1"))
	err := store.Release(ctx, "myapp", "user2")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotOwner)
}

func TestRelease_Nonexistent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Release(ctx, "nonexistent", "user1")
	assert.NoError(t, err, "releasing nonexistent subdomain should be a no-op")
}

func TestOwner_Nonexistent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	owner, err := store.Owner(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, owner)
}

func TestIsAvailable_Unreserved(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	available, err := store.IsAvailable(ctx, "fresh")
	require.NoError(t, err)
	assert.True(t, available)
}

func TestIsAvailable_Reserved(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "taken", "user1"))

	available, err := store.IsAvailable(ctx, "taken")
	require.NoError(t, err)
	assert.False(t, available)
}

func TestIsAvailable_ActiveTunnel(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.SetActive(ctx, "live", "client1"))

	available, err := store.IsAvailable(ctx, "live")
	require.NoError(t, err)
	assert.False(t, available)
}

func TestSetActive_And_ClearActive(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.SetActive(ctx, "myapp", "client1")
	assert.NoError(t, err)

	available, err := store.IsAvailable(ctx, "myapp")
	require.NoError(t, err)
	assert.False(t, available)

	err = store.ClearActive(ctx, "myapp")
	assert.NoError(t, err)

	available, err = store.IsAvailable(ctx, "myapp")
	require.NoError(t, err)
	assert.True(t, available)
}

func TestCanClaim_NoReservationNoActive(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	ok, err := store.CanClaim(ctx, "free", "user1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCanClaim_ReservedBySameUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "mine", "user1"))

	ok, err := store.CanClaim(ctx, "mine", "user1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCanClaim_ReservedByOtherUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "theirs", "user1"))

	ok, err := store.CanClaim(ctx, "theirs", "user2")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCanClaim_ActiveTunnel(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.SetActive(ctx, "busy", "client1"))

	ok, err := store.CanClaim(ctx, "busy", "user2")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestListByUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Reserve(ctx, "app1", "user1"))
	require.NoError(t, store.Reserve(ctx, "app2", "user1"))
	require.NoError(t, store.Reserve(ctx, "other", "user2"))

	subs, err := store.ListByUser(ctx, "user1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"app1", "app2"}, subs)
}

func TestListByUser_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	subs, err := store.ListByUser(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestSystemReserved(t *testing.T) {
	store := newTestStore(t)

	tests := []string{"www", "api", "relay", "admin", "mail", "app", "dashboard"}
	for _, sub := range tests {
		assert.True(t, store.IsSystemReserved(sub), "should be system reserved: %s", sub)
	}
	assert.False(t, store.IsSystemReserved("myapp"))
}

func TestValidateSubdomain(t *testing.T) {
	tests := []struct {
		subdomain string
		valid     bool
	}{
		{"myapp", true},
		{"my-app", true},
		{"abc", true},
		{"a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6", true}, // 32 chars
		{"ab", false},                                           // too short
		{"a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q", false},          // 33 chars, too long
		{"-myapp", false},
		{"myapp-", false},
		{"my--app", true},
		{"MY_APP", false},
		{"my app", false},
		{"", false},
	}

	for _, tt := range tests {
		err := ValidateSubdomain(tt.subdomain)
		if tt.valid {
			assert.NoError(t, err, "subdomain %q should be valid", tt.subdomain)
		} else {
			assert.Error(t, err, "subdomain %q should be invalid", tt.subdomain)
		}
	}
}
