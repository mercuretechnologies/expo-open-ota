// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package identity

import (
	"context"
	"encoding/json"
	"expo-open-ota/ee/licensing"
	"expo-open-ota/internal/database"
	"expo-open-ota/internal/database/postgres/pgdb"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// FreeDeviceLimit is how many devices an app may keep WITHOUT a valid
	// enterprise license. Beyond it, the oldest-by-last_seen device is evicted
	// when a new one is registered, so an unlicensed app tracks its 1000 most
	// recently active installs. A valid license lifts the cap entirely.
	FreeDeviceLimit = 1000
	// maxEvictPerWrite bounds how many devices a single write evicts, so a
	// large app that just lost its license shrinks toward the cap over many
	// registrations instead of in one giant transaction.
	maxEvictPerWrite = 50
)

type PostgresIdentityStore struct {
	engine *database.Engine
	// licenseValid reports whether a valid enterprise license is active. Nil
	// or true means no cap; false activates the device-limit eviction. It
	// defaults to licensing.IsEnterprise, imported directly ON PURPOSE: the gate
	// must live in EE code so bypassing the cap means editing an EE-licensed
	// file, not swapping a func handed in from the MIT composition root. A field
	// so same-package tests flip it without a signed key.
	licenseValid func() bool
	// deviceLimit is the free-tier cap, defaulting to FreeDeviceLimit; a field
	// so tests can shrink it instead of seeding a thousand rows.
	deviceLimit int
}

func NewPostgresIdentityStore(engine *database.Engine) *PostgresIdentityStore {
	return &PostgresIdentityStore{engine: engine, licenseValid: licensing.IsEnterprise, deviceLimit: FreeDeviceLimit}
}

// capActive reports whether the free-tier device cap should be enforced: only
// when a license check is configured and it says unlicensed.
func (s *PostgresIdentityStore) capActive() bool {
	return s.licenseValid != nil && !s.licenseValid()
}

// toPgUUID differs from store.ToPgUUID on purpose: identity ids come from the
// unauthenticated wire, so a parse failure must surface as an error the caller
// can act on, not as a zero UUID silently written to the database.
func toPgUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid uuid %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func specFromRow(row pgdb.IdentitySchema) KeySpec {
	return KeySpec{Key: row.Key, Type: ValueType(row.ValueType), MaxLength: int(row.MaxLength)}
}

func schemaFromRows(rows []pgdb.IdentitySchema) Schema {
	schema := make(Schema, len(rows))
	for _, row := range rows {
		schema[row.Key] = specFromRow(row)
	}
	return schema
}

func deviceFromRow(row pgdb.DeviceIdentity) (Device, error) {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return Device{}, fmt.Errorf("corrupt device metadata: %w", err)
		}
	}
	return Device{
		AppID:       uuid.UUID(row.AppID.Bytes).String(),
		EASClientID: uuid.UUID(row.EasClientID.Bytes).String(),
		Metadata:    metadata,
		CountryCode: row.CountryCode,
		City:        row.City,
		Lat:         row.Lat,
		Lng:         row.Lng,
		FirstSeenAt: row.FirstSeenAt.Time,
		LastSeenAt:  row.LastSeenAt.Time,
	}, nil
}

// GetSchema returns the app's allowlist. An app with no declared keys gets an
// empty schema, under which Sanitize drops everything: identity is opt-in per
// app by declaring keys, there is no implicit passthrough.
func (s *PostgresIdentityStore) GetSchema(ctx context.Context, appID string) (Schema, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := s.engine.Queries.ListIdentitySchemaKeys(ctx, appUUID)
	if err != nil {
		return nil, fmt.Errorf("listing identity schema: %w", err)
	}
	return schemaFromRows(rows), nil
}

func (s *PostgresIdentityStore) UpsertSchemaKey(ctx context.Context, appID string, spec KeySpec) (KeySpec, error) {
	if spec.MaxLength == 0 {
		spec.MaxLength = DefaultMaxLength
	}
	if err := ValidateKeySpec(spec); err != nil {
		return KeySpec{}, err
	}
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return KeySpec{}, err
	}

	var saved KeySpec
	err = s.engine.WithTx(ctx, func(q *pgdb.Queries) error {
		// The key-count cap runs in the same transaction as the insert so two
		// concurrent declarations cannot both slip under the limit. Updating
		// an already-declared key is always allowed, even at the cap.
		existing, err := q.ListIdentitySchemaKeys(ctx, appUUID)
		if err != nil {
			return fmt.Errorf("listing identity schema: %w", err)
		}
		if _, declared := schemaFromRows(existing)[spec.Key]; !declared && len(existing) >= MaxSchemaKeys {
			return ErrTooManySchemaKeys
		}
		row, err := q.UpsertIdentitySchemaKey(ctx, pgdb.UpsertIdentitySchemaKeyParams{
			AppID:     appUUID,
			Key:       spec.Key,
			ValueType: string(spec.Type),
			MaxLength: int32(spec.MaxLength),
		})
		if err != nil {
			return fmt.Errorf("upserting identity schema key: %w", err)
		}
		saved = specFromRow(row)
		return nil
	})
	if err != nil {
		return KeySpec{}, err
	}
	return saved, nil
}

