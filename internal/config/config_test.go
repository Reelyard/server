package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REELYARD_DATA_DIR", dir)
	cfg, err := Load("", false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
	if want := filepath.Join(dir, "downloads"); cfg.DownloadDir != want {
		t.Errorf("DownloadDir = %q, want %q", cfg.DownloadDir, want)
	}
	if cfg.Listen != ":8787" || cfg.Log.Level != "info" || !cfg.Log.File {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.Path() != "" {
		t.Errorf("Path = %q, want empty", cfg.Path())
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	lib := filepath.Join(dir, "media")
	file := filepath.Join(dir, "reelyard.yaml")
	yaml := "data_dir: " + data + "\nlibrary_dir: " + lib + "\nlog:\n  file: false\n  format: json\n"
	if err := os.WriteFile(file, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REELYARD_LISTEN", "127.0.0.1:9000")

	cfg, err := Load(file, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != data {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if want := filepath.Join(data, "downloads"); cfg.DownloadDir != want {
		t.Errorf("DownloadDir should follow data_dir: got %q want %q", cfg.DownloadDir, want)
	}
	if cfg.LibraryDir != lib {
		t.Errorf("LibraryDir = %q", cfg.LibraryDir)
	}
	if cfg.Log.File {
		t.Error("log.file: false in file was ignored")
	}
	if cfg.Log.Format != "json" || cfg.Log.Level != "info" {
		t.Errorf("log = %+v", cfg.Log)
	}
	if cfg.Listen != "127.0.0.1:9000" {
		t.Errorf("env did not override listen: %q", cfg.Listen)
	}
	if cfg.Path() != file {
		t.Errorf("Path = %q", cfg.Path())
	}
}

func TestLoadMissingExplicitFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), false); err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

func TestLoadRejectsBadLevel(t *testing.T) {
	t.Setenv("REELYARD_DATA_DIR", t.TempDir())
	t.Setenv("REELYARD_LOG_LEVEL", "loud")
	if _, err := Load("", false); err == nil {
		t.Fatal("expected error for bad log level")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REELYARD_DATA_DIR", dir)
	cfg, err := Load("", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = ":9999"
	p := filepath.Join(dir, FileName)
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REELYARD_DATA_DIR", "")
	got, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != ":9999" || got.DataDir != dir {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestUpdateFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := UpdateFile(p, map[string]string{"library_dir": "/srv/lib"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# my settings\nlisten: \":9000\" # keep\nlibrary_dir: /old\nlog:\n  level: debug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, dl := filepath.Join(t.TempDir(), "media library"), filepath.Join(t.TempDir(), "dl")
	if err := UpdateFile(p, map[string]string{"library_dir": lib, "download_dir": dl}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# my settings", "# keep", `listen: ":9000"`, "level: debug"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	t.Setenv("REELYARD_DATA_DIR", filepath.Dir(p))
	cfg, err := Load("", false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LibraryDir != lib || cfg.DownloadDir != dl || cfg.Listen != ":9000" || cfg.Log.Level != "debug" {
		t.Fatalf("reloaded %+v", cfg)
	}
}

// Installs from before the rename keep their folder, database and
// HARBOR_* variables.
func TestLegacyHarborInstall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "harbor.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Config{DataDir: dir}
	if got := c.DBPath(); got != filepath.Join(dir, "harbor.db") {
		t.Fatalf("DBPath = %s, want the legacy harbor.db", got)
	}
	fresh := &Config{DataDir: t.TempDir()}
	if got := fresh.DBPath(); filepath.Base(got) != "reelyard.db" {
		t.Fatalf("fresh DBPath = %s", got)
	}

	t.Setenv("REELYARD_LISTEN", "")
	t.Setenv("HARBOR_LISTEN", "127.0.0.1:9999")
	if Getenv("LISTEN") != "127.0.0.1:9999" {
		t.Fatal("HARBOR_LISTEN not used as a fallback")
	}
	t.Setenv("REELYARD_LISTEN", "127.0.0.1:7777")
	if Getenv("LISTEN") != "127.0.0.1:7777" {
		t.Fatal("REELYARD_LISTEN should win")
	}
	t.Setenv("HARBOR_LIBRARY_DIR", "/x")
	t.Setenv("REELYARD_LIBRARY_DIR", "")
	if got := EnvOverrides(); len(got) != 1 || got[0] != "HARBOR_LIBRARY_DIR" {
		t.Fatalf("EnvOverrides = %v", got)
	}

	if runtime.GOOS != "linux" {
		return
	}
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := DefaultDataDir(false); got != filepath.Join(xdg, "reelyard") {
		t.Fatalf("no install: %s", got)
	}
	if err := os.Mkdir(filepath.Join(xdg, "harbor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultDataDir(false); got != filepath.Join(xdg, "harbor") {
		t.Fatalf("legacy install: %s", got)
	}
	if err := os.Mkdir(filepath.Join(xdg, "reelyard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultDataDir(false); got != filepath.Join(xdg, "reelyard") {
		t.Fatalf("both exist: %s", got)
	}
}
