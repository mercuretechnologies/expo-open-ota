// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"testing"
	"time"

	"expo-open-ota/internal/database/postgres/pgdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

type recordingSnapshotBatch struct {
	values []any
}

func (b *recordingSnapshotBatch) Append(values ...any) error {
	b.values = values
	return nil
}

func TestAppendSnapshotMapsAndClampsDatabaseValues(t *testing.T) {
	appID, updateID := uuid.New(), uuid.New()
	bucket := time.Date(2026, 7, 24, 10, 30, 0, 0, time.UTC)
	capturedAt := bucket.Add(12 * time.Second)
	batch := &recordingSnapshotBatch{}

	err := appendSnapshot(batch, pgdb.ListCurrentUpdateHealthSnapshotsRow{
		AppID:             pgtype.UUID{Bytes: appID, Valid: true},
		UpdateUuid:        pgtype.UUID{Bytes: updateID, Valid: true},
		Role:              "candidate",
		DevicesOnUpdate:   12,
		SuccessfulDevices: 10,
		FaultyDevices:     2,
		UpdateIssues:      -1,
		RuntimeIssues:     2,
	}, bucket, capturedAt)

	require.NoError(t, err)
	require.Equal(t, []any{
		appID.String(),
		updateID.String(),
		bucket,
		capturedAt,
		"candidate",
		uint64(12),
		uint64(10),
		uint64(2),
		uint64(0),
		uint64(2),
	}, batch.values)
}
