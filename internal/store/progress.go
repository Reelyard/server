package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Progress is where a user is in a film or episode.
type Progress struct {
	ItemID      string    `json:"itemId"` // tt… or tt…:S:E
	MediaFileID *int64    `json:"fileId,omitempty"`
	PositionSec int       `json:"positionSec"`
	DurationSec int       `json:"durationSec"`
	Watched     bool      `json:"watched"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const progressCols = `item_id, media_file_id, position_sec, duration_sec, watched, updated_at`

func scanProgress(row scanner) (*Progress, error) {
	var p Progress
	var file sql.NullInt64
	var updated string
	if err := row.Scan(&p.ItemID, &file, &p.PositionSec, &p.DurationSec, &p.Watched, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if file.Valid {
		p.MediaFileID = &file.Int64
	}
	p.UpdatedAt = parseTime(updated)
	return &p, nil
}

// SaveProgress records a user's position in a film or episode.
func (s *Store) SaveProgress(ctx context.Context, userID int64, p Progress) (*Progress, error) {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO progress (user_id, item_id, media_file_id, position_sec, duration_sec, watched, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, item_id) DO UPDATE SET media_file_id = excluded.media_file_id,
			position_sec = excluded.position_sec, duration_sec = excluded.duration_sec,
			watched = excluded.watched, updated_at = excluded.updated_at`,
		userID, p.ItemID, p.MediaFileID, p.PositionSec, p.DurationSec, p.Watched, now())
	if err != nil {
		return nil, err
	}
	return scanProgress(s.DB.QueryRowContext(ctx,
		`SELECT `+progressCols+` FROM progress WHERE user_id = ? AND item_id = ?`, userID, p.ItemID))
}

// DeleteProgress forgets a user's position and watched mark for an item.
func (s *Store) DeleteProgress(ctx context.Context, userID int64, itemID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM progress WHERE user_id = ? AND item_id = ?`, userID, itemID)
	return err
}

// ListProgress returns a user's progress for items whose id starts with
// prefix ("tt0903747" matches the film or every episode), newest first;
// "" lists everything. limit <= 0 means no limit.
func (s *Store) ListProgress(ctx context.Context, userID int64, prefix string, limit int) ([]*Progress, error) {
	q := `SELECT ` + progressCols + ` FROM progress WHERE user_id = ?`
	args := []any{userID}
	if prefix != "" {
		q += ` AND (item_id = ? OR item_id LIKE ?)`
		args = append(args, prefix, strings.ReplaceAll(prefix, "%", "")+":%")
	}
	q += ` ORDER BY updated_at DESC, item_id`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Progress{}
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProgress returns a user's progress for one item, or ErrNotFound.
func (s *Store) GetProgress(ctx context.Context, userID int64, itemID string) (*Progress, error) {
	return scanProgress(s.DB.QueryRowContext(ctx,
		`SELECT `+progressCols+` FROM progress WHERE user_id = ? AND item_id = ?`, userID, itemID))
}
