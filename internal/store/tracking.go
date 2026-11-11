package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ---- profiles ----

// Profile describes which releases are acceptable for a title.
type Profile struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Resolutions    []string  `json:"resolutions"` // best first
	MovieMinMB     int       `json:"movieMinMB"`
	MovieMaxMB     int       `json:"movieMaxMB"`
	EpisodeMinMB   int       `json:"episodeMinMB"`
	EpisodeMaxMB   int       `json:"episodeMaxMB"`
	AudioLangs     []string  `json:"audioLangs"`
	AudioRequired  bool      `json:"audioRequired"`
	SubtitleLangs  []string  `json:"subtitleLangs"`
	BlockedTerms   []string  `json:"blockedTerms"`
	PreferredTerms []string  `json:"preferredTerms"`
	MinSeeders     int       `json:"minSeeders"`
	SeedRatio      float64   `json:"seedRatio"`
	SeedHours      int       `json:"seedHours"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

const profileCols = `id, name, resolutions, movie_min_mb, movie_max_mb, episode_min_mb, episode_max_mb, audio_langs,
	audio_required, subtitle_langs, blocked_terms, preferred_terms, min_seeders, seed_ratio, seed_hours, created_at, updated_at`

func unlist(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func scanProfile(row scanner) (*Profile, error) {
	var p Profile
	var res, audio, subs, blocked, preferred, created, updated string
	if err := row.Scan(&p.ID, &p.Name, &res, &p.MovieMinMB, &p.MovieMaxMB, &p.EpisodeMinMB, &p.EpisodeMaxMB, &audio,
		&p.AudioRequired, &subs, &blocked, &preferred, &p.MinSeeders, &p.SeedRatio, &p.SeedHours, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.Resolutions, p.AudioLangs, p.SubtitleLangs = unlist(res), unlist(audio), unlist(subs)
	p.BlockedTerms, p.PreferredTerms = unlist(blocked), unlist(preferred)
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
	return &p, nil
}

// ListProfiles returns all profiles by name.
func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+profileCols+` FROM profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetProfile returns one profile.
func (s *Store) GetProfile(ctx context.Context, id int64) (*Profile, error) {
	return scanProfile(s.DB.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE id = ?`, id))
}

// SaveProfile inserts (ID 0) or updates a profile.
func (s *Store) SaveProfile(ctx context.Context, p *Profile) (*Profile, error) {
	t := now()
	args := []any{p.Name, jsonList(p.Resolutions), p.MovieMinMB, p.MovieMaxMB, p.EpisodeMinMB, p.EpisodeMaxMB,
		jsonList(p.AudioLangs), p.AudioRequired, jsonList(p.SubtitleLangs), jsonList(p.BlockedTerms), jsonList(p.PreferredTerms),
		p.MinSeeders, p.SeedRatio, p.SeedHours}
	var err error
	id := p.ID
	if id == 0 {
		var res sql.Result
		res, err = s.DB.ExecContext(ctx, `INSERT INTO profiles (name, resolutions, movie_min_mb, movie_max_mb, episode_min_mb,
			episode_max_mb, audio_langs, audio_required, subtitle_langs, blocked_terms, preferred_terms, min_seeders, seed_ratio,
			seed_hours, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append(args, t, t)...)
		if err == nil {
			id, _ = res.LastInsertId()
		}
	} else {
		var res sql.Result
		res, err = s.DB.ExecContext(ctx, `UPDATE profiles SET name = ?, resolutions = ?, movie_min_mb = ?, movie_max_mb = ?,
			episode_min_mb = ?, episode_max_mb = ?, audio_langs = ?, audio_required = ?, subtitle_langs = ?, blocked_terms = ?,
			preferred_terms = ?, min_seeders = ?, seed_ratio = ?, seed_hours = ?, updated_at = ? WHERE id = ?`, append(args, t, id)...)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, ErrNotFound
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return s.GetProfile(ctx, id)
}

// ErrInUse means a row is still referenced.
var ErrInUse = errors.New("still in use")

// DeleteProfile removes a profile no title uses.
func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM titles WHERE profile_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- titles ----

// Title is a movie or series being tracked.
type Title struct {
	ID          int64      `json:"id"`
	IMDbID      string     `json:"imdbId"`
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Year        int        `json:"year,omitempty"`
	Poster      string     `json:"poster,omitempty"`
	ProfileID   int64      `json:"profileId"`
	Monitor     string     `json:"monitor"` // all | future | none
	Seasons     []int      `json:"seasons"` // empty = all
	AddedAt     time.Time  `json:"addedAt"`
	RefreshedAt *time.Time `json:"refreshedAt,omitempty"`
	// Counts, filled by ListTitles/GetTitle.
	Wanted      int `json:"wanted"`
	Downloading int `json:"downloading"`
	Imported    int `json:"imported"`
	Unaired     int `json:"unaired"`
}

const titleCols = `t.id, t.imdb_id, t.type, t.name, t.year, t.poster, t.profile_id, t.monitor, t.seasons, t.added_at, t.refreshed_at,
	COALESCE(SUM(i.monitored AND i.state = 'missing'), 0), COALESCE(SUM(i.state = 'downloading'), 0),
	COALESCE(SUM(i.state = 'imported'), 0), COALESCE(SUM(i.monitored AND i.state = 'unaired'), 0)`

func scanTitle(row scanner) (*Title, error) {
	var t Title
	var seasons, added string
	var refreshed sql.NullString
	if err := row.Scan(&t.ID, &t.IMDbID, &t.Type, &t.Name, &t.Year, &t.Poster, &t.ProfileID, &t.Monitor, &seasons, &added, &refreshed,
		&t.Wanted, &t.Downloading, &t.Imported, &t.Unaired); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(seasons), &t.Seasons)
	if t.Seasons == nil {
		t.Seasons = []int{}
	}
	t.AddedAt, t.RefreshedAt = parseTime(added), nullTime(refreshed)
	return &t, nil
}

