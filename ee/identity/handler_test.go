// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

// fakeStore is an in-memory identity.Store for handler tests. It defines the
// query/CRUD methods the dashboard exercises; the embedded Store supplies the
// unused ingest mutator methods so it satisfies the interface.
type fakeStore struct {
	Store
	schema  Schema
	devices map[string]*Device
	values  []ValueCount
	// listDevices lets a test control pagination output.
	listDevices func(filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error)
	upsertErr   error
	health      map[string]UpdateHealth
}

func newFakeStore() *fakeStore {
	return &fakeStore{schema: Schema{}, devices: map[string]*Device{}}
}

func (f *fakeStore) GetSchema(_ context.Context, _ string) (Schema, error) { return f.schema, nil }

func (f *fakeStore) UpsertSchemaKey(_ context.Context, _ string, spec KeySpec) (KeySpec, error) {
	if f.upsertErr != nil {
		return KeySpec{}, f.upsertErr
	}
	f.schema[spec.Key] = spec
	return spec, nil
}

func (f *fakeStore) DeleteSchemaKey(_ context.Context, _ string, key string) (bool, error) {
	if _, ok := f.schema[key]; !ok {
		return false, nil
	}
	delete(f.schema, key)
	return true, nil
}

func (f *fakeStore) SearchMetadataValues(_ context.Context, _ string, _ string, _ string, _ int) ([]ValueCount, error) {
	return f.values, nil
}

func (f *fakeStore) ListDevices(_ context.Context, _ string, filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error) {
	if f.listDevices != nil {
		return f.listDevices(filter, limit, cursor)
	}
	return nil, nil, nil
}

func (f *fakeStore) GetDevice(_ context.Context, _ string, easClientID string) (*Device, error) {
	return f.devices[easClientID], nil
}

func (f *fakeStore) UpdateHealthByIDs(_ context.Context, _ string, updateIDs []string) (map[string]UpdateHealth, error) {
	out := make(map[string]UpdateHealth, len(updateIDs))
	for _, id := range updateIDs {
		if h, ok := f.health[id]; ok {
			out[id] = h
		}
	}
	return out, nil
}

