// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

// Integration tests for the identity store: the merge-under-lock transaction,
// the value-stat bookkeeping and the trigram search need a real Postgres.
// They skip unless TEST_DATABASE_URL is set, e.g.:
//
//	docker run -d --name eoo-pg -e POSTGRES_PASSWORD=test -p 55432:5432 postgres:16-alpine
//	TEST_DATABASE_URL="postgres://postgres:test@localhost:55432/postgres?sslmode=disable" go test ./ee/identity/
//
// Every test creates its own app row, so tests never observe each other's
// devices or stats even on a database reused across runs.

package identity

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"expo-open-ota/internal/database"
	"expo-open-ota/internal/database/postgres"
	"expo-open-ota/internal/database/postgres/pgdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupIdentityStore(t *testing.T) (*PostgresIdentityStore, *pgxpool.Pool) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		// Same guard as the audit and rbac store tests: a skip in CI is a
		// green job that ran none of these queries.
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL must be set in CI: these tests cover SQL that unit tests cannot reach")
		}
		t.Skip("TEST_DATABASE_URL not set — start a Postgres and set it to run the identity store tests")
	}
	t.Setenv("ADMIN_EMAIL", "seed-admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "Sup3rSecret!")
	postgres.RunDBMigrations(dbURL)

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	// Licensed by default so the free-tier cap is inert; the cap test builds
	// its own unlicensed store.
	store := NewPostgresIdentityStore(&database.Engine{Queries: pgdb.New(pool), DB: pool})
	store.licenseValid = alwaysLicensed
	return store, pool
}

func alwaysLicensed() bool { return true }

func seedApp(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	appID := uuid.NewString()
	_, err := pool.Exec(context.Background(), "INSERT INTO apps (id, name) VALUES ($1, $2)", appID, "identity-test-"+appID[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM apps WHERE id = $1", appID)
	})
	return appID
}

func declareKey(t *testing.T, store *PostgresIdentityStore, appID, key string, valueType ValueType) {
	t.Helper()
	_, err := store.UpsertSchemaKey(context.Background(), appID, KeySpec{Key: key, Type: valueType, MaxLength: DefaultMaxLength})
	require.NoError(t, err)
}

func TestSchemaCRUD(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()

	schema, err := store.GetSchema(ctx, appID)
	require.NoError(t, err)
	require.Empty(t, schema)

	_, err = store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "userId", Type: ValueTypeString})
	require.NoError(t, err)
	_, err = store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "seats", Type: ValueTypeNumber, MaxLength: 32})
	require.NoError(t, err)

	schema, err = store.GetSchema(ctx, appID)
	require.NoError(t, err)
	require.Len(t, schema, 2)
	// Omitted max length lands on the default, not on zero.
	require.Equal(t, DefaultMaxLength, schema["userId"].MaxLength)
	require.Equal(t, 32, schema["seats"].MaxLength)

	// Upsert re-types a key in place.
	_, err = store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "seats", Type: ValueTypeString, MaxLength: 32})
	require.NoError(t, err)
	schema, err = store.GetSchema(ctx, appID)
	require.NoError(t, err)
	require.Equal(t, ValueTypeString, schema["seats"].Type)

	// Invalid specs are rejected before touching the database.
	_, err = store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "bad key", Type: ValueTypeString})
	require.Error(t, err)

	deleted, err := store.DeleteSchemaKey(ctx, appID, "seats")
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = store.DeleteSchemaKey(ctx, appID, "seats")
	require.NoError(t, err)
	require.False(t, deleted)
}

