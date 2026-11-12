package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device is an app signed in by pairing code. Pending devices have no user yet.
type Device struct {
	ID         int64      `json:"id"`
	UserID     *int64     `json:"-"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"-"`
	ApprovedAt *time.Time `json:"approvedAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	LastIP     string     `json:"lastIp,omitempty"`
	// Paired is true once the device has collected its token.
	Paired bool `json:"paired"`
}

// ErrPending means a pairing is waiting for approval.
var ErrPending = errors.New("pairing not approved yet")

const deviceCols = `id, user_id, name, platform, created_at, expires_at, approved_at, last_used_at, last_ip, token_hash IS NOT NULL`

func scanDevice(row scanner) (*Device, error) {
	var d Device
	var uid sql.NullInt64
	var created, expires string
	var approved, used sql.NullString
	if err := row.Scan(&d.ID, &uid, &d.Name, &d.Platform, &created, &expires, &approved, &used, &d.LastIP, &d.Paired); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if uid.Valid {
		d.UserID = &uid.Int64
	}
	d.CreatedAt, d.ExpiresAt = parseTime(created), parseTime(expires)
	if approved.Valid {
		t := parseTime(approved.String)
		d.ApprovedAt = &t
	}
	if used.Valid {
		t := parseTime(used.String)
		d.LastUsedAt = &t
	}
	return &d, nil
}

// CountPendingDevices counts pairings still waiting for approval.
func (s *Store) CountPendingDevices(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE user_id IS NULL AND expires_at > ?`, now()).Scan(&n)
	return n, err
}

// CreatePairing stores a new pending device.
func (s *Store) CreatePairing(ctx context.Context, name, platform, codeHash, pollHash string, expires time.Time, ip string) (*Device, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO devices (name, platform, code_hash, poll_hash, created_at, expires_at, last_ip) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, platform, codeHash, pollHash, now(), expires.UTC().Format(time.RFC3339Nano), ip)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

// ApprovePairing gives the pending device with this code to a user.
// It returns ErrNotFound for an unknown or expired code.
func (s *Store) ApprovePairing(ctx context.Context, codeHash string, userID int64) (*Device, error) {
	t := now()
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`UPDATE devices SET user_id = ?, approved_at = ?, code_hash = NULL
		  WHERE code_hash = ? AND user_id IS NULL AND expires_at > ? RETURNING id`,
		userID, t, codeHash, t).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

// CollectToken hands an approved device its token, once. It returns
// ErrPending while waiting for approval and ErrNotFound when the poll secret
// is unknown, expired, or already used.
func (s *Store) CollectToken(ctx context.Context, pollHash, tokenHash string) (*Device, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, err := scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE poll_hash = ?`, pollHash))
	if err != nil {
		return nil, err
	}
	if d.UserID == nil {
		if !d.ExpiresAt.After(time.Now()) {
			return nil, ErrNotFound
		}
		return nil, ErrPending
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET poll_hash = NULL, token_hash = ?, last_used_at = ? WHERE id = ?`,
		tokenHash, now(), d.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	d.Paired = true
	return d, nil
}

// DeviceUser returns the user a device token belongs to and notes its use.
func (s *Store) DeviceUser(ctx context.Context, tokenHash, ip string) (*User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.password_hash, u.created_at
		   FROM devices d JOIN users u ON u.id = d.user_id WHERE d.token_hash = ?`, tokenHash))
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE devices SET last_used_at = ?, last_ip = ? WHERE token_hash = ?`, now(), ip, tokenHash)
	return u, nil
}

// ListDevices lists a user's devices and every pairing still waiting.
func (s *Store) ListDevices(ctx context.Context, userID int64) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE user_id = ? OR (user_id IS NULL AND expires_at > ?) ORDER BY id`, userID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDevice signs a user's device out, or turns down a pending pairing.
func (s *Store) DeleteDevice(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND (user_id = ? OR user_id IS NULL)`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneDevices drops pairings that expired before approval, or were approved
// but never collected.
func (s *Store) PruneDevices(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE token_hash IS NULL AND expires_at <= ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
