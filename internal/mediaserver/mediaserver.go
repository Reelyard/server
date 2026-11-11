// Package mediaserver asks Jellyfin, Emby or Plex to rescan their libraries
// after Reelyard adds files, so new titles show up without waiting for the
// media server's own schedule.
package mediaserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/reelyard/server/internal/store"
)

const settingsKey = "media_servers"

// Kinds of media server.
const (
	Jellyfin = "jellyfin"
	Emby     = "emby"
	Plex     = "plex"
)

// Target is one media server to refresh.
type Target struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Token   string `json:"token,omitempty"` // never sent to the browser
	Enabled bool   `json:"enabled"`
}

// View is a Target as the UI sees it: the token is replaced by HasToken.
type View struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	HasToken bool   `json:"hasToken"`
	Enabled  bool   `json:"enabled"`
	// Last refresh result.
	LastAt    *time.Time `json:"lastAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

type result struct {
	at  time.Time
	err string
}

// Options configure the service.
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	HTTP  *http.Client
	// Delay groups imports that arrive close together into one refresh.
	Delay time.Duration
}

// Service keeps the targets and refreshes them.
type Service struct {
	opts Options

	mu      sync.Mutex
	targets []Target
	last    map[string]result
	timer   *time.Timer
}

// New loads the saved targets.
func New(ctx context.Context, opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if opts.Delay == 0 {
		opts.Delay = 30 * time.Second
	}
	s := &Service{opts: opts, last: map[string]result{}}
	_ = opts.Store.GetSetting(ctx, settingsKey, &s.targets)
	return s
}

// List returns the targets without tokens.
func (s *Service) List() []View {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]View, 0, len(s.targets))
	for _, t := range s.targets {
		v := View{ID: t.ID, Kind: t.Kind, Name: t.Name, URL: t.URL, HasToken: t.Token != "", Enabled: t.Enabled}
		if r, ok := s.last[t.ID]; ok {
			at := r.at
			v.LastAt, v.LastError = &at, r.err
		}
		out = append(out, v)
	}
	return out
}

// ErrNotFound is returned for an unknown target id.
var ErrNotFound = errors.New("media server not found")

// Save adds (empty ID) or updates a target. An empty token keeps the saved one.
func (s *Service) Save(ctx context.Context, t Target) (View, error) {
	t.Name = strings.TrimSpace(t.Name)
	t.Token = strings.TrimSpace(t.Token)
	u, err := cleanURL(t.URL)
	if err != nil {
		return View{}, err
	}
	t.URL = u
	if t.Kind != Jellyfin && t.Kind != Emby && t.Kind != Plex {
		return View{}, fmt.Errorf("unknown media server type %q", t.Kind)
	}
	if t.Name == "" {
		t.Name = map[string]string{Jellyfin: "Jellyfin", Emby: "Emby", Plex: "Plex"}[t.Kind]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]Target(nil), s.targets...)
	if t.ID == "" {
		if t.Token == "" {
			return View{}, errors.New("enter the API key or token")
		}
		t.ID = newID()
		next = append(next, t)
	} else {
		i := s.index(t.ID)
		if i < 0 {
			return View{}, ErrNotFound
		}
		if t.Token == "" {
			t.Token = next[i].Token
		}
		next[i] = t
	}
	if err := s.opts.Store.SetSetting(ctx, settingsKey, next); err != nil {
		return View{}, err
	}
	s.targets = next
	delete(s.last, t.ID)
	return View{ID: t.ID, Kind: t.Kind, Name: t.Name, URL: t.URL, HasToken: true, Enabled: t.Enabled}, nil
}

// Delete removes a target.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return ErrNotFound
	}
	next := append(append([]Target(nil), s.targets[:i]...), s.targets[i+1:]...)
	if err := s.opts.Store.SetSetting(ctx, settingsKey, next); err != nil {
		return err
	}
	s.targets = next
	delete(s.last, id)
	return nil
}

func (s *Service) index(id string) int {
	for i, t := range s.targets {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// Test checks that a target is reachable and the token is accepted.
// t.ID set with an empty token tests with the saved token.
func (s *Service) Test(ctx context.Context, t Target) error {
	u, err := cleanURL(t.URL)
	if err != nil {
		return err
	}
	t.URL = u
	if t.Token == "" && t.ID != "" {
		s.mu.Lock()
		if i := s.index(t.ID); i >= 0 {
			t.Token = s.targets[i].Token
		}
		s.mu.Unlock()
	}
	if t.Token == "" {
		return errors.New("enter the API key or token")
	}
	path := "/System/Info"
	if t.Kind == Plex {
		path = "/library/sections"
	}
	return s.call(ctx, t, http.MethodGet, path)
}

// Kick schedules a refresh of every enabled target. Calls within Delay of
// each other share one refresh.
func (s *Service) Kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil || !s.anyEnabled() {
		return
	}
	s.timer = time.AfterFunc(s.opts.Delay, func() {
		s.mu.Lock()
		s.timer = nil
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		s.RefreshAll(ctx)
	})
}

func (s *Service) anyEnabled() bool {
	for _, t := range s.targets {
		if t.Enabled {
			return true
		}
	}
	return false
}

// RefreshAll asks every enabled target to rescan now.
func (s *Service) RefreshAll(ctx context.Context) {
	s.mu.Lock()
	targets := append([]Target(nil), s.targets...)
	s.mu.Unlock()
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		err := s.Refresh(ctx, t)
		r := result{at: time.Now()}
		if err != nil {
			r.err = err.Error()
			s.opts.Log.Warn("media server refresh failed", "server", t.Name, "err", err)
		} else {
			s.opts.Log.Info("media server refreshed", "server", t.Name)
		}
		s.mu.Lock()
		s.last[t.ID] = r
		s.mu.Unlock()
	}
}

// Refresh asks one target to rescan its libraries.
func (s *Service) Refresh(ctx context.Context, t Target) error {
	switch t.Kind {
	case Plex:
		return s.call(ctx, t, http.MethodGet, "/library/sections/all/refresh")
	default:
		return s.call(ctx, t, http.MethodPost, "/Library/Refresh")
	}
}

func (s *Service) call(ctx context.Context, t Target, method, path string) error {
	req, err := http.NewRequestWithContext(ctx, method, t.URL+path, nil)
	if err != nil {
		return err
	}
	switch t.Kind {
	case Plex:
		req.Header.Set("X-Plex-Token", t.Token)
		req.Header.Set("X-Plex-Client-Identifier", "reelyard")
		req.Header.Set("X-Plex-Product", "Reelyard")
	case Jellyfin:
		req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Client="Reelyard", Device="Reelyard", DeviceId="reelyard", Version="1", Token="%s"`, t.Token))
	case Emby:
		req.Header.Set("X-Emby-Token", t.Token)
	default:
		return fmt.Errorf("unknown media server type %q", t.Kind)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.opts.HTTP.Do(req)
	if err != nil {
		return reachErr(t.URL, err)
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("the server refused the API key or token")
	case resp.StatusCode >= 300:
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	return nil
}

// reachErr explains a connection failure in plain words.
func reachErr(addr string, err error) error {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "actively refused"): // the latter on Windows
		return fmt.Errorf("nothing is answering at %s. Check the address and port, and that the server is running", addr)
	case errors.As(err, &dns):
		return fmt.Errorf("couldn't find the host %q", dns.Name)
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("%s didn't answer in time", addr)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("couldn't reach %s: %v", addr, err)
}

// cleanURL checks a base URL and strips the trailing slash.
func cleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("enter the server's address, like http://192.168.1.10:8096")
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return strings.TrimSuffix(u.String(), "/"), nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
