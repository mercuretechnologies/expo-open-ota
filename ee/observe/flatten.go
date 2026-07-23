// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"encoding/json"
	"expo-open-ota/ee/identity"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// The flattener turns decoded OTLP batches into rows matching the ClickHouse
// schema (observe_metrics / observe_logs) column for column. It is pure:
// Branch stays empty here and is filled by the caller from the update->branch
// cache (a database concern), everything else comes from the wire. Resource
// attributes are denormalized onto every row so queries only ever touch plain
// columns.

// ZeroUpdateID is the update_id sentinel for "running the embedded bundle":
// the sorting key forbids Nullable, and a missing/invalid wire update id must
// still land somewhere queryable.
const ZeroUpdateID = "00000000-0000-0000-0000-000000000000"

// MetricRow mirrors the observe_metrics table.
type MetricRow struct {
	AppID          string
	EASClientID    string
	UpdateID       string
	Branch         string
	Channel        string
	RuntimeVersion string
	Platform       string
	SessionID      string
	MetricName     string
	Value          float64
	RouteName      string
	CustomParams   string
	// Attributes carries the leftover point attributes as sorted JSON:
	// setGlobalAttributes merges arbitrary user keys into every metric.
	Attributes  string
	OSName      string
	OSVersion   string
	DeviceModel string
	AppVersion  string
	SDKVersion  string
	Timestamp   time.Time
	ContentHash uint64
}

// LogRow mirrors the observe_logs table.
type LogRow struct {
	AppID          string
	EASClientID    string
	UpdateID       string
	Branch         string
	Channel        string
	RuntimeVersion string
	Platform       string
	SessionID      string
	EventName      string
	SeverityNumber uint8
	SeverityText   string
	IsFatal        bool
	Body           string
	Attributes     string
	OSName         string
	OSVersion      string
	DeviceModel    string
	AppVersion     string
	SDKVersion     string
	Timestamp      time.Time
	ContentHash    uint64
}

// Wire attribute keys (resource level unless noted).
const (
	updateIDKey       = "expo.app.updates.id"
	legacyUpdateIDKey = "expo.app.update_id"
	channelKey        = "expo.app.updates.channel"
	runtimeVersionKey = "expo.app.updates.runtime_version"
	osNameKey         = "os.name"
	osVersionKey      = "os.version"
	deviceModelKey    = "device.model.identifier"
	deviceModelAltKey = "device.model.name"
	appVersionKey     = "service.version"
	sdkVersionKey     = "telemetry.sdk.version"
	sdkLanguageKey    = "telemetry.sdk.language"
	sessionIDKey      = "session.id" // record/point level
	routeNameKey      = "expo.route_name"
	customParamsKey   = "expo.custom_params"
	pointUpdateIDKey  = "expo.update_id" // point level; see FlattenMetrics
	isFatalKey        = "expo.error.is_fatal"
)

// maxTimestampAge and maxTimestampSkew bound accepted wire timestamps. Out of
// range (a device clock set to 2093, an unparseable stored date arriving as 0)
// maps to the ingestion time: bogus values would scatter junk partitions, and
// an insert block spanning more than 100 distinct months is rejected whole by
// ClickHouse.
const (
	maxTimestampAge  = 396 * 24 * time.Hour // ~13 months, matching one partition of slack past a year
	maxTimestampSkew = 24 * time.Hour
)

func clampTimestamp(nano uint64, now time.Time) time.Time {
	if nano == 0 || nano > uint64(1<<63-1) {
		return now
	}
	ts := time.Unix(0, int64(nano)).UTC()
	if ts.Before(now.Add(-maxTimestampAge)) || ts.After(now.Add(maxTimestampSkew)) {
		return now
	}
	return ts
}

// resourceInfo is the per-session context shared by every row of a resource
// block, extracted once.
type resourceInfo struct {
	easClientID    string
	updateID       string
	channel        string
	runtimeVersion string
	platform       string
	osName         string
	osVersion      string
	deviceModel    string
	appVersion     string
	sdkVersion     string
}

