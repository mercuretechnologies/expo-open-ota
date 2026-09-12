-- name: BuildLogEndOffset :one
SELECT COALESCE((SELECT byte_offset + octet_length(content) FROM build_log_chunks
WHERE build_id=$1 ORDER BY byte_offset DESC LIMIT 1), 0)::integer AS end_offset;

-- name: GetBuildLogChunk :one
SELECT content, format FROM build_log_chunks WHERE build_id=$1 AND byte_offset=$2;

-- name: InsertBuildLogChunk :exec
INSERT INTO build_log_chunks(build_id, byte_offset, content, format) VALUES ($1,$2,$3,$4);

-- name: ListBuildLogChunks :many
SELECT l.byte_offset, l.content, l.format, l.created_at FROM build_log_chunks l
JOIN builds b ON b.id=l.build_id
WHERE b.app_id=$1 AND l.build_id=$2 AND l.byte_offset >= $3
ORDER BY l.byte_offset LIMIT 32;
