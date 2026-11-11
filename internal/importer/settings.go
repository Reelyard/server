package importer

import (
	"context"
	"fmt"

	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/store"
)

const settingsKey = "library"

// Settings are the library options people change from the UI.
type Settings struct {
	// SubtitleLangs are fetched for every imported file unless already
	// embedded or saved. Quality profiles (M4) will override this per title.
	SubtitleLangs []string `json:"subtitleLangs"`
}

// DefaultSettings fetch English subtitles.
var DefaultSettings = Settings{SubtitleLangs: []string{"en"}}

// LoadSettings reads the saved settings or the defaults.
func LoadSettings(ctx context.Context, st *store.Store) Settings {
	s := DefaultSettings
	_ = st.GetSetting(ctx, settingsKey, &s)
	if s.SubtitleLangs == nil {
		s.SubtitleLangs = []string{}
	}
	return s
}

// SaveSettings validates and stores settings, returning the cleaned copy.
func SaveSettings(ctx context.Context, st *store.Store, s Settings) (Settings, error) {
	clean := []string{}
	for _, l := range s.SubtitleLangs {
		n := lang.Normalize(l)
		if n == "" {
			return s, fmt.Errorf("unknown language %q", l)
		}
		clean = lang.Merge(clean, []string{n})
	}
	if len(clean) > 10 {
		return s, fmt.Errorf("pick at most 10 subtitle languages")
	}
	s.SubtitleLangs = clean
	return s, st.SetSetting(ctx, settingsKey, s)
}
