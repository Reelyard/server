// Package update keeps track of Reelyard's releases on GitHub and, where it
// can, installs a newer one: it downloads the release archive for this
// system, checks it against the release's checksums.txt, swaps the binary
// and asks the service manager to restart Reelyard.
//
// Only a plain binary running as a service updates itself. Docker images,
// deb/rpm and Homebrew installs, and development builds are told how to
// update instead, because something else owns their files.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/reelyard/server/internal/store"
)

const (
	settingsKey  = "updates"
	firstCheck   = time.Minute
	checkEvery   = 6 * time.Hour
	minRecheck   = 30 * time.Second // "Check now" more often than this reuses the last answer
	maxArchive   = 300 << 20
	maxChecksums = 1 << 20
)

// Install methods.
const (
	MethodBinary   = "binary"
	MethodDocker   = "docker"
	MethodPackage  = "package"
	MethodHomebrew = "homebrew"
	MethodDev      = "dev"
)

// Settings are the user's update choices.
type Settings struct {
	// Channel is "stable" (releases only) or "beta" (prereleases too).
	Channel string `json:"channel"`
	// Auto installs new versions by itself when Reelyard can.
	Auto bool `json:"auto"`
}

// Release is a published GitHub release.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	assets      map[string]string
}

// Status is what the settings page and the update banner show.
type Status struct {
	Current string `json:"current"`
	Dev     bool   `json:"dev"`
	Method  string `json:"method"`
	// CanApply says Reelyard can install the update itself; otherwise
	// Reason says why and Instructions how to update by hand.
	CanApply     bool       `json:"canApply"`
	Reason       string     `json:"reason,omitempty"`
	Instructions string     `json:"instructions,omitempty"`
	Settings     Settings   `json:"settings"`
	Latest       *Release   `json:"latest"`
	Available    bool       `json:"available"`
	CheckedAt    *time.Time `json:"checkedAt"`
	CheckError   string     `json:"checkError,omitempty"`
	// State is "idle", "updating", "restarting" or "failed".
	State      string `json:"state"`
	StateError string `json:"stateError,omitempty"`
}

// Options configure the service.
type Options struct {
	Store   *store.Store
	Log     *slog.Logger
	HTTP    *http.Client
	Repo    string // "Reelyard/server"
	API     string // GitHub's API, "https://api.github.com"
	Current string // this build's version
	// Exe is this program's path; empty uses os.Executable.
	Exe string
	// Restart stops Reelyard so the service manager starts it again; nil
	// when it doesn't run as a service.
	Restart func()
	// inDocker overrides the /.dockerenv check (tests).
	InDocker *bool
}

// Service tracks releases and installs updates.
type Service struct {
	o Options

	mu         sync.Mutex
	latest     *Release
	checkedAt  time.Time
	checkErr   string
	state      string
	stateErr   string
	failedFor  string // version an automatic update already failed for
	installing bool
}

// New returns a Service.
func New(o Options) *Service {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if o.Repo == "" {
		o.Repo = "Reelyard/server"
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Exe == "" {
		if exe, err := os.Executable(); err == nil {
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				exe = real
			}
			o.Exe = exe
		}
	}
	return &Service{o: o, state: "idle"}
}

// CleanupOld removes the binary an update replaced, left beside the new one.
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	_ = os.Remove(exe + ".old")
}

// ---- settings ----

func (s *Service) defaults() Settings {
	ch := "stable"
	if IsPrerelease(s.o.Current) {
		ch = "beta"
	}
	return Settings{Channel: ch, Auto: true}
}

// LoadSettings returns the saved settings, or the defaults.
func (s *Service) LoadSettings(ctx context.Context) Settings {
	st := s.defaults()
	if s.o.Store != nil {
		_ = s.o.Store.GetSetting(ctx, settingsKey, &st)
	}
	if st.Channel != "beta" {
		st.Channel = "stable"
	}
	return st
}

// SaveSettings stores the settings and checks again if the channel changed.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	if st.Channel != "stable" && st.Channel != "beta" {
		return errors.New(`channel must be "stable" or "beta"`)
	}
	old := s.LoadSettings(ctx)
	if err := s.o.Store.SetSetting(ctx, settingsKey, st); err != nil {
		return err
	}
	if old.Channel != st.Channel {
		s.mu.Lock()
		s.checkedAt = time.Time{} // force the next check
		s.mu.Unlock()
		_ = s.Check(ctx)
	}
	return nil
}

// ---- install method ----

