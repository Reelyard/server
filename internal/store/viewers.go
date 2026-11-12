package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Viewer is a family member who watches on TVs: their own watched marks
// and resume points, optionally a limited set of titles, and their own DLNA
// server. Viewers can't sign in to Reelyard.
type Viewer struct {
	ID   int64  `json:"id"` // the viewer's user id (progress is kept under it)
	Name string `json:"name"`
	// Slot numbers the viewer's DLNA server: it listens on the base port + Slot.
	Slot int `json:"slot"`
	// AllTitles lets the viewer see the whole library; otherwise only Titles.
	AllTitles bool `json:"allTitles"`
	// Titles are film or series ids (a series includes all its episodes).
	Titles []string `json:"titles"`
	// DLNA gives the viewer their own DLNA server.
	DLNA bool `json:"dlna"`
	// TVs are the addresses of the TVs the viewer's server shows on; empty
	// means every TV.
	TVs       []string  `json:"tvs"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrNameTaken means another viewer already has this name.
var ErrNameTaken = errors.New("another viewer has this name")

// noLogin is a password hash nothing matches: viewers can't sign in.
const noLogin = "!viewer"

const viewerCols = `user_id, name, slot, all_titles, dlna, tvs, created_at`

func scanViewer(row scanner) (*Viewer, error) {
	var v Viewer
	var tvs, created string
	if err := row.Scan(&v.ID, &v.Name, &v.Slot, &v.AllTitles, &v.DLNA, &tvs, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(tvs), &v.TVs)
	if v.TVs == nil {
		v.TVs = []string{}
	}
	v.Titles = []string{}
	v.CreatedAt = parseTime(created)
	return &v, nil
}

// ListViewers returns all viewers, oldest first, with their titles.
func (s *Store) ListViewers(ctx context.Context) ([]*Viewer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+viewerCols+` FROM viewers ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Viewer{}
	byID := map[int64]*Viewer{}
	for rows.Next() {
		v, err := scanViewer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		byID[v.ID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trows, err := s.DB.QueryContext(ctx, `SELECT user_id, title_id FROM viewer_titles ORDER BY title_id`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var id int64
		var t string
		if err := trows.Scan(&id, &t); err != nil {
			return nil, err
		}
		if v := byID[id]; v != nil {
			v.Titles = append(v.Titles, t)
		}
	}
	return out, trows.Err()
}

// GetViewer returns one viewer with their titles.
func (s *Store) GetViewer(ctx context.Context, id int64) (*Viewer, error) {
	v, err := scanViewer(s.DB.QueryRowContext(ctx, `SELECT `+viewerCols+` FROM viewers WHERE user_id = ?`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT title_id FROM viewer_titles WHERE user_id = ? ORDER BY title_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		v.Titles = append(v.Titles, t)
	}
	return v, rows.Err()
}

// CreateViewer adds a viewer (and the user row their progress lives under).
// ID, Slot and CreatedAt are filled in.
func (s *Store) CreateViewer(ctx context.Context, v *Viewer) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM viewers WHERE name = ?`, v.Name).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ErrNameTaken
	}
	// The lowest free slot, so ports stay small and stable.
	slot := 1
	for {
		var used int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM viewers WHERE slot = ?`, slot).Scan(&used); err != nil {
			return err
		}
		if used == 0 {
			break
		}
		slot++
	}
	t := now()
	// The username only satisfies the users table: it can't be signed in
	// with (noLogin matches no password) and can't clash with a real one.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		fmt.Sprintf("~viewer-%d-%d", slot, time.Now().UnixNano()), noLogin, t, t)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	v.ID, v.Slot, v.CreatedAt = id, slot, parseTime(t)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO viewers (user_id, name, slot, all_titles, dlna, tvs, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, v.Name, slot, v.AllTitles, v.DLNA, jsonList(v.TVs), t); err != nil {
		return err
	}
	if err := setViewerTitles(ctx, tx, id, v.Titles); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateViewer saves a viewer's name, titles and DLNA options.
func (s *Store) UpdateViewer(ctx context.Context, v *Viewer) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM viewers WHERE name = ? AND user_id <> ?`, v.Name, v.ID).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ErrNameTaken
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE viewers SET name = ?, all_titles = ?, dlna = ?, tvs = ? WHERE user_id = ?`,
		v.Name, v.AllTitles, v.DLNA, jsonList(v.TVs), v.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := setViewerTitles(ctx, tx, v.ID, v.Titles); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteViewer removes a viewer with their progress.
func (s *Store) DeleteViewer(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM viewers WHERE user_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	// Deleting the user row cascades to the viewer, its titles and progress.
	_, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func setViewerTitles(ctx context.Context, tx *sql.Tx, id int64, titles []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM viewer_titles WHERE user_id = ?`, id); err != nil {
		return err
	}
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO viewer_titles (user_id, title_id) VALUES (?, ?)`, id, t); err != nil {
			return err
		}
	}
	return nil
}

// TitleAllowed reports whether a library item id (a film, or an episode
// "tt…:S:E") is one of titles: the id itself or its series.
func TitleAllowed(titles []string, itemID string) bool {
	series, _, _ := strings.Cut(itemID, ":")
	for _, t := range titles {
		if t == itemID || t == series {
			return true
		}
	}
	return false
}

// SetViewerTitle lets a viewer see a title (a film's or series' id), or
// stops them.
func (s *Store) SetViewerTitle(ctx context.Context, viewerID int64, titleID string, on bool) error {
	titleID = strings.TrimSpace(titleID)
	if titleID == "" {
		return errors.New("no title")
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM viewers WHERE user_id = ?`, viewerID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	var err error
	if on {
		_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO viewer_titles (user_id, title_id) VALUES (?, ?)`, viewerID, titleID)
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM viewer_titles WHERE user_id = ? AND title_id = ?`, viewerID, titleID)
	}
	return err
}