func TestApplySetMergesAndCounts(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "userId", ValueTypeString)
	declareKey(t, store, appID, "tenant", ValueTypeString)

	clientID := uuid.NewString()
	result, err := store.ApplySet(ctx, appID, clientID, map[string]any{
		"userId": "user_1",
		"junk":   "dropped by the allowlist",
	}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"userId": "user_1"}, result.Device.Metadata)
	require.Equal(t, []string{"junk"}, result.DroppedKeys)

	// Second identify adds a key and keeps the first one (per-key merge).
	result, err = store.ApplySet(ctx, appID, clientID, map[string]any{"tenant": "acme"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"userId": "user_1", "tenant": "acme"}, result.Device.Metadata)

	// Changing a value moves the device count from the old value to the new
	// one and prunes the emptied row.
	_, err = store.ApplySet(ctx, appID, clientID, map[string]any{"tenant": "globex"}, nil)
	require.NoError(t, err)
	values, err := store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "globex", DeviceCount: 1}}, values)

	// Re-identifying the same value must not inflate the count.
	_, err = store.ApplySet(ctx, appID, clientID, map[string]any{"tenant": "globex"}, nil)
	require.NoError(t, err)
	values, err = store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "globex", DeviceCount: 1}}, values)

	device, err := store.GetDevice(ctx, appID, clientID)
	require.NoError(t, err)
	require.NotNil(t, device)
	require.Equal(t, "globex", device.Metadata["tenant"])

	missing, err := store.GetDevice(ctx, appID, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, missing)

	_, err = store.ApplySet(ctx, appID, "not-a-uuid", map[string]any{}, nil)
	require.Error(t, err)
}

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }

func TestApplySetGeoCoalesce(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	clientID := uuid.NewString()

	fullGeo := &Geo{CountryCode: strPtr("FR"), City: strPtr("Paris"), Lat: floatPtr(48.85), Lng: floatPtr(2.35)}
	result, err := store.ApplySet(ctx, appID, clientID, nil, fullGeo)
	require.NoError(t, err)
	require.NotNil(t, result.Device.CountryCode)
	require.Equal(t, "FR", *result.Device.CountryCode)

	// An identify that resolves no geo keeps the previously known location.
	result, err = store.ApplySet(ctx, appID, clientID, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result.Device.CountryCode)
	require.Equal(t, "FR", *result.Device.CountryCode)
	require.NotNil(t, result.Device.Lat)
	require.InDelta(t, 48.85, *result.Device.Lat, 0.001)

	// A PARTIAL resolution (country-only is the common GeoLite2 case) updates
	// what it knows and never blanks the rest with '' or 0/0.
	result, err = store.ApplySet(ctx, appID, clientID, nil, &Geo{CountryCode: strPtr("BE")})
	require.NoError(t, err)
	require.Equal(t, "BE", *result.Device.CountryCode)
	require.NotNil(t, result.Device.City)
	require.Equal(t, "Paris", *result.Device.City)
	require.NotNil(t, result.Device.Lat)
	require.InDelta(t, 48.85, *result.Device.Lat, 0.001)
}

func TestSearchMetadataValuesRankingAndFilter(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "tenant", ValueTypeString)

	seed := map[string]int{"acme": 3, "acme-eu": 2, "globex": 1}
	for tenant, devices := range seed {
		for i := 0; i < devices; i++ {
			_, err := store.ApplySet(ctx, appID, uuid.NewString(), map[string]any{"tenant": tenant}, nil)
			require.NoError(t, err)
		}
	}

	// Empty search: top values by device count.
	values, err := store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "acme", DeviceCount: 3}, {Value: "acme-eu", DeviceCount: 2}, {Value: "globex", DeviceCount: 1}}, values)

	// Case-insensitive substring narrows, ranking is preserved.
	values, err = store.SearchMetadataValues(ctx, appID, "tenant", "ACME", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "acme", DeviceCount: 3}, {Value: "acme-eu", DeviceCount: 2}}, values)

	// Limit applies after ranking.
	values, err = store.SearchMetadataValues(ctx, appID, "tenant", "", 1)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "acme", DeviceCount: 3}}, values)

	// Unknown key: no rows, no error.
	values, err = store.SearchMetadataValues(ctx, appID, "nope", "", 10)
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestDeleteSchemaKeyWipesItsStats(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "tenant", ValueTypeString)
	declareKey(t, store, appID, "plan", ValueTypeString)

	_, err := store.ApplySet(ctx, appID, uuid.NewString(), map[string]any{"tenant": "acme", "plan": "pro"}, nil)
	require.NoError(t, err)

	deleted, err := store.DeleteSchemaKey(ctx, appID, "tenant")
	require.NoError(t, err)
	require.True(t, deleted)

	// The removed key stops being suggested; the surviving key is untouched.
	values, err := store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Empty(t, values)
	values, err = store.SearchMetadataValues(ctx, appID, "plan", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "pro", DeviceCount: 1}}, values)

	// And its values are no longer accepted on the next identify.
	result, err := store.ApplySet(ctx, appID, uuid.NewString(), map[string]any{"tenant": "acme"}, nil)
	require.NoError(t, err)
	require.Empty(t, result.Device.Metadata)
	require.Equal(t, []string{"tenant"}, result.DroppedKeys)
}