// ListTitles returns tracked titles, newest first.
func (s *Store) ListTitles(ctx context.Context) ([]Title, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+titleCols+` FROM titles t LEFT JOIN items i ON i.title_id = t.id GROUP BY t.id ORDER BY t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Title{}
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTitle returns one tracked title.
func (s *Store) GetTitle(ctx context.Context, id int64) (*Title, error) {
	return scanTitle(s.DB.QueryRowContext(ctx, `SELECT `+titleCols+` FROM titles t LEFT JOIN items i ON i.title_id = t.id WHERE t.id = ? GROUP BY t.id`, id))
}

// TitleByIMDb returns the tracked title with this IMDb id.
func (s *Store) TitleByIMDb(ctx context.Context, imdb string) (*Title, error) {
	return scanTitle(s.DB.QueryRowContext(ctx, `SELECT `+titleCols+` FROM titles t LEFT JOIN items i ON i.title_id = t.id WHERE t.imdb_id = ? GROUP BY t.id`, imdb))
}

// CreateTitle starts tracking a title.
func (s *Store) CreateTitle(ctx context.Context, t *Title) (*Title, error) {
	seasons, _ := json.Marshal(t.Seasons)
	if t.Seasons == nil {
		seasons = []byte("[]")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO titles (imdb_id, type, name, year, poster, profile_id, monitor, seasons, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.IMDbID, t.Type, t.Name, t.Year, t.Poster, t.ProfileID, t.Monitor, string(seasons), now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTitle(ctx, id)
}

// UpdateTitle changes profile, monitor mode and seasons.
func (s *Store) UpdateTitle(ctx context.Context, id int64, profileID int64, monitor string, seasons []int) error {
	b, _ := json.Marshal(seasons)
	if seasons == nil {
		b = []byte("[]")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE titles SET profile_id = ?, monitor = ?, seasons = ? WHERE id = ?`, profileID, monitor, string(b), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchTitleRefreshed records a metadata refresh, and the name/year/poster seen.
func (s *Store) TouchTitleRefreshed(ctx context.Context, id int64, name string, year int, poster string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE titles SET refreshed_at = ?, name = ?, year = ?, poster = ? WHERE id = ?`, now(), name, year, poster, id)
	return err
}

// TitlesToRefresh lists titles whose metadata is older than maxAge.
func (s *Store) TitlesToRefresh(ctx context.Context, maxAge time.Duration) ([]Title, error) {
	cut := time.Now().Add(-maxAge).UTC().Format(time.RFC3339Nano)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+titleCols+` FROM titles t LEFT JOIN items i ON i.title_id = t.id
		WHERE t.refreshed_at IS NULL OR t.refreshed_at < ? GROUP BY t.id ORDER BY t.refreshed_at`, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// DeleteTitle stops tracking (items go too; downloads and files stay).
func (s *Store) DeleteTitle(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM titles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- items ----

// Item states.
const (
	ItemUnaired     = "unaired"
	ItemMissing     = "missing"
	ItemDownloading = "downloading"
	ItemImported    = "imported"
)

// Item is a movie or episode Reelyard wants.
type Item struct {
	ID           int64  `json:"id"`
	TitleID      int64  `json:"titleId"`
	ItemID       string `json:"itemId"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	Name         string `json:"name,omitempty"`
	AirDate      string `json:"airDate,omitempty"`
	Monitored    bool   `json:"monitored"`
	State        string `json:"state"`
	DownloadID   *int64 `json:"downloadId,omitempty"`
	GrabbedHash  string `json:"grabbedHash,omitempty"`
	GrabbedIdx   int    `json:"grabbedIdx"`
	GrabbedName  string `json:"grabbedName,omitempty"`
	Attempts     int    `json:"attempts"`
	NextSearchAt string `json:"nextSearchAt,omitempty"`
	LastSearchAt string `json:"lastSearchAt,omitempty"`
	Note         string `json:"note,omitempty"`
}

const itemCols = `id, title_id, item_id, season, episode, name, air_date, monitored, state, download_id, grabbed_hash, grabbed_idx,
	grabbed_name, attempts, next_search_at, last_search_at, note`

func scanItem(row scanner) (*Item, error) {
	var it Item
	var dl sql.NullInt64
	if err := row.Scan(&it.ID, &it.TitleID, &it.ItemID, &it.Season, &it.Episode, &it.Name, &it.AirDate, &it.Monitored, &it.State,
		&dl, &it.GrabbedHash, &it.GrabbedIdx, &it.GrabbedName, &it.Attempts, &it.NextSearchAt, &it.LastSearchAt, &it.Note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if dl.Valid {
		it.DownloadID = &dl.Int64
	}
	return &it, nil
}

func (s *Store) queryItems(ctx context.Context, where string, args ...any) ([]Item, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+itemCols+` FROM items `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// ItemsOfTitle returns a title's items in episode order.
func (s *Store) ItemsOfTitle(ctx context.Context, titleID int64) ([]Item, error) {
	return s.queryItems(ctx, `WHERE title_id = ? ORDER BY season = 0, season, episode`, titleID)
}

// GetItem returns one item.
func (s *Store) GetItem(ctx context.Context, id int64) (*Item, error) {
	return scanItem(s.DB.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE id = ?`, id))
}

// ItemByItemID returns the item for tt… or tt…:S:E.
func (s *Store) ItemByItemID(ctx context.Context, itemID string) (*Item, error) {
	return scanItem(s.DB.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE item_id = ?`, itemID))
}

// DueItems returns monitored missing items whose next search time has come.
func (s *Store) DueItems(ctx context.Context, limit int) ([]Item, error) {
	return s.queryItems(ctx, `WHERE monitored = 1 AND state = ? AND (next_search_at = '' OR next_search_at <= ?)
		ORDER BY next_search_at, id LIMIT ?`, ItemMissing, now(), limit)
}

// ItemsDownloading returns items waiting on a download.
func (s *Store) ItemsDownloading(ctx context.Context) ([]Item, error) {
	return s.queryItems(ctx, `WHERE state = ?`, ItemDownloading)
}

// UpsertItem creates an item or refreshes its episode metadata (name, air
// date). State and monitoring are only set when the row is new.
func (s *Store) UpsertItem(ctx context.Context, it Item) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO items (title_id, item_id, season, episode, name, air_date, monitored, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET name = excluded.name, air_date = excluded.air_date`,
		it.TitleID, it.ItemID, it.Season, it.Episode, it.Name, it.AirDate, it.Monitored, it.State)
	return err
}

// ItemUpdate holds optional changes to an item.
type ItemUpdate struct {
	State        *string
	Monitored    *bool
	DownloadID   *int64          // 0 clears it
	Grabbed      *BlockedRelease // release grabbed (InfoHash, FileIdx, Name)
	Attempts     *int
	NextSearchAt *string
	LastSearchAt *string
	Note         *string
}

// UpdateItem applies changes.
func (s *Store) UpdateItem(ctx context.Context, id int64, u ItemUpdate) error {
	var sets []string
	var args []any
	add := func(col string, v any) { sets, args = append(sets, col+" = ?"), append(args, v) }
	if u.State != nil {
		add("state", *u.State)
	}
	if u.Monitored != nil {
		add("monitored", *u.Monitored)
	}
	if u.DownloadID != nil {
		if *u.DownloadID == 0 {
			add("download_id", nil)
		} else {
			add("download_id", *u.DownloadID)
		}
	}
	if u.Grabbed != nil {
		add("grabbed_hash", strings.ToLower(u.Grabbed.InfoHash))
		add("grabbed_idx", u.Grabbed.FileIdx)
		add("grabbed_name", u.Grabbed.Name)
	}
	if u.Attempts != nil {
		add("attempts", *u.Attempts)
	}
	if u.NextSearchAt != nil {
		add("next_search_at", *u.NextSearchAt)
	}
	if u.LastSearchAt != nil {
		add("last_search_at", *u.LastSearchAt)
	}
	if u.Note != nil {
		add("note", *u.Note)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.DB.ExecContext(ctx, `UPDATE items SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// ---- blocklist ----

// BlockedRelease is a release not to grab again for an item.
type BlockedRelease struct {
	InfoHash  string    `json:"infoHash"`
	FileIdx   int       `json:"fileIdx"`
	ItemID    string    `json:"itemId"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

// Block adds a release to the blocklist for an item.
func (s *Store) Block(ctx context.Context, b BlockedRelease) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO blocklist (info_hash, file_idx, item_id, name, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO UPDATE SET reason = excluded.reason, created_at = excluded.created_at`,
		strings.ToLower(b.InfoHash), b.FileIdx, b.ItemID, b.Name, b.Reason, now())
	return err
}

// BlockedFor returns the blocked info hashes for an item.
func (s *Store) BlockedFor(ctx context.Context, itemID string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT info_hash FROM blocklist WHERE item_id = ?`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}

// ListBlocklist returns all blocked releases, newest first.
func (s *Store) ListBlocklist(ctx context.Context) ([]BlockedRelease, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT info_hash, file_idx, item_id, name, reason, created_at FROM blocklist ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlockedRelease{}
	for rows.Next() {
		var b BlockedRelease
		var created string
		if err := rows.Scan(&b.InfoHash, &b.FileIdx, &b.ItemID, &b.Name, &b.Reason, &created); err != nil {
			return nil, err
		}
		b.CreatedAt = parseTime(created)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Unblock removes a blocklist entry.
func (s *Store) Unblock(ctx context.Context, infoHash string, fileIdx int, itemID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM blocklist WHERE info_hash = ? AND file_idx = ? AND item_id = ?`, infoHash, fileIdx, itemID)
	return err
}

// PromoteAired turns unaired items whose air date has passed into missing.
func (s *Store) PromoteAired(ctx context.Context, titleID int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE items SET state = ?, next_search_at = '' WHERE title_id = ? AND state = ?
		AND air_date != '' AND air_date <= ?`, ItemMissing, titleID, ItemUnaired, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetMonitored sets the monitored flag of one item.
func (s *Store) SetMonitored(ctx context.Context, id int64, on bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE items SET monitored = ? WHERE id = ?`, on, id)
	return err
}

// SearchSoon makes missing items of a title (or one item) due now.
func (s *Store) SearchSoon(ctx context.Context, titleID, itemID int64) error {
	if itemID > 0 {
		_, err := s.DB.ExecContext(ctx, `UPDATE items SET next_search_at = '' WHERE id = ?`, itemID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE items SET next_search_at = '' WHERE title_id = ? AND state = ?`, titleID, ItemMissing)
	return err
}
