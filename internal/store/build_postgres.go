package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"xprem/internal/database"
	"xprem/internal/database/postgres/pgdb"
	"xprem/internal/types"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresBuildStore struct{ engine *database.Engine }

func NewPostgresBuildStore(engine *database.Engine) *PostgresBuildStore {
	return &PostgresBuildStore{engine: engine}
}

func buildRecord(row pgdb.Build) (*types.BuildRecord, error) {
	record := &types.BuildRecord{ID: row.ID.String(), AppID: row.AppID.String(), AppIdentifierID: row.AppIdentifierID.String(), Platform: row.Platform, ApplicationID: row.ApplicationID, Status: row.Status, ArtifactType: row.ArtifactType, Size: row.Size, SHA256: row.Sha256, ArtifactKey: row.ArtifactKey, ActorType: row.ActorType, ActorID: row.ActorID, ActorDisplay: row.ActorDisplay, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.ReadyAt.Valid {
		record.ReadyAt = &row.ReadyAt.Time
	}
	if err := json.Unmarshal(row.Metadata, &record.Metadata); err != nil {
		return nil, err
	}
	record.Metadata.StartedAt = row.StartedAt.Time
	if row.FinishedAt.Valid {
		record.Metadata.FinishedAt = row.FinishedAt.Time
	}
	if row.DurationMs != nil {
		record.Metadata.DurationMs = *row.DurationMs
	}
	return record, nil
}

// Timing lives in its own columns, so the JSON document only keeps the rest.
func buildMetadataJSON(metadata types.BuildMetadata) ([]byte, error) {
	metadata.StartedAt, metadata.FinishedAt, metadata.DurationMs = time.Time{}, time.Time{}, 0
	// StartedAt, FinishedAt & DurationMS  has omitEmpty attributes, so setting them to the previous value = removing them from the json
	// btw metadata is not a pointer so it's a copy
	return json.Marshal(metadata)
}

// A duration without a finish time is passed through so builds_duration rejects it.
func buildTiming(metadata types.BuildMetadata) (pgtype.Timestamptz, *int64) {
	if metadata.FinishedAt.IsZero() && metadata.DurationMs == 0 {
		return pgtype.Timestamptz{}, nil
	}
	duration := metadata.DurationMs
	return pgtype.Timestamptz{Time: metadata.FinishedAt, Valid: !metadata.FinishedAt.IsZero()}, &duration
}

func buildNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return &ErrResourceNotFound{Resource: "build", Identifier: "requested build"}
	}
	return err
}

// Create inserts the record, or returns the row that already holds its ID.
func (s *PostgresBuildStore) Create(ctx context.Context, record types.BuildRecord) (*types.BuildRecord, bool, error) {
	metadata, err := buildMetadataJSON(record.Metadata)
	if err != nil {
		return nil, false, err
	}
	finishedAt, duration := buildTiming(record.Metadata)
	row, err := s.engine.Queries.InsertBuild(ctx, pgdb.InsertBuildParams{ID: ToPgUUID(record.ID), AppID: ToPgUUID(record.AppID), AppIdentifierID: ToPgUUID(record.AppIdentifierID), Platform: record.Platform, ApplicationID: record.ApplicationID, Status: record.Status, ArtifactType: record.ArtifactType, Size: record.Size, Sha256: record.SHA256, ArtifactKey: record.ArtifactKey, Metadata: metadata, ActorType: record.ActorType, ActorID: record.ActorID, ActorDisplay: record.ActorDisplay, StartedAt: pgtype.Timestamptz{Time: record.Metadata.StartedAt, Valid: !record.Metadata.StartedAt.IsZero()}, FinishedAt: finishedAt, DurationMs: duration})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Get(ctx, record.AppID, record.ID)
		return existing, false, err
	}
	if err != nil {
		return nil, false, err
	}
	created, err := buildRecord(row)
	return created, true, err
}

func (s *PostgresBuildStore) Get(ctx context.Context, appID, id string) (*types.BuildRecord, error) {
	row, err := s.engine.Queries.GetBuild(ctx, pgdb.GetBuildParams{AppID: ToPgUUID(appID), ID: ToPgUUID(id)})
	if err != nil {
		return nil, buildNotFound(err)
	}
	return buildRecord(row)
}

func (s *PostgresBuildStore) List(ctx context.Context, appID string, limit, offset int32) ([]types.BuildRecord, int64, error) {
	rows, err := s.engine.Queries.ListBuilds(ctx, pgdb.ListBuildsParams{AppID: ToPgUUID(appID), Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}
	records := make([]types.BuildRecord, 0, len(rows))
	for _, row := range rows {
		record, err := buildRecord(row)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, *record)
	}
	count, err := s.engine.Queries.CountBuilds(ctx, ToPgUUID(appID))
	return records, count, err
}

// Transition locks the row, lets decide compute the next state, and persists
// it; a nil next state leaves the row untouched.
func (s *PostgresBuildStore) Transition(ctx context.Context, appID, id string, decide func(types.BuildRecord) (*types.BuildRecord, error)) (*types.BuildRecord, error) {
	var record *types.BuildRecord
	err := s.engine.WithTx(ctx, func(q *pgdb.Queries) error {
		row, err := q.LockBuild(ctx, pgdb.LockBuildParams{AppID: ToPgUUID(appID), ID: ToPgUUID(id)})
		if err != nil {
			return buildNotFound(err)
		}
		record, err = buildRecord(row)
		if err != nil {
			return err
		}
		next, err := decide(*record)
		if err != nil || next == nil {
			return err
		}
		metadata, err := buildMetadataJSON(next.Metadata)
		if err != nil {
			return err
		}
		finishedAt, duration := buildTiming(next.Metadata)
		row, err = q.UpdateBuild(ctx, pgdb.UpdateBuildParams{AppID: ToPgUUID(appID), ID: ToPgUUID(id), Status: next.Status, Size: next.Size, Sha256: next.SHA256, Metadata: metadata, FinishedAt: finishedAt, DurationMs: duration})
		if err != nil {
			return err
		}
		record, err = buildRecord(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}
