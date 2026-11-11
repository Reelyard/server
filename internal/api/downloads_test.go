package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/engine"
	"github.com/reelyard/server/internal/events"
	"github.com/reelyard/server/internal/store"
)

func newDownloadServer(t *testing.T) *client {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.New()
	eng, err := engine.New(ctx, engine.Options{Store: st, Bus: bus, Log: log, DataDir: dir, DownloadDir: filepath.Join(dir, "dl"),
		NoDHT: true, NoDefaultTrackers: true, Tick: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	cfg := config.Defaults(false)
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: log, Engine: eng, Bus: bus}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	return c
}

func TestDownloadsAPI(t *testing.T) {
	c := newDownloadServer(t)
	ih := "3333333333333333333333333333333333333333"

	if code, _ := c.do("POST", "/api/v1/downloads", `{"infoHash":"xyz"}`); code != 400 {
		t.Fatalf("bad hash: %d", code)
	}
	code, body := c.do("POST", "/api/v1/downloads", `{"infoHash":"`+ih+`","fileIdx":2,"name":"Show S01E01","sources":["tracker:udp://t.example:1/announce"],"titleType":"series","titleId":"tt1:1:1","titleName":"Show"}`)
	if code != 201 {
		t.Fatalf("add: %d %v", code, body)
	}
	d := body["download"].(map[string]any)
	id := fmt.Sprint(d["id"])
	if d["fileIdx"].(float64) != 2 || d["titleId"] != "tt1:1:1" || fmt.Sprint(d["sources"]) != "[udp://t.example:1/announce]" {
		t.Fatalf("download: %v", d)
	}
	if code, _ := c.do("POST", "/api/v1/downloads", `{"infoHash":"`+ih+`","fileIdx":2}`); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}

	// Live stream sends the list straight away.
	req, _ := http.NewRequest("GET", c.srv.URL+"/api/v1/stream", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.c.Do(req.WithContext(ctx))
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %v %v", resp, err)
	}
	r := bufio.NewReader(resp.Body)
	line, _ := r.ReadString('\n')
	if line != "event: downloads\n" {
		t.Fatalf("first stream line %q", line)
	}
	data, _ := r.ReadString('\n')
	if !strings.Contains(data, ih) {
		t.Fatalf("stream data %q", data)
	}
	cancel()
	resp.Body.Close()

	if code, body := c.do("POST", "/api/v1/downloads/"+id+"/pause", ""); code != 200 || body["download"].(map[string]any)["state"] != "paused" {
		t.Fatalf("pause: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/downloads/"+id+"/resume", ""); code != 200 || body["download"].(map[string]any)["state"] != "queued" {
		t.Fatalf("resume: %d %v", code, body)
	}
	_, body = c.do("GET", "/api/v1/downloads", "")
	if n := len(body["downloads"].([]any)); n != 1 {
		t.Fatalf("list: %d", n)
	}

	if code, _ := c.do("PUT", "/api/v1/settings/downloads", `{"maxActive":-3,"downloadLimitKBps":0,"uploadLimitKBps":0}`); code != 400 {
		t.Fatalf("bad settings: %d", code)
	}
	code, body = c.do("PUT", "/api/v1/settings/downloads", `{"maxActive":2,"downloadLimitKBps":500,"uploadLimitKBps":100}`)
	if code != 200 || body["settings"].(map[string]any)["uploadLimitKBps"].(float64) != 100 {
		t.Fatalf("settings: %d %v", code, body)
	}

	if code, _ := c.do("DELETE", "/api/v1/downloads/"+id+"?deleteData=true", ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/downloads/"+id, ""); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}
