package store_test

import (
	"context"
	"sync"
	"testing"
	"xprem/internal/store"
	"xprem/internal/types"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBuildLogsOrderedIdempotentAndScoped(t *testing.T) {
	f := setupBuildStore(t)
	ctx := context.Background()
	id := uuid.NewString()
	_, _, err := f.builds.Create(ctx, f.record(id, types.BuildStatusBuilding))
	require.NoError(t, err)
	// An uncertain response can be retried concurrently without duplicating output.
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- f.builds.AppendLogs(ctx, f.app, id, 0, "héllo\n", "text")
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.ErrorIs(t, f.builds.AppendLogs(ctx, f.app, id, 0, "different", "text"), store.ErrBuildLogOffset)
	require.ErrorIs(t, f.builds.AppendLogs(ctx, f.app, id, 50, "gap", "text"), store.ErrBuildLogOffset)
	require.ErrorIs(t, f.builds.AppendLogs(ctx, f.app, id, 1, "overlap", "text"), store.ErrBuildLogOffset)
	require.NoError(t, f.builds.AppendLogs(ctx, f.app, id, 7, "done\n", "text"))
	logs, err := f.builds.ListLogs(ctx, f.app, id, 0)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	require.Equal(t, "héllo\n", logs[0].Content)
	require.Equal(t, "text", logs[0].Format)
	require.Equal(t, int32(7), logs[1].Offset)
	logs, err = f.builds.ListLogs(ctx, f.app, id, 7)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	content := `{"logId":"event","time":"2026-09-09T10:00:00Z","level":30,"msg":"structured"}` + "\n"
	require.NoError(t, f.builds.AppendLogs(ctx, f.app, id, 12, content, "ndjson"))
	require.NoError(t, f.builds.AppendLogs(ctx, f.app, id, 12, content, "ndjson"))
	require.ErrorIs(t, f.builds.AppendLogs(ctx, f.app, id, 12, content, "text"), store.ErrBuildLogOffset)
	logs, err = f.builds.ListLogs(ctx, f.app, id, 12)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, content, logs[0].Content)
	require.Equal(t, "ndjson", logs[0].Format)
	require.Error(t, f.builds.AppendLogs(ctx, uuid.NewString(), id, 12, "other app", "text"))
	logs, err = f.builds.ListLogs(ctx, uuid.NewString(), id, 0)
	require.NoError(t, err)
	require.Empty(t, logs)
	_, err = f.pool.Exec(ctx, "DELETE FROM builds WHERE id=$1", id)
	require.NoError(t, err)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM build_log_chunks WHERE build_id=$1", id).Scan(&count))
	require.Zero(t, count)
}
