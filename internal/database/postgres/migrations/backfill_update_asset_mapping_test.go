package migrations_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"xprem/internal/bucket"
	"xprem/internal/database"
	"xprem/internal/database/postgres"
	"xprem/internal/database/postgres/migrations"
	"xprem/internal/database/postgres/pgdb"
	"xprem/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestUpBackfillUpdateAssetMappingCopiesBucketMappings(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL must be set in CI")
		}
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv("ADMIN_EMAIL", "seed-admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "Sup3rSecret!")
	postgres.RunDBMigrations(dbURL)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	migrations.SetEngine(&database.Engine{Queries: pgdb.New(pool), DB: pool})
	t.Cleanup(func() { migrations.SetEngine(nil) })

	root := t.TempDir()
	t.Setenv("STORAGE_MODE", "local")
	t.Setenv("LOCAL_BUCKET_BASE_PATH", root)
	bucket.ResetBucketInstance()
	t.Cleanup(bucket.ResetBucketInstance)

	appID := uuid.NewString()
	_, err = pool.Exec(ctx, "INSERT INTO apps (id, name) VALUES ($1, $2)", appID, "backfill-"+appID[:8])
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM apps WHERE id = $1", appID) })
	var branchID, runtimeVersionID int64
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO branches (app_id, name) VALUES ($1, 'production') RETURNING id", appID).Scan(&branchID))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO runtime_versions (app_id, version) VALUES ($1, '1') RETURNING id", appID).Scan(&runtimeVersionID))

	mapping := types.UpdateAssetMapping{
		LaunchAsset: types.ShapedAsset{Hash: "vH93RoNbdzk_2emr38L0ZVYJVBTPcspX5-5DXLUkiQ8", Key: "e44a25e2b1df198470a04adc1dd82e4e", FileExtension: ".bundle", ContentType: "application/javascript"},
		Assets:      []types.ShapedAsset{{Hash: "JCcs2u_4LMX6zazNmCpvBbYMRQRwS7-UwZpjiGWYgLs", Key: "4f1cb2cac2370cd5050681232e8575a8", FileExtension: ".png", ContentType: "image/png"}},
	}
	withMapping, err := json.Marshal(map[string]any{"platform": "ios", "assetMapping": mapping})
	require.NoError(t, err)
	withoutMapping := []byte(`{"platform":"ios"}`)
	const (
		casUpdate      = 1001
		legacyUpdate   = 1002
		rollbackUpdate = 1003
		orphanUpdate   = 1004
	)
	for _, row := range []struct {
		id         int64
		updateType types.UpdateType
		metadata   []byte
	}{
		{casUpdate, types.NormalUpdate, withMapping},
		{legacyUpdate, types.NormalUpdate, withoutMapping},
		{rollbackUpdate, types.Rollback, withMapping},
		{orphanUpdate, types.NormalUpdate, nil},
	} {
		_, err = pool.Exec(ctx, "INSERT INTO updates (id, branch_id, runtime_version_id, update_type, commit_hash, platform, checked_at) VALUES ($1, $2, $3, $4, 'abc', 'ios', now())", row.id, branchID, runtimeVersionID, int32(row.updateType))
		require.NoError(t, err)
		if row.metadata != nil {
			dir := filepath.Join(root, appID, "production", "1", strconv.FormatInt(row.id, 10))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "update-metadata.json"), row.metadata, 0o644))
		}
	}

	storedMapping := func(id int64) []byte {
		var raw []byte
		require.NoError(t, pool.QueryRow(ctx, "SELECT asset_mapping FROM updates WHERE branch_id = $1 AND id = $2", branchID, id).Scan(&raw))
		return raw
	}

	for run := 0; run < 2; run++ {
		require.NoError(t, migrations.UpBackfillUpdateAssetMapping(ctx, nil), "run %d", run)
		var got types.UpdateAssetMapping
		require.NoError(t, json.Unmarshal(storedMapping(casUpdate), &got))
		require.Equal(t, mapping, got)
		require.Nil(t, storedMapping(legacyUpdate), "a bucket file without a mapping leaves the column NULL")
		require.Nil(t, storedMapping(rollbackUpdate), "rollbacks carry no assets")
		require.Nil(t, storedMapping(orphanUpdate), "an update with no bucket metadata is left alone")
	}
}
