-- Dubbing data model (00-DABING_PLAN §6). Embeddings are little-endian []float32 BLOBs,
-- cosine is computed in Go. Paths are relative to the storage root.

CREATE TABLE series (
    id          INTEGER PRIMARY KEY,
    slug        TEXT    NOT NULL UNIQUE,
    title       TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE episodes (
    id              INTEGER PRIMARY KEY,
    series_id       INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    slug            TEXT    NOT NULL,
    input_path      TEXT    NOT NULL,
    work_dir        TEXT    NOT NULL,
    model_versions  TEXT    NOT NULL DEFAULT '{}',  -- JSON {step: model version}
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (series_id, slug)
);

-- Pipeline step state per episode. outputs = JSON {name: relative path}.
CREATE TABLE episode_steps (
    episode_id   INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    step         TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK (status IN ('pending', 'running', 'done', 'failed')),
    outputs      TEXT    NOT NULL DEFAULT '{}',
    error        TEXT    NOT NULL DEFAULT '',
    started_at   TEXT,
    finished_at  TEXT,
    PRIMARY KEY (episode_id, step)
);

CREATE TABLE characters (
    id              INTEGER PRIMARY KEY,
    series_id       INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    centroid        BLOB,
    centroid_count  INTEGER NOT NULL DEFAULT 0,  -- episodes in the running average
    f0_mean         REAL,
    f0_std          REAL,
    gender          TEXT    NOT NULL DEFAULT '',
    age_group       TEXT    NOT NULL DEFAULT '',
    strategy        TEXT    NOT NULL DEFAULT 'A' CHECK (strategy IN ('A', 'B', 'C')),
    voice_id        INTEGER REFERENCES voices(id),
    ref_path        TEXT    NOT NULL DEFAULT '',
    speed           REAL    NOT NULL DEFAULT 1.0,
    notes           TEXT    NOT NULL DEFAULT '',
    UNIQUE (series_id, name)
);

CREATE TABLE voices (
    id          INTEGER PRIMARY KEY,
    slug        TEXT    NOT NULL UNIQUE,      -- e.g. cz_m_042
    path        TEXT    NOT NULL,             -- voices/<slug>/
    gender      TEXT    NOT NULL DEFAULT '',
    age_group   TEXT    NOT NULL DEFAULT '',
    f0_mean     REAL,
    f0_std      REAL,
    embedding   BLOB,
    tags        TEXT    NOT NULL DEFAULT '[]', -- JSON array
    source      TEXT    NOT NULL DEFAULT '',
    license     TEXT    NOT NULL DEFAULT '',
    meta        TEXT    NOT NULL DEFAULT '{}'
);

CREATE TABLE segments (
    id            INTEGER PRIMARY KEY,
    episode_id    INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    idx           INTEGER NOT NULL,
    start_s       REAL    NOT NULL,
    end_s         REAL    NOT NULL,
    speaker       TEXT    NOT NULL DEFAULT '',  -- SPK_xx from diarization
    character_id  INTEGER REFERENCES characters(id),
    text_en       TEXT    NOT NULL DEFAULT '',
    text_cs       TEXT    NOT NULL DEFAULT '',
    tts_path      TEXT    NOT NULL DEFAULT '',
    tts_duration  REAL,
    stretch       REAL,
    fit_iter      INTEGER NOT NULL DEFAULT 0,
    is_song       INTEGER NOT NULL DEFAULT 0,
    UNIQUE (episode_id, idx)
);

-- Every worker / LLM call.
CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY,
    episode_id   INTEGER REFERENCES episodes(id) ON DELETE CASCADE,
    step         TEXT    NOT NULL,
    worker       TEXT    NOT NULL,
    request      TEXT    NOT NULL DEFAULT '{}',
    response     TEXT    NOT NULL DEFAULT '{}',
    status       TEXT    NOT NULL CHECK (status IN ('running', 'ok', 'error')),
    error        TEXT    NOT NULL DEFAULT '',
    duration_ms  INTEGER,
    started_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at  TEXT
);

CREATE INDEX jobs_episode ON jobs (episode_id, step);
