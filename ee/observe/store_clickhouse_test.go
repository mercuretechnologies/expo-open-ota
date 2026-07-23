// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"expo-open-ota/internal/database/clickhouse"
	"expo-open-ota/internal/database/postgres/pgtest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ClickHouse migration lock lives on TEST_DATABASE_URL, so this package
// follows the pgtest serialization convention like every package touching it.
func TestMain(m *testing.M) { os.Exit(pgtest.RunSerialized(m)) }

// Needs real servers, like the clickhouse package's migration test: set
// TEST_CLICKHOUSE_URL and TEST_DATABASE_URL (the migration lock is a Postgres
// advisory lock) to run. A random app id isolates each run, so no cleanup:
// ClickHouse deletes are async mutations, not something a test should wait on.
func TestClickHouseTelemetrySinkRoundTrip(t *testing.T) {
	chURL := os.Getenv("TEST_CLICKHOUSE_URL")
	pgURL := os.Getenv("TEST_DATABASE_URL")
	if chURL == "" || pgURL == "" {
		t.Skip("TEST_CLICKHOUSE_URL and TEST_DATABASE_URL not both set; skipping sink test")
	}
	clickhouse.RunDBMigrations(chURL, pgURL)

	ctx := context.Background()
	engine, err := clickhouse.NewClickHouseEngine(ctx, chURL)
	require.NoError(t, err)
	defer engine.Close()
	sink := NewClickHouseTelemetrySink(engine)

	appID := uuid.NewString()
	now := time.Now().UTC()

	metricBatch, err := DecodeMetrics(loadFixture(t, "ios_metrics.json"))
	require.NoError(t, err)
	metricRows := FlattenMetrics(appID, metricBatch, now)
	require.NotEmpty(t, metricRows)
	for i := range metricRows {
		metricRows[i].Branch = "main"
	}
	require.NoError(t, sink.InsertMetrics(ctx, metricRows))

	logBatch, err := DecodeLogs(loadFixture(t, "ios_logs.json"))
	require.NoError(t, err)
	logRows := FlattenLogs(appID, logBatch, now)
	require.NotEmpty(t, logRows)
	require.NoError(t, sink.InsertLogs(ctx, logRows))

	var metricCount, logCount uint64
	require.NoError(t, engine.Conn.QueryRow(ctx,
		"SELECT count() FROM observe_metrics WHERE app_id = ?", appID).Scan(&metricCount))
	require.EqualValues(t, len(metricRows), metricCount)
	require.NoError(t, engine.Conn.QueryRow(ctx,
		"SELECT count() FROM observe_logs WHERE app_id = ?", appID).Scan(&logCount))
	require.EqualValues(t, len(logRows), logCount)

	// Spot-check one metric row end to end: enrichment and column mapping.
	var branch, updateID, platform string
	var value float64
	require.NoError(t, engine.Conn.QueryRow(ctx, `
		SELECT branch, toString(update_id), platform, value
		FROM observe_metrics
		WHERE app_id = ? AND metric_name = 'expo.app_startup.tti'`, appID,
	).Scan(&branch, &updateID, &platform, &value))
	assert.Equal(t, "main", branch)
	assert.Equal(t, "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10", updateID)
	assert.Equal(t, "ios", platform)
	assert.InDelta(t, 1.842, value, 0.0001)

	// The fatal exception is queryable the way the dashboard will ask for it.
	var fatalCount uint64
	require.NoError(t, engine.Conn.QueryRow(ctx, `
		SELECT count() FROM observe_logs
		WHERE app_id = ? AND event_name = 'exception' AND is_fatal = 1`, appID).Scan(&fatalCount))
	require.EqualValues(t, 1, fatalCount)

	// A published-SDK retry re-sends the identical batch: rows double, but
	// uniqExact(content_hash) holds. This is the query-time dedup contract.
	require.NoError(t, sink.InsertLogs(ctx, logRows))
	var total, distinct uint64
	require.NoError(t, engine.Conn.QueryRow(ctx, `
		SELECT count(), uniqExact(content_hash) FROM observe_logs WHERE app_id = ?`, appID,
	).Scan(&total, &distinct))
	assert.EqualValues(t, 2*len(logRows), total)
	assert.EqualValues(t, len(logRows), distinct)
}
