-- 0002: Stremio addon suppliers.

CREATE TABLE suppliers (
    id              INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL,
    manifest_url    TEXT    NOT NULL UNIQUE,  -- may carry addon config (and keys) in its path
    manifest        TEXT    NOT NULL,         -- last fetched manifest, JSON
    enabled         INTEGER NOT NULL DEFAULT 1,
    priority        INTEGER NOT NULL DEFAULT 0, -- higher first
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    last_checked_at TEXT,
    last_ok_at      TEXT,
    last_error      TEXT    NOT NULL DEFAULT ''
);