// Concurrent first identifies of the same install must both land: the
// insert-then-lock sequence serializes the merges, so neither metadata write
// nor stat increment is lost.
func TestApplySetConcurrentFirstWrite(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "userId", ValueTypeString)
	declareKey(t, store, appID, "tenant", ValueTypeString)

	clientID := uuid.NewString()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = store.ApplySet(ctx, appID, clientID, map[string]any{"userId": "user_1"}, nil)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = store.ApplySet(ctx, appID, clientID, map[string]any{"tenant": "acme"}, nil)
	}()
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	device, err := store.GetDevice(ctx, appID, clientID)
	require.NoError(t, err)
	require.NotNil(t, device)
	require.Equal(t, map[string]any{"userId": "user_1", "tenant": "acme"}, device.Metadata)

	// The stat increments must survive the serialization too: a lost or
	// double-counted increment would pass a metadata-only assertion.
	values, err := store.SearchMetadataValues(ctx, appID, "userId", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "user_1", DeviceCount: 1}}, values)
	values, err = store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "acme", DeviceCount: 1}}, values)
}

// Two identifies of DIFFERENT devices sharing stat rows (same tenant/plan
// values) must not deadlock: the store orders its stat-row locks by
// (key, value) precisely for this. Before that ordering, this test deadlocked
// within a handful of iterations (40P01 after the 1s deadlock_timeout).
func TestApplySetConcurrentSharedStatRowsNoDeadlock(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "tenant", ValueTypeString)
	declareKey(t, store, appID, "plan", ValueTypeString)
	declareKey(t, store, appID, "region", ValueTypeString)

	deviceA, deviceB := uuid.NewString(), uuid.NewString()
	payload := map[string]any{"tenant": "acme", "plan": "pro", "region": "eu"}
	// Alternating payload so decrements and increments cross between rounds.
	alternate := map[string]any{"tenant": "globex", "plan": "free", "region": "us"}

	const rounds = 40
	var wg sync.WaitGroup
	errsA := make([]error, rounds)
	errsB := make([]error, rounds)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			p := payload
			if i%2 == 1 {
				p = alternate
			}
			if _, err := store.ApplySet(ctx, appID, deviceA, p, nil); err != nil {
				errsA[i] = err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			p := alternate
			if i%2 == 1 {
				p = payload
			}
			if _, err := store.ApplySet(ctx, appID, deviceB, p, nil); err != nil {
				errsB[i] = err
				return
			}
		}
	}()
	wg.Wait()
	for i := 0; i < rounds; i++ {
		require.NoError(t, errsA[i], "device A round %d", i)
		require.NoError(t, errsB[i], "device B round %d", i)
	}

	// Both devices ran an even number of rounds, so A ends on `alternate` and
	// B ends on `payload`: every value should count exactly one device.
	for key, want := range map[string][]ValueCount{
		"tenant": {{Value: "acme", DeviceCount: 1}, {Value: "globex", DeviceCount: 1}},
		"plan":   {{Value: "free", DeviceCount: 1}, {Value: "pro", DeviceCount: 1}},
		"region": {{Value: "eu", DeviceCount: 1}, {Value: "us", DeviceCount: 1}},
	} {
		values, err := store.SearchMetadataValues(ctx, appID, key, "", 10)
		require.NoError(t, err)
		require.ElementsMatch(t, want, values, "key %s", key)
	}
}