// serve routes a request through a real mux router so path vars resolve.
func serve(handler *IdentityHandler, method, path, body string) *httptest.ResponseRecorder {
	router := mux.NewRouter()
	router.HandleFunc("/api/apps/{APP_ID}/identity/schema", handler.GetSchemaHandler).Methods(http.MethodGet)
	router.HandleFunc("/api/apps/{APP_ID}/identity/schema/{KEY}", handler.UpsertSchemaKeyHandler).Methods(http.MethodPut)
	router.HandleFunc("/api/apps/{APP_ID}/identity/schema/{KEY}", handler.DeleteSchemaKeyHandler).Methods(http.MethodDelete)
	router.HandleFunc("/api/apps/{APP_ID}/identity/values", handler.SearchValuesHandler).Methods(http.MethodGet)
	router.HandleFunc("/api/apps/{APP_ID}/identity/devices", handler.ListDevicesHandler).Methods(http.MethodGet)
	router.HandleFunc("/api/apps/{APP_ID}/identity/devices/{EAS_CLIENT_ID}", handler.GetDeviceHandler).Methods(http.MethodGet)
	router.HandleFunc("/api/apps/{APP_ID}/identity/update-health", handler.UpdateHealthHandler).Methods(http.MethodGet)
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

const appPath = "/api/apps/app-1/identity"

func TestNilStoreAnswers400(t *testing.T) {
	h := NewIdentityHandler(nil)
	for _, path := range []string{
		appPath + "/schema",
		appPath + "/values?key=userId",
		appPath + "/devices",
		appPath + "/devices/abc",
		appPath + "/update-health?ids=9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10",
	} {
		rec := serve(h, http.MethodGet, path, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

func TestSchemaCRUDHandlers(t *testing.T) {
	store := newFakeStore()
	h := NewIdentityHandler(NewService(store, nil))

	// Empty schema.
	rec := serve(h, http.MethodGet, appPath+"/schema", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var listed struct {
		Keys []schemaKeyResponse `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Empty(t, listed.Keys)

	// Upsert a valid key; omitted maxLength defaults.
	rec = serve(h, http.MethodPut, appPath+"/schema/userId", `{"type":"string"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var saved schemaKeyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	require.Equal(t, schemaKeyResponse{Key: "userId", Type: "string", MaxLength: DefaultMaxLength}, saved)

	// Invalid type is a 400 before the store.
	rec = serve(h, http.MethodPut, appPath+"/schema/bad", `{"type":"uuid"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	// Malformed body is a 400.
	rec = serve(h, http.MethodPut, appPath+"/schema/userId", `not json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Delete existing then missing.
	rec = serve(h, http.MethodDelete, appPath+"/schema/userId", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = serve(h, http.MethodDelete, appPath+"/schema/userId", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpsertSchemaKeyLimitIs409(t *testing.T) {
	store := newFakeStore()
	store.upsertErr = ErrTooManySchemaKeys
	h := NewIdentityHandler(NewService(store, nil))
	rec := serve(h, http.MethodPut, appPath+"/schema/userId", `{"type":"string"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestSearchValuesHandler(t *testing.T) {
	store := newFakeStore()
	store.values = []ValueCount{{Value: "acme", DeviceCount: 3}, {Value: "globex", DeviceCount: 1}}
	h := NewIdentityHandler(NewService(store, nil))

	// Missing key is a 400.
	rec := serve(h, http.MethodGet, appPath+"/values", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = serve(h, http.MethodGet, appPath+"/values?key=tenant&search=ac", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Values []ValueCount `json:"values"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, store.values, out.Values)
}

func TestGetDeviceHandler(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	deviceID := uuid.NewString()
	store.devices[deviceID] = &Device{
		EASClientID: deviceID,
		Metadata:    map[string]any{"userId": "u1"},
		CountryCode: strPtr("FR"),
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	h := NewIdentityHandler(NewService(store, nil))

	rec := serve(h, http.MethodGet, appPath+"/devices/"+deviceID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var d deviceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	require.Equal(t, deviceID, d.EasClientId)
	require.Equal(t, "u1", d.Metadata["userId"])
	require.Equal(t, "FR", *d.CountryCode)
	require.Equal(t, "2026-07-23T10:00:00Z", d.LastSeenAt)

	// A missing but well-formed uuid → 404.
	rec = serve(h, http.MethodGet, appPath+"/devices/"+uuid.NewString(), "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// A non-uuid path segment is 404, not a 500 from the store's uuid parse.
	rec = serve(h, http.MethodGet, appPath+"/devices/not-a-uuid", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListDevicesTamperedCursorIs400(t *testing.T) {
	store := newFakeStore()
	h := NewIdentityHandler(NewService(store, nil))
	// Valid base64 + valid timestamp but a non-uuid second segment: must 400
	// at the handler, never reach the store to 500 on the uuid parse.
	tampered := base64.RawURLEncoding.EncodeToString([]byte("2026-01-01T00:00:00Z|not-a-uuid"))
	rec := serve(h, http.MethodGet, appPath+"/devices?cursor="+tampered, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListDevicesHandlerPaginationAndFilter(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	deviceID := uuid.NewString()
	var gotFilter *MetadataFilter
	var gotCursor *DeviceCursor
	store.listDevices = func(filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error) {
		gotFilter, gotCursor = filter, cursor
		return []Device{{EASClientID: deviceID, FirstSeenAt: now, LastSeenAt: now}},
			&DeviceCursor{LastSeenAt: now, EASClientID: deviceID}, nil
	}
	h := NewIdentityHandler(NewService(store, nil))

	rec := serve(h, http.MethodGet, appPath+"/devices?filterKey=userId&filterValue=u1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var page struct {
		Devices    []deviceResponse `json:"devices"`
		NextCursor *string          `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Devices, 1)
	require.NotNil(t, page.NextCursor)
	require.Equal(t, &MetadataFilter{Key: "userId", Value: "u1"}, gotFilter)
	require.Nil(t, gotCursor, "first page has no cursor")

	// The opaque nextCursor round-trips: sending it back decodes to the same position.
	rec2 := serve(h, http.MethodGet, appPath+"/devices?cursor="+*page.NextCursor, "")
	require.Equal(t, http.StatusOK, rec2.Code)
	require.NotNil(t, gotCursor)
	require.Equal(t, deviceID, gotCursor.EASClientID)
	require.True(t, gotCursor.LastSeenAt.Equal(now))

	// A malformed cursor is a 400.
	rec3 := serve(h, http.MethodGet, appPath+"/devices?cursor=!!!notbase64", "")
	require.Equal(t, http.StatusBadRequest, rec3.Code)
}

func TestDeviceCursorRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 30, 15, 123456789, time.UTC)
	deviceID := uuid.NewString()
	c := &DeviceCursor{LastSeenAt: now, EASClientID: deviceID}
	encoded := encodeDeviceCursor(c)
	require.NotNil(t, encoded)
	decoded, err := decodeDeviceCursor(*encoded)
	require.NoError(t, err)
	require.Equal(t, deviceID, decoded.EASClientID)
	require.True(t, decoded.LastSeenAt.Equal(now))

	require.Nil(t, encodeDeviceCursor(nil))
	got, err := decodeDeviceCursor("")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestUpdateHealthHandler(t *testing.T) {
	store := newFakeStore()
	healthy := "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"
	broken := "0f61f1d1-3f5f-4b6a-9a44-6e9a1c2b3d4e"
	untried := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	store.health = map[string]UpdateHealth{
		healthy: {DevicesOnUpdate: 99, LaunchFailures: 1},
		broken:  {DevicesOnUpdate: 0, LaunchFailures: 7},
	}
	h := NewIdentityHandler(NewService(store, nil))

	rec := serve(h, http.MethodGet, appPath+"/update-health?ids="+healthy+","+broken+","+untried+",garbage", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Updates map[string]struct {
			DevicesOnUpdate int64    `json:"devicesOnUpdate"`
			LaunchFailures  int64    `json:"launchFailures"`
			HealthPercent   *float64 `json:"healthPercent"`
		} `json:"updates"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// Garbage id: silently absent. Every valid id gets an entry.
	require.Len(t, body.Updates, 3)
	require.NotNil(t, body.Updates[healthy].HealthPercent)
	require.InDelta(t, 99.0, *body.Updates[healthy].HealthPercent, 0.001)
	// Zero successes with failures is a hard 0%, the broken-update red badge.
	require.NotNil(t, body.Updates[broken].HealthPercent)
	require.InDelta(t, 0.0, *body.Updates[broken].HealthPercent, 0.001)
	// Nothing attempted it: percent stays null, never a fake 100%.
	require.Nil(t, body.Updates[untried].HealthPercent)
	require.EqualValues(t, 0, body.Updates[untried].DevicesOnUpdate)

	// Input contract: missing ids is a 400, an oversized list is a 400.
	require.Equal(t, http.StatusBadRequest, serve(h, http.MethodGet, appPath+"/update-health", "").Code)
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = healthy
	}
	require.Equal(t, http.StatusBadRequest, serve(h, http.MethodGet, appPath+"/update-health?ids="+strings.Join(tooMany, ","), "").Code)
}
