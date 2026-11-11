-- 0003: torrent downloads.

CREATE TABLE downloads (
    id           INTEGER PRIMARY KEY,
    info_hash    TEXT    NOT NULL,             -- lowercase hex
    file_idx     INTEGER NOT NULL DEFAULT -1,  -- file inside the torrent; -1 = whole torrent
    name         TEXT    NOT NULL,             -- what to show (release name)
    file_path    TEXT    NOT NULL DEFAULT '',  -- path inside the torrent, once metadata is known
    sources      TEXT    NOT NULL DEFAULT '[]',-- tracker URLs, JSON
    title_type   TEXT    NOT NULL DEFAULT '',  -- movie | series
    title_id     TEXT    NOT NULL DEFAULT '',  -- tt… or tt…:S:E
    title_name   TEXT    NOT NULL DEFAULT '',
    state        TEXT    NOT NULL,             -- queued metadata downloading paused seeding failed
    error        TEXT    NOT NULL DEFAULT '',
    size         INTEGER NOT NULL DEFAULT 0,   -- bytes wanted
    done_bytes   INTEGER NOT NULL DEFAULT 0,   -- bytes of the wanted part verified
    uploaded     INTEGER NOT NULL DEFAULT 0,   -- bytes sent to peers, all sessions
    added_at     TEXT    NOT NULL,
    completed_at TEXT,
    updated_at   TEXT    NOT NULL,
    UNIQUE (info_hash, file_idx)
);
CREATE INDEX downloads_state ON downloads(state);