// A number-typed key must round-trip through JSONB without corrupting the
// stat bookkeeping: 42 stored then re-read as float64 must compare equal to
// an incoming 42 (no phantom dec/inc), and a real change must move the count.
func TestApplySetNumberRoundtrip(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "seats", ValueTypeNumber)

	clientID := uuid.NewString()
	_, err := store.ApplySet(ctx, appID, clientID, map[string]any{"seats": int64(42)}, nil)
	require.NoError(t, err)
	_, err = store.ApplySet(ctx, appID, clientID, map[string]any{"seats": float64(42)}, nil)
	require.NoError(t, err)
	values, err := store.SearchMetadataValues(ctx, appID, "seats", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "42", DeviceCount: 1}}, values)

	_, err = store.ApplySet(ctx, appID, clientID, map[string]any{"seats": 42.5}, nil)
	require.NoError(t, err)
	values, err = store.SearchMetadataValues(ctx, appID, "seats", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "42.5", DeviceCount: 1}}, values)
}

func TestApplySetOnce(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "initialReferrer", ValueTypeString)
	declareKey(t, store, appID, "plan", ValueTypeString)

	clientID := uuid.NewString()
	result, err := store.ApplySetOnce(ctx, appID, clientID, map[string]any{"initialReferrer": "organic"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"initialReferrer": "organic"}, result.Device.Metadata)

	// A second set_once on a held key is silently ignored; absent keys apply.
	result, err = store.ApplySetOnce(ctx, appID, clientID, map[string]any{"initialReferrer": "paid", "plan": "pro"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"initialReferrer": "organic", "plan": "pro"}, result.Device.Metadata)

	// The ignored write must not have touched the stats either.
	values, err := store.SearchMetadataValues(ctx, appID, "initialReferrer", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "organic", DeviceCount: 1}}, values)

	// $set still overwrites what $set_once pinned.
	result, err = store.ApplySet(ctx, appID, clientID, map[string]any{"initialReferrer": "paid"}, nil)
	require.NoError(t, err)
	require.Equal(t, "paid", result.Device.Metadata["initialReferrer"])
}

func TestApplyUnset(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "userId", ValueTypeString)
	declareKey(t, store, appID, "tenant", ValueTypeString)

	clientID := uuid.NewString()
	_, err := store.ApplySet(ctx, appID, clientID, map[string]any{"userId": "user_1", "tenant": "acme"}, nil)
	require.NoError(t, err)
	// A second device holds the same userId value so the count sits at 2:
	// without the payload dedupe, the duplicated key below would decrement
	// twice, hit zero, and wrongly prune this survivor's count.
	survivor := uuid.NewString()
	_, err = store.ApplySet(ctx, appID, survivor, map[string]any{"userId": "user_1"}, nil)
	require.NoError(t, err)

	// Unset removes the key, decrements its stat once, and ignores
	// duplicated and unknown keys in the payload.
	result, err := store.ApplyUnset(ctx, appID, clientID, []string{"userId", "userId", "neverSeen"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"tenant": "acme"}, result.Device.Metadata)
	values, err := store.SearchMetadataValues(ctx, appID, "userId", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "user_1", DeviceCount: 1}}, values)

	// Unsetting the survivor takes the count to zero and prunes the row.
	_, err = store.ApplyUnset(ctx, appID, survivor, []string{"userId"}, nil)
	require.NoError(t, err)
	values, err = store.SearchMetadataValues(ctx, appID, "userId", "", 10)
	require.NoError(t, err)
	require.Empty(t, values)
	values, err = store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.Equal(t, []ValueCount{{Value: "acme", DeviceCount: 1}}, values)

	// Unset still works for a key removed from the allowlist: cleanup path.
	deleted, err := store.DeleteSchemaKey(ctx, appID, "tenant")
	require.NoError(t, err)
	require.True(t, deleted)
	result, err = store.ApplyUnset(ctx, appID, clientID, []string{"tenant"}, nil)
	require.NoError(t, err)
	require.Empty(t, result.Device.Metadata)

	// Unsetting on a never-seen device just creates the empty row, no error.
	fresh := uuid.NewString()
	result, err = store.ApplyUnset(ctx, appID, fresh, []string{"userId"}, nil)
	require.NoError(t, err)
	require.Empty(t, result.Device.Metadata)
}