func newResourceInfo(attrs map[string]any) resourceInfo {
	str := func(key string) string {
		s, _ := attrs[key].(string)
		return s
	}
	osName := str(osNameKey)
	return resourceInfo{
		easClientID:    str(EASClientIDKey),
		updateID:       normalizeUpdateID(firstNonEmpty(str(updateIDKey), str(legacyUpdateIDKey))),
		channel:        str(channelKey),
		runtimeVersion: str(runtimeVersionKey),
		platform:       normalizePlatform(osName, str(sdkLanguageKey)),
		osName:         osName,
		osVersion:      str(osVersionKey),
		deviceModel:    firstNonEmpty(str(deviceModelKey), str(deviceModelAltKey)),
		appVersion:     str(appVersionKey),
		sdkVersion:     str(sdkVersionKey),
	}
}

// normalizeUpdateID lowercases and validates the wire update id; anything that
// is not a UUID becomes the embedded-bundle sentinel rather than poisoning the
// sorting key with garbage.
func normalizeUpdateID(raw string) string {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return ZeroUpdateID
	}
	return parsed.String()
}

// normalizeSessionID guards the UUID column: a forged non-UUID session id
// must degrade to the zero UUID, not fail the whole ClickHouse batch (a
// failed batch answers 503 and the device would retry the poison forever).
func normalizeSessionID(raw string) string {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return ZeroUpdateID
	}
	return parsed.String()
}

