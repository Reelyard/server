-- 0005: quality profiles, tracked titles and their items, release blocklist.

CREATE TABLE profiles (
    id              INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    resolutions     TEXT    NOT NULL,              -- JSON, best first, e.g. ["1080p","720p"]
    movie_min_mb    INTEGER NOT NULL DEFAULT 0,
    movie_max_mb    INTEGER NOT NULL DEFAULT 0,    -- 0 = no limit
    episode_min_mb  INTEGER NOT NULL DEFAULT 0,
    episode_max_mb  INTEGER NOT NULL DEFAULT 0,
    audio_langs     TEXT    NOT NULL DEFAULT '[]', -- JSON, preferred audio languages
    audio_required  INTEGER NOT NULL DEFAULT 0,    -- reject releases without one of them
    subtitle_langs  TEXT    NOT NULL DEFAULT '[]', -- JSON, saved next to imported files
    blocked_terms   TEXT    NOT NULL DEFAULT '[]', -- JSON
    preferred_terms TEXT    NOT NULL DEFAULT '[]', -- JSON
    min_seeders     INTEGER NOT NULL DEFAULT 1,
    seed_ratio      REAL    NOT NULL DEFAULT 1.0,  -- used from M5
    seed_hours      INTEGER NOT NULL DEFAULT 48,   -- used from M5
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL
);

-- A movie or series Harbor fetches by itself.
CREATE TABLE titles (
    id              INTEGER PRIMARY KEY,
    imdb_id         TEXT    NOT NULL UNIQUE,
    type            TEXT    NOT NULL,              -- movie | series
    name            TEXT    NOT NULL,
    year            INTEGER NOT NULL DEFAULT 0,
    poster          TEXT    NOT NULL DEFAULT '',
    profile_id      INTEGER NOT NULL REFERENCES profiles(id),
    monitor         TEXT    NOT NULL DEFAULT 'all', -- all | future | none (series)
    seasons         TEXT    NOT NULL DEFAULT '[]',  -- JSON; empty = all seasons
    added_at        TEXT    NOT NULL,
    refreshed_at    TEXT
);

-- One wanted thing: the movie itself, or one episode.
CREATE TABLE items (
    id              INTEGER PRIMARY KEY,
    title_id        INTEGER NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    item_id         TEXT    NOT NULL UNIQUE,        -- tt… or tt…:S:E
    season          INTEGER NOT NULL DEFAULT 0,
    episode         INTEGER NOT NULL DEFAULT 0,
    name            TEXT    NOT NULL DEFAULT '',
    air_date        TEXT    NOT NULL DEFAULT '',    -- RFC 3339, '' = unknown
    monitored       INTEGER NOT NULL DEFAULT 1,
    state           TEXT    NOT NULL,               -- unaired missing downloading imported
    download_id     INTEGER REFERENCES downloads(id) ON DELETE SET NULL,
    grabbed_hash    TEXT    NOT NULL DEFAULT '',    -- release last grabbed, to block it if
    grabbed_idx     INTEGER NOT NULL DEFAULT -1,    -- its download is removed or fails
    grabbed_name    TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_search_at  TEXT    NOT NULL DEFAULT '',
    last_search_at  TEXT    NOT NULL DEFAULT '',
    note            TEXT    NOT NULL DEFAULT ''     -- why nothing was grabbed, or what was
);
CREATE INDEX items_title ON items(title_id);
CREATE INDEX items_state ON items(state, next_search_at);

-- Releases not to grab again for an item (failed, stalled, wrong language).
CREATE TABLE blocklist (
    info_hash  TEXT    NOT NULL,
    file_idx   INTEGER NOT NULL DEFAULT -1,
    item_id    TEXT    NOT NULL,
    name       TEXT    NOT NULL DEFAULT '',
    reason     TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (info_hash, file_idx, item_id)
);