func TestUpsertSchemaKeyCap(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()

	for i := 0; i < MaxSchemaKeys; i++ {
		_, err := store.UpsertSchemaKey(ctx, appID, KeySpec{Key: fmt.Sprintf("key%d", i), Type: ValueTypeString})
		require.NoError(t, err)
	}
	// The 101st key is rejected with the typed sentinel...
	_, err := store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "overflow", Type: ValueTypeString})
	require.ErrorIs(t, err, ErrTooManySchemaKeys)
	// ...but re-declaring an existing key at the cap still works.
	_, err = store.UpsertSchemaKey(ctx, appID, KeySpec{Key: "key0", Type: ValueTypeNumber})
	require.NoError(t, err)
}

func TestListDevicesPaginationAndFilter(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	declareKey(t, store, appID, "tenant", ValueTypeString)

	// Seed devices with staggered last_seen_at (later ApplySet = more recent).
	var ids []string
	for i := 0; i < 5; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		tenant := "acme"
		if i%2 == 1 {
			tenant = "globex"
		}
		_, err := store.ApplySet(ctx, appID, id, map[string]any{"tenant": tenant}, nil)
		require.NoError(t, err)
	}

	// Full unfiltered listing, newest-first, paginated 2 at a time.
	var seen []string
	var cursor *DeviceCursor
	for {
		devices, next, err := store.ListDevices(ctx, appID, nil, 2, cursor)
		require.NoError(t, err)
		for _, d := range devices {
			seen = append(seen, d.EASClientID)
		}
		if next == nil {
			break
		}
		cursor = next
		require.LessOrEqual(t, len(seen), 5, "pagination must terminate")
	}
	require.Len(t, seen, 5)
	// Newest-first: the last-seeded device comes first.
	require.Equal(t, ids[4], seen[0])
	// No duplicates across pages.
	require.Len(t, uniqueStrings(seen), 5)

	// Filter to tenant=globex (devices 1 and 3): 2 of them.
	filtered, next, err := store.ListDevices(ctx, appID, &MetadataFilter{Key: "tenant", Value: "globex"}, 10, nil)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Len(t, filtered, 2)
	for _, d := range filtered {
		require.Equal(t, "globex", d.Metadata["tenant"])
	}

	// A filter matching nothing returns an empty page.
	none, _, err := store.ListDevices(ctx, appID, &MetadataFilter{Key: "tenant", Value: "nope"}, 10, nil)
	require.NoError(t, err)
	require.Empty(t, none)
}

// When many devices share the exact same last_seen_at (the likely case: a
// burst of identifies), pagination must fall back on the eas_client_id
// tiebreaker and still return every row once. Sequential ApplySet calls get
// distinct timestamps, so force a tie with a direct UPDATE.
func TestListDevicesKeysetUnderTies(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		_, err := store.ApplySet(ctx, appID, uuid.NewString(), map[string]any{}, nil)
		require.NoError(t, err)
	}
	// Pin all six rows to the same instant.
	_, err := pool.Exec(ctx,
		"UPDATE device_identity SET last_seen_at = '2026-07-23T10:00:00Z' WHERE app_id = $1", appID)
	require.NoError(t, err)

	var seen []string
	var cursor *DeviceCursor
	for {
		devices, next, err := store.ListDevices(ctx, appID, nil, 2, cursor)
		require.NoError(t, err)
		for _, d := range devices {
			seen = append(seen, d.EASClientID)
		}
		if next == nil {
			break
		}
		cursor = next
		require.LessOrEqual(t, len(seen), 6, "pagination must terminate under ties")
	}
	// All six, each exactly once, despite identical last_seen_at.
	require.Len(t, seen, 6)
	require.Len(t, uniqueStrings(seen), 6)
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// unlicensedStore builds a store with the free-tier cap active at a small
// limit so the eviction path is testable without seeding a thousand rows.
func unlicensedStore(pool *pgxpool.Pool, limit int) *PostgresIdentityStore {
	s := NewPostgresIdentityStore(&database.Engine{Queries: pgdb.New(pool), DB: pool})
	s.licenseValid = func() bool { return false }
	s.deviceLimit = limit
	return s
}

