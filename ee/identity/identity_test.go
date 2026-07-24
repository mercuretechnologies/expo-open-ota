// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateKeySpec(t *testing.T) {
	valid := []KeySpec{
		{Key: "userId", Type: ValueTypeString, MaxLength: 256},
		{Key: "is_internal", Type: ValueTypeBoolean, MaxLength: 1},
		{Key: "org.tier-2", Type: ValueTypeNumber, MaxLength: 64},
	}
	for _, spec := range valid {
		require.NoError(t, ValidateKeySpec(spec), spec.Key)
	}

	invalid := []KeySpec{
		{Key: "", Type: ValueTypeString, MaxLength: 10},
		{Key: "_leading", Type: ValueTypeString, MaxLength: 10},
		{Key: "has space", Type: ValueTypeString, MaxLength: 10},
		{Key: strings.Repeat("k", 65), Type: ValueTypeString, MaxLength: 10},
		{Key: "ok", Type: ValueType("uuid"), MaxLength: 10},
		{Key: "ok", Type: ValueTypeString, MaxLength: 0},
		{Key: "ok", Type: ValueTypeString, MaxLength: MaxLengthCeiling + 1},
	}
	for _, spec := range invalid {
		require.Error(t, ValidateKeySpec(spec), "%+v", spec)
	}
}

func TestSanitize(t *testing.T) {
	schema := Schema{
		"userId":     {Key: "userId", Type: ValueTypeString, MaxLength: 8},
		"seats":      {Key: "seats", Type: ValueTypeNumber, MaxLength: DefaultMaxLength},
		"isInternal": {Key: "isInternal", Type: ValueTypeBoolean, MaxLength: DefaultMaxLength},
	}

	sanitized, dropped := schema.Sanitize(map[string]any{
		"userId":     "user_42",
		"seats":      int64(12),
		"isInternal": true,
		// Undeclared key: the 50k-keys attack lands here and dies here.
		"junk": "x",
		// Declared key, wrong JSON type.
		"seats2":     "12",
		"userIdLong": "way too long for the limit",
	})
	require.Equal(t, map[string]any{
		"userId":     "user_42",
		"seats":      float64(12),
		"isInternal": true,
	}, sanitized)
	require.ElementsMatch(t, []string{"junk", "seats2", "userIdLong"}, dropped)

	t.Run("type mismatches drop the entry only", func(t *testing.T) {
		sanitized, dropped := schema.Sanitize(map[string]any{
			"userId":     42,                       // number into string
			"seats":      "12",                     // string into number
			"isInternal": "true",                   // string into boolean
			"junk":       map[string]any{"a": "b"}, // containers never pass
		})
		require.Empty(t, sanitized)
		require.Len(t, dropped, 4)
	})

	t.Run("oversized strings are dropped, not truncated", func(t *testing.T) {
		sanitized, dropped := schema.Sanitize(map[string]any{"userId": "123456789"})
		require.Empty(t, sanitized)
		require.Equal(t, []string{"userId"}, dropped)
	})

	t.Run("max length counts runes, not bytes", func(t *testing.T) {
		sanitized, dropped := schema.Sanitize(map[string]any{"userId": "éèêëàâüö"})
		require.Empty(t, dropped)
		require.Equal(t, "éèêëàâüö", sanitized["userId"])
	})

	t.Run("json.Number and unsigned ints coerce like numbers", func(t *testing.T) {
		sanitized, dropped := schema.Sanitize(map[string]any{"seats": json.Number("42")})
		require.Empty(t, dropped)
		require.Equal(t, float64(42), sanitized["seats"])
		sanitized, _ = schema.Sanitize(map[string]any{"seats": uint64(7)})
		require.Equal(t, float64(7), sanitized["seats"])
		_, dropped = schema.Sanitize(map[string]any{"seats": json.Number("not-a-number")})
		require.Equal(t, []string{"seats"}, dropped)
	})

	t.Run("non-finite numbers are dropped", func(t *testing.T) {
		for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			sanitized, dropped := schema.Sanitize(map[string]any{"seats": v})
			require.Empty(t, sanitized)
			require.Equal(t, []string{"seats"}, dropped)
		}
	})

	t.Run("null and nil never pass", func(t *testing.T) {
		sanitized, dropped := schema.Sanitize(map[string]any{"userId": nil})
		require.Empty(t, sanitized)
		require.Equal(t, []string{"userId"}, dropped)
	})

	t.Run("empty schema drops everything", func(t *testing.T) {
		sanitized, dropped := Schema{}.Sanitize(map[string]any{"userId": "x"})
		require.Empty(t, sanitized)
		require.Equal(t, []string{"userId"}, dropped)
	})
}

func TestRenderValue(t *testing.T) {
	require.Equal(t, "acme", RenderValue("acme"))
	require.Equal(t, "true", RenderValue(true))
	require.Equal(t, "false", RenderValue(false))
	// Integral floats render without a decimal part: 42 identified from JS and
	// 42.0 re-decoded from JSONB must count as the same value.
	require.Equal(t, "42", RenderValue(float64(42)))
	require.Equal(t, "42.5", RenderValue(42.5))
}
