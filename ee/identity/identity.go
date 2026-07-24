// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

// Package identity maps expo-eas-client install UUIDs to operator-defined
// metadata (userId, tenant, ...), fed by `identify` log events on the observe
// ingestion route. The dashboard "Identity" section declares which metadata
// keys are accepted and with which type; everything else coming from the wire
// is dropped, so hostile payloads are bounded by construction.
//
// The package is EE-licensed but NOT license-gated: the feature works on
// community deployments too.
package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// ErrTooManySchemaKeys is returned when declaring a new allowlist key would
// exceed MaxSchemaKeys. The dashboard surfaces it as a 409.
var ErrTooManySchemaKeys = errors.New("identity schema key limit reached")

type ValueType string

const (
	ValueTypeString  ValueType = "string"
	ValueTypeNumber  ValueType = "number"
	ValueTypeBoolean ValueType = "boolean"
)

const (
	// DefaultMaxLength bounds string values when the operator does not pick a
	// limit; MaxLengthCeiling bounds what the operator may pick. Identity
	// metadata is lookup keys, not payloads.
	DefaultMaxLength = 256
	MaxLengthCeiling = 1024
	// MaxSchemaKeys caps the allowlist size. Every declared key multiplies the
	// worst-case device row that the unauthenticated wire can fill, so the
	// operator-side bound keeps hostile identifies at ~tens of KB per row
	// instead of megabytes.
	MaxSchemaKeys = 100
)

// Key names stay path-friendly and unambiguous: they end up in API routes,
// autocomplete queries and JSONB lookups.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// KeySpec is one allowlisted metadata key as declared in the dashboard.
type KeySpec struct {
	Key       string    `json:"key"`
	Type      ValueType `json:"type"`
	MaxLength int       `json:"maxLength"`
}

// Schema is an app's full allowlist, keyed by metadata key.
type Schema map[string]KeySpec

func ValidateKeySpec(spec KeySpec) error {
	if !keyPattern.MatchString(spec.Key) {
		return fmt.Errorf("invalid metadata key %q: must match %s", spec.Key, keyPattern.String())
	}
	switch spec.Type {
	case ValueTypeString, ValueTypeNumber, ValueTypeBoolean:
	default:
		return fmt.Errorf("invalid value type %q for key %q: must be string, number or boolean", spec.Type, spec.Key)
	}
	if spec.MaxLength < 1 || spec.MaxLength > MaxLengthCeiling {
		return fmt.Errorf("invalid max length %d for key %q: must be in [1, %d]", spec.MaxLength, spec.Key, MaxLengthCeiling)
	}
	return nil
}

// Sanitize filters raw wire metadata down to the allowlist. A value survives
// only when its key is declared and its JSON type matches the declared type;
// violations drop the single entry, never the whole identify. Oversized
// strings are dropped rather than truncated (a truncated userId would corrupt
// the mapping silently). The dropped keys come back for counters and logs.
//
// Client-side validation (expo-app-metrics caps) is irrelevant here: anything
// can be forged with a plain HTTP request, this is the enforcement point.
func (s Schema) Sanitize(raw map[string]any) (map[string]any, []string) {
	sanitized := make(map[string]any, len(raw))
	var dropped []string
	for key, value := range raw {
		spec, declared := s[key]
		if !declared {
			dropped = append(dropped, key)
			continue
		}
		coerced, ok := coerceValue(spec, value)
		if !ok {
			dropped = append(dropped, key)
			continue
		}
		sanitized[key] = coerced
	}
	return sanitized, dropped
}

// coerceValue validates one raw value against its spec. Numbers normalize to
// float64 (what JSON decoding produces anyway); NaN and infinities are
// dropped because they do not survive json.Marshal into JSONB.
func coerceValue(spec KeySpec, value any) (any, bool) {
	switch spec.Type {
	case ValueTypeString:
		str, ok := value.(string)
		if !ok || utf8.RuneCountInString(str) > spec.MaxLength {
			return nil, false
		}
		return str, true
	case ValueTypeNumber:
		num, ok := toFloat(value)
		if !ok || math.IsNaN(num) || math.IsInf(num, 0) {
			return nil, false
		}
		return num, true
	case ValueTypeBoolean:
		b, ok := value.(bool)
		if !ok {
			return nil, false
		}
		return b, true
	}
	return nil, false
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		// The future ingest path may decode with UseNumber(); support it now
		// so numbers do not silently start dropping when it does.
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

// RenderValue is the canonical text form of a metadata value, used as the
// identity_value_stats key. Integral floats render without a decimal part so
// 42 and 42.0 count as the same value.
func RenderValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		// Unreachable after Sanitize; fmt keeps a debuggable fallback.
		return fmt.Sprintf("%v", v)
	}
}

// Device is one install of an app and what we know about it. Timestamps stay
// time.Time here; formatting belongs to the handlers (same split as ee/audit).
type Device struct {
	AppID       string
	EASClientID string
	Metadata    map[string]any
	CountryCode *string
	City        *string
	Lat         *float64
	Lng         *float64
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// DeviceCursor is the keyset position for paginating the device inventory:
// the (last_seen_at, eas_client_id) of the last row returned. Newest-first.
type DeviceCursor struct {
	LastSeenAt  time.Time
	EASClientID string
}

// MetadataFilter narrows the device inventory to installs whose metadata
// contains an exact key/value. String values only for now (userId, tenant,
// plan — the dominant filter targets); typed number/bool filtering is not
// wired into the dashboard yet.
type MetadataFilter struct {
	Key   string
	Value string
}

const (
	// DefaultDevicesPageSize and MaxDevicesPageSize bound the inventory page.
	DefaultDevicesPageSize = 50
	MaxDevicesPageSize     = 200
)

// Geo is an optional enrichment resolved from the request IP (GeoLite2,
// city-level accuracy: lat/lng is a city centroid, not a device position).
// Fields are per-field optional because partial resolutions are the norm
// (country without city is very common); a nil field never overwrites a
// previously known value, only a present one does.
type Geo struct {
	CountryCode *string
	City        *string
	Lat         *float64
	Lng         *float64
}

// ValueCount is one autocomplete suggestion for a metadata key.
type ValueCount struct {
	Value       string `json:"value"`
	DeviceCount int64  `json:"deviceCount"`
}

// ApplyResult reports what an identify did: the device after merge, and which
// incoming keys were rejected by the allowlist.
type ApplyResult struct {
	Device      Device
	DroppedKeys []string
}