func TestFreeTierCapEvictsOldest(t *testing.T) {
	_, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	store := unlicensedStore(pool, 3)
	declareKey(t, store, appID, "tenant", ValueTypeString)

	// Register 3 devices (at the cap). Stagger last_seen via distinct txns.
	ids := make([]string, 4)
	for i := 0; i < 3; i++ {
		ids[i] = uuid.NewString()
		_, err := store.ApplySet(ctx, appID, ids[i], map[string]any{"tenant": "acme"}, nil)
		require.NoError(t, err)
	}
	count, err := store.engine.Queries.CountDevices(ctx, mustPgUUID(t, appID))
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// A 4th device evicts the oldest (ids[0]); count stays at the cap.
	ids[3] = uuid.NewString()
	_, err = store.ApplySet(ctx, appID, ids[3], map[string]any{"tenant": "globex"}, nil)
	require.NoError(t, err)

	count, err = store.engine.Queries.CountDevices(ctx, mustPgUUID(t, appID))
	require.NoError(t, err)
	require.Equal(t, int64(3), count, "cap holds the app at the limit")

	// The oldest is gone, the newest is present.
	evicted, err := store.GetDevice(ctx, appID, ids[0])
	require.NoError(t, err)
	require.Nil(t, evicted, "the oldest device was evicted")
	newest, err := store.GetDevice(ctx, appID, ids[3])
	require.NoError(t, err)
	require.NotNil(t, newest)

	// Value stats followed the eviction: acme went 3 -> 2 (one evicted),
	// globex is 1. No ghost counts from the evicted device.
	values, err := store.SearchMetadataValues(ctx, appID, "tenant", "", 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []ValueCount{{Value: "acme", DeviceCount: 2}, {Value: "globex", DeviceCount: 1}}, values)
}

func TestLicensedStoreHasNoCap(t *testing.T) {
	baseline, pool := setupIdentityStore(t) // alwaysLicensed
	appID := seedApp(t, pool)
	ctx := context.Background()
	// Same tiny limit, but licensed → cap inert.
	licensed := NewPostgresIdentityStore(&database.Engine{Queries: pgdb.New(pool), DB: pool})
	licensed.licenseValid = func() bool { return true }
	licensed.deviceLimit = 3
	_ = baseline

	for i := 0; i < 6; i++ {
		_, err := licensed.ApplySet(ctx, appID, uuid.NewString(), map[string]any{}, nil)
		require.NoError(t, err)
	}
	count, err := licensed.engine.Queries.CountDevices(ctx, mustPgUUID(t, appID))
	require.NoError(t, err)
	require.Equal(t, int64(6), count, "a valid license lifts the cap")
}

// Updates to existing devices must not trigger eviction (only new inserts do).
func TestFreeTierCapIgnoresUpdates(t *testing.T) {
	_, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	store := unlicensedStore(pool, 3)
	declareKey(t, store, appID, "tenant", ValueTypeString)

	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		ids[i] = uuid.NewString()
		_, err := store.ApplySet(ctx, appID, ids[i], map[string]any{"tenant": "acme"}, nil)
		require.NoError(t, err)
	}
	// Re-identify the OLDEST many times: it stays (updates don't evict), and
	// no device is dropped.
	for i := 0; i < 5; i++ {
		_, err := store.ApplySet(ctx, appID, ids[0], map[string]any{"tenant": "acme"}, nil)
		require.NoError(t, err)
	}
	count, err := store.engine.Queries.CountDevices(ctx, mustPgUUID(t, appID))
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
	for _, id := range ids {
		d, err := store.GetDevice(ctx, appID, id)
		require.NoError(t, err)
		require.NotNil(t, d, "no device evicted on updates")
	}
}

func mustPgUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	u, err := toPgUUID(id)
	require.NoError(t, err)
	return u
}

