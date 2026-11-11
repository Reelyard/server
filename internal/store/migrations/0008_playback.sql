-- 0008: where each person is in each film or episode, and which audio codecs
-- library files use (so apps can tell up front whether a file will play).

-- '' = not checked yet (files imported before this, or without ffprobe).
ALTER TABLE media_files ADD COLUMN audio_codecs TEXT NOT NULL DEFAULT '';  -- JSON list

-- Resume points and watched marks, per user and per film or episode (not
-- per file, so an upgraded file keeps them).
CREATE TABLE progress (
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id       TEXT    NOT NULL,           -- tt… or tt…:S:E
    media_file_id INTEGER REFERENCES media_files(id) ON DELETE SET NULL,
    position_sec  INTEGER NOT NULL DEFAULT 0,
    duration_sec  INTEGER NOT NULL DEFAULT 0,
    watched       INTEGER NOT NULL DEFAULT 0,
    updated_at    TEXT    NOT NULL,
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX progress_recent ON progress(user_id, updated_at);
