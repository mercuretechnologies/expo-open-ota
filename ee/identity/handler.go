// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"expo-open-ota/internal/handlers"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// IdentityHandler serves the dashboard "Identity" section: the metadata
// allowlist (schema), value autocomplete, and the device inventory. It wraps
// the same *Service the ingest route uses (handler-over-service, as in
// ee/audit / ee/rbac). A nil service (stateless, no control plane) answers 400.
type IdentityHandler struct {
	service *Service
}

func NewIdentityHandler(service *Service) *IdentityHandler {
	return &IdentityHandler{service: service}
}

// requireService short-circuits with a 400 when identity is not wired:
// stateless mode, or no ClickHouse configured (identity is part of Observe
// and only comes up with it). Returns the service and true when available.
func (h *IdentityHandler) requireService(w http.ResponseWriter) (*Service, bool) {
	if h.service == nil {
		handlers.RenderError(w, http.StatusBadRequest, "Device identity is part of Observe: it requires a control plane (DB_URL) and a configured ClickHouse (CLICKHOUSE_URL).")
		return nil, false
	}
	return h.service, true
}

func renderIdentityServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTooManySchemaKeys):
		handlers.RenderError(w, http.StatusConflict, err.Error())
	default:
		handlers.RenderError(w, http.StatusInternalServerError, "An internal error occurred.")
	}
}

// --- Response shapes (camelCase; timestamps RFC3339 UTC) ---

type schemaKeyResponse struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	MaxLength int    `json:"maxLength"`
}

func schemaKeyResponseFrom(spec KeySpec) schemaKeyResponse {
	return schemaKeyResponse{Key: spec.Key, Type: string(spec.Type), MaxLength: spec.MaxLength}
}

type deviceResponse struct {
	EasClientId string         `json:"easClientId"`
	Metadata    map[string]any `json:"metadata"`
	CountryCode *string        `json:"countryCode,omitempty"`
	City        *string        `json:"city,omitempty"`
	Lat         *float64       `json:"lat,omitempty"`
	Lng         *float64       `json:"lng,omitempty"`
	FirstSeenAt string         `json:"firstSeenAt"`
	LastSeenAt  string         `json:"lastSeenAt"`
}

func deviceResponseFrom(d Device) deviceResponse {
	return deviceResponse{
		EasClientId: d.EASClientID,
		Metadata:    d.Metadata,
		CountryCode: d.CountryCode,
		City:        d.City,
		Lat:         d.Lat,
		Lng:         d.Lng,
		FirstSeenAt: d.FirstSeenAt.UTC().Format(time.RFC3339),
		LastSeenAt:  d.LastSeenAt.UTC().Format(time.RFC3339),
	}
}

// --- Schema (allowlist) ---

func (h *IdentityHandler) GetSchemaHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	schema, err := service.GetSchema(r.Context(), appID)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	// Stable order so the dashboard list does not jitter between reads.
	keys := make([]schemaKeyResponse, 0, len(schema))
	for _, spec := range schema {
		keys = append(keys, schemaKeyResponseFrom(spec))
	}
	sortSchemaKeys(keys)
	handlers.RenderJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *IdentityHandler) UpsertSchemaKeyHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	key := mux.Vars(r)["KEY"]

	var body struct {
		Type      string `json:"type"`
		MaxLength int    `json:"maxLength"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	spec := KeySpec{Key: key, Type: ValueType(body.Type), MaxLength: body.MaxLength}
	if spec.MaxLength == 0 {
		spec.MaxLength = DefaultMaxLength
	}
	// Validate before the store so a bad spec is a clear 400, not a 500.
	if err := ValidateKeySpec(spec); err != nil {
		handlers.RenderError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := service.UpsertSchemaKey(r.Context(), appID, spec)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	handlers.RenderJSON(w, http.StatusOK, schemaKeyResponseFrom(saved))
}

func (h *IdentityHandler) DeleteSchemaKeyHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	key := mux.Vars(r)["KEY"]
	deleted, err := service.DeleteSchemaKey(r.Context(), appID, key)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	if !deleted {
		handlers.RenderError(w, http.StatusNotFound, "No such identity key.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Value autocomplete (searchMetadata) ---

func (h *IdentityHandler) SearchValuesHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	query := r.URL.Query()
	key := query.Get("key")
	if key == "" {
		handlers.RenderError(w, http.StatusBadRequest, "Query parameter 'key' is required.")
		return
	}
	limit := parseLimit(query.Get("limit"), 20)
	values, err := service.SearchMetadataValues(r.Context(), appID, key, query.Get("search"), limit)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	out := make([]ValueCount, 0, len(values))
	out = append(out, values...) // To init with [] instead of nil for the renderJSON
	handlers.RenderJSON(w, http.StatusOK, map[string]any{"values": out})
}

// --- Device inventory ---

func (h *IdentityHandler) ListDevicesHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	query := r.URL.Query()

	var filter *MetadataFilter
	if fk, fv := query.Get("filterKey"), query.Get("filterValue"); fk != "" && fv != "" {
		filter = &MetadataFilter{Key: fk, Value: fv}
	}

	cursor, err := decodeDeviceCursor(query.Get("cursor"))
	if err != nil {
		handlers.RenderError(w, http.StatusBadRequest, "Invalid cursor.")
		return
	}
	limit := parseLimit(query.Get("limit"), DefaultDevicesPageSize)

	devices, next, err := service.ListDevices(r.Context(), appID, filter, limit, cursor)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	items := make([]deviceResponse, 0, len(devices))
	for _, d := range devices {
		items = append(items, deviceResponseFrom(d))
	}
	handlers.RenderJSON(w, http.StatusOK, map[string]any{
		"devices":    items,
		"nextCursor": encodeDeviceCursor(next),
	})
}

func (h *IdentityHandler) GetDeviceHandler(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireService(w)
	if !ok {
		return
	}
	appID := mux.Vars(r)["APP_ID"]
	easClientID := mux.Vars(r)["EAS_CLIENT_ID"]
	// A non-UUID path segment is definitionally not a device: 404, not a 500
	// from the store's uuid parse.
	if _, err := uuid.Parse(easClientID); err != nil {
		handlers.RenderError(w, http.StatusNotFound, "No such device.")
		return
	}
	device, err := service.GetDevice(r.Context(), appID, easClientID)
	if err != nil {
		renderIdentityServiceError(w, err)
		return
	}
	if device == nil {
		handlers.RenderError(w, http.StatusNotFound, "No such device.")
		return
	}
	handlers.RenderJSON(w, http.StatusOK, deviceResponseFrom(*device))
}

// --- helpers ---

func parseLimit(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n // the store clamps to its own bounds
}

func sortSchemaKeys(keys []schemaKeyResponse) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1].Key > keys[j].Key; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
}

// The device cursor is opaque on the wire: base64 of "RFC3339Nano|uuid". The
// client only echoes nextCursor back, it never parses it.
func encodeDeviceCursor(c *DeviceCursor) *string {
	if c == nil {
		return nil
	}
	raw := c.LastSeenAt.UTC().Format(time.RFC3339Nano) + "|" + c.EASClientID
	encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))
	return &encoded
}

func decodeDeviceCursor(encoded string) (*DeviceCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return nil, errors.New("malformed cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, err
	}
	// Validate the uuid here so a tampered cursor is a 400, not a 500 from the
	// store's parse.
	if _, err := uuid.Parse(parts[1]); err != nil {
		return nil, err
	}
	return &DeviceCursor{LastSeenAt: ts, EASClientID: parts[1]}, nil
}
