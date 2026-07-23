// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"expo-open-ota/ee/identity"
	"expo-open-ota/internal/cache"
	"log"
	"time"

	"github.com/google/uuid"
)

// DeviceContactRecorder registers device contacts in the universal registry
// (device_identity): every manifest poll and every telemetry batch lands
// there, identity ops only layer metadata on top. The registry is uncapped;
// what the recorder provides is the WRITE-RATE bound: the hot paths cost one
// registry write per device per TTL, not per request, and a registration
// that cannot succeed (database down, app row deleted under a live fleet)
// costs one attempt per TTL instead of a failed transaction and a log line
// per poll.
type DeviceContactRecorder struct {
	identity *identity.Service
	cache    cache.Cache
}

func NewDeviceContactRecorder(identityService *identity.Service, c cache.Cache) *DeviceContactRecorder {
	return &DeviceContactRecorder{identity: identityService, cache: c}
}

// contactTTLSeconds bounds the last_seen bump rate per device; ±60s of
// last_seen precision is far inside what any consumer needs (dashboards,
// instant-T update health).
const contactTTLSeconds = 60

const (
	contactRecordedCacheValue = "1"
	contactErrorCacheValue    = "e" // registration failed: retry after the TTL
)

func contactCacheKey(appID, easClientID string) string {
	return "observe:contact:" + appID + ":" + easClientID
}

// touchTimeout bounds the background registration a contact triggers.
const touchTimeout = 5 * time.Second

// NoteContact records one device contact, debounced. Both ids are raw header
// input on the manifest path (no app-existence middleware there), so
// non-UUIDs are ignored outright: unattributable, and they must not spawn a
// doomed background transaction per poll. The cache check stays inline (a
// map or Redis read); only a miss spawns the background write, bounded to
// one per device per TTL, detached from the request on purpose
// (WithoutCancel, like audit's recorder) so a canceled poll still counts.
func (r *DeviceContactRecorder) NoteContact(ctx context.Context, appID string, easClientID string, remoteIP string) {
	if _, err := uuid.Parse(appID); err != nil {
		return
	}
	if _, err := uuid.Parse(easClientID); err != nil {
		return
	}
	key := contactCacheKey(appID, easClientID)
	if r.cache.Get(key) != "" {
		return
	}
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), touchTimeout)
	go func() {
		defer cancel()
		ttl := contactTTLSeconds
		if err := r.identity.TouchDevice(bgCtx, appID, easClientID, remoteIP); err != nil {
			log.Printf("observe: device contact registration failed: %v", err)
			_ = r.cache.Set(key, contactErrorCacheValue, &ttl)
			return
		}
		_ = r.cache.Set(key, contactRecordedCacheValue, &ttl)
	}()
}
