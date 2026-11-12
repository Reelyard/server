-- 0009: viewers, the family members who watch on TVs. Each viewer is a user
-- row (so progress keeps working per person) that can't sign in, plus what
-- they may see and where their DLNA server shows up.

CREATE TABLE viewers (
    user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    slot       INTEGER NOT NULL UNIQUE,            -- DLNA port = base port + slot
    all_titles INTEGER NOT NULL DEFAULT 1,         -- 0: only viewer_titles
    dlna       INTEGER NOT NULL DEFAULT 1,         -- has its own DLNA server
    tvs        TEXT    NOT NULL DEFAULT '[]',      -- JSON list of TV addresses; [] = every TV
    created_at TEXT    NOT NULL
);

-- Titles a viewer may see when all_titles = 0: a film's id, or a series' id
-- (all its episodes).
CREATE TABLE viewer_titles (
    user_id  INTEGER NOT NULL REFERENCES viewers(user_id) ON DELETE CASCADE,
    title_id TEXT    NOT NULL,
    PRIMARY KEY (user_id, title_id)
);
