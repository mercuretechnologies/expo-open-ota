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
	failing atomic.Bool
	calls   atomic.Int32
}

func (f *fakeTouchStore) TouchDevice(_ context.Context, _ string, _ string, _ *identity.Geo) error {
	f.calls.Add(1)
	if f.failing.Load() {
		return errors.New("connection refused")
	}
	return nil
}

const (
	testAppID    = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	testDeviceID = "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b"
)

func TestNoteContactDebounces(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewDeviceContactRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// Raw header garbage, BOTH sides (the manifest route has no app-existence
	// middleware): ignored outright, no registry call, no log spam.
	recorder.NoteContact(ctx, testAppID, "not-a-uuid", "")
	recorder.NoteContact(ctx, testAppID, "", "")
	recorder.NoteContact(ctx, "not-a-uuid-app", testDeviceID, "")
	assert.EqualValues(t, 0, store.calls.Load())

	// A real device registers in the background. Wait on the CACHED marker,
	// not the call counter: the counter increments before the cache write,
	// and the next NoteContact below must be guaranteed to hit the cache.
	recorder.NoteContact(ctx, testAppID, testDeviceID, "")
	require.Eventually(t, func() bool {
		return localCache.Get(contactCacheKey(testAppID, testDeviceID)) != ""
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, store.calls.Load())

	// Once recorded, further contacts spawn no background work for the TTL.
	recorder.NoteContact(ctx, testAppID, testDeviceID, "")
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, store.calls.Load())
}

func TestNoteContactErrorCooldown(t *testing.T) {
	store := &fakeTouchStore{}
	store.failing.Store(true)
	localCache := cache.NewLocalCache()
	recorder := NewDeviceContactRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// A doomed registration (database down, app row deleted under a live
	// fleet) costs one attempt per TTL, not one failed transaction and a log
	// line per poll.
	recorder.NoteContact(ctx, testAppID, testDeviceID, "")
	require.Eventually(t, func() bool {
		return localCache.Get(contactCacheKey(testAppID, testDeviceID)) == contactErrorCacheValue
	}, 2*time.Second, 10*time.Millisecond)
	recorder.NoteContact(ctx, testAppID, testDeviceID, "")
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, store.calls.Load())

	// After the cooldown (simulated by dropping the key), it retries for real.
	localCache.Delete(contactCacheKey(testAppID, testDeviceID))
	store.failing.Store(false)
	recorder.NoteContact(ctx, testAppID, testDeviceID, "")
	require.Eventually(t, func() bool {
		return localCache.Get(contactCacheKey(testAppID, testDeviceID)) == contactRecordedCacheValue
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 2, store.calls.Load())
}
