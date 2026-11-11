-- 0004: library files, subtitles, activity log, and import state on downloads.

ALTER TABLE downloads ADD COLUMN import_state TEXT NOT NULL DEFAULT '';  -- '' | importing | imported | failed | skipped
ALTER TABLE downloads ADD COLUMN import_error TEXT NOT NULL DEFAULT '';

-- A video file placed in the library.
CREATE TABLE media_files (
    id            INTEGER PRIMARY KEY,
    download_id   INTEGER REFERENCES downloads(id) ON DELETE SET NULL,
    title_type    TEXT    NOT NULL,           -- movie | series
    title_id      TEXT    NOT NULL,           -- tt… or tt…:S:E
    title_name    TEXT    NOT NULL,
    path          TEXT    NOT NULL UNIQUE,    -- relative to the library folder
    size          INTEGER NOT NULL,
    method        TEXT    NOT NULL,           -- hardlink | copy
    resolution    TEXT    NOT NULL DEFAULT '',
    video_codec   TEXT    NOT NULL DEFAULT '',
    duration_sec  INTEGER NOT NULL DEFAULT 0,
    audio_langs   TEXT    NOT NULL DEFAULT '[]', -- JSON
    embedded_subs TEXT    NOT NULL DEFAULT '[]', -- JSON
    os_hash       TEXT    NOT NULL DEFAULT '',   -- OpenSubtitles hash
    imported_at   TEXT    NOT NULL
);
CREATE INDEX media_files_title ON media_files(title_id);

-- A subtitle file saved next to a media file.
CREATE TABLE subtitle_files (
    id            INTEGER PRIMARY KEY,
    media_file_id INTEGER NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    lang          TEXT    NOT NULL,
    path          TEXT    NOT NULL UNIQUE,    -- relative to the library folder
    source        TEXT    NOT NULL,           -- supplier name
    source_id     TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL
);

-- What Harbor did, newest last. Pruned to the most recent rows.
CREATE TABLE activity (
    id       INTEGER PRIMARY KEY,
    time     TEXT    NOT NULL,
    level    TEXT    NOT NULL,                -- info | warn | error
    kind     TEXT    NOT NULL,                -- download | import | subtitles | …
    title_id TEXT    NOT NULL DEFAULT '',
    message  TEXT    NOT NULL
);
CREATE INDEX activity_time ON activity(time);
