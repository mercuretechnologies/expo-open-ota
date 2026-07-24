// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

type recordingHealthHistoryReader struct {
	appID     string
	updateIDs []string
	from      time.Time
	to        time.Time
	points    map[string][]HealthHistoryPoint
}

func (r *recordingHealthHistoryReader) Read(
	_ context.Context,
	appID string,
	updateIDs []string,
	from, to time.Time,
) (map[string][]HealthHistoryPoint, error) {
	r.appID = appID
	r.updateIDs = updateIDs
	r.from = from
	r.to = to
	return r.points, nil
}

func serveHealthHistory(handler *HealthHistoryHandler, appID, query string) *httptest.ResponseRecorder {
	router := mux.NewRouter()
	router.HandleFunc(
		"/api/apps/{APP_ID}/identity/update-health/history",
		handler.GetUpdateHealthHistoryHandler,
	).Methods(http.MethodGet)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/apps/"+appID+"/identity/update-health/history?"+query,
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestHealthHistoryHandlerReturnsUnavailableWithoutClickHouse(t *testing.T) {
	recorder := serveHealthHistory(
		NewHealthHistoryHandler(nil),
		uuid.NewString(),
		"ids="+uuid.NewString(),
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"available":false,"updates":{}}`, recorder.Body.String())
}

func TestHealthHistoryHandlerReadsRequestedWindow(t *testing.T) {
	appID := uuid.NewString()
	updateA, updateB := uuid.NewString(), uuid.NewString()
	from := "2026-07-23T10:00:00Z"
	to := "2026-07-24T10:00:00Z"
	reader := &recordingHealthHistoryReader{
		points: map[string][]HealthHistoryPoint{
			updateA: {{Timestamp: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC), Role: "candidate"}},
			updateB: {},
		},
	}

	recorder := serveHealthHistory(
		NewHealthHistoryHandler(reader),
		appID,
		"ids="+updateA+","+updateB+","+updateA+"&from="+from+"&to="+to,
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, appID, reader.appID)
	require.Equal(t, []string{updateA, updateB}, reader.updateIDs)
	require.Equal(t, from, reader.from.Format(time.RFC3339))
	require.Equal(t, to, reader.to.Format(time.RFC3339))
	var response struct {
		Available bool                            `json:"available"`
		Updates   map[string][]HealthHistoryPoint `json:"updates"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Available)
	require.Len(t, response.Updates[updateA], 1)
	require.Empty(t, response.Updates[updateB])
}

func TestHealthHistoryHandlerRejectsInvalidInput(t *testing.T) {
	appID := uuid.NewString()
	updateID := uuid.NewString()
	tests := []string{
		"ids=not-a-uuid",
		"ids=" + updateID + "&from=nope",
		"ids=" + updateID + "&from=2026-07-24T11:00:00Z&to=2026-07-24T10:00:00Z",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			recorder := serveHealthHistory(NewHealthHistoryHandler(nil), appID, query)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
