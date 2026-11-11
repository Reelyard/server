package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Import states on a download.
const (
	ImportNone      = ""
	ImportImporting = "importing"
	ImportImported  = "imported"
	ImportFailed    = "failed"
	ImportSkipped   = "skipped"  // nothing to import into (no title attached)
	ImportRejected  = "rejected" // the file doesn't fit its profile (e.g. audio language)
)

// SetImportState records the import outcome of a download.
func (s *Store) SetImportState(ctx context.Context, downloadID int64, state, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE downloads SET import_state = ?, import_error = ?, updated_at = ? WHERE id = ?`, state, msg, now(), downloadID)
	return err
}

// ImportState returns a download's import state and error.
func (s *Store) ImportState(ctx context.Context, downloadID int64) (string, string, error) {
	var st, msg string
	err := s.DB.QueryRowContext(ctx, `SELECT import_state, import_error FROM downloads WHERE id = ?`, downloadID).Scan(&st, &msg)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return st, msg, err
}

// PendingImports lists finished downloads that haven't been imported yet.
func (s *Store) PendingImports(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id FROM downloads WHERE state = ? AND import_state IN ('', ?) ORDER BY id`, StateSeeding, ImportImporting)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MediaFile is a video placed in the library.
type MediaFile struct {
	ID           int64    `json:"id"`
	DownloadID   *int64   `json:"downloadId,omitempty"`
	TitleType    string   `json:"titleType"`
	TitleID      string   `json:"titleId"`
	TitleName    string   `json:"titleName"`
	Path         string   `json:"path"`
	Size         int64    `json:"size"`
	Method       string   `json:"method"`
	Resolution   string   `json:"resolution,omitempty"`
	VideoCodec   string   `json:"videoCodec,omitempty"`
	DurationSec  int      `json:"durationSec,omitempty"`
	AudioLangs   []string `json:"audioLangs"`
	EmbeddedSubs []string `json:"embeddedSubs"`
	// AudioCodecs is null until the file has been checked with ffprobe.
	AudioCodecs []string       `json:"audioCodecs"`
	OSHash      string         `json:"osHash,omitempty"`
	ImportedAt  time.Time      `json:"importedAt"`
	Subtitles   []SubtitleFile `json:"subtitles"`
}

// SubtitleFile is a subtitle saved next to a media file.
type SubtitleFile struct {
	ID          int64     `json:"id"`
	MediaFileID int64     `json:"mediaFileId"`
	Lang        string    `json:"lang"`
	Path        string    `json:"path"`
	Source      string    `json:"source"`
	SourceID    string    `json:"sourceId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

const mediaCols = `id, download_id, title_type, title_id, title_name, path, size, method, resolution, video_codec,
	duration_sec, audio_langs, embedded_subs, os_hash, imported_at, audio_codecs`

func scanMedia(row scanner) (*MediaFile, error) {
	var m MediaFile
	var dl sql.NullInt64
	var audio, subs, imported, codecs string
	if err := row.Scan(&m.ID, &dl, &m.TitleType, &m.TitleID, &m.TitleName, &m.Path, &m.Size, &m.Method, &m.Resolution,
		&m.VideoCodec, &m.DurationSec, &audio, &subs, &m.OSHash, &imported, &codecs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if dl.Valid {
		m.DownloadID = &dl.Int64
	}
	_ = json.Unmarshal([]byte(audio), &m.AudioLangs)
	_ = json.Unmarshal([]byte(subs), &m.EmbeddedSubs)
	if m.AudioLangs == nil {
		m.AudioLangs = []string{}
	}
	if m.EmbeddedSubs == nil {
		m.EmbeddedSubs = []string{}
	}
	if codecs != "" {
		m.AudioCodecs = []string{}
		_ = json.Unmarshal([]byte(codecs), &m.AudioCodecs)
	}
	m.ImportedAt = parseTime(imported)
	m.Subtitles = []SubtitleFile{}
	return &m, nil
}

func jsonList(v []string) string {
	if v == nil {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// codecList stores a codec list, with nil (not checked) as ”.
func codecList(v []string) string {
	if v == nil {
		return ""
	}
	return jsonList(v)
}

// SetMediaProbe records what ffprobe found in a file imported before Reelyard
// kept audio codecs. An empty video codec or zero duration keeps what was stored.
func (s *Store) SetMediaProbe(ctx context.Context, id int64, videoCodec string, audioCodecs []string, durationSec int) error {
	if audioCodecs == nil {
		audioCodecs = []string{}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE media_files SET audio_codecs = ?,
		video_codec = CASE WHEN ? != '' THEN ? ELSE video_codec END,
		duration_sec = CASE WHEN ? > 0 THEN ? ELSE duration_sec END WHERE id = ?`,
		jsonList(audioCodecs), videoCodec, videoCodec, durationSec, durationSec, id)
	return err
}

// UpsertMediaFile stores a media file, replacing a row with the same path.
func (s *Store) UpsertMediaFile(ctx context.Context, m *MediaFile) (*MediaFile, error) {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO media_files (download_id, title_type, title_id, title_name, path, size, method, resolution, video_codec,
			duration_sec, audio_langs, embedded_subs, os_hash, imported_at, audio_codecs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET download_id = excluded.download_id, title_type = excluded.title_type,
			title_id = excluded.title_id, title_name = excluded.title_name, size = excluded.size, method = excluded.method,
			resolution = excluded.resolution, video_codec = excluded.video_codec, duration_sec = excluded.duration_sec,
			audio_langs = excluded.audio_langs, embedded_subs = excluded.embedded_subs, os_hash = excluded.os_hash,
			imported_at = excluded.imported_at, audio_codecs = excluded.audio_codecs`,
		m.DownloadID, m.TitleType, m.TitleID, m.TitleName, m.Path, m.Size, m.Method, m.Resolution, m.VideoCodec,
		m.DurationSec, jsonList(m.AudioLangs), jsonList(m.EmbeddedSubs), m.OSHash, now(), codecList(m.AudioCodecs))
	if err != nil {
		return nil, err
	}
	return scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media_files WHERE path = ?`, m.Path))
}

