-- name: InsertBuild :one
INSERT INTO builds(id, app_id, app_identifier_id, platform, application_id, status, artifact_type, size, sha256, artifact_key, metadata, actor_type, actor_id, actor_display, started_at, finished_at, duration_ms)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO NOTHING RETURNING *;

-- name: GetBuild :one
SELECT * FROM builds WHERE app_id=$1 AND id=$2;

-- name: LockBuild :one
SELECT * FROM builds WHERE app_id=$1 AND id=$2 FOR UPDATE;

-- name: UpdateBuild :one
UPDATE builds SET status=$3, size=$4, sha256=$5, metadata=$6, finished_at=$7, duration_ms=$8,
    ready_at=CASE WHEN $3='ready' THEN COALESCE(ready_at, now()) ELSE NULL END, updated_at=now()
WHERE app_id=$1 AND id=$2 RETURNING *;

-- name: ListBuilds :many
SELECT * FROM builds WHERE app_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3;

-- name: CountBuilds :one
SELECT count(*) FROM builds WHERE app_id=$1;

-- name: ListDueBuildArtifactCleanup :many
SELECT id, app_identifier_id, build_id, artifact_type, attempts
FROM build_artifact_cleanup WHERE due_at <= now()
ORDER BY due_at, id LIMIT sqlc.arg('batch_size') FOR UPDATE SKIP LOCKED;

-- name: DeferBuildArtifactCleanup :exec
UPDATE build_artifact_cleanup SET attempts = attempts + 1, last_error = sqlc.arg('last_error'), due_at = now() + sqlc.arg('backoff')::interval
WHERE id = sqlc.arg('id');

-- name: DeleteBuildArtifactCleanup :exec
DELETE FROM build_artifact_cleanup WHERE id = $1;

-- name: ListStaleBuildStaging :many
SELECT b.id, b.app_identifier_id, b.artifact_type
FROM builds b LEFT JOIN build_staging_sweeps s ON s.build_id = b.id
WHERE b.updated_at < now() - sqlc.arg('stale_after')::interval
  AND b.status IN ('ready', 'failed', 'uploading')
  AND (s.build_id IS NULL OR (b.status <> 'ready' AND s.swept_at < now() - sqlc.arg('stale_after')::interval))
ORDER BY b.created_at, b.id LIMIT sqlc.arg('batch_size') FOR UPDATE OF b SKIP LOCKED;

-- name: MarkBuildStagingSwept :exec
INSERT INTO build_staging_sweeps (build_id, swept_at) VALUES ($1, now())
ON CONFLICT (build_id) DO UPDATE SET swept_at = now();
