package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/store"
)

func TestOnboarding(t *testing.T) {
	t.Setenv("REELYARD_DOWNLOAD_DIR", "")
	t.Setenv("REELYARD_LIBRARY_DIR", "")
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults(false)
	cfg.DataDir = dir
	cfg.DownloadDir = filepath.Join(dir, "downloads")
	cfg.LibraryDir = filepath.Join(dir, "library")
	var restarts atomic.Int32
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Restart: func() { restarts.Add(1) }}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}

	code, body := c.do("GET", "/api/v1/onboarding", "")
	if code != 200 || body["done"] != false || body["locked"] != "" || body["hardlinks"] != true || body["canRestart"] != true {
		t.Fatalf("status: %d %v", code, body)
	}
	if body["download"].(map[string]any)["free"].(float64) <= 0 {
		t.Fatalf("free space: %v", body["download"])
	}

	enc := func(dl, lib string) string {
		b, _ := json.Marshal(map[string]string{"download": dl, "library": lib})
		return string(b)
	}
	if code, body := c.do("PUT", "/api/v1/onboarding/folders", enc("relative/dir", cfg.LibraryDir)); code != 400 || !strings.Contains(body["error"].(string), "full path") {
		t.Fatalf("relative: %d %v", code, body)
	}
	media := filepath.Join(t.TempDir(), "media")
	if code, _ := c.do("PUT", "/api/v1/onboarding/folders", enc(media, filepath.Join(media, "lib"))); code != 400 {
		t.Fatalf("nested: %d", code)
	}
	// Unchanged folders: nothing written, no restart.
	if code, body := c.do("PUT", "/api/v1/onboarding/folders", enc(cfg.DownloadDir, cfg.LibraryDir)); code != 200 || body["changed"] != false {
		t.Fatalf("same: %d %v", code, body)
	}
	newDL, newLib := filepath.Join(media, "downloads"), filepath.Join(media, "library")
	code, body = c.do("PUT", "/api/v1/onboarding/folders", enc(newDL, newLib))
	if code != 200 || body["changed"] != true || body["restarting"] != true || body["hardlinks"] != true {
		t.Fatalf("change: %d %v", code, body)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil || !strings.Contains(string(b), "download_dir: "+newDL) {
		t.Fatalf("config file: %s %v", b, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for restarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Fatalf("restarts = %d", restarts.Load())
	}

	// Folders set by the environment (Docker) can't be changed here.
	t.Setenv("REELYARD_LIBRARY_DIR", "/data/library")
	if code, body := c.do("PUT", "/api/v1/onboarding/folders", enc(newDL, newLib)); code != 409 || !strings.Contains(body["error"].(string), "REELYARD_LIBRARY_DIR") {
		t.Fatalf("env locked: %d %v", code, body)
	}

	if code, _ := c.do("POST", "/api/v1/onboarding/done", ""); code != 204 {
		t.Fatalf("done: %d", code)
	}
	if _, body := c.do("GET", "/api/v1/onboarding", ""); body["done"] != true {
		t.Fatalf("after done: %v", body)
	}
}
