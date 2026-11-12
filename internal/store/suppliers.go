package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Supplier is a configured Stremio addon.
type Supplier struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ManifestURL string `json:"manifestUrl"`
	Manifest    string `json:"-"` // raw JSON
	Enabled     bool   `json:"enabled"`
	Priority    int    `json:"priority"`
	// Language is the audio language of this supplier's releases when their
	// names don't say ("" = not set).
	Language      string     `json:"language"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	LastOKAt      *time.Time `json:"lastOkAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
}

// ErrDuplicate means a unique value already exists.
var ErrDuplicate = errors.New("already exists")

const supplierCols = `id, name, manifest_url, manifest, enabled, priority, language, created_at, updated_at, last_checked_at, last_ok_at, last_error`

type scanner interface{ Scan(...any) error }

func scanSupplier(row scanner) (*Supplier, error) {
	var s Supplier
	var created, updated string
	var checked, ok sql.NullString
	if err := row.Scan(&s.ID, &s.Name, &s.ManifestURL, &s.Manifest, &s.Enabled, &s.Priority, &s.Language,
		&created, &updated, &checked, &ok, &s.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt, s.UpdatedAt = parseTime(created), parseTime(updated)
	s.LastCheckedAt, s.LastOKAt = nullTime(checked), nullTime(ok)
	return &s, nil
}

func nullTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

// ListSuppliers returns all suppliers, highest priority first.
func (s *Store) ListSuppliers(ctx context.Context) ([]Supplier, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+supplierCols+` FROM suppliers ORDER BY priority DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Supplier{}
	for rows.Next() {
		sp, err := scanSupplier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sp)
	}
	return out, rows.Err()
}

// GetSupplier returns one supplier.
func (s *Store) GetSupplier(ctx context.Context, id int64) (*Supplier, error) {
	return scanSupplier(s.DB.QueryRowContext(ctx, `SELECT `+supplierCols+` FROM suppliers WHERE id = ?`, id))
}

// CreateSupplier inserts a supplier that was just checked successfully.
func (s *Store) CreateSupplier(ctx context.Context, name, manifestURL, manifest string) (*Supplier, error) {
	t := now()
	var maxPrio sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(priority) FROM suppliers`).Scan(&maxPrio)
	prio := 0
	if maxPrio.Valid {
		prio = int(maxPrio.Int64) - 1 // new suppliers go last
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO suppliers (name, manifest_url, manifest, enabled, priority, created_at, updated_at, last_checked_at, last_ok_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)`, name, manifestURL, manifest, prio, t, t, t, t)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetSupplier(ctx, id)
}

// SupplierPatch holds optional changes.
type SupplierPatch struct {
	Name     *string
	Enabled  *bool
	Priority *int
	Language *string
}

// UpdateSupplier applies a patch.
func (s *Store) UpdateSupplier(ctx context.Context, id int64, p SupplierPatch) (*Supplier, error) {
	sets, args := []string{"updated_at = ?"}, []any{now()}
	if p.Name != nil {
		sets, args = append(sets, "name = ?"), append(args, *p.Name)
	}
	if p.Enabled != nil {
		sets, args = append(sets, "enabled = ?"), append(args, *p.Enabled)
	}
	if p.Priority != nil {
		sets, args = append(sets, "priority = ?"), append(args, *p.Priority)
	}
	if p.Language != nil {
		sets, args = append(sets, "language = ?"), append(args, *p.Language)
	}
	args = append(args, id)
	res, err := s.DB.ExecContext(ctx, `UPDATE suppliers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetSupplier(ctx, id)
}

// RecordSupplierCheck stores the outcome of contacting a supplier, and the
// fresh manifest when one was fetched.
func (s *Store) RecordSupplierCheck(ctx context.Context, id int64, manifest string, checkErr error) error {
	t := now()
	var err error
	switch {
	case checkErr != nil:
		_, err = s.DB.ExecContext(ctx, `UPDATE suppliers SET last_checked_at = ?, last_error = ? WHERE id = ?`, t, checkErr.Error(), id)
	case manifest != "":
		_, err = s.DB.ExecContext(ctx, `UPDATE suppliers SET last_checked_at = ?, last_ok_at = ?, last_error = '', manifest = ?, updated_at = ? WHERE id = ?`, t, t, manifest, t, id)
	default:
		_, err = s.DB.ExecContext(ctx, `UPDATE suppliers SET last_checked_at = ?, last_ok_at = ?, last_error = '' WHERE id = ?`, t, t, id)
	}
	return err
}

// DeleteSupplier removes a supplier.
func (s *Store) DeleteSupplier(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM suppliers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
