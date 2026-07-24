// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

// Package observe receives expo-observe telemetry: the OTLP/JSON ingestion
// routes, decoding, and dispatch. It sees EVERY log record a device ships;
// identity operations ($set, $set_once, $unset) are routed to ee/identity,
// and the remaining telemetry records will feed the ClickHouse path when it
// lands. Identity dispatch is NOT license-gated (the identity feature is free);
// the future telemetry path is where the enterprise gate will sit.
package observe

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Minimal, tolerant decoder for the OTLP/JSON bodies the expo-observe SDK
// POSTs. Deliberately hand-rolled over the otlp protobuf bindings: we only
// need resource attributes and per-record attributes, and the two platforms
// diverge from strict OTLP anyway (iOS sends intValue as a raw JSON number
// where the spec says string). Unknown fields are ignored by encoding/json,
// which is exactly the tolerance we want: rejecting a batch destroys it on
// the device. The decoded types are neutral on purpose: the ClickHouse
// flattener will extend them (timestamps, severity, body) without another
// decoder.

// LogBatch is one decoded /v1/logs body.
type LogBatch struct {
	Resources []ResourceLogs
}

// ResourceLogs is one device session's worth of records: the resource
// attributes (device, app, update...) and the log records they scope.
type ResourceLogs struct {
	Attributes map[string]any
	Records    []LogRecord
}

// LogRecord is one log record's attributes. Telemetry fields (timestamp,
// severity, body) are not decoded yet: nothing consumes them before the
// ClickHouse path exists.
type LogRecord struct {
	Attributes map[string]any
}

// EASClientIDKey is the resource attribute carrying the persistent
// per-install UUID from expo-eas-client.
const EASClientIDKey = "expo.eas_client.id"

// EventNameKey is the record attribute carrying the log event name; it is
// what identity operations are recognized by. Deliberately also defined in
// ee/identity (recordEventNameKey): observe reads it here to classify
// identity-vs-telemetry, identity strips it there as envelope. The dependency
// direction (observe → identity) and the future flattener wanting it as a real
// column both forbid collapsing the two into one owner today.
const EventNameKey = "event.name"

// DecodeLogs parses an OTLP/JSON logs body. An error means the body is not
// JSON we can read at all; a readable body with zero records is normal.
func DecodeLogs(body []byte) (LogBatch, error) {
	var decoded otlpLogsBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		return LogBatch{}, fmt.Errorf("unreadable OTLP logs body: %w", err)
	}
	batch := LogBatch{Resources: make([]ResourceLogs, 0, len(decoded.ResourceLogs))}
	for _, resource := range decoded.ResourceLogs {
		entry := ResourceLogs{Attributes: kvToMap(resource.Resource.Attributes)}
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				entry.Records = append(entry.Records, LogRecord{Attributes: kvToMap(record.Attributes)})
			}
		}
		batch.Resources = append(batch.Resources, entry)
	}
	return batch, nil
}

type otlpLogsBody struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpKV `json:"attributes"`
}

type otlpScopeLogs struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpLogRecord struct {
	Attributes []otlpKV `json:"attributes"`
}

type otlpKV struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string          `json:"stringValue"`
	IntValue    json.RawMessage  `json:"intValue"`
	DoubleValue *float64         `json:"doubleValue"`
	BoolValue   *bool            `json:"boolValue"`
	ArrayValue  *otlpArrayValue  `json:"arrayValue"`
	KvlistValue *otlpKvlistValue `json:"kvlistValue"`
}

type otlpArrayValue struct {
	Values []otlpAnyValue `json:"values"`
}

type otlpKvlistValue struct {
	Values []otlpKV `json:"values"`
}

// toGo unwraps the single-key AnyValue union into plain Go values. intValue
// accepts both wire forms: Android emits the OTLP-conformant string ("42"),
// iOS deliberately emits a raw JSON number (42). Unrepresentable values decode
// to nil; downstream consumers (identity's Sanitize, the future flattener)
// drop them.
func (v otlpAnyValue) toGo() any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case len(v.IntValue) > 0:
		var num json.Number
		if err := json.Unmarshal(v.IntValue, &num); err == nil {
			if i, err := num.Int64(); err == nil {
				return i
			}
		}
		var str string
		if err := json.Unmarshal(v.IntValue, &str); err == nil {
			if i, err := strconv.ParseInt(str, 10, 64); err == nil {
				return i
			}
		}
		return nil
	case v.DoubleValue != nil:
		return *v.DoubleValue
	case v.BoolValue != nil:
		return *v.BoolValue
	case v.ArrayValue != nil:
		values := make([]any, 0, len(v.ArrayValue.Values))
		for _, item := range v.ArrayValue.Values {
			values = append(values, item.toGo())
		}
		return values
	case v.KvlistValue != nil:
		return kvToMap(v.KvlistValue.Values)
	}
	return nil
}

func kvToMap(kvs []otlpKV) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if kv.Key == "" {
			continue
		}
		m[kv.Key] = kv.Value.toGo()
	}
	return m
}
