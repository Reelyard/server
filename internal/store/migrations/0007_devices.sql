-- 0007: devices (TVs, phones, desktop apps) that sign in by pairing code.
--
-- A device asks to pair and shows a short code; a signed-in person enters that
-- code under Settings → Devices to approve it; the device, which has been
-- polling with a secret only it knows, then collects its own token once.
-- Codes, poll secrets and tokens are stored as SHA-256 hashes.

CREATE TABLE devices (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER REFERENCES users(id) ON DELETE CASCADE, -- NULL until approved
    name         TEXT    NOT NULL,
    platform     TEXT    NOT NULL DEFAULT '',
    code_hash    TEXT    UNIQUE,              -- NULL once approved
    poll_hash    TEXT    UNIQUE,              -- NULL once the token is collected
    token_hash   TEXT    UNIQUE,              -- NULL until the token is collected
    created_at   TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,            -- a pairing not approved by then is dropped
    approved_at  TEXT,
    last_used_at TEXT,
    last_ip      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX devices_user ON devices(user_id);
CREATE INDEX devices_expires ON devices(expires_at);