// DeleteSchemaKey removes a key from the allowlist and wipes its autocomplete
// stats in the same transaction, so searchMetadata never suggests values of a
// removed key. Values already merged into device metadata are left in place;
// they stop being accepted and stop being suggested.
func (s *PostgresIdentityStore) DeleteSchemaKey(ctx context.Context, appID string, key string) (bool, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return false, err
	}
	var deleted bool
	err = s.engine.WithTx(ctx, func(q *pgdb.Queries) error {
		tag, err := q.DeleteIdentitySchemaKey(ctx, pgdb.DeleteIdentitySchemaKeyParams{AppID: appUUID, Key: key})
		if err != nil {
			return fmt.Errorf("deleting identity schema key: %w", err)
		}
		if err := q.DeleteIdentityValueStatsForKey(ctx, pgdb.DeleteIdentityValueStatsForKeyParams{AppID: appUUID, Key: key}); err != nil {
			return fmt.Errorf("deleting identity value stats: %w", err)
		}
		deleted = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// statOp is one pending change to identity_value_stats. Ops are executed in
// deterministic (key, value) order across the whole transaction: increments
// and decrements both take row locks held until commit, and Go map iteration
// order is random, so unordered execution lets two identifies of DIFFERENT
// devices that share stat rows (same tenant, same plan...) acquire those locks
// in opposite orders and deadlock. Sorting by key alone is not enough: A
// moving tenant acme->globex and B moving globex->acme would still cross.
type statOp struct {
	key       string
	value     string
	decrement bool
}

type mutationKind int

const (
	mutationSet mutationKind = iota
	mutationSetOnce
	mutationUnset
)

// ApplySet runs one $set against the store: sanitize the raw wire metadata
// against the allowlist, merge it into the device row (per-key merge, incoming
// keys win), refresh geo when provided, and keep the per-value device counts
// in sync. Everything happens in one transaction with the device row locked,
// so concurrent identifies of the same install serialize and the counts never
// drift from the merges that produced them.
func (s *PostgresIdentityStore) ApplySet(ctx context.Context, appID string, easClientID string, raw map[string]any, geo *Geo) (ApplyResult, error) {
	return s.mutate(ctx, appID, easClientID, mutationSet, raw, nil, geo)
}

// ApplySetOnce is $set_once: a sanitized key is written only when the device
// does not hold it yet; keys already present are silently left untouched
// (same contract as PostHog/Mixpanel/Amplitude).
func (s *PostgresIdentityStore) ApplySetOnce(ctx context.Context, appID string, easClientID string, raw map[string]any, geo *Geo) (ApplyResult, error) {
	return s.mutate(ctx, appID, easClientID, mutationSetOnce, raw, nil, geo)
}

// ApplyUnset removes keys from the device and moves the stat counts down.
// Keys the device does not hold are ignored, which also bounds the work to
// the (schema-capped) size of the device's metadata no matter how many keys
// a hostile payload lists. Unset works even for keys since removed from the
// allowlist: it is the cleanup path.
func (s *PostgresIdentityStore) ApplyUnset(ctx context.Context, appID string, easClientID string, keys []string, geo *Geo) (ApplyResult, error) {
	return s.mutate(ctx, appID, easClientID, mutationUnset, nil, keys, geo)
}

func (s *PostgresIdentityStore) mutate(ctx context.Context, appID string, easClientID string, kind mutationKind, raw map[string]any, unsetKeys []string, geo *Geo) (ApplyResult, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return ApplyResult{}, err
	}
	clientUUID, err := toPgUUID(easClientID)
	if err != nil {
		return ApplyResult{}, err
	}

	var result ApplyResult
	err = s.engine.WithTx(ctx, func(q *pgdb.Queries) error {
		var sanitized map[string]any
		var dropped []string
		if kind != mutationUnset {
			// The schema read shares the transaction so a concurrent allowlist
			// change cannot produce a merge mixing two versions of the schema.
			// Unset skips it entirely: it bypasses the allowlist by design.
			schemaRows, err := q.ListIdentitySchemaKeys(ctx, appUUID)
			if err != nil {
				return fmt.Errorf("listing identity schema: %w", err)
			}
			sanitized, dropped = schemaFromRows(schemaRows).Sanitize(raw)
		}

		inserted, err := q.EnsureDeviceIdentity(ctx, pgdb.EnsureDeviceIdentityParams{AppID: appUUID, EasClientID: clientUUID})
		if err != nil {
			return fmt.Errorf("ensuring device row: %w", err)
		}
		current, err := q.GetDeviceIdentityForUpdate(ctx, pgdb.GetDeviceIdentityForUpdateParams{AppID: appUUID, EasClientID: clientUUID})
		if err != nil {
			return fmt.Errorf("locking device row: %w", err)
		}
		previous := map[string]any{}
		if len(current.Metadata) > 0 {
			if err := json.Unmarshal(current.Metadata, &previous); err != nil {
				return fmt.Errorf("corrupt device metadata: %w", err)
			}
		}

		merged := make(map[string]any, len(previous)+len(sanitized))
		for key, value := range previous {
			merged[key] = value
		}
		var ops []statOp
		switch kind {
		case mutationSet, mutationSetOnce:
			for key, value := range sanitized {
				oldValue, existed := previous[key]
				if kind == mutationSetOnce && existed {
					continue
				}
				merged[key] = value
				newRendered := RenderValue(value)
				if existed {
					oldRendered := RenderValue(oldValue)
					if oldRendered == newRendered {
						continue
					}
					ops = append(ops, statOp{key: key, value: oldRendered, decrement: true})
				}
				ops = append(ops, statOp{key: key, value: newRendered})
			}
		case mutationUnset:
			for _, key := range unsetKeys {
				oldValue, existed := previous[key]
				if !existed {
					continue
				}
				// Also remove from previous so a duplicated key in the payload
				// cannot decrement the same stat row twice.
				delete(previous, key)
				delete(merged, key)
				ops = append(ops, statOp{key: key, value: RenderValue(oldValue), decrement: true})
			}
		}
		mergedJSON, err := json.Marshal(merged)
		if err != nil {
			return fmt.Errorf("marshalling merged metadata: %w", err)
		}

		params := pgdb.UpdateDeviceIdentityParams{
			AppID:       appUUID,
			EasClientID: clientUUID,
			Metadata:    mergedJSON,
		}
		if geo != nil {
			params.CountryCode = geo.CountryCode
			params.City = geo.City
			params.Lat = geo.Lat
			params.Lng = geo.Lng
		}
		updated, err := q.UpdateDeviceIdentity(ctx, params)
		if err != nil {
			return fmt.Errorf("updating device row: %w", err)
		}

		// Free-tier cap: only a genuine new-device insert can push an app over
		// the limit, so the count + eviction run solely on that path. Evicted
		// devices' stat decrements fold into the same sorted op sequence below,
		// keeping the deadlock-free (key,value) ordering; their rows are
		// deleted after the stats settle.
		var evictIDs []pgtype.UUID
		if inserted == 1 && s.capActive() {
			count, err := q.CountDevices(ctx, appUUID)
			if err != nil {
				return fmt.Errorf("counting devices: %w", err)
			}
			if overflow := count - int64(s.deviceLimit); overflow > 0 {
				limit := overflow
				if limit > maxEvictPerWrite {
					limit = maxEvictPerWrite
				}
				oldest, err := q.OldestDevicesExcluding(ctx, pgdb.OldestDevicesExcludingParams{
					AppID: appUUID, EasClientID: clientUUID, Lim: int32(limit),
				})
				if err != nil {
					return fmt.Errorf("selecting devices to evict: %w", err)
				}
				for _, row := range oldest {
					evictIDs = append(evictIDs, row.EasClientID)
					evictedMeta := map[string]any{}
					if len(row.Metadata) > 0 {
						if err := json.Unmarshal(row.Metadata, &evictedMeta); err != nil {
							return fmt.Errorf("corrupt evicted device metadata: %w", err)
						}
					}
					for key, value := range evictedMeta {
						ops = append(ops, statOp{key: key, value: RenderValue(value), decrement: true})
					}
				}
			}
		}

		sort.Slice(ops, func(i, j int) bool {
			if ops[i].key != ops[j].key {
				return ops[i].key < ops[j].key
			}
			if ops[i].value != ops[j].value {
				return ops[i].value < ops[j].value
			}
			return ops[i].decrement
		})
		for _, op := range ops {
			if op.decrement {
				decParams := pgdb.DecrementIdentityValueStatParams{AppID: appUUID, Key: op.key, Value: op.value}
				if err := q.DecrementIdentityValueStat(ctx, decParams); err != nil {
					return fmt.Errorf("decrementing value stat: %w", err)
				}
				// Prune immediately: same row, already locked by the
				// decrement, so this cannot introduce a new lock ordering.
				delParams := pgdb.DeleteZeroIdentityValueStatsParams{AppID: appUUID, Key: op.key, Value: op.value}
				if err := q.DeleteZeroIdentityValueStats(ctx, delParams); err != nil {
					return fmt.Errorf("pruning zero value stat: %w", err)
				}
				continue
			}
			incParams := pgdb.IncrementIdentityValueStatParams{AppID: appUUID, Key: op.key, Value: op.value}
			if err := q.IncrementIdentityValueStat(ctx, incParams); err != nil {
				return fmt.Errorf("incrementing value stat: %w", err)
			}
		}

		// Delete the evicted device rows now that their stats are decremented.
		if len(evictIDs) > 0 {
			if err := q.DeleteDevices(ctx, pgdb.DeleteDevicesParams{AppID: appUUID, ClientIds: evictIDs}); err != nil {
				return fmt.Errorf("deleting evicted devices: %w", err)
			}
		}

		device, err := deviceFromRow(updated)
		if err != nil {
			return err
		}
		result = ApplyResult{Device: device, DroppedKeys: dropped}
		return nil
	})
	if err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

// GetDevice returns nil when the install was never seen.
func (s *PostgresIdentityStore) GetDevice(ctx context.Context, appID string, easClientID string) (*Device, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return nil, err
	}
	clientUUID, err := toPgUUID(easClientID)
	if err != nil {
		return nil, err
	}
	row, err := s.engine.Queries.GetDeviceIdentity(ctx, pgdb.GetDeviceIdentityParams{AppID: appUUID, EasClientID: clientUUID})
	if err != nil {
		if database.IsNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting device identity: %w", err)
	}
	device, err := deviceFromRow(row)
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// ListDevices returns one page of the device inventory, newest-seen first,
// keyset-paginated. A nil cursor starts at the first page; the returned cursor
// is nil on the last page. An optional filter narrows to installs whose
// metadata contains the key/value (served by the GIN index).
func (s *PostgresIdentityStore) ListDevices(ctx context.Context, appID string, filter *MetadataFilter, limit int, cursor *DeviceCursor) ([]Device, *DeviceCursor, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case limit < 1:
		limit = DefaultDevicesPageSize
	case limit > MaxDevicesPageSize:
		limit = MaxDevicesPageSize
	}

	params := pgdb.ListDevicesParams{
		AppID: appUUID,
		// One extra row detects whether a next page exists.
		Lim: int32(limit + 1),
	}
	if filter != nil {
		filterJSON, err := json.Marshal(map[string]string{filter.Key: filter.Value})
		if err != nil {
			return nil, nil, fmt.Errorf("marshalling device filter: %w", err)
		}
		params.Filter = filterJSON
	}
	if cursor != nil {
		params.BeforeLastSeen = pgtype.Timestamptz{Time: cursor.LastSeenAt, Valid: true}
		cursorUUID, err := toPgUUID(cursor.EASClientID)
		if err != nil {
			return nil, nil, err
		}
		params.BeforeClientID = cursorUUID
	}

	rows, err := s.engine.Queries.ListDevices(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("listing devices: %w", err)
	}

	var next *DeviceCursor
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = &DeviceCursor{
			LastSeenAt:  last.LastSeenAt.Time,
			EASClientID: uuid.UUID(last.EasClientID.Bytes).String(),
		}
	}
	devices := make([]Device, 0, len(rows))
	for _, row := range rows {
		device, err := deviceFromRow(row)
		if err != nil {
			return nil, nil, err
		}
		devices = append(devices, device)
	}
	return devices, next, nil
}

