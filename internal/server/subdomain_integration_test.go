package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistrationFlow_MatchesCFBehavior simulates the exact registration
// flow from edge/src/tunnel.ts handleRegistration() but using SQLite.
func TestRegistrationFlow_MatchesCFBehavior(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "relay.db")
	store, err := NewSQLiteSubdomainStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// === Scenario 1: Anonymous user gets random subdomain ===
	randomSub := "k7x9m2"
	available, err := store.IsAvailable(ctx, randomSub)
	require.NoError(t, err)
	assert.True(t, available)

	err = store.SetActive(ctx, randomSub, "client_abc")
	require.NoError(t, err)

	// Subdomain is now in use
	available, err = store.IsAvailable(ctx, randomSub)
	require.NoError(t, err)
	assert.False(t, available)

	// === Scenario 2: Authenticated user reserves custom subdomain ===
	err = ValidateSubdomain("myapp")
	require.NoError(t, err)

	assert.False(t, store.IsSystemReserved("myapp"))

	canClaim, err := store.CanClaim(ctx, "myapp", "user_github_123")
	require.NoError(t, err)
	assert.True(t, canClaim)

	err = store.Reserve(ctx, "myapp", "user_github_123")
	require.NoError(t, err)

	err = store.SetActive(ctx, "myapp", "client_xyz")
	require.NoError(t, err)

	// === Scenario 3: Different user tries to claim "myapp" ===
	canClaim, err = store.CanClaim(ctx, "myapp", "user_github_456")
	require.NoError(t, err)
	assert.False(t, canClaim, "different user should not be able to claim reserved subdomain")

	// === Scenario 4: System reserved subdomain rejected ===
	assert.True(t, store.IsSystemReserved("api"))
	assert.True(t, store.IsSystemReserved("www"))

	// === Scenario 5: Client disconnects — clear active tunnel ===
	err = store.ClearActive(ctx, randomSub)
	require.NoError(t, err)

	available, err = store.IsAvailable(ctx, randomSub)
	require.NoError(t, err)
	assert.True(t, available, "subdomain should be available after client disconnect")

	// "myapp" still reserved even after clearing active
	err = store.ClearActive(ctx, "myapp")
	require.NoError(t, err)

	owner, err := store.Owner(ctx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, "user_github_123", owner, "reservation persists after tunnel disconnect")

	// Same user can reclaim
	canClaim, err = store.CanClaim(ctx, "myapp", "user_github_123")
	require.NoError(t, err)
	assert.True(t, canClaim)

	// === Scenario 6: DB persistence — reopen and verify ===
	store.Close()

	store2, err := NewSQLiteSubdomainStore(dbPath)
	require.NoError(t, err)
	defer store2.Close()

	owner, err = store2.Owner(ctx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, "user_github_123", owner, "reservation survives DB reopen")

	subs, err := store2.ListByUser(ctx, "user_github_123")
	require.NoError(t, err)
	assert.Equal(t, []string{"myapp"}, subs)

	// === Scenario 7: Auto-reserve + rate limit (matches edge behavior) ===
	// User reserves 2 more subdomains (already has "myapp" = 1)
	err = store2.Reserve(ctx, "second", "user_github_123")
	require.NoError(t, err)
	err = store2.Reserve(ctx, "third", "user_github_123")
	require.NoError(t, err)

	// 4th subdomain should fail — limit is 3
	err = store2.Reserve(ctx, "fourth", "user_github_123")
	assert.ErrorIs(t, err, ErrLimitReached)

	// Verify count
	count, err := store2.CountByUser(ctx, "user_github_123")
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Release one, then reserve again — should work
	err = store2.Release(ctx, "second", "user_github_123")
	require.NoError(t, err)

	err = store2.Reserve(ctx, "replacement", "user_github_123")
	assert.NoError(t, err, "should succeed after releasing a slot")

	// Another user is unaffected by first user's limit
	err = store2.Reserve(ctx, "other1", "user_github_456")
	assert.NoError(t, err)
}
