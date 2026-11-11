package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// User is a Harbor account.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Session is a logged-in browser session.
type Session struct {
	TokenHash string
	UserID    int64
	ExpiresAt time.Time
}

// APIKey describes a stored key (never the key itself).
type APIKey struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// CountUsers returns how many accounts exist (0 means first-run setup).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts an account.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (*User, error) {
	t := now()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, t, t)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: parseTime(t)}, nil
}

// CreateFirstUser inserts an account only if none exist yet, atomically.
// It returns ErrUsersExist if setup already happened.
func (s *Store) CreateFirstUser(ctx context.Context, username, passwordHash string) (*User, error) {
	t := now()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, updated_at)
		 SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)`,
		username, passwordHash, t, t)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrUsersExist
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: parseTime(t)}, nil
}

// ErrUsersExist means first-run setup is already done.
var ErrUsersExist = errors.New("an account already exists")

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var created string
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	return &u, nil
}

// UserByName looks an account up by username (case-insensitive).
func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username))
}

// UserByID looks an account up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE id = ?`, id))
}

// FirstUser returns the oldest account (the admin).
func (s *Store) FirstUser(ctx context.Context) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users ORDER BY id LIMIT 1`))
}

// SetPassword replaces a user's password hash and ends all their sessions.
func (s *Store) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession stores a session token hash.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expires time.Time, userAgent, remote string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, user_agent, remote) VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, now(), expires.UTC().Format(time.RFC3339Nano), userAgent, remote)
	return err
}

// SessionUser returns the user for a live session.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.password_hash, u.created_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now()))
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PruneSessions removes expired sessions.
func (s *Store) PruneSessions(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CreateAPIKey stores a hashed key.
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name, prefix, keyHash string) (*APIKey, error) {
	t := now()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, name, prefix, key_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, prefix, keyHash, t)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &APIKey{ID: id, UserID: userID, Name: name, Prefix: prefix, CreatedAt: parseTime(t)}, nil
}

// APIKeyUser returns the user owning a key hash and records its use.
func (s *Store) APIKeyUser(ctx context.Context, keyHash string) (*User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.password_hash, u.created_at
		   FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ?`, keyHash))
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE key_hash = ?`, now(), keyHash)
	return u, nil
}

// ListAPIKeys lists a user's keys.
func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, prefix, created_at, last_used_at FROM api_keys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var created string
		var used sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &created, &used); err != nil {
			return nil, err
		}
		k.UserID = userID
		k.CreatedAt = parseTime(created)
		if used.Valid {
			t := parseTime(used.String)
			k.LastUsedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey removes one of a user's keys.
func (s *Store) DeleteAPIKey(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSetting decodes a JSON setting into dst. It returns ErrNotFound if unset.
func (s *Store) GetSetting(ctx context.Context, key string, dst any) error {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(v), dst)
}

// SetSetting stores v as JSON under key.
func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(b), now())
	return err
}
