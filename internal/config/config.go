// Package config loads Harbor's startup configuration.
//
// Sources, lowest to highest priority:
//  1. built-in defaults (per-OS data directory)
//  2. YAML file (default: <data dir>/config.yaml, or --config)
//  3. environment variables (HARBOR_*)
//
// Only settings needed before the database is open live here. Everything
// a user changes day to day (speed limits, schedules, DLNA, ...) is stored
// in the database and edited from the UI.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the config file name inside the data directory.
const FileName = "config.yaml"

// Config is Harbor's startup configuration.
type Config struct {
	// Listen is the HTTP address for the web UI and API.
	Listen string `yaml:"listen"`
	// DataDir holds the database, logs and torrent state.
	DataDir string `yaml:"data_dir"`
	// DownloadDir holds incomplete and seeding torrent data.
	DownloadDir string `yaml:"download_dir"`
	// LibraryDir is the media library (Movies/, TV/) read by DLNA/Jellyfin/Plex.
	LibraryDir string `yaml:"library_dir"`
	Log        Log    `yaml:"log"`

	// path is the file this config was read from ("" if none).
	path string
}

// Log controls logging output.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
	// File enables a rotating log file at <data dir>/logs/harbor.log.
	File bool `yaml:"file"`
}

// Path returns the file the config was loaded from, or "".
func (c *Config) Path() string { return c.path }

// DBPath is the SQLite database file.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "harbor.db") }

// LogDir is where rotating log files are written.
func (c *Config) LogDir() string { return filepath.Join(c.DataDir, "logs") }

// Defaults returns the built-in configuration for this OS.
// system selects the system-service locations instead of per-user ones.
func Defaults(system bool) Config {
	data := DefaultDataDir(system)
	return Config{
		Listen:      ":8787",
		DataDir:     data,
		DownloadDir: filepath.Join(data, "downloads"),
		LibraryDir:  filepath.Join(data, "library"),
		Log:         Log{Level: "info", Format: "text", File: true},
	}
}

// DefaultDataDir returns the per-OS data directory.
//
//	Linux:   /var/lib/harbor (system) or $XDG_DATA_HOME/harbor, ~/.local/share/harbor
//	macOS:   /Library/Application Support/Harbor (system) or ~/Library/Application Support/Harbor
//	Windows: %ProgramData%\Harbor
func DefaultDataDir(system bool) string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "Harbor")
		}
		return filepath.Join(`C:\ProgramData`, "Harbor")
	case "darwin":
		if system {
			return "/Library/Application Support/Harbor"
		}
		return filepath.Join(home, "Library", "Application Support", "Harbor")
	default:
		if system {
			return "/var/lib/harbor"
		}
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "harbor")
		}
		return filepath.Join(home, ".local", "share", "harbor")
	}
}

// Load builds the configuration. If path is empty, <data dir>/config.yaml is
// used when it exists. A missing default file is not an error; a missing
// explicit file is.
func Load(path string, system bool) (*Config, error) {
	cfg := Defaults(system)

	// The data dir can be moved by env before we look for the default file.
	if v := os.Getenv("HARBOR_DATA_DIR"); v != "" {
		cfg = rebase(cfg, v)
	}

	explicit := path != ""
	if !explicit {
		path = filepath.Join(cfg.DataDir, FileName)
	}
	if err := cfg.readFile(path); err != nil {
		if explicit || !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	cfg.applyEnv()
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) readFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// A file that sets data_dir but not the other dirs moves them along.
	var probe struct {
		DataDir string `yaml:"data_dir"`
	}
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if probe.DataDir != "" {
		*c = rebase(*c, probe.DataDir)
	}
	// yaml.v3 only overwrites fields present in the file.
	if err := yaml.Unmarshal(b, c); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	c.path = path
	return nil
}

// rebase moves DataDir and the dirs that default to living inside it.
func rebase(c Config, dataDir string) Config {
	old := c.DataDir
	c.DataDir = dataDir
	if c.DownloadDir == filepath.Join(old, "downloads") {
		c.DownloadDir = filepath.Join(dataDir, "downloads")
	}
	if c.LibraryDir == filepath.Join(old, "library") {
		c.LibraryDir = filepath.Join(dataDir, "library")
	}
	return c
}

func (c *Config) applyEnv() {
	set := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set("HARBOR_LISTEN", &c.Listen)
	set("HARBOR_DOWNLOAD_DIR", &c.DownloadDir)
	set("HARBOR_LIBRARY_DIR", &c.LibraryDir)
	set("HARBOR_LOG_LEVEL", &c.Log.Level)
	set("HARBOR_LOG_FORMAT", &c.Log.Format)
	if v := os.Getenv("HARBOR_LOG_FILE"); v != "" {
		c.Log.File = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
}

func (c *Config) normalize() error {
	for _, p := range []*string{&c.DataDir, &c.DownloadDir, &c.LibraryDir} {
		abs, err := filepath.Abs(expandHome(*p))
		if err != nil {
			return err
		}
		*p = abs
	}
	c.Log.Level = strings.ToLower(c.Log.Level)
	c.Log.Format = strings.ToLower(c.Log.Format)
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level %q: want debug, info, warn or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format %q: want text or json", c.Log.Format)
	}
	if c.Listen == "" {
		return errors.New("listen address is empty")
	}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// EnsureDirs creates the data, download, library and log directories.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.DownloadDir, c.LibraryDir, c.LogDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// Save writes the config as YAML to path (used by the service installer so
// the service and the CLI agree on locations).
func (c *Config) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
