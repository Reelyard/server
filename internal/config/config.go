// Package config loads Reelyard's startup configuration.
//
// Sources, lowest to highest priority:
//  1. built-in defaults (per-OS data directory)
//  2. YAML file (default: <data dir>/config.yaml, or --config)
//  3. environment variables (REELYARD_*)
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
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the config file name inside the data directory.
const FileName = "config.yaml"

// Config is Reelyard's startup configuration.
type Config struct {
	// Listen is the HTTP address for the web UI and API.
	Listen string `yaml:"listen"`
	// DataDir holds the database, logs and torrent state.
	DataDir string `yaml:"data_dir"`
	// DownloadDir holds incomplete and seeding torrent data.
	DownloadDir string `yaml:"download_dir"`
	// LibraryDir is the media library (Movies/, TV/) read by DLNA/Jellyfin/Plex.
	LibraryDir string `yaml:"library_dir"`
	// TorrentPort is where other peers connect (TCP and UDP). Forward it in
	// your router for better speeds. 0 picks a random free port.
	TorrentPort int `yaml:"torrent_port"`
	// BindInterface sends all torrent traffic through this network
	// interface only (e.g. your VPN's "wg0" or "tun0"). If it goes down,
	// torrents pause instead of using another connection.
	BindInterface string `yaml:"bind_interface,omitempty"`
	// Announce makes Reelyard findable by its apps on the local network
	// (multicast DNS, service _reelyard._tcp).
	Announce bool `yaml:"announce"`
	Log      Log  `yaml:"log"`

	// path is the file this config was read from ("" if none).
	path string
}

// Log controls logging output.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
	// File enables a rotating log file at <data dir>/logs/reelyard.log.
	File bool `yaml:"file"`
}

// Path returns the file the config was loaded from, or "".
func (c *Config) Path() string { return c.path }

// DBPath is the SQLite database file. Data folders from before the rename
// keep their harbor.db.
func (c *Config) DBPath() string {
	p := filepath.Join(c.DataDir, "reelyard.db")
	if legacy := filepath.Join(c.DataDir, "harbor.db"); !exists(p) && exists(legacy) {
		return legacy
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

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
		TorrentPort: 42069,
		Announce:    true,
		Log:         Log{Level: "info", Format: "text", File: true},
	}
}

// DefaultDataDir returns the per-OS data directory.
//
//	Linux:   /var/lib/reelyard (system) or $XDG_DATA_HOME/reelyard, ~/.local/share/reelyard
//	macOS:   /Library/Application Support/Reelyard (system) or ~/Library/Application Support/Reelyard
//	Windows: %ProgramData%\Reelyard
//
// Installs from before the rename (when Reelyard was called Harbor) keep
// using their folder: if the new one doesn't exist yet but the old one does,
// the old one is returned.
func DefaultDataDir(system bool) string {
	dir := dataDir(system, "Reelyard", "reelyard")
	if !exists(dir) {
		if old := dataDir(system, "Harbor", "harbor"); exists(old) {
			return old
		}
	}
	return dir
}

func dataDir(system bool, title, lower string) string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, title)
		}
		return filepath.Join(`C:\ProgramData`, title)
	case "darwin":
		if system {
			return filepath.Join("/Library/Application Support", title)
		}
		return filepath.Join(home, "Library", "Application Support", title)
	default:
		if system {
			return filepath.Join("/var/lib", lower)
		}
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, lower)
		}
		return filepath.Join(home, ".local", "share", lower)
	}
}

// Load builds the configuration. If path is empty, <data dir>/config.yaml is
// used when it exists. A missing default file is not an error; a missing
// explicit file is.
func Load(path string, system bool) (*Config, error) {
	cfg := Defaults(system)

	// The data dir can be moved by env before we look for the default file.
	if v := Getenv("DATA_DIR"); v != "" {
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
		if v := Getenv(key); v != "" {
			*dst = v
		}
	}
	set("LISTEN", &c.Listen)
	set("DOWNLOAD_DIR", &c.DownloadDir)
	set("LIBRARY_DIR", &c.LibraryDir)
	set("LOG_LEVEL", &c.Log.Level)
	set("LOG_FORMAT", &c.Log.Format)
	set("BIND_INTERFACE", &c.BindInterface)
	if v := Getenv("TORRENT_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.TorrentPort = n
		}
	}
	if v := Getenv("LOG_FILE"); v != "" {
		c.Log.File = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	if v := Getenv("ANNOUNCE"); v != "" {
		c.Announce = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
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
	if c.TorrentPort < 0 || c.TorrentPort > 65535 {
		return fmt.Errorf("torrent_port %d: want 0–65535", c.TorrentPort)
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

// FilePath is where settings are saved: the file the config was read from,
// or <data dir>/config.yaml.
func (c *Config) FilePath() string {
	if c.path != "" {
		return c.path
	}
	return filepath.Join(c.DataDir, FileName)
}

// EnvOverrides lists the REELYARD_* variables that set the download and
// library folders (a file change can't move those).
func EnvOverrides() []string {
	var out []string
	for _, k := range []string{"DOWNLOAD_DIR", "LIBRARY_DIR"} {
		for _, prefix := range []string{"REELYARD_", "HARBOR_"} {
			if os.Getenv(prefix+k) != "" {
				out = append(out, prefix+k)
				break
			}
		}
	}
	return out
}

// Getenv reads REELYARD_<name>, falling back to the pre-rename HARBOR_<name>.
func Getenv(name string) string {
	if v := os.Getenv("REELYARD_" + name); v != "" {
		return v
	}
	return os.Getenv("HARBOR_" + name)
}

// UpdateFile sets top-level keys in the YAML config file at path, keeping
// everything else (and comments) as it is. The file is created if missing.
func UpdateFile(path string, values map[string]string) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		return err
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s: top level is not a mapping", path)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := values[k]
		found := false
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == k {
				m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: v}
				found = true
			}
		}
		if !found {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
