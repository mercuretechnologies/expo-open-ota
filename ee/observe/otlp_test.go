// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Android wire shape: pretty-printed, intValue as OTLP-conformant string.
const androidLogsFixture = `{
  "resourceLogs": [
    {
      "resource": {
        "attributes": [
          { "key": "expo.eas_client.id", "value": { "stringValue": "8b9c1fe0-93b3-4b3a-8c1d-2f4a5e6b7c8d" } },
          { "key": "os.type", "value": { "stringValue": "linux" } },
          { "key": "service.name", "value": { "stringValue": "fr.skeat.myapp" } }
        ]
      },
      "scopeLogs": [
        {
          "scope": { "name": "expo-observe", "version": "56.0.16" },
          "logRecords": [
            {
              "timeUnixNano": 1767960489000000000,
              "severityNumber": 9,
              "severityText": "INFO",
              "body": { "stringValue": "" },
              "attributes": [
                { "key": "session.id", "value": { "stringValue": "aaaa-1111" } },
                { "key": "event.name", "value": { "stringValue": "$set" } },
                { "key": "userId", "value": { "stringValue": "user_42" } },
                { "key": "seats", "value": { "intValue": "12" } },
                { "key": "isInternal", "value": { "boolValue": true } }
              ]
            }
          ]
        }
      ],
      "schemaUrl": "https://opentelemetry.io/schemas/1.27.0"
    }
  ]
}`

// iOS wire shape: compact, intValue as a raw JSON number (deliberate OTLP
// deviation of the Swift client).
const iosLogsFixture = `{"resourceLogs":[{"resource":{"attributes":[{"key":"expo.eas_client.id","value":{"stringValue":"7A6B5C4D-3E2F-1A0B-9C8D-7E6F5A4B3C2D"}}]},"scopeLogs":[{"scope":{"name":"expo-observe","version":"56.0.16"},"logRecords":[{"timeUnixNano":1767960489000000000,"severityNumber":9,"severityText":"INFO","body":{"stringValue":"user logged in"},"attributes":[{"key":"event.name","value":{"stringValue":"$unset"}},{"key":"keys","value":{"arrayValue":{"values":[{"stringValue":"userId"},{"stringValue":"tenant"}]}}},{"key":"seats","value":{"intValue":42}}]}]}],"schemaUrl":"https://opentelemetry.io/schemas/1.27.0"}]}`

func TestDecodeLogsAndroidShape(t *testing.T) {
	batch, err := DecodeLogs([]byte(androidLogsFixture))
	require.NoError(t, err)
	require.Len(t, batch.Resources, 1)

	resource := batch.Resources[0]
	require.Equal(t, "8b9c1fe0-93b3-4b3a-8c1d-2f4a5e6b7c8d", resource.Attributes[EASClientIDKey])
	require.Len(t, resource.Records, 1)

	attrs := resource.Records[0].Attributes
	require.Equal(t, "$set", attrs[EventNameKey])
	require.Equal(t, "user_42", attrs["userId"])
	// Android string-form intValue decodes to a number.
	require.Equal(t, int64(12), attrs["seats"])
	require.Equal(t, true, attrs["isInternal"])
}

func TestDecodeLogsIOSShape(t *testing.T) {
	batch, err := DecodeLogs([]byte(iosLogsFixture))
	require.NoError(t, err)
	require.Len(t, batch.Resources, 1)

	attrs := batch.Resources[0].Records[0].Attributes
	require.Equal(t, "$unset", attrs[EventNameKey])
	// iOS raw-number intValue decodes to the same Go value as Android's.
	require.Equal(t, int64(42), attrs["seats"])
	// Nested arrayValue unwraps to []any of plain values.
	require.Equal(t, []any{"userId", "tenant"}, attrs["keys"])
}

func TestDecodeLogsTolerance(t *testing.T) {
	// Unknown fields and empty bodies are tolerated: rejecting destroys the
	// batch on the device.
	batch, err := DecodeLogs([]byte(`{"resourceLogs":[],"partialSuccess":{"whatever":1}}`))
	require.NoError(t, err)
	require.Empty(t, batch.Resources)

	batch, err = DecodeLogs([]byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, batch.Resources)

	// Structural garbage is a hard error (the 400 arm).
	_, err = DecodeLogs([]byte(`not json at all`))
	require.Error(t, err)

	// Unrepresentable values decode to nil instead of failing the batch.
	batch, err = DecodeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"x","value":{"intValue":"not-a-number"}}]},"scopeLogs":[]}]}`))
	require.NoError(t, err)
	require.Nil(t, batch.Resources[0].Attributes["x"])

	// kvlistValue unwraps to a nested map.
	batch, err = DecodeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"nested","value":{"kvlistValue":{"values":[{"key":"a","value":{"doubleValue":1.5}}]}}}]},"scopeLogs":[]}]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": 1.5}, batch.Resources[0].Attributes["nested"])
}