// normalizePlatform folds os.name into the two-value platform column the rest
// of the server uses ("ios" / "android"). os.name is only "present when
// available" on the wire; telemetry.sdk.language (swift/kotlin, always sent)
// is the fallback. Anything unrecognized keeps its lowercased name so a
// future platform is visible instead of silently bucketed.
func normalizePlatform(osName, sdkLanguage string) string {
	switch osName {
	case "iOS", "iPadOS", "tvOS":
		return "ios"
	case "Android":
		return "android"
	}
	switch sdkLanguage {
	case "swift":
		return "ios"
	case "kotlin":
		return "android"
	}
	return lowerASCII(osName)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// FlattenMetrics turns a decoded metrics batch into rows. Resources whose
// client id is not a UUID are dropped whole (unattributable, same rule as the
// identity path) and counted.
func FlattenMetrics(appID string, batch MetricBatch, now time.Time) []MetricRow {
	var rows []MetricRow
	for _, resource := range batch.Resources {
		info := newResourceInfo(resource.Attributes)
		if _, err := uuid.Parse(info.easClientID); err != nil {
			observeRecordsDropped(reasonForgedClientID, len(resource.Points))
			continue
		}
		for _, point := range resource.Points {
			str := func(key string) string {
				s, _ := point.Attributes[key].(string)
				return s
			}
			// A point-level expo.update_id overrides the resource's: on
			// expo.updates.download_time it names the update that was just
			// DOWNLOADED, not the one running, and that is the update the
			// metric is about.
			updateID := info.updateID
			if pointUpdate := str(pointUpdateIDKey); pointUpdate != "" {
				if parsed, err := uuid.Parse(pointUpdate); err == nil {
					updateID = parsed.String()
				}
			}
			row := MetricRow{
				AppID:          appID,
				EASClientID:    info.easClientID,
				UpdateID:       updateID,
				Channel:        info.channel,
				RuntimeVersion: info.runtimeVersion,
				Platform:       info.platform,
				SessionID:      normalizeSessionID(str(sessionIDKey)),
				MetricName:     point.MetricName,
				Value:          point.Value,
				RouteName:      str(routeNameKey),
				CustomParams:   str(customParamsKey),
				Attributes:     marshalAttributes(point.Attributes, metricEnvelopeKeys),
				OSName:         info.osName,
				OSVersion:      info.osVersion,
				DeviceModel:    info.deviceModel,
				AppVersion:     info.appVersion,
				SDKVersion:     info.sdkVersion,
				Timestamp:      clampTimestamp(point.TimeUnixNano, now),
			}
			// The raw nano (not the clamped time) goes into the hash so a
			// retried batch hashes identically whenever it re-arrives.
			row.ContentHash = contentHash(
				row.SessionID, row.MetricName,
				strconv.FormatUint(point.TimeUnixNano, 10),
				strconv.FormatFloat(point.Value, 'g', -1, 64),
				row.RouteName, row.CustomParams, row.Attributes,
			)
			rows = append(rows, row)
		}
	}
	return rows
}

// FlattenLogs turns a decoded logs batch into rows, skipping identity
// operations: those are applied by ee/identity, not stored as telemetry.
// Unattributable resources are dropped and counted by the identity pass that
// runs before this one, so they are skipped silently here.
func FlattenLogs(appID string, batch LogBatch, now time.Time) []LogRow {
	var rows []LogRow
	for _, resource := range batch.Resources {
		info := newResourceInfo(resource.Attributes)
		if _, err := uuid.Parse(info.easClientID); err != nil {
			continue
		}
		for _, record := range resource.Records {
			eventName, _ := record.Attributes[EventNameKey].(string)
			if identity.IsIdentityOp(eventName) {
				continue
			}
			isFatal, _ := record.Attributes[isFatalKey].(bool)
			str := func(key string) string {
				s, _ := record.Attributes[key].(string)
				return s
			}
			row := LogRow{
				AppID:          appID,
				EASClientID:    info.easClientID,
				UpdateID:       info.updateID,
				Channel:        info.channel,
				RuntimeVersion: info.runtimeVersion,
				Platform:       info.platform,
				SessionID:      normalizeSessionID(str(sessionIDKey)),
				EventName:      eventName,
				SeverityNumber: record.SeverityNumber,
				SeverityText:   record.SeverityText,
				IsFatal:        isFatal,
				Body:           record.Body,
				Attributes:     marshalAttributes(record.Attributes, logEnvelopeKeys),
				OSName:         info.osName,
				OSVersion:      info.osVersion,
				DeviceModel:    info.deviceModel,
				AppVersion:     info.appVersion,
				SDKVersion:     info.sdkVersion,
				Timestamp:      clampTimestamp(record.TimeUnixNano, now),
			}
			row.ContentHash = contentHash(
				row.SessionID, row.EventName,
				strconv.FormatUint(record.TimeUnixNano, 10),
				strconv.Itoa(int(record.SeverityNumber)),
				row.Body, row.Attributes,
			)
			rows = append(rows, row)
		}
	}
	return rows
}

// Envelope keys already stored as dedicated columns; everything else stays in
// the attributes JSON (exception.type/message/stacktrace, user attributes).
var (
	logEnvelopeKeys = map[string]bool{
		EventNameKey: true,
		sessionIDKey: true,
		isFatalKey:   true,
	}
	metricEnvelopeKeys = map[string]bool{
		sessionIDKey:     true,
		routeNameKey:     true,
		customParamsKey:  true,
		pointUpdateIDKey: true,
	}
)

// marshalAttributes serializes the non-envelope attributes as JSON.
// encoding/json sorts map keys, so the output (and therefore the content
// hash) is deterministic across retries of the same batch.
func marshalAttributes(attrs map[string]any, envelope map[string]bool) string {
	kept := make(map[string]any, len(attrs))
	for key, value := range attrs {
		if envelope[key] || value == nil {
			continue
		}
		kept[key] = value
	}
	if len(kept) == 0 {
		return ""
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return ""
	}
	return string(out)
}

// contentHash fingerprints one row for retry deduplication: published SDKs
// re-send a whole batch after ANY non-2xx, so duplicate rows are a certainty,
// not an edge case. iOS only sends whole-second timestamps, so (session,
// name, nano) alone could collide across genuinely distinct records; the
// value/body and serialized attributes disambiguate. FNV-1a over the parts in
// fixed order, deterministic across processes.
func contentHash(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}
