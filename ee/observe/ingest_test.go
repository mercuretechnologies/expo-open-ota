// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"expo-open-ota/ee/identity"
	"expo-open-ota/internal/database"
	"expo-open-ota/internal/database/postgres"
	"expo-open-ota/internal/database/postgres/pgdb"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// serveIngest routes a request through the handler ONLY (no middleware), so
// handler-contract tests are not perturbed by the rate limiter or the app
// resolver. The middleware chain has its own tests.
func serveIngest(handler *IngestHandler, method, path string, body []byte) *httptest.ResponseRecorder {
	router := mux.NewRouter()
	router.HandleFunc("/observe/{APP_ID}/{PROJECT_ID}/v1/logs", handler.HandleLogs).Methods(http.MethodPost)
	router.HandleFunc("/observe/{APP_ID}/{PROJECT_ID}/v1/metrics", handler.HandleMetrics).Methods(http.MethodPost)
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:40000"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

type recordingMutator struct {
	// The embedded Store supplies the dashboard query methods (never called on
	// the ingest path) so the fake satisfies identity.Store; only the three
	// write methods below are exercised.
	identity.Store
	sets   []map[string]any
	unsets [][]string
	fail   bool
	// hadDeadline proves the HTTP handler bounds each store operation.
	hadDeadline bool
}

func (m *recordingMutator) ApplySet(ctx context.Context, _ string, _ string, raw map[string]any, _ *identity.Geo) (identity.ApplyResult, error) {
	_, m.hadDeadline = ctx.Deadline()
	if m.fail {
		return identity.ApplyResult{}, fmt.Errorf("database is down")
	}
	m.sets = append(m.sets, raw)
	return identity.ApplyResult{}, nil
}

func (m *recordingMutator) ApplySetOnce(_ context.Context, _ string, _ string, raw map[string]any, _ *identity.Geo) (identity.ApplyResult, error) {
	return identity.ApplyResult{}, nil
}

func (m *recordingMutator) ApplyUnset(_ context.Context, _ string, _ string, keys []string, _ *identity.Geo) (identity.ApplyResult, error) {
	if m.fail {
		return identity.ApplyResult{}, fmt.Errorf("database is down")
	}
	m.unsets = append(m.unsets, keys)
	return identity.ApplyResult{}, nil
}

const logsPath = "/observe/app-1/ignored-project/v1/logs"

func TestHandleLogsResponseContract(t *testing.T) {
	t.Run("nil service acknowledges and drops", func(t *testing.T) {
		recorder := serveIngest(NewIngestHandler(nil, nil, nil, nil), http.MethodPost, logsPath, []byte(androidLogsFixture))
		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("unreadable body is a permanent 400", func(t *testing.T) {
		handler := NewIngestHandler(identity.NewService(&recordingMutator{}, nil), nil, nil, nil)
		recorder := serveIngest(handler, http.MethodPost, logsPath, []byte("not json"))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("oversized body is a permanent 413", func(t *testing.T) {
		handler := NewIngestHandler(identity.NewService(&recordingMutator{}, nil), nil, nil, nil)
		big := bytes.Repeat([]byte("x"), maxLogsBodyBytes+1)
		recorder := serveIngest(handler, http.MethodPost, logsPath, big)
		require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	})

	t.Run("store failure is a retryable 503, never 500", func(t *testing.T) {
		handler := NewIngestHandler(identity.NewService(&recordingMutator{fail: true}, nil), nil, nil, nil)
		recorder := serveIngest(handler, http.MethodPost, logsPath, []byte(androidLogsFixture))
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	})

	t.Run("telemetry-only batch is acknowledged untouched", func(t *testing.T) {
		mutator := &recordingMutator{}
		handler := NewIngestHandler(identity.NewService(mutator, nil), nil, nil, nil)
		body := strings.ReplaceAll(androidLogsFixture, "$set", "exception")
		recorder := serveIngest(handler, http.MethodPost, logsPath, []byte(body))
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Empty(t, mutator.sets)
	})

	t.Run("forged client id skips records without failing the batch", func(t *testing.T) {
		mutator := &recordingMutator{}
		handler := NewIngestHandler(identity.NewService(mutator, nil), nil, nil, nil)
		body := strings.ReplaceAll(androidLogsFixture, "8b9c1fe0-93b3-4b3a-8c1d-2f4a5e6b7c8d", "not-a-uuid")
		recorder := serveIngest(handler, http.MethodPost, logsPath, []byte(body))
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Empty(t, mutator.sets)
	})

	t.Run("identity ops reach the service", func(t *testing.T) {
		mutator := &recordingMutator{}
		handler := NewIngestHandler(identity.NewService(mutator, nil), nil, nil, nil)
		recorder := serveIngest(handler, http.MethodPost, logsPath, []byte(androidLogsFixture))
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.True(t, mutator.hadDeadline, "each identity apply must have a request-scoped deadline")
		require.Len(t, mutator.sets, 1)
		require.Equal(t, "user_42", mutator.sets[0]["userId"])
		// The envelope attributes were stripped before the store.
		require.NotContains(t, mutator.sets[0], "event.name")
		require.NotContains(t, mutator.sets[0], "session.id")

		recorder = serveIngest(handler, http.MethodPost, logsPath, []byte(iosLogsFixture))
		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, [][]string{{"userId", "tenant"}}, mutator.unsets)
	})

	t.Run("metrics stub acknowledges and drops", func(t *testing.T) {
		recorder := serveIngest(NewIngestHandler(nil, nil, nil, nil), http.MethodPost, "/observe/app-1/p/v1/metrics", []byte(`{"resourceMetrics":[]}`))
		require.Equal(t, http.StatusNoContent, recorder.Code)
	})
}

// End-to-end against a real Postgres: an SDK-shaped batch lands as a device
// row. Gated like the identity store tests.
func TestIngestEndToEnd(t *testing.T) {
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
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	appID := uuid.NewString()
	_, err = pool.Exec(context.Background(), "INSERT INTO apps (id, name) VALUES ($1, $2)", appID, "observe-e2e")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM apps WHERE id = $1", appID) })

	identityStore := identity.NewPostgresIdentityStore(&database.Engine{Queries: pgdb.New(pool), DB: pool})
	for _, spec := range []identity.KeySpec{
		{Key: "userId", Type: identity.ValueTypeString},
		{Key: "seats", Type: identity.ValueTypeNumber},
		{Key: "isInternal", Type: identity.ValueTypeBoolean},
	} {
		_, err := identityStore.UpsertSchemaKey(context.Background(), appID, spec)
		require.NoError(t, err)
	}

	handler := NewIngestHandler(identity.NewService(identityStore, nil), nil, nil, nil)
	path := "/observe/" + appID + "/whatever-project/v1/logs"
	recorder := serveIngest(handler, http.MethodPost, path, []byte(androidLogsFixture))
	require.Equal(t, http.StatusNoContent, recorder.Code)

	device, err := identityStore.GetDevice(context.Background(), appID, "8b9c1fe0-93b3-4b3a-8c1d-2f4a5e6b7c8d")
	require.NoError(t, err)
	require.NotNil(t, device)
	require.Equal(t, "user_42", device.Metadata["userId"])
	require.Equal(t, float64(12), device.Metadata["seats"])
	require.Equal(t, true, device.Metadata["isInternal"])
}

func TestIdentityRequestsFromBatch(t *testing.T) {
	batch, err := DecodeLogs([]byte(androidLogsFixture))
	require.NoError(t, err)
	requests := identityRequestsFromBatch(batch, "app-1", "203.0.113.7")
	require.Len(t, requests, 1)
	require.Equal(t, identity.OpSet, requests[0].Op)
	require.Equal(t, "app-1", requests[0].AppID)
	require.Equal(t, "203.0.113.7", requests[0].RemoteIP)
	require.Equal(t, "user_42", requests[0].Attributes["userId"])

	t.Run("forged client id yields no requests", func(t *testing.T) {
		body := strings.ReplaceAll(androidLogsFixture, "8b9c1fe0-93b3-4b3a-8c1d-2f4a5e6b7c8d", "not-a-uuid")
		b, err := DecodeLogs([]byte(body))
		require.NoError(t, err)
		require.Empty(t, identityRequestsFromBatch(b, "app-1", ""))
	})

	t.Run("telemetry records yield no requests", func(t *testing.T) {
		body := strings.ReplaceAll(androidLogsFixture, "$set", "exception")
		b, err := DecodeLogs([]byte(body))
		require.NoError(t, err)
		require.Empty(t, identityRequestsFromBatch(b, "app-1", ""))
	})
}
