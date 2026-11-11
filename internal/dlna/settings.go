package dlna

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/inluxc/harbor/internal/store"
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
}

// DefaultSettings: on, named "Harbor", on minidlna's usual port.
var DefaultSettings = Settings{Enabled: true, Name: "Harbor", Port: 8200, Interfaces: []string{}}

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
	return s
}

// SaveSettings validates and stores the settings.
func SaveSettings(ctx context.Context, st *store.Store, s Settings) (Settings, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Interfaces == nil {
		s.Interfaces = []string{}
	}
	if err := s.Validate(); err != nil {
		return s, err
	}
	if err := st.SetSetting(ctx, settingsKey, s); err != nil {
		return s, fmt.Errorf("save dlna settings: %w", err)
	}
	return s, nil
}
