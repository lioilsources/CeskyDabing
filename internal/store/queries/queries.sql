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

-- name: DeleteSegments :exec
DELETE FROM segments WHERE episode_id = ?;

-- name: InsertSegment :exec
INSERT INTO segments (episode_id, idx, start_s, end_s, speaker, text_en)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListSegments :many
SELECT * FROM segments WHERE episode_id = ? ORDER BY idx;

-- name: UpsertCharacter :one
INSERT INTO characters (series_id, name, centroid, centroid_count, f0_mean, f0_std, gender, age_group, strategy, ref_path, speed, notes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (series_id, name) DO UPDATE SET
    centroid = excluded.centroid, centroid_count = excluded.centroid_count,
    f0_mean = excluded.f0_mean, f0_std = excluded.f0_std, gender = excluded.gender,
    age_group = excluded.age_group, strategy = excluded.strategy, ref_path = excluded.ref_path,
    speed = excluded.speed, notes = excluded.notes
RETURNING id;

-- name: ListCharacters :many
SELECT * FROM characters WHERE series_id = ? ORDER BY name;

-- name: UnlinkCharacterSegments :exec
UPDATE segments SET character_id = NULL WHERE character_id = ?;

-- name: DeleteCharacter :exec
DELETE FROM characters WHERE id = ?;

-- name: SetSegmentsCharacter :exec
UPDATE segments SET character_id = ? WHERE episode_id = ? AND speaker = ?;

-- name: SetCharacterRef :exec
UPDATE characters SET ref_path = ? WHERE series_id = ? AND name = ?;

-- name: SetSegmentCS :exec
UPDATE segments SET text_cs = ? WHERE episode_id = ? AND idx = ?;

-- name: ListSegmentsWithCharacter :many
SELECT s.*, COALESCE(c.name, '') AS character_name
FROM segments s LEFT JOIN characters c ON c.id = s.character_id
WHERE s.episode_id = ? ORDER BY s.idx;

-- name: ListSegmentsForTTS :many
SELECT s.id, s.idx, s.start_s, s.end_s, s.text_cs, s.tts_path, s.is_song,
       COALESCE(c.name, '') AS character_name, COALESCE(c.ref_path, '') AS ref_path,
       COALESCE(c.speed, 1.0) AS speed
FROM segments s LEFT JOIN characters c ON c.id = s.character_id
WHERE s.episode_id = ? ORDER BY s.idx;

-- name: SetSegmentTTS :exec
UPDATE segments SET tts_path = ?, tts_duration = ? WHERE episode_id = ? AND idx = ?;

-- name: SetSegmentFit :exec
UPDATE segments SET stretch = ?, fit_iter = ? WHERE episode_id = ? AND idx = ?;