// MediaFileByPath finds a media file by library path.
func (s *Store) MediaFileByPath(ctx context.Context, path string) (*MediaFile, error) {
	m, err := scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media_files WHERE path = ?`, path))
	if err != nil {
		return nil, err
	}
	return m, s.attachSubtitles(ctx, []*MediaFile{m})
}

// GetMediaFile returns one media file with its subtitles.
func (s *Store) GetMediaFile(ctx context.Context, id int64) (*MediaFile, error) {
	m, err := scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media_files WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return m, s.attachSubtitles(ctx, []*MediaFile{m})
}

// ListMediaFiles returns media files whose title id starts with prefix
// ("tt0903747" matches all episodes), newest first; "" lists everything.
func (s *Store) ListMediaFiles(ctx context.Context, prefix string) ([]*MediaFile, error) {
	q := `SELECT ` + mediaCols + ` FROM media_files`
	var args []any
	if prefix != "" {
		q += ` WHERE title_id = ? OR title_id LIKE ?`
		args = append(args, prefix, strings.ReplaceAll(prefix, "%", "")+":%")
	}
	q += ` ORDER BY title_id, id DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MediaFile{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachSubtitles(ctx, out)
}

// MediaFileForDownload returns the media file imported from a download.
func (s *Store) MediaFileForDownload(ctx context.Context, downloadID int64) (*MediaFile, error) {
	m, err := scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media_files WHERE download_id = ? ORDER BY id DESC LIMIT 1`, downloadID))
	if err != nil {
		return nil, err
	}
	return m, s.attachSubtitles(ctx, []*MediaFile{m})
}

func (s *Store) attachSubtitles(ctx context.Context, files []*MediaFile) error {
	if len(files) == 0 {
		return nil
	}
	byID := map[int64]*MediaFile{}
	ids := make([]string, 0, len(files))
	args := make([]any, 0, len(files))
	for _, m := range files {
		byID[m.ID] = m
		ids = append(ids, "?")
		args = append(args, m.ID)
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, media_file_id, lang, path, source, source_id, created_at FROM subtitle_files
		 WHERE media_file_id IN (`+strings.Join(ids, ",")+`) ORDER BY lang`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sf SubtitleFile
		var created string
		if err := rows.Scan(&sf.ID, &sf.MediaFileID, &sf.Lang, &sf.Path, &sf.Source, &sf.SourceID, &created); err != nil {
			return err
		}
		sf.CreatedAt = parseTime(created)
		byID[sf.MediaFileID].Subtitles = append(byID[sf.MediaFileID].Subtitles, sf)
	}
	return rows.Err()
}

// DeleteMediaFile removes a media file row (and its subtitle rows).
func (s *Store) DeleteMediaFile(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM media_files WHERE id = ?`, id)
	return err
}

// UpsertSubtitleFile records a subtitle, replacing one at the same path.
func (s *Store) UpsertSubtitleFile(ctx context.Context, sf SubtitleFile) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO subtitle_files (media_file_id, lang, path, source, source_id, created_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET media_file_id = excluded.media_file_id, lang = excluded.lang,
			source = excluded.source, source_id = excluded.source_id, created_at = excluded.created_at`,
		sf.MediaFileID, sf.Lang, sf.Path, sf.Source, sf.SourceID, now())
	return err
}

// Activity is one line of the activity log.
type Activity struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Kind    string    `json:"kind"`
	TitleID string    `json:"titleId,omitempty"`
	Message string    `json:"message"`
}

const activityKeep = 2000

// AddActivity appends to the activity log and prunes old rows now and then.
func (s *Store) AddActivity(ctx context.Context, level, kind, titleID, msg string) (*Activity, error) {
	t := now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO activity (time, level, kind, title_id, message) VALUES (?, ?, ?, ?, ?)`, t, level, kind, titleID, msg)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if id%100 == 0 {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM activity WHERE id <= ?`, id-activityKeep)
	}
	return &Activity{ID: id, Time: parseTime(t), Level: level, Kind: kind, TitleID: titleID, Message: msg}, nil
}

// ListActivity returns the newest entries first.
func (s *Store) ListActivity(ctx context.Context, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, time, level, kind, title_id, message FROM activity ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var t string
		if err := rows.Scan(&a.ID, &t, &a.Level, &a.Kind, &a.TitleID, &a.Message); err != nil {
			return nil, err
		}
		a.Time = parseTime(t)
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetSubtitleFile returns one saved subtitle.
func (s *Store) GetSubtitleFile(ctx context.Context, id int64) (*SubtitleFile, error) {
	var sf SubtitleFile
	var created string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, media_file_id, lang, path, source, source_id, created_at FROM subtitle_files WHERE id = ?`, id).
		Scan(&sf.ID, &sf.MediaFileID, &sf.Lang, &sf.Path, &sf.Source, &sf.SourceID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sf.CreatedAt = parseTime(created)
	return &sf, nil
}