func (s *Service) method() (method string, can bool, reason, how string) {
	switch {
	case IsDev(s.o.Current):
		return MethodDev, false, "This is a development build, so it isn't replaced automatically.",
			"Update it from source: git pull, then rebuild (make build) and restart."
	case s.inDocker():
		return MethodDocker, false, "Reelyard runs in Docker, where the image is updated, not the program inside it.",
			"Pull the new image and recreate the container, e.g. docker compose pull && docker compose up -d (or update it in your NAS's app store)."
	}
	exe := filepath.ToSlash(s.o.Exe)
	switch {
	case strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/Caskroom/") || strings.Contains(exe, "/homebrew/"):
		return MethodHomebrew, false, "Reelyard was installed with Homebrew.", "Run: brew upgrade --cask reelyard"
	case strings.HasPrefix(exe, "/usr/bin/") || strings.HasPrefix(exe, "/usr/sbin/"):
		return MethodPackage, false, "Reelyard was installed from a .deb or .rpm package.",
			"Download the new package from the release page and install it (sudo apt install ./reelyard_….deb or sudo dnf install ./reelyard-….rpm)."
	case s.o.Restart == nil:
		return MethodBinary, false, "Reelyard isn't running as a service, so it can't restart itself after updating.",
			"Download the new version from the release page and replace the program, or install Reelyard as a service (reelyard install)."
	case s.o.Exe == "":
		return MethodBinary, false, "Reelyard couldn't find its own program file.", "Download the new version from the release page."
	}
	dir := filepath.Dir(s.o.Exe)
	f, err := os.CreateTemp(dir, ".reelyard-update-check-*")
	if err != nil {
		return MethodBinary, false, "Reelyard can't write to its own folder (" + dir + ").",
			"Download the new version from the release page and replace the program, or let the service's user write to that folder."
	}
	f.Close()
	os.Remove(f.Name())
	return MethodBinary, true, "", ""
}

func (s *Service) inDocker() bool {
	if s.o.InDocker != nil {
		return *s.o.InDocker
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// ---- status ----

// Status reports the current version, the latest release and what can be done.
func (s *Service) Status(ctx context.Context) Status {
	m, can, reason, how := s.method()
	st := Status{
		Current: s.o.Current, Dev: IsDev(s.o.Current), Method: m, CanApply: can, Reason: reason, Instructions: how,
		Settings: s.LoadSettings(ctx),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Latest, st.CheckError, st.State, st.StateError = s.latest, s.checkErr, s.state, s.stateErr
	if !s.checkedAt.IsZero() {
		t := s.checkedAt
		st.CheckedAt = &t
	}
	st.Available = s.latest != nil && Newer(s.latest.Version, s.o.Current)
	return st
}

// ---- checking ----

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub for the newest release on the chosen channel.
func (s *Service) Check(ctx context.Context) error {
	s.mu.Lock()
	if time.Since(s.checkedAt) < minRecheck {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	rel, err := s.fetchLatest(ctx, s.LoadSettings(ctx).Channel == "beta")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkedAt = time.Now()
	if err != nil {
		s.checkErr = "Couldn't reach GitHub to check for updates: " + err.Error()
		return err
	}
	s.checkErr, s.latest = "", rel
	if rel != nil && Newer(rel.Version, s.o.Current) {
		s.o.Log.Info("update available", "current", s.o.Current, "latest", rel.Version)
	}
	return nil
}

func (s *Service) fetchLatest(ctx context.Context, beta bool) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.o.API, "/")+"/repos/"+s.o.Repo+"/releases?per_page=30", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Reelyard/"+s.o.Current)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	var best *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || (r.Prerelease && !beta) {
			continue
		}
		if _, ok := parse(r.TagName); !ok {
			continue
		}
		if best == nil || Newer(r.TagName, best.TagName) {
			best = r
		}
	}
	if best == nil {
		return nil, nil
	}
	out := &Release{
		Version: best.TagName, Name: best.Name, URL: best.HTMLURL, Notes: best.Body,
		PublishedAt: best.PublishedAt, Prerelease: best.Prerelease, assets: map[string]string{},
	}
	if len(out.Notes) > 20000 {
		out.Notes = out.Notes[:20000] + "…"
	}
	for _, a := range best.Assets {
		out.assets[a.Name] = a.URL
	}
	return out, nil
}

// Run checks for releases now and then until ctx ends, and installs them
// when automatic updates are on and Reelyard can.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(firstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.Check(ctx); err != nil {
			s.o.Log.Warn("update check failed", "err", err)
		}
		s.maybeAuto(ctx)
		t.Reset(checkEvery)
	}
}

