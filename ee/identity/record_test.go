// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestFromRecord(t *testing.T) {
	t.Run("set strips the envelope and keeps the payload", func(t *testing.T) {
		req, ok := RequestFromRecord("app", "client", OpSet, map[string]any{
			"event.name": "$set",
			"session.id": "aaaa",
			"userId":     "u1",
		}, "203.0.113.7")
		require.True(t, ok)
		require.Equal(t, OpSet, req.Op)
		require.Equal(t, map[string]any{"userId": "u1"}, req.Attributes)
		require.Equal(t, "203.0.113.7", req.RemoteIP)
	})

	t.Run("set with only envelope is skipped", func(t *testing.T) {
		_, ok := RequestFromRecord("app", "client", OpSet, map[string]any{
			"event.name": "$set",
			"session.id": "aaaa",
		}, "")
		require.False(t, ok)
	})

	t.Run("unset reads its keys array and tolerates junk entries", func(t *testing.T) {
		req, ok := RequestFromRecord("app", "client", OpUnset, map[string]any{
			"event.name": "$unset",
			"keys":       []any{"userId", "", 42, "tenant"},
		}, "")
		require.True(t, ok)
		require.Equal(t, []string{"userId", "tenant"}, req.UnsetKeys)
	})

	t.Run("unset without usable keys is skipped", func(t *testing.T) {
		_, ok := RequestFromRecord("app", "client", OpUnset, map[string]any{"event.name": "$unset"}, "")
		require.False(t, ok)
		_, ok = RequestFromRecord("app", "client", OpUnset, map[string]any{"keys": "userId"}, "")
		require.False(t, ok)
	})

	t.Run("unknown op is skipped", func(t *testing.T) {
		_, ok := RequestFromRecord("app", "client", Op("identify"), map[string]any{"userId": "u1"}, "")
		require.False(t, ok)
	})
}

func TestCoalesceRequests(t *testing.T) {
	set := func(device string, kv map[string]any) Request {
		return Request{AppID: "app", EASClientID: device, Op: OpSet, Attributes: kv}
	}

	t.Run("adjacent sets of one device fold, later keys win", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			set("d1", map[string]any{"a": "1", "b": "1"}),
			set("d1", map[string]any{"b": "2"}),
			set("d2", map[string]any{"a": "x"}),
		})
		require.Len(t, out, 2)
		require.Equal(t, map[string]any{"a": "1", "b": "2"}, out[0].Attributes)
		require.Equal(t, "d2", out[1].EASClientID)
	})

	t.Run("a different op in between blocks the fold", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			set("d1", map[string]any{"a": "1"}),
			{AppID: "app", EASClientID: "d1", Op: OpUnset, UnsetKeys: []string{"a"}},
			set("d1", map[string]any{"a": "2"}),
		})
		require.Len(t, out, 3, "$set $unset $set must not collapse")
	})

	t.Run("set_once folds with first value winning", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			{AppID: "app", EASClientID: "d1", Op: OpSetOnce, Attributes: map[string]any{"ref": "organic"}},
			{AppID: "app", EASClientID: "d1", Op: OpSetOnce, Attributes: map[string]any{"ref": "paid", "v": "1"}},
		})
		require.Len(t, out, 1)
		require.Equal(t, map[string]any{"ref": "organic", "v": "1"}, out[0].Attributes)
	})

	t.Run("adjacent unsets append keys", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			{AppID: "app", EASClientID: "d1", Op: OpUnset, UnsetKeys: []string{"a"}},
			{AppID: "app", EASClientID: "d1", Op: OpUnset, UnsetKeys: []string{"b"}},
		})
		require.Len(t, out, 1)
		require.Equal(t, []string{"a", "b"}, out[0].UnsetKeys)
	})

	t.Run("interleaved devices preserve cross-device order", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			set("d1", map[string]any{"a": "1"}),
			set("d2", map[string]any{"a": "x"}),
			set("d1", map[string]any{"b": "2"}),
		})
		require.Len(t, out, 3)
		require.Equal(t, map[string]any{"a": "1"}, out[0].Attributes)
		require.Equal(t, "d2", out[1].EASClientID)
		require.Equal(t, map[string]any{"b": "2"}, out[2].Attributes)
	})

	t.Run("same client id in different apps does not fold", func(t *testing.T) {
		out := CoalesceRequests([]Request{
			set("d1", map[string]any{"a": "1"}),
			{AppID: "other-app", EASClientID: "d1", Op: OpSet, Attributes: map[string]any{"b": "2"}},
		})
		require.Len(t, out, 2)
	})
}
