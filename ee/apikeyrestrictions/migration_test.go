// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPermissionsMigrationRetryPreservesPolicies(t *testing.T) {
	store, pool := setupAccessStore(t)
	ctx := context.Background()
	appID := insertTestApp(t, pool)
	key := insertTestApiKey(t, pool, appID, "scoped")
	emptyKey := insertTestApiKey(t, pool, appID, "no-access")
	identifierID := insertTestIdentifier(t, pool, appID, "android")
	policy := ApiKeyAccess{
		ApiKeyID:    key,
		UpdateRules: []UpdateRule{{Pattern: "staging", Actions: []UpdateAction{UpdateActionRead}}},
		BuildRules:  []BuildRule{{AppIdentifierID: identifierID, Actions: []BuildAction{BuildActionCreate}}},
		SubmitRules: []SubmitRule{{AppIdentifierID: identifierID, Destination: SubmitDestinationInternal, Actions: []SubmitAction{SubmitActionUpload}}},
	}
	require.NoError(t, store.SetAccess(ctx, appID, policy))
	// Reproduce a crash after the schema committed but before Goose recorded it.
	const version = 20260906120000
	var originalID int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT id FROM goose_db_version WHERE version_id=$1 ORDER BY id DESC LIMIT 1", int64(version)).Scan(&originalID))
	_, err := pool.Exec(ctx, "DELETE FROM goose_db_version WHERE version_id=$1", int64(version))
	require.NoError(t, err)
	t.Cleanup(func() {
		// Goose orders applied migrations by row ID; restore the replayed row's position.
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, "DELETE FROM goose_db_version WHERE version_id=$1", int64(version))
		require.NoError(t, err)
		_, err = tx.Exec(ctx, "INSERT INTO goose_db_version(id,version_id,is_applied) VALUES ($1,$2,true)", originalID, int64(version))
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
	})
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	require.NoError(t, err)
	defer db.Close()
	// setupAccessStore installed the same embedded migrations used at startup.
	require.NoError(t, goose.Up(db, "migrations", goose.WithAllowMissing()))
	actual, err := store.GetAccess(ctx, appID, key)
	require.NoError(t, err)
	assert.Equal(t, policy, actual)
	empty, err := store.GetAccess(ctx, appID, emptyKey)
	require.NoError(t, err)
	assert.Empty(t, empty.UpdateRules, "a retry must not grant Updates access to new tokens")
	assert.Empty(t, empty.BuildRules)
	assert.Empty(t, empty.SubmitRules)
	var temporaryIndexes int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM pg_class WHERE relname IN ('idx_api_keys_id_app','idx_app_identifiers_id_app')").Scan(&temporaryIndexes))
	assert.Zero(t, temporaryIndexes)
	current, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, current, int64(version))
}
