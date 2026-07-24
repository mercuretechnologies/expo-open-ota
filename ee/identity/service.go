// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"context"
	"fmt"
	"time"
)

// Op is one identity operation as carried on the wire (the log event name).
// The vocabulary mirrors PostHog/Mixpanel/Amplitude on purpose: $set merges,
// $set_once only fills absent keys, $unset removes. There is deliberately no
// reset(): the eas_client_id cannot rotate, so logout is expressed as $unset.
type Op string

const (
	OpSet     Op = "$set"
	OpSetOnce Op = "$set_once"
	OpUnset   Op = "$unset"
)

// IsIdentityOp reports whether a log event name is one of the identity
// operations; the ingest route uses it to route identity events away from the
// telemetry path.
func IsIdentityOp(eventName string) bool {
	switch Op(eventName) {
	case OpSet, OpSetOnce, OpUnset:
		return true
	}
	return false
}

// IdentityMutator is the ingest write path: $set / $set_once / $unset with
// geo enrichment. Kept as its own interface so the vocabulary stays explicit.
type IdentityMutator interface {
	ApplySet(ctx context.Context, appID string, easClientID string, raw map[string]any, geo *Geo) (ApplyResult, error)
	ApplySetOnce(ctx context.Context, appID string, easClientID string, raw map[string]any, geo *Geo) (ApplyResult, error)
	ApplyUnset(ctx context.Context, appID string, easClientID string, keys []string, geo *Geo) (ApplyResult, error)
}

// Store is the full data surface the service needs: the ingest write path plus
// the dashboard read/CRUD queries. *PostgresIdentityStore implements it. The
// service is the single owner of the store, so both the ingest route and the
// dashboard handler go through it — which is also where license gating will
// sit (one gate for the whole feature).
type Store interface {
	IdentityMutator
	GetSchema(ctx context.Context, appID string) (Schema, error)
	UpsertSchemaKey(ctx context.Context, appID string, spec KeySpec) (KeySpec, error)
	DeleteSchemaKey(ctx context.Context, appID string, key string) (bool, error)
	SearchMetadataValues(ctx context.Context, appID string, key string, search string, limit int) ([]ValueCount, error)
	ListDevices(ctx context.Context, appID string, filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error)
	GetDevice(ctx context.Context, appID string, easClientID string) (*Device, error)
}

// Service owns the store and the geo resolver. The ingest route calls Apply;
// the dashboard handler calls the read/CRUD methods below. The route/handler
// own transport (decoding, response codes); the service owns semantics.
type Service struct {
	store Store
	geo   GeoResolver
}

// NewService builds the identity service. geo may be nil: identity works
// without a GeoLite2 database, devices simply stay unlocated.
func NewService(store Store, geo GeoResolver) *Service {
	return &Service{store: store, geo: geo}
}

// Dashboard read/CRUD surface. Thin delegations today; the license gate will
// live here in the enterprise batch so it covers ingest and dashboard alike.

func (s *Service) GetSchema(ctx context.Context, appID string) (Schema, error) {
	return s.store.GetSchema(ctx, appID)
}

func (s *Service) UpsertSchemaKey(ctx context.Context, appID string, spec KeySpec) (KeySpec, error) {
	return s.store.UpsertSchemaKey(ctx, appID, spec)
}

func (s *Service) DeleteSchemaKey(ctx context.Context, appID string, key string) (bool, error) {
	return s.store.DeleteSchemaKey(ctx, appID, key)
}

func (s *Service) SearchMetadataValues(ctx context.Context, appID string, key string, search string, limit int) ([]ValueCount, error) {
	return s.store.SearchMetadataValues(ctx, appID, key, search, limit)
}

func (s *Service) ListDevices(ctx context.Context, appID string, filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error) {
	return s.store.ListDevices(ctx, appID, filter, limit, cursor)
}

func (s *Service) GetDevice(ctx context.Context, appID string, easClientID string) (*Device, error) {
	return s.store.GetDevice(ctx, appID, easClientID)
}

// Request is one identity operation extracted from a log event.
type Request struct {
	AppID       string
	EASClientID string
	Op          Op
	// Attributes carries the key/value payload of $set and $set_once.
	Attributes map[string]any
	// UnsetKeys carries the key names of $unset.
	UnsetKeys []string
	// RemoteIP is the already-resolved client IP of the HTTP request that
	// delivered the batch (proxy handling happens upstream).
	RemoteIP string
}

func (s *Service) Apply(ctx context.Context, req Request) (ApplyResult, error) {
	start := time.Now()

	var geo *Geo
	if s.geo != nil && req.RemoteIP != "" {
		geo = s.geo.Resolve(req.RemoteIP)
	}

	var result ApplyResult
	var err error
	switch req.Op {
	case OpSet:
		result, err = s.store.ApplySet(ctx, req.AppID, req.EASClientID, req.Attributes, geo)
	case OpSetOnce:
		result, err = s.store.ApplySetOnce(ctx, req.AppID, req.EASClientID, req.Attributes, geo)
	case OpUnset:
		result, err = s.store.ApplyUnset(ctx, req.AppID, req.EASClientID, req.UnsetKeys, geo)
	default:
		// The op sentinel keeps the label set bounded: req.Op is wire input
		// here, not one of our constants.
		err = fmt.Errorf("unknown identity op %q", req.Op)
		observeApply(req.AppID, Op("unknown"), err, 0, time.Since(start))
		return ApplyResult{}, err
	}

	observeApply(req.AppID, req.Op, err, len(result.DroppedKeys), time.Since(start))
	if err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}
