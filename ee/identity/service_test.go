// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeMutator records the exact call the service dispatched, geo included.
// The embedded Store supplies the dashboard query methods (unused here) so the
// fake satisfies identity.Store.
type fakeMutator struct {
	Store
	calledOp Op
	raw      map[string]any
	keys     []string
	geo      *Geo
}

func (f *fakeMutator) ApplySet(_ context.Context, _ string, _ string, raw map[string]any, geo *Geo) (ApplyResult, error) {
	f.calledOp, f.raw, f.geo = OpSet, raw, geo
	return ApplyResult{}, nil
}

func (f *fakeMutator) ApplySetOnce(_ context.Context, _ string, _ string, raw map[string]any, geo *Geo) (ApplyResult, error) {
	f.calledOp, f.raw, f.geo = OpSetOnce, raw, geo
	return ApplyResult{}, nil
}

func (f *fakeMutator) ApplyUnset(_ context.Context, _ string, _ string, keys []string, geo *Geo) (ApplyResult, error) {
	f.calledOp, f.keys, f.geo = OpUnset, keys, geo
	return ApplyResult{}, nil
}

type fakeResolver struct {
	geo    *Geo
	lastIP string
}

func (f *fakeResolver) Resolve(ip string) *Geo {
	f.lastIP = ip
	return f.geo
}

func TestIsIdentityOp(t *testing.T) {
	require.True(t, IsIdentityOp("$set"))
	require.True(t, IsIdentityOp("$set_once"))
	require.True(t, IsIdentityOp("$unset"))
	require.False(t, IsIdentityOp("identify"))
	require.False(t, IsIdentityOp("exception"))
	require.False(t, IsIdentityOp(""))
}

func TestServiceDispatchAndGeo(t *testing.T) {
	appID, clientID := uuid.NewString(), uuid.NewString()
	geo := &Geo{CountryCode: strPtr("FR")}

	t.Run("set carries attributes and resolved geo", func(t *testing.T) {
		mutator := &fakeMutator{}
		resolver := &fakeResolver{geo: geo}
		service := NewService(mutator, resolver)
		_, err := service.Apply(context.Background(), Request{
			AppID: appID, EASClientID: clientID, Op: OpSet,
			Attributes: map[string]any{"userId": "u1"},
			RemoteIP:   "203.0.113.7",
		})
		require.NoError(t, err)
		require.Equal(t, OpSet, mutator.calledOp)
		require.Equal(t, map[string]any{"userId": "u1"}, mutator.raw)
		require.Equal(t, geo, mutator.geo)
		require.Equal(t, "203.0.113.7", resolver.lastIP)
	})

	t.Run("set_once and unset dispatch to their store paths", func(t *testing.T) {
		mutator := &fakeMutator{}
		service := NewService(mutator, nil)
		_, err := service.Apply(context.Background(), Request{AppID: appID, EASClientID: clientID, Op: OpSetOnce, Attributes: map[string]any{"a": "b"}})
		require.NoError(t, err)
		require.Equal(t, OpSetOnce, mutator.calledOp)

		_, err = service.Apply(context.Background(), Request{AppID: appID, EASClientID: clientID, Op: OpUnset, UnsetKeys: []string{"userId"}})
		require.NoError(t, err)
		require.Equal(t, OpUnset, mutator.calledOp)
		require.Equal(t, []string{"userId"}, mutator.keys)
	})

	t.Run("nil resolver and empty ip mean nil geo", func(t *testing.T) {
		mutator := &fakeMutator{}
		service := NewService(mutator, nil)
		_, err := service.Apply(context.Background(), Request{AppID: appID, EASClientID: clientID, Op: OpSet})
		require.NoError(t, err)
		require.Nil(t, mutator.geo)

		resolver := &fakeResolver{geo: geo}
		service = NewService(mutator, resolver)
		_, err = service.Apply(context.Background(), Request{AppID: appID, EASClientID: clientID, Op: OpSet})
		require.NoError(t, err)
		require.Empty(t, resolver.lastIP, "resolver must not be called without an IP")
	})

	t.Run("unknown op is an error", func(t *testing.T) {
		service := NewService(&fakeMutator{}, nil)
		_, err := service.Apply(context.Background(), Request{AppID: appID, EASClientID: clientID, Op: Op("identify")})
		require.ErrorContains(t, err, "unknown identity op")
	})
}

func TestGeoLite2ResolverGuards(t *testing.T) {
	// Constructor surfaces a clear error on a missing database.
	_, err := NewGeoLite2Resolver("/nonexistent/GeoLite2-City.mmdb")
	require.Error(t, err)

	// The IP guards run before any database access, so an empty resolver is
	// safe: garbage, private, loopback and unspecified IPs resolve to nil.
	resolver := &GeoLite2Resolver{}
	for _, ip := range []string{"", "not-an-ip", "10.1.2.3", "192.168.1.1", "127.0.0.1", "0.0.0.0", "::1", "fd00::1"} {
		require.Nil(t, resolver.Resolve(ip), "ip %q", ip)
	}
	// Public IP with no database still resolves to nil instead of panicking.
	require.Nil(t, resolver.Resolve("203.0.113.7"))
}
