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

-- name: InsertBuildShare :one
INSERT INTO build_shares(id,build_id,token_hash,expires_at) VALUES ($1,$2,$3,$4) RETURNING *;

-- name: ListBuildShares :many
SELECT * FROM build_shares WHERE build_id=$1 ORDER BY created_at DESC;

-- name: RevokeBuildShare :execrows
UPDATE build_shares SET revoked_at=COALESCE(revoked_at,now()) WHERE build_id=$1 AND id=$2;

-- name: ResolveBuildShare :one
SELECT b.id, b.app_id, s.expires_at AS share_expires_at FROM build_shares s JOIN builds b ON b.id=s.build_id
WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND b.status='ready' AND b.artifact_type='apk';
