// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"errors"
	"expo-open-ota/ee/identity"
	"expo-open-ota/internal/cache"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTouchStore fakes the registry: only TouchDevice is exercised, the
// embedded Store covers the rest of the interface (recordingMutator pattern).
type fakeTouchStore struct {
	identity.Store
	tracked atomic.Bool
	failing atomic.Bool
	calls   atomic.Int32
}

func (f *fakeTouchStore) TouchDevice(_ context.Context, _ string, _ string, _ *identity.Geo) (bool, error) {
	f.calls.Add(1)
	if f.failing.Load() {
		return false, errors.New("connection refused")
	}
	return f.tracked.Load(), nil
}

func newAdmissionForTest(store *fakeTouchStore) *DeviceAdmission {
	return NewDeviceAdmission(identity.NewService(store, nil), cache.NewLocalCache())
}

func TestAdmitCachesVerdicts(t *testing.T) {
	store := &fakeTouchStore{}
	store.tracked.Store(true)
	admission := newAdmissionForTest(store)
	ctx := context.Background()

	assert.True(t, admission.Admit(ctx, "app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", ""))
	assert.True(t, admission.Admit(ctx, "app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", ""))
	assert.EqualValues(t, 1, store.calls.Load(), "verdict cached, one registry write per TTL")

	// A distinct device gets its own verdict.
	store.tracked.Store(false)
	assert.False(t, admission.Admit(ctx, "app-1", "4127c568-af7f-4d2b-9e0a-1c6e2b7d9f31", ""))
	assert.False(t, admission.Admit(ctx, "app-1", "4127c568-af7f-4d2b-9e0a-1c6e2b7d9f31", ""))
	assert.EqualValues(t, 2, store.calls.Load(), "over-cap verdict cached too")
}

func TestAdmitErrorCooldown(t *testing.T) {
	store := &fakeTouchStore{}
	store.failing.Store(true)
	localCache := cache.NewLocalCache()
	admission := NewDeviceAdmission(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// Registry trouble admits (dropping a tracked fleet's telemetry over a
	// Postgres blink would be worse) under a cooldown: a doomed registration
	// (deleted app, database down) costs one attempt per TTL, not one failed
	// transaction and a log line per request.
	assert.True(t, admission.Admit(ctx, "app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", ""))
	assert.True(t, admission.Admit(ctx, "app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", ""))
	assert.EqualValues(t, 1, store.calls.Load(), "errors cached as fail-open cooldown")

	// After the cooldown (simulated by dropping the key) the verdict is
	// re-evaluated for real, not poisoned by the earlier failure.
	localCache.Delete(admissionCacheKey("app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b"))
	store.failing.Store(false)
	store.tracked.Store(false)
	assert.False(t, admission.Admit(ctx, "app-1", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", ""))
	assert.EqualValues(t, 2, store.calls.Load())
}

func TestNoteContactIgnoresForgedIDsAndRegistersInBackground(t *testing.T) {
	store := &fakeTouchStore{}
	store.tracked.Store(true)
	localCache := cache.NewLocalCache()
	admission := NewDeviceAdmission(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	appID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

	// Raw header garbage, BOTH sides (the manifest route has no app-existence
	// middleware): ignored outright, no registry call, no log spam.
	admission.NoteContact(ctx, appID, "not-a-uuid", "")
	admission.NoteContact(ctx, appID, "", "")
	admission.NoteContact(ctx, "not-a-uuid-app", "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", "")
	assert.EqualValues(t, 0, store.calls.Load())

	// A real device registers in the background. Wait on the CACHED verdict,
	// not the call counter: the counter increments before Admit writes the
	// cache, and the next NoteContact below must be guaranteed to hit it.
	admission.NoteContact(ctx, appID, "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", "")
	require.Eventually(t, func() bool {
		return localCache.Get(admissionCacheKey(appID, "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b")) != ""
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, store.calls.Load())

	// Once the verdict is cached, polls stop spawning background work.
	admission.NoteContact(ctx, appID, "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b", "")
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, store.calls.Load())
}
