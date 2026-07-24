// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"net/http"
	"strings"
	"time"

	"expo-open-ota/internal/handlers"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

const maxHealthHistoryUpdateIDs = 20

type HealthHistoryReader interface {
	Read(
		ctx context.Context,
		appID string,
		updateIDs []string,
		from, to time.Time,
	) (map[string][]HealthHistoryPoint, error)
}

// HealthHistoryHandler exposes ClickHouse history without making ClickHouse a
// requirement for the dashboard. Deployments without it return available=false
// and keep the PostgreSQL instant-T health endpoint fully operational.
type HealthHistoryHandler struct {
	reader HealthHistoryReader
}

func NewHealthHistoryHandler(reader HealthHistoryReader) *HealthHistoryHandler {
	// A nil *HealthHistory stored in an interface is itself non-nil. Wiring
	// does exactly that when ClickHouse is disabled, so normalize it here
	// before the handler uses the interface.
	if history, ok := reader.(*HealthHistory); ok && history == nil {
		reader = nil
	}
	return &HealthHistoryHandler{reader: reader}
}

func (h *HealthHistoryHandler) GetUpdateHealthHistoryHandler(w http.ResponseWriter, r *http.Request) {
	updateIDs, ok := parseHealthHistoryUpdateIDs(r.URL.Query().Get("ids"))
	if !ok {
		handlers.RenderError(
			w,
			http.StatusBadRequest,
			"'ids' must contain between 1 and 20 unique update UUIDs.",
		)
		return
	}

	to, err := parseHistoryTime(r.URL.Query().Get("to"), time.Now().UTC())
	if err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "'to' must be an RFC3339 timestamp.")
		return
	}
	from, err := parseHistoryTime(r.URL.Query().Get("from"), to.Add(-24*time.Hour))
	if err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "'from' must be an RFC3339 timestamp.")
		return
	}
	if !from.Before(to) {
		handlers.RenderError(w, http.StatusBadRequest, "'from' must be earlier than 'to'.")
		return
	}

	if h.reader == nil {
		handlers.RenderJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"updates":   map[string][]HealthHistoryPoint{},
		})
		return
	}

	points, err := h.reader.Read(r.Context(), mux.Vars(r)["APP_ID"], updateIDs, from, to)
	if err != nil {
		handlers.RenderError(w, http.StatusInternalServerError, "An internal error occurred.")
		return
	}
	handlers.RenderJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"updates":   points,
	})
}

func parseHealthHistoryUpdateIDs(raw string) ([]string, bool) {
	seen := make(map[string]struct{})
	updateIDs := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		updateID := strings.TrimSpace(part)
		parsed, err := uuid.Parse(updateID)
		if err != nil {
			return nil, false
		}
		canonical := parsed.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		updateIDs = append(updateIDs, canonical)
		if len(updateIDs) > maxHealthHistoryUpdateIDs {
			return nil, false
		}
	}
	return updateIDs, len(updateIDs) > 0
}

func parseHistoryTime(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	return time.Parse(time.RFC3339, raw)
}
