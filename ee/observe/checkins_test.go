// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"errors"
	"expo-open-ota/ee/identity"
	"expo-open-ota/internal/cache"
	"expo-open-ota/internal/handlers"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTouchStore fakes the registry: only the check-in write path is
// exercised, the embedded Store covers the rest of the interface.
type fakeTouchStore struct {
	identity.Store
	failing      atomic.Bool
	failFailures atomic.Bool
	calls        atomic.Int32

	mu             sync.Mutex
	lastCurrent    *string
	failedRecorded [][]string
	lastFatal      string
	lastType       identity.FailureType
}

func (f *fakeTouchStore) TouchDevice(_ context.Context, _ string, _ string, _ *identity.Geo, currentUpdateID *string) error {
	f.calls.Add(1)
	if f.failing.Load() {
		return errors.New("connection refused")
	}
	f.mu.Lock()
	f.lastCurrent = currentUpdateID
	f.mu.Unlock()
	return nil
}

func (f *fakeTouchStore) RecordUpdateFailures(_ context.Context, _ string, _ string, updateIDs []string, fatalError string, failureType identity.FailureType) error {
	if f.failFailures.Load() {
		return errors.New("failures write refused")
	}
	f.mu.Lock()
	f.failedRecorded = append(f.failedRecorded, updateIDs)
	f.lastFatal = fatalError
	f.lastType = failureType
	f.mu.Unlock()
	return nil
}

const (
	testAppID    = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	testDeviceID = "3f9b2c81-4a5d-4e6f-8a9b-0c1d2e3f4a5b"
	testUpdateA  = "9b3b89b6-5a0d-4a57-b1f5-6e1d5b7c2a10"
	testUpdateB  = "0f61f1d1-3f5f-4b6a-9a44-6e9a1c2b3d4e"
)

func checkInWith(current, failedRaw, fatal string) handlers.DeviceCheckIn {
	return handlers.DeviceCheckIn{
		AppID:              testAppID,
		EASClientID:        testDeviceID,
		CurrentUpdateID:    current,
		FailedUpdateIDsRaw: failedRaw,
		FatalError:         fatal,
	}
}

func waitRecorded(t *testing.T, c cache.Cache, want int32, store *fakeTouchStore) {
	t.Helper()
	require.Eventually(t, func() bool {
		return c.Get(checkInCacheKey(testAppID, testDeviceID)) != "" && store.calls.Load() == want
	}, 2*time.Second, 10*time.Millisecond)
}

func TestRecordDebouncesSteadyState(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// Raw header garbage, BOTH sides: ignored outright.
	recorder.Record(ctx, handlers.DeviceCheckIn{AppID: testAppID, EASClientID: "not-a-uuid"})
	recorder.Record(ctx, handlers.DeviceCheckIn{AppID: "not-a-uuid-app", EASClientID: testDeviceID})
	assert.EqualValues(t, 0, store.calls.Load())

	// First check-in registers (with its running update)...
	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	waitRecorded(t, localCache, 1, store)
	store.mu.Lock()
	require.NotNil(t, store.lastCurrent)
	assert.Equal(t, testUpdateA, *store.lastCurrent)
	store.mu.Unlock()

	// ...and the SAME state within the TTL is debounced.
	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, store.calls.Load())
}

func TestRecordStateTransitionBustsDebounce(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	waitRecorded(t, localCache, 1, store)

	// The device moved to update B: the fingerprint changes, the debounce
	// must NOT swallow the transition.
	recorder.Record(ctx, checkInWith(testUpdateB, "", ""))
	require.Eventually(t, func() bool { return store.calls.Load() == 2 }, 2*time.Second, 10*time.Millisecond)
	store.mu.Lock()
	require.NotNil(t, store.lastCurrent)
	assert.Equal(t, testUpdateB, *store.lastCurrent)
	store.mu.Unlock()
}

func TestRecordRecordsFailures(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// The post-crash poll: current back on A, B in the failed list, the
	// consumed fatal error riding along.
	raw := `"` + testUpdateB + `"`
	recorder.Record(ctx, checkInWith(testUpdateA, raw, "TypeError: undefined is not a function"))
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.failedRecorded) == 1
	}, 2*time.Second, 10*time.Millisecond)
	store.mu.Lock()
	assert.Equal(t, []string{testUpdateB}, store.failedRecorded[0])
	assert.Equal(t, "TypeError: undefined is not a function", store.lastFatal)
	assert.Equal(t, identity.FailureTypeUpdate, store.lastType)
	store.mu.Unlock()
	assert.EqualValues(t, 1, store.calls.Load())
}

func TestRecordErrorCooldown(t *testing.T) {
	store := &fakeTouchStore{}
	store.failing.Store(true)
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	recorder.Record(ctx, checkInWith("", "", ""))
	require.Eventually(t, func() bool {
		return localCache.Get(checkInCacheKey(testAppID, testDeviceID)) == checkInErrorCacheValue
	}, 2*time.Second, 10*time.Millisecond)

	// The cooldown holds even for a DIFFERENT state fingerprint: one doomed
	// attempt per TTL, not one per distinct poll shape.
	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, store.calls.Load())

	// After the cooldown (simulated by dropping the key), it retries.
	localCache.Delete(checkInCacheKey(testAppID, testDeviceID))
	store.failing.Store(false)
	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	require.Eventually(t, func() bool { return store.calls.Load() == 2 }, 2*time.Second, 10*time.Millisecond)
}

