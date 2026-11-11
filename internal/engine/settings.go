package engine

import (
	"context"
	"errors"
	"net"

	"golang.org/x/time/rate"

	"github.com/inluxc/harbor/internal/store"
)

const settingsKey = "engine"

const unlimitedBurst = 1 << 30

// Settings are the download options people change from the UI.
type Settings struct {
	MaxActive         int `json:"maxActive"`         // downloads running at once (seeding not counted); 0 = no limit
	DownloadLimitKBps int `json:"downloadLimitKBps"` // 0 = unlimited
	UploadLimitKBps   int `json:"uploadLimitKBps"`   // 0 = unlimited
	// Sharing limits for downloads not covered by a tracked title's
	// profile. Sharing stops at whichever comes first; 0 = no limit.
	SeedRatio float64 `json:"seedRatio"`
	SeedHours int     `json:"seedHours"`
	// StallMinutes fails a download that makes no progress for this long
	// (0 = never). Tracked titles then try another release.
	StallMinutes int `json:"stallMinutes"`
	// MinFreeGB keeps this much disk space free in the download folder.
	MinFreeGB int `json:"minFreeGB"`
}

// DefaultSettings are used until changed.
var DefaultSettings = Settings{MaxActive: 3, SeedRatio: 1, SeedHours: 48, StallMinutes: 30, MinFreeGB: 2}

// Validate checks ranges.
func (s Settings) Validate() error {
	if s.MaxActive < 0 || s.MaxActive > 50 {
		return errors.New("downloads at once must be between 0 (no limit) and 50")
	}
	if s.DownloadLimitKBps < 0 || s.UploadLimitKBps < 0 {
		return errors.New("speed limits can't be negative")
	}
	if s.SeedRatio < 0 || s.SeedHours < 0 || s.StallMinutes < 0 || s.MinFreeGB < 0 {
		return errors.New("sharing limits, stall time and free space can't be negative")
	}
	if s.StallMinutes > 0 && s.StallMinutes < 5 {
		return errors.New("give stalled downloads at least 5 minutes")
	}
	return nil
}

func loadSettings(ctx context.Context, st *store.Store) Settings {
	s := DefaultSettings
	_ = st.GetSetting(ctx, settingsKey, &s)
	return s
}

// Settings returns the current settings.
func (e *Engine) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// SetSettings saves and applies new settings immediately.
func (e *Engine) SetSettings(ctx context.Context, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := e.opts.Store.SetSetting(ctx, settingsKey, s); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = s
	e.applyLimits()
	e.mu.Unlock()
	e.kick()
	return nil
}

func (e *Engine) applyLimits() {
	set := func(l *rate.Limiter, kbps int) {
		if kbps <= 0 {
			// The torrent client opens no connections while the limiter
			// reports zero tokens, and an infinite limiter with burst 0
			// always reports zero; give it a large burst.
			l.SetBurst(unlimitedBurst)
			l.SetLimit(rate.Inf)
			return
		}
		bps := kbps * 1024
		burst := bps
		if burst < 256<<10 {
			burst = 256 << 10 // at least a few 16 KiB chunks
		}
		l.SetBurst(burst)
		l.SetLimit(rate.Limit(bps))
	}
	set(e.dl, e.settings.DownloadLimitKBps)
	set(e.ul, e.settings.UploadLimitKBps)
}

func resolvePeer(hostport string) (*net.TCPAddr, error) {
	return net.ResolveTCPAddr("tcp", hostport)
}
