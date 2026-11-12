package dlna

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/reelyard/server/internal/store"
)

const settingsKey = "dlna"

// Settings are the DLNA options people change from the UI.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Name is how TVs list the server.
	Name string `json:"name"`
	// Port for the DLNA HTTP server (TVs fetch the list and the videos here).
	Port int `json:"port"`
	// Interfaces to announce on; empty means every local network.
	Interfaces []string `json:"interfaces"`
	// Transcode offers converted versions of videos for TVs that can't play
	// the original. Needs ffmpeg and uses a lot of CPU.
	Transcode bool `json:"transcode"`
	// TrackProgress shares watched marks and resume points with TVs: they
	// see what was watched in Reelyard, and resume points TVs report are
	// saved to the Reelyard account.
	TrackProgress bool `json:"trackProgress"`
	// WatchedMark puts a check mark before watched titles, for TVs that
	// ignore the DLNA watched fields (most of them).
	WatchedMark bool `json:"watchedMark"`
	// TVs are the addresses of the TVs the main server shows on; empty
	// means every TV. (Each viewer's server has its own list.)
	TVs []string `json:"tvs"`
}

// DefaultSettings: on, named "Reelyard", on minidlna's usual port, sharing
// watched marks.
var DefaultSettings = Settings{Enabled: true, Name: "Reelyard", Port: 8200, Interfaces: []string{}, TrackProgress: true, TVs: []string{}}

// Validate checks the settings.
func (s Settings) Validate() error {
	switch {
	case strings.TrimSpace(s.Name) == "":
		return errors.New("give the server a name")
	case utf8.RuneCountInString(s.Name) > 64:
		return errors.New("the name can be at most 64 characters")
	case s.Port < 1024 || s.Port > 65535:
		return errors.New("the port must be between 1024 and 65535")
	}
	return nil
}

// LoadSettings reads the saved settings or the defaults.
func LoadSettings(ctx context.Context, st *store.Store) Settings {
	s := DefaultSettings
	_ = st.GetSetting(ctx, settingsKey, &s)
	if s.Interfaces == nil {
		s.Interfaces = []string{}
	}
	if s.TVs == nil {
		s.TVs = []string{}
	}
	return s
}

// SaveSettings validates and stores the settings.
func SaveSettings(ctx context.Context, st *store.Store, s Settings) (Settings, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Interfaces == nil {
		s.Interfaces = []string{}
	}
	s.TVs = CleanTVs(s.TVs)
	if err := s.Validate(); err != nil {
		return s, err
	}
	if err := st.SetSetting(ctx, settingsKey, s); err != nil {
		return s, fmt.Errorf("save dlna settings: %w", err)
	}
	return s, nil
}

// CleanTVs trims a TV address list and drops blanks and repeats.
func CleanTVs(tvs []string) []string {
	out := []string{}
	for _, t := range tvs {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