func TestTouchDeviceMonthlyQuota(t *testing.T) {
	licensedStore, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	store := unlicensedStore(pool, 2)

	d1, d2, d3 := uuid.NewString(), uuid.NewString(), uuid.NewString()

	// Two devices claim the month's slots...
	tracked, err := store.TouchDevice(ctx, appID, d1, nil)
	require.NoError(t, err)
	require.True(t, tracked)
	tracked, err = store.TouchDevice(ctx, appID, d2, nil)
	require.NoError(t, err)
	require.True(t, tracked)

	// ...the month is full: a third is refused and gets NO row.
	tracked, err = store.TouchDevice(ctx, appID, d3, nil)
	require.NoError(t, err)
	require.False(t, tracked)
	ghost, err := store.GetDevice(ctx, appID, d3)
	require.NoError(t, err)
	require.Nil(t, ghost, "a refused device must not create a row")

	// Slot-holders keep bumping freely at the full quota.
	tracked, err = store.TouchDevice(ctx, appID, d1, nil)
	require.NoError(t, err)
	require.True(t, tracked)

	// d1's activity slides into the previous month (simulated month
	// boundary): its slot frees, d3 claims it...
	_, err = pool.Exec(ctx,
		"UPDATE device_identity SET last_seen_at = date_trunc('month', CURRENT_TIMESTAMP) - INTERVAL '1 day' WHERE app_id = $1 AND eas_client_id = $2",
		appID, d1)
	require.NoError(t, err)
	tracked, err = store.TouchDevice(ctx, appID, d3, nil)
	require.NoError(t, err)
	require.True(t, tracked)

	// ...and d1, back from its silent month with the quota full again, is
	// refused WITHOUT its last_seen being bumped: a refused device must not
	// become "active this month" while being dropped, and its row (metadata
	// included) survives for the next month.
	tracked, err = store.TouchDevice(ctx, appID, d1, nil)
	require.NoError(t, err)
	require.False(t, tracked)
	dormant, err := store.GetDevice(ctx, appID, d1)
	require.NoError(t, err)
	require.NotNil(t, dormant, "the refused row must survive")
	require.True(t, dormant.LastSeenAt.Before(time.Now().AddDate(0, 0, -1)), "refusal must not bump last_seen")

	// A licensed deployment has no quota.
	tracked, err = licensedStore.TouchDevice(ctx, appID, uuid.NewString(), nil)
	require.NoError(t, err)
	require.True(t, tracked)
}

func TestTouchDeviceGeoCoalesce(t *testing.T) {
	store, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()

	deviceID := uuid.NewString()
	country := "FR"
	tracked, err := store.TouchDevice(ctx, appID, deviceID, &Geo{CountryCode: &country})
	require.NoError(t, err)
	require.True(t, tracked)

	// A later contact resolving no geo must not erase the known one.
	_, err = store.TouchDevice(ctx, appID, deviceID, nil)
	require.NoError(t, err)
	device, err := store.GetDevice(ctx, appID, deviceID)
	require.NoError(t, err)
	require.NotNil(t, device)
	require.NotNil(t, device.CountryCode)
	require.Equal(t, "FR", *device.CountryCode)
}

// The advisory lock's contract: concurrent registrations cannot overshoot
// the monthly quota. 20 distinct devices race for 5 slots; exactly 5 must be
// tracked and exactly 5 rows exist. Without the per-app lock, racers count a
// stale total and the quota leaks (this test then fails with >5 tracked).
func TestTouchDeviceConcurrentRegistrationsExactQuota(t *testing.T) {
	_, pool := setupIdentityStore(t)
	appID := seedApp(t, pool)
	ctx := context.Background()
	store := unlicensedStore(pool, 5)

	var wg sync.WaitGroup
	var trackedCount atomic.Int32
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracked, err := store.TouchDevice(ctx, appID, uuid.NewString(), nil)
			require.NoError(t, err)
			if tracked {
				trackedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, 5, trackedCount.Load(), "the quota must be exact under concurrency")
	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM device_identity WHERE app_id = $1", appID).Scan(&rows))
	require.Equal(t, 5, rows)
}