func TestParseFailedUpdateIDs(t *testing.T) {
	// The wire form: RFC 8941 list of quoted lowercase UUIDs.
	assert.Equal(t, []string{testUpdateA, testUpdateB},
		ParseFailedUpdateIDs(`"`+testUpdateA+`", "`+testUpdateB+`"`))
	// Uppercase normalizes, unquoted tolerated, garbage dropped.
	assert.Equal(t, []string{testUpdateA},
		ParseFailedUpdateIDs(`"9B3B89B6-5A0D-4A57-B1F5-6E1D5B7C2A10", "not-a-uuid"`))
	assert.Nil(t, ParseFailedUpdateIDs(""))
	assert.Nil(t, ParseFailedUpdateIDs(`totally, broken, garbage`))
}

func TestRecordCrossSourceEquivalence(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// Manifest-style check-in: RAW uppercase header value.
	recorder.Record(ctx, checkInWith("9B3B89B6-5A0D-4A57-B1F5-6E1D5B7C2A10", "", ""))
	waitRecorded(t, localCache, 1, store)

	// Telemetry-style check-in for the SAME state: normalized lowercase.
	// Same effective state, no debounce bust.
	recorder.Record(ctx, checkInWith(testUpdateA, "", ""))
	// Telemetry from a resource with no update id: the zero-UUID sentinel
	// means "does not know", never a transition.
	recorder.Record(ctx, checkInWith(ZeroUpdateID, "", ""))
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, store.calls.Load(), "same effective state across sources must stay debounced")
}

func TestRecordFatalBypassesCooldown(t *testing.T) {
	store := &fakeTouchStore{}
	store.failing.Store(true)
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// A plain check-in fails and arms the cooldown...
	recorder.Record(ctx, checkInWith("", "", ""))
	require.Eventually(t, func() bool {
		return localCache.Get(checkInCacheKey(testAppID, testDeviceID)) == checkInErrorCacheValue
	}, 2*time.Second, 10*time.Millisecond)

	// ...which must NOT swallow the one-shot fatal error: that poll always
	// gets its attempt.
	recorder.Record(ctx, checkInWith(testUpdateA, `"`+testUpdateB+`"`, "FATAL BOOM"))
	require.Eventually(t, func() bool { return store.calls.Load() >= 1 }, 2*time.Second, 10*time.Millisecond)
}

func TestRecordFatalStashSurvivesFailuresOutage(t *testing.T) {
	store := &fakeTouchStore{}
	store.failFailures.Store(true)
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// The fatal-carrying poll arrives while the failures write is down: the
	// crash detail must be stashed, not lost (the client never re-sends it).
	raw := `"` + testUpdateB + `"`
	recorder.Record(ctx, checkInWith(testUpdateA, raw, "FATAL BOOM"))
	// Wait for the goroutine's LAST write (the cooldown marker): deleting the
	// key any earlier races the goroutine re-arming it after the stash write.
	require.Eventually(t, func() bool {
		return localCache.Get(checkInCacheKey(testAppID, testDeviceID)) == checkInErrorCacheValue &&
			localCache.Get(fatalStashKey(testAppID, testDeviceID)) == "FATAL BOOM"
	}, 2*time.Second, 10*time.Millisecond)

	// Recovery: the sticky header re-sends the failed id WITHOUT the error;
	// the stash re-attaches it.
	localCache.Delete(checkInCacheKey(testAppID, testDeviceID)) // cooldown expiry
	store.failFailures.Store(false)
	recorder.Record(ctx, checkInWith(testUpdateA, raw, ""))
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.failedRecorded) == 1 && store.lastFatal == "FATAL BOOM"
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, "", localCache.Get(fatalStashKey(testAppID, testDeviceID)), "stash consumed on success")
}

func TestRecordsPicksNewestRowPerDevice(t *testing.T) {
	store := &fakeTouchStore{}
	localCache := cache.NewLocalCache()
	recorder := NewCheckInRecorder(identity.NewService(store, nil), localCache)
	ctx := context.Background()

	// A backlog flush: old sessions on update A first, the newest on B. The
	// check-in must carry B, not regress to A.
	now := time.Now()
	rows := []LogRow{
		{EASClientID: testDeviceID, UpdateID: testUpdateA, Timestamp: now.Add(-2 * time.Hour)},
		{EASClientID: testDeviceID, UpdateID: testUpdateA, Timestamp: now.Add(-1 * time.Hour)},
		{EASClientID: testDeviceID, UpdateID: testUpdateB, Timestamp: now},
	}
	recordCheckIns(ctx, recorder, testAppID, "", rows,
		func(row LogRow) string { return row.EASClientID },
		func(row LogRow) string { return row.UpdateID },
		func(row LogRow) time.Time { return row.Timestamp })
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.lastCurrent != nil && *store.lastCurrent == testUpdateB
	}, 2*time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 1, store.calls.Load(), "one check-in per device per batch")
}