func (s *Service) maybeAuto(ctx context.Context) {
	st := s.Status(ctx)
	if !st.Settings.Auto || !st.Available || !st.CanApply {
		return
	}
	s.mu.Lock()
	failed := s.failedFor == st.Latest.Version
	s.mu.Unlock()
	if failed {
		return
	}
	s.o.Log.Info("installing update automatically", "version", st.Latest.Version)
	if err := s.Apply(ctx); err != nil {
		s.o.Log.Warn("automatic update failed", "err", err)
	}
}

// ---- installing ----

// ErrNothing means there's no newer release to install.
var ErrNothing = errors.New("there's no newer version to install")

// Apply starts installing the latest release in the background; Reelyard
// restarts when it's in place. It fails fast when it can't start.
func (s *Service) Apply(ctx context.Context) error {
	st := s.Status(ctx)
	if !st.CanApply {
		return errors.New(st.Reason)
	}
	if !st.Available {
		return ErrNothing
	}
	s.mu.Lock()
	if s.installing {
		s.mu.Unlock()
		return errors.New("an update is already being installed")
	}
	s.installing, s.state, s.stateErr = true, "updating", ""
	s.mu.Unlock()

	rel := st.Latest
	go func() {
		ictx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := s.install(ictx, rel)
		s.mu.Lock()
		s.installing = false
		if err != nil {
			s.state, s.stateErr, s.failedFor = "failed", err.Error(), rel.Version
			s.mu.Unlock()
			s.o.Log.Error("update failed", "version", rel.Version, "err", err)
			return
		}
		s.state = "restarting"
		s.mu.Unlock()
		s.o.Log.Info("update installed; restarting", "from", s.o.Current, "to", rel.Version)
		time.Sleep(time.Second) // let the page see "restarting"
		s.o.Restart()
	}()
	return nil
}

// ArchiveName is the release archive GoReleaser makes for this system.
func ArchiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("reelyard_%s_%s_%s.%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

func (s *Service) install(ctx context.Context, rel *Release) error {
	name := ArchiveName(rel.Version, runtime.GOOS, runtime.GOARCH)
	archiveURL, sumsURL := rel.assets[name], rel.assets["checksums.txt"]
	if archiveURL == "" {
		return fmt.Errorf("the release has no download for this system (%s)", name)
	}
	if sumsURL == "" {
		return errors.New("the release has no checksums.txt, so the download can't be checked")
	}
	prefix := strings.ToLower(s.o.API)
	if prefix == "https://api.github.com" {
		prefix = "https://github.com/" + strings.ToLower(s.o.Repo) + "/releases/download/"
		for _, u := range []string{archiveURL, sumsURL} {
			if !strings.HasPrefix(strings.ToLower(u), prefix) {
				return fmt.Errorf("unexpected download address %s", u)
			}
		}
	}

	sums, err := s.get(ctx, sumsURL, maxChecksums)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	want := checksumFor(sums, name)
	if want == "" {
		return fmt.Errorf("checksums.txt doesn't list %s", name)
	}

	dir := filepath.Dir(s.o.Exe)
	tmp, err := os.CreateTemp(dir, ".reelyard-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := s.download(ctx, archiveURL, tmp, want); err != nil {
		return err
	}

	bin := "reelyard"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	newPath := s.o.Exe + ".new"
	if err := extract(tmp, name, bin, newPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("unpack %s: %w", name, err)
	}
	return swap(s.o.Exe, newPath)
}

func (s *Service) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Reelyard/"+s.o.Current)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// download writes url to f and checks its SHA-256.
func (s *Service) download(ctx context.Context, url string, f *os.File, wantHex string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Reelyard/"+s.o.Current)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n > maxArchive {
		return errors.New("download is unexpectedly large")
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantHex) {
		return errors.New("the download doesn't match the release's checksum, so it wasn't installed")
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

// checksumFor finds name's hash in a GoReleaser checksums.txt.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return f[0]
		}
	}
	return ""
}

// extract copies the file called bin out of the archive to dst.
func extract(f *os.File, archive, bin, dst string) error {
	var src io.Reader
	if strings.HasSuffix(archive, ".zip") {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return err
		}
		for _, zf := range zr.File {
			if path.Base(zf.Name) == bin && !zf.FileInfo().IsDir() {
				rc, err := zf.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				src = rc
				break
			}
		}
	} else {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == bin {
				src = tr
				break
			}
		}
	}
	if src == nil {
		return fmt.Errorf("%s isn't in the archive", bin)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(src, maxArchive)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// swap puts newPath in place of exe, keeping the old one as exe.old (a
// running program can be renamed, even on Windows, but not overwritten).
func swap(exe, newPath string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("move the current program aside: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(old, exe)
		os.Remove(newPath)
		return fmt.Errorf("put the new program in place: %w", err)
	}
	return nil
}
