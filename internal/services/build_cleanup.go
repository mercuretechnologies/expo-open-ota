package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	"xprem/internal/bucket"
	"xprem/internal/database"
	"xprem/internal/types"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	buildCleanupStartDelay     = 30 * time.Second
	buildOutboxInterval        = time.Minute
	buildStagingSweepInterval  = 15 * time.Minute
	buildCleanupBatchTimeout   = 5 * time.Minute
	buildCleanupItemTimeout    = 30 * time.Second
	buildOutboxBatchSize       = 25
	buildStagingSweepBatchSize = 100
	buildOutboxMaxBackoff      = 6 * time.Hour
	buildStagingStaleAfter     = 24 * time.Hour
)

// BuildArtifactDeleter removes one artifact object; absent objects are not an error.
type BuildArtifactDeleter interface {
	Delete(context.Context, bucket.BuildArtifact, bool) error
}

// BuildCleanup drains the build_artifact_cleanup outbox and sweeps stale
// staging uploads. Final artifacts of ready builds are never touched.
type BuildCleanup struct {
	db      database.DBTX
	storage BuildArtifactDeleter
}

func NewBuildCleanup(db database.DBTX, storage BuildArtifactDeleter) *BuildCleanup {
	return &BuildCleanup{db: db, storage: storage}
}

// Start runs both loops until the returned stop function is called.
func (c *BuildCleanup) Start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.loop(ctx, buildOutboxInterval, "outbox", c.DrainOutbox)
	}()
	go func() {
		defer wg.Done()
		c.loop(ctx, buildStagingSweepInterval, "staging sweep", c.SweepStaging)
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func (c *BuildCleanup) loop(ctx context.Context, interval time.Duration, name string, run func(context.Context) (int, error)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(buildCleanupStartDelay):
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		batchCtx, cancel := context.WithTimeout(ctx, buildCleanupBatchTimeout)
		count, err := run(batchCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("🧹 [BUILD-CLEANUP] %s failed: %v", name, err)
		} else if count > 0 {
			log.Printf("🧹 [BUILD-CLEANUP] %s handled %d builds", name, count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type buildCleanupItem struct {
	id       int64
	ref      bucket.BuildArtifact
	attempts int32
}

// DrainOutbox deletes the final and staging objects of one batch of due
// outbox rows and removes each row once both deletes succeeded.
func (c *BuildCleanup) DrainOutbox(ctx context.Context) (int, error) {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, platform, app_identifier_id, build_id, artifact_type, attempts
FROM build_artifact_cleanup WHERE due_at <= now() ORDER BY due_at, id LIMIT $1 FOR UPDATE SKIP LOCKED`, buildOutboxBatchSize)
	if err != nil {
		return 0, err
	}
	items, err := scanCleanupItems(rows)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, item := range items {
		if err := c.deleteArtifact(ctx, item.ref, true, true); err != nil {
			backoff := buildOutboxBackoff(item.attempts)
			log.Printf("🧹 [BUILD-CLEANUP] build %s artifact delete failed (attempt %d, retry in %s): %v", item.ref.BuildID, item.attempts+1, backoff, err)
			if _, err := tx.Exec(ctx, `UPDATE build_artifact_cleanup SET attempts = attempts + 1, last_error = $2, due_at = now() + $3::interval WHERE id = $1`, item.id, truncateError(err), pgtype.Interval{Microseconds: backoff.Microseconds(), Valid: true}); err != nil {
				return done, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM build_artifact_cleanup WHERE id = $1`, item.id); err != nil {
			return done, err
		}
		done++
	}
	return done, tx.Commit(ctx)
}

func scanCleanupItems(rows pgx.Rows) ([]buildCleanupItem, error) {
	defer rows.Close()
	var items []buildCleanupItem
	for rows.Next() {
		var item buildCleanupItem
		var platform string
		var identifier, build pgtype.UUID
		if err := rows.Scan(&item.id, &platform, &identifier, &build, &item.ref.Format, &item.attempts); err != nil {
			return nil, err
		}
		item.ref.Platform = types.Platform(platform)
		item.ref.IdentifierID = identifier.String()
		item.ref.BuildID = build.String()
		items = append(items, item)
	}
	return items, rows.Err()
}

// SweepStaging deletes the staging upload of builds untouched for a day whose
// upload is finished or abandoned. Ready builds are swept once, the others
// again after a day, so a retried upload is not left behind either.
func (c *BuildCleanup) SweepStaging(ctx context.Context) (int, error) {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT b.id, b.platform, b.app_identifier_id, b.artifact_type
FROM builds b LEFT JOIN build_staging_sweeps s ON s.build_id = b.id
WHERE b.updated_at < now() - $1::interval
  AND b.status IN ('ready', 'failed', 'uploading')
  AND (s.build_id IS NULL OR (b.status <> 'ready' AND s.swept_at < now() - $1::interval))
ORDER BY b.created_at, b.id LIMIT $2 FOR UPDATE OF b SKIP LOCKED`, pgtype.Interval{Microseconds: buildStagingStaleAfter.Microseconds(), Valid: true}, buildStagingSweepBatchSize)
	if err != nil {
		return 0, err
	}
	refs, err := scanStagingRefs(rows)
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, ref := range refs {
		if err := c.deleteArtifact(ctx, ref, true, false); err != nil {
			log.Printf("🧹 [BUILD-CLEANUP] build %s staging delete failed: %v", ref.BuildID, err)
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO build_staging_sweeps (build_id, swept_at) VALUES ($1, now())
ON CONFLICT (build_id) DO UPDATE SET swept_at = now()`, ref.BuildID); err != nil {
			return swept, err
		}
		swept++
	}
	return swept, tx.Commit(ctx)
}

func scanStagingRefs(rows pgx.Rows) ([]bucket.BuildArtifact, error) {
	defer rows.Close()
	var refs []bucket.BuildArtifact
	for rows.Next() {
		var ref bucket.BuildArtifact
		var platform string
		var identifier, build pgtype.UUID
		if err := rows.Scan(&build, &platform, &identifier, &ref.Format); err != nil {
			return nil, err
		}
		ref.Platform = types.Platform(platform)
		ref.IdentifierID = identifier.String()
		ref.BuildID = build.String()
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (c *BuildCleanup) deleteArtifact(ctx context.Context, ref bucket.BuildArtifact, staging, final bool) error {
	if _, err := ref.Key(false); err != nil {
		return fmt.Errorf("unusable artifact reference: %w", err)
	}
	var errs []error
	if staging {
		if err := c.deleteOne(ctx, ref, true); err != nil {
			errs = append(errs, fmt.Errorf("staging: %w", err))
		}
	}
	if final {
		if err := c.deleteOne(ctx, ref, false); err != nil {
			errs = append(errs, fmt.Errorf("final: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (c *BuildCleanup) deleteOne(ctx context.Context, ref bucket.BuildArtifact, staging bool) error {
	itemCtx, cancel := context.WithTimeout(ctx, buildCleanupItemTimeout)
	defer cancel()
	return c.storage.Delete(itemCtx, ref, staging)
}

func buildOutboxBackoff(attempts int32) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 20 {
		return buildOutboxMaxBackoff
	}
	backoff := time.Minute << uint(attempts)
	if backoff > buildOutboxMaxBackoff {
		return buildOutboxMaxBackoff
	}
	return backoff
}

func truncateError(err error) string {
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}
