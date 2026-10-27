-- name: UpsertSeries :one
INSERT INTO series (slug, title) VALUES (?, ?)
ON CONFLICT (slug) DO UPDATE SET slug = excluded.slug
RETURNING *;

-- name: GetSeries :one
SELECT * FROM series WHERE slug = ?;

-- name: ListSeries :many
SELECT * FROM series ORDER BY slug;

-- name: CreateEpisode :one
INSERT INTO episodes (series_id, slug, input_path, work_dir) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetEpisode :one
SELECT * FROM episodes WHERE series_id = ? AND slug = ?;

-- name: ListEpisodes :many
SELECT * FROM episodes WHERE series_id = ? ORDER BY slug;

-- name: SetModelVersions :exec
UPDATE episodes SET model_versions = ? WHERE id = ?;

-- name: ListSteps :many
SELECT * FROM episode_steps WHERE episode_id = ?;

-- name: StartStep :exec
INSERT INTO episode_steps (episode_id, step, status, outputs, error, started_at, finished_at)
VALUES (?, ?, 'running', '{}', '', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL)
ON CONFLICT (episode_id, step) DO UPDATE SET
    status = 'running', outputs = '{}', error = '',
    started_at = excluded.started_at, finished_at = NULL;

-- name: FinishStep :exec
UPDATE episode_steps
SET status = 'done', outputs = ?, error = '', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE episode_id = ? AND step = ?;

-- name: FailStep :exec
UPDATE episode_steps
SET status = 'failed', error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE episode_id = ? AND step = ?;

-- name: ResetStep :exec
DELETE FROM episode_steps WHERE episode_id = ? AND step = ?;

-- name: StartJob :one
INSERT INTO jobs (episode_id, step, worker, request, status) VALUES (?, ?, ?, ?, 'running')
RETURNING id;

-- name: FinishJob :exec
UPDATE jobs
SET status = ?, response = ?, error = ?, duration_ms = ?,
    finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;
