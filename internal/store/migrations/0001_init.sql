-- 0001: accounts, sessions, API keys and key/value settings.
-- Media tables (suppliers, titles, downloads, ...) arrive with later milestones.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);

-- Session tokens are stored as SHA-256 hashes; the raw token only lives in the cookie.
CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL,
    user_agent TEXT    NOT NULL DEFAULT '',
    remote     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expires ON sessions(expires_at);

-- API keys for scripts; stored hashed, shown once at creation.
CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,            -- first chars, shown in the UI to tell keys apart
    key_hash     TEXT    NOT NULL UNIQUE,
    created_at   TEXT    NOT NULL,
    last_used_at TEXT
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,                 -- JSON
    updated_at TEXT NOT NULL
);