// SearchMetadataValues is the autocomplete behind searchMetadata: top values
// of one key ranked by device count, optionally narrowed by a substring. The
// two arms are separate prepared statements on purpose (see queries.sql).
func (s *PostgresIdentityStore) SearchMetadataValues(ctx context.Context, appID string, key string, search string, limit int) ([]ValueCount, error) {
	appUUID, err := toPgUUID(appID)
	if err != nil {
		return nil, err
	}
	switch {
	case limit < 1:
		limit = 20
	case limit > 100:
		limit = 100
	}

	if search == "" {
		rows, err := s.engine.Queries.TopIdentityValues(ctx, pgdb.TopIdentityValuesParams{
			AppID:      appUUID,
			Key:        key,
			MaxResults: int32(limit),
		})
		if err != nil {
			return nil, fmt.Errorf("listing top identity values: %w", err)
		}
		values := make([]ValueCount, 0, len(rows))
		for _, row := range rows {
			values = append(values, ValueCount{Value: row.Value, DeviceCount: row.DeviceCount})
		}
		return values, nil
	}

	rows, err := s.engine.Queries.SearchIdentityValues(ctx, pgdb.SearchIdentityValuesParams{
		AppID:      appUUID,
		Key:        key,
		Search:     search,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("searching identity values: %w", err)
	}
	values := make([]ValueCount, 0, len(rows))
	for _, row := range rows {
		values = append(values, ValueCount{Value: row.Value, DeviceCount: row.DeviceCount})
	}
	return values, nil
}
