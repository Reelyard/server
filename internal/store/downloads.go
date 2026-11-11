package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Download states.
const (
	StateQueued      = "queued"      // waiting for a free slot
	StateMetadata    = "metadata"    // looking for peers / fetching torrent info
	StateDownloading = "downloading" // transferring
	StatePaused      = "paused"      // stopped by the user
	StateSeeding     = "seeding"     // wanted data complete, still sharing
	StateFailed      = "failed"      // gave up; see Error
	StateDone        = "done"        // finished sharing; torrent stopped (data may be deleted)
)

// Download is a torrent (or one file of it) Harbor is fetching.
type Download struct {
	ID          int64      `json:"id"`
	InfoHash    string     `json:"infoHash"`
	FileIdx     int        `json:"fileIdx"` // -1 = whole torrent
	Name        string     `json:"name"`
	FilePath    string     `json:"filePath,omitempty"`
	Sources     []string   `json:"sources,omitempty"`
	TitleType   string     `json:"titleType,omitempty"`
	TitleID     string     `json:"titleId,omitempty"`
	TitleName   string     `json:"titleName,omitempty"`
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	Size        int64      `json:"size"`
	DoneBytes   int64      `json:"doneBytes"`
	Uploaded    int64      `json:"uploaded"`
	AddedAt     time.Time  `json:"addedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ImportState string     `json:"importState,omitempty"`
	DataRemoved bool       `json:"dataRemoved,omitempty"`
	ImportError string     `json:"importError,omitempty"`
}

const downloadCols = `id, info_hash, file_idx, name, file_path, sources, title_type, title_id, title_name,
	state, error, size, done_bytes, uploaded, added_at, completed_at, updated_at, import_state, import_error, data_removed`

func scanDownload(row scanner) (*Download, error) {
	var d Download
	var sources, added, updated string
	var completed sql.NullString
	if err := row.Scan(&d.ID, &d.InfoHash, &d.FileIdx, &d.Name, &d.FilePath, &sources, &d.TitleType, &d.TitleID, &d.TitleName,
		&d.State, &d.Error, &d.Size, &d.DoneBytes, &d.Uploaded, &added, &completed, &updated, &d.ImportState, &d.ImportError, &d.DataRemoved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(sources), &d.Sources)
	d.AddedAt, d.UpdatedAt = parseTime(added), parseTime(updated)
	d.CompletedAt = nullTime(completed)
	return &d, nil
}

// NewDownload is what's needed to create a download.
type NewDownload struct {
	InfoHash  string
	FileIdx   int
	Name      string
	Sources   []string
	TitleType string
	TitleID   string
	TitleName string
}

// CreateDownload inserts a queued download. It returns ErrDuplicate if
// the same torrent file is already in the list.
func (s *Store) CreateDownload(ctx context.Context, n NewDownload) (*Download, error) {
	src, _ := json.Marshal(n.Sources)
	if n.Sources == nil {
		src = []byte("[]")
	}
	t := now()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO downloads (info_hash, file_idx, name, sources, title_type, title_id, title_name, state, added_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.ToLower(n.InfoHash), n.FileIdx, n.Name, string(src), n.TitleType, n.TitleID, n.TitleName, StateQueued, t, t)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDownload(ctx, id)
}

// GetDownload returns one download.
func (s *Store) GetDownload(ctx context.Context, id int64) (*Download, error) {
	return scanDownload(s.DB.QueryRowContext(ctx, `SELECT `+downloadCols+` FROM downloads WHERE id = ?`, id))
}

// ListDownloads returns downloads, newest first.
func (s *Store) ListDownloads(ctx context.Context) ([]Download, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+downloadCols+` FROM downloads ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DownloadUpdate holds the fields the engine writes back.
type DownloadUpdate struct {
	State       *string
	Error       *string
	Name        *string
	FilePath    *string
	Size        *int64
	DoneBytes   *int64
	Uploaded    *int64
	Completed   bool // set completed_at now (only if not set yet)
	DataRemoved *bool
}

// UpdateDownload applies an update.
func (s *Store) UpdateDownload(ctx context.Context, id int64, u DownloadUpdate) error {
	sets, args := []string{"updated_at = ?"}, []any{now()}
	add := func(col string, v any) { sets, args = append(sets, col+" = ?"), append(args, v) }
	if u.State != nil {
		add("state", *u.State)
	}
	if u.Error != nil {
		add("error", *u.Error)
	}
	if u.Name != nil {
		add("name", *u.Name)
	}
	if u.FilePath != nil {
		add("file_path", *u.FilePath)
	}
	if u.Size != nil {
		add("size", *u.Size)
	}
	if u.DoneBytes != nil {
		add("done_bytes", *u.DoneBytes)
	}
	if u.Uploaded != nil {
		add("uploaded", *u.Uploaded)
	}
	if u.DataRemoved != nil {
		add("data_removed", *u.DataRemoved)
	}
	if u.Completed {
		sets = append(sets, "completed_at = COALESCE(completed_at, ?)")
		args = append(args, now())
	}
	args = append(args, id)
	res, err := s.DB.ExecContext(ctx, `UPDATE downloads SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDownload removes a download row.
func (s *Store) DeleteDownload(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM downloads WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OtherDownloadsOfTorrent counts rows sharing an info hash (several files
// of one season pack can be separate downloads of the same torrent).
func (s *Store) OtherDownloadsOfTorrent(ctx context.Context, infoHash string, exceptID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloads WHERE info_hash = ? AND id != ?`, infoHash, exceptID).Scan(&n)
	return n, err
}
