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

// DeviceAdmission is the free-tier gate of the telemetry pipeline. EVERY
// contact registers the device in device_identity (the universal registry,
// capped at identity.FreeDeviceLimit without an enterprise license); a device
// holding a slot is "tracked" and its telemetry is ingested, an over-cap
// device is acknowledged and dropped. The verdict is cached so the hot paths
// (every manifest poll, every telemetry batch) cost one registry write per
// device per TTL, not per request.
type DeviceAdmission struct {
	identity *identity.Service
	cache    cache.Cache
}

func NewDeviceAdmission(identityService *identity.Service, c cache.Cache) *DeviceAdmission {
	return &DeviceAdmission{identity: identityService, cache: c}
}

// admissionTTLSeconds bounds both the last_seen bump rate and how long an
// over-cap verdict sticks (a license activation or an identify-driven
// eviction changes the answer within a minute).
const admissionTTLSeconds = 60

const (
	admittedCacheValue = "1"
	overCapCacheValue  = "0"
	// errorCooldownCacheValue marks a registry failure: admitted (fail-open)
	// but WITHOUT a registration. Cached so a device whose registration
	// cannot succeed (app row deleted with a live fleet, Postgres down) costs
	// one attempt per TTL instead of a failed transaction and a log line per
	// poll; the TTL doubles as a backoff for a struggling registry.
	errorCooldownCacheValue = "e"
)

func admissionCacheKey(appID, easClientID string) string {
	return "observe:admit:" + appID + ":" + easClientID
}

// Admit reports whether this device's telemetry is ingested, registering the
// contact on the way. Registry trouble admits (fail-open: a Postgres blink
// must not drop a tracked fleet's telemetry or fail ingestion) under an
// error-cooldown cache entry, so a persistently failing registration is
// retried once per TTL, never per request.
func (a *DeviceAdmission) Admit(ctx context.Context, appID string, easClientID string, remoteIP string) bool {
	key := admissionCacheKey(appID, easClientID)
	if cached := a.cache.Get(key); cached != "" {
		return cached != overCapCacheValue
	}

	ttl := admissionTTLSeconds
	tracked, err := a.identity.TouchDevice(ctx, appID, easClientID, remoteIP)
	if err != nil {
		log.Printf("observe: device admission check failed: %v", err)
		_ = a.cache.Set(key, errorCooldownCacheValue, &ttl)
		return true
	}

	value := overCapCacheValue
	if tracked {
		value = admittedCacheValue
	}
	_ = a.cache.Set(key, value, &ttl)
	return tracked
}

// touchTimeout bounds the background registration a manifest poll triggers.
const touchTimeout = 5 * time.Second

// NoteContact is the manifest-poll entry: same registration, but the caller
// serves OTA updates and must never wait on it. The client id is raw header
// input here (the ingest path validates upstream), so non-UUIDs are ignored
// outright: unattributable, and they must not spam the error log on every
// poll. The cache check stays inline (a map or Redis read); only a cache
// miss spawns the background write, so the goroutine rate is bounded to one
// per device per TTL, and it outlives the request on purpose (WithoutCancel,
// like audit's recorder).
func (a *DeviceAdmission) NoteContact(ctx context.Context, appID string, easClientID string, remoteIP string) {
	// BOTH ids are raw header input on this path (the manifest route has no
	// app-existence middleware): a garbage expo-app-id must be ignored here,
	// not discovered per-poll by a doomed background transaction.
	if _, err := uuid.Parse(appID); err != nil {
		return
	}
	if _, err := uuid.Parse(easClientID); err != nil {
		return
	}
	if a.cache.Get(admissionCacheKey(appID, easClientID)) != "" {
		return
	}
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), touchTimeout)
	go func() {
		defer cancel()
		a.Admit(bgCtx, appID, easClientID, remoteIP)
	}()
}
