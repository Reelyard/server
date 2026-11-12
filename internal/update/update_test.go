package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reelyard/server/internal/store"
)

func TestVersions(t *testing.T) {
	newer := [][2]string{
		{"v0.1.0", "0.1.0-beta.2"},
		{"v0.1.0-beta.10", "v0.1.0-beta.2"},
		{"v0.2.0", "v0.1.9"},
		{"v1.0.0", "dev"},
		{"v0.1.0-beta.3", "v0.1.0-beta.2-18-gb15fd6d"},
		{"v0.1.0-rc.1", "v0.1.0-beta.9"},
	}
	for _, c := range newer {
		if !Newer(c[0], c[1]) {
			t.Errorf("%s should be newer than %s", c[0], c[1])
		}
		if !IsDev(c[1]) && Newer(c[1], c[0]) {
			t.Errorf("%s shouldn't be newer than %s", c[1], c[0])
		}
	}
	for _, c := range [][2]string{{"v0.1.0-beta.2", "0.1.0-beta.2"}, {"v0.1.0-beta.2", "v0.1.0-beta.2-18-gb15fd6d-dirty"}, {"junk", "v0.1.0"}} {
		if Newer(c[0], c[1]) {
			t.Errorf("%s shouldn't be newer than %s", c[0], c[1])
		}
	}
	for v, dev := range map[string]bool{"dev": true, "v0.1.0-beta.2-18-gb15fd6d": true, "v0.1.0-dirty": true, "0.1.0-beta.2": false, "v1.2.3": false} {
		if IsDev(v) != dev {
			t.Errorf("IsDev(%q) = %v", v, !dev)
		}
	}
	if !IsPrerelease("0.1.0-beta.2") || IsPrerelease("1.0.0") {
		t.Error("IsPrerelease")
	}
}

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	files      map[string][]byte
}

// fakeGitHub serves the releases API and the release files.
func fakeGitHub(t *testing.T, rels []fakeRelease) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/Reelyard/server/releases" {
			out := []map[string]any{}
			for _, rel := range rels {
				assets := []map[string]string{}
				for name := range rel.files {
					assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + "/dl/" + rel.tag + "/" + name})
				}
				out = append(out, map[string]any{"tag_name": rel.tag, "name": "Reelyard " + rel.tag, "html_url": "https://example.test/" + rel.tag,
					"body": "notes", "draft": rel.draft, "prerelease": rel.prerelease, "published_at": time.Now(), "assets": assets})
			}
			json.NewEncoder(w).Encode(out)
			return
		}
		for _, rel := range rels {
			for name, b := range rel.files {
				if r.URL.Path == "/dl/"+rel.tag+"/"+name {
					w.Write(b)
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// releaseFiles builds this system's archive holding binary, plus checksums.txt.
func releaseFiles(t *testing.T, tag string, binary []byte, badSum bool) map[string][]byte {
	t.Helper()
	name := ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	bin := "reelyard"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	var buf bytes.Buffer
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create(bin)
		f.Write(binary)
		zw.Close()
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
		tw.Write([]byte("hi"))
		tw.WriteHeader(&tar.Header{Name: bin, Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg})
		tw.Write(binary)
		tw.Close()
		gz.Close()
	}
	sum := sha256.Sum256(buf.Bytes())
	h := hex.EncodeToString(sum[:])
	if badSum {
		h = strings.Repeat("0", 64)
	}
	return map[string][]byte{name: buf.Bytes(), "checksums.txt": []byte(h + "  " + name + "\n")}
}

func newService(t *testing.T, gh *httptest.Server, current string, restart func()) (*Service, string) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	exe := filepath.Join(t.TempDir(), "reelyard")
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	no := false
	return New(Options{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), API: gh.URL, Current: current,
		Exe: exe, Restart: restart, InDocker: &no}), exe
}

func TestCheckChannels(t *testing.T) {
	gh := fakeGitHub(t, []fakeRelease{
		{tag: "v0.1.0", files: map[string][]byte{}},
		{tag: "v0.2.0-beta.1", prerelease: true},
		{tag: "v0.3.0", draft: true},
	})
	ctx := context.Background()
	s, _ := newService(t, gh, "0.1.0", func() {})
	if st := s.Status(ctx); st.Settings.Channel != "stable" || !st.Settings.Auto {
		t.Fatalf("defaults for a release: %+v", st.Settings)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx); st.Available || st.Latest.Version != "v0.1.0" {
		t.Fatalf("stable: %+v", st)
	}
	if err := s.SaveSettings(ctx, Settings{Channel: "beta", Auto: false}); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx); !st.Available || st.Latest.Version != "v0.2.0-beta.1" {
		t.Fatalf("beta (drafts ignored): %+v %+v", st, st.Latest)
	}

	b, _ := newService(t, gh, "0.1.0-beta.2", func() {})
	if b.Status(ctx).Settings.Channel != "beta" {
		t.Fatal("a beta build should follow the beta channel")
	}
}

func TestMethods(t *testing.T) {
	gh := fakeGitHub(t, nil)
	ctx := context.Background()
	if st := mustService(t, gh, "v0.1.0-beta.2-18-gb15fd6d", func() {}).Status(ctx); st.Method != MethodDev || st.CanApply {
		t.Fatalf("dev: %+v", st)
	}
	if st := mustService(t, gh, "0.1.0", nil).Status(ctx); st.Method != MethodBinary || st.CanApply || st.Reason == "" {
		t.Fatalf("not a service: %+v", st)
	}
	s := mustService(t, gh, "0.1.0", func() {})
	yes := true
	s.o.InDocker = &yes
	if st := s.Status(ctx); st.Method != MethodDocker || st.CanApply || !strings.Contains(st.Instructions, "docker compose pull") {
		t.Fatalf("docker: %+v", st)
	}
	s.o.InDocker = nil
	no := false
	s.o.InDocker = &no
	s.o.Exe = "/usr/bin/reelyard"
	if st := s.Status(ctx); st.Method != MethodPackage || st.CanApply {
		t.Fatalf("package: %+v", st)
	}
	if st := mustService(t, gh, "0.1.0", func() {}).Status(ctx); !st.CanApply {
		t.Fatalf("binary service: %+v", st)
	}
}

func mustService(t *testing.T, gh *httptest.Server, current string, restart func()) *Service {
	s, _ := newService(t, gh, current, restart)
	return s
}

func TestApply(t *testing.T) {
	gh := fakeGitHub(t, []fakeRelease{{tag: "v0.2.0", files: releaseFiles(t, "v0.2.0", []byte("new program"), false)}})
	ctx := context.Background()
	var restarted atomic.Bool
	s, exe := newService(t, gh, "0.1.0", func() { restarted.Store(true) })
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, restarted.Load)
	if b, _ := os.ReadFile(exe); string(b) != "new program" {
		t.Fatalf("binary not replaced: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old program" {
		t.Fatalf("old binary not kept: %q", b)
	}
	if st := s.Status(ctx); st.State != "restarting" {
		t.Fatalf("state %q", st.State)
	}
}

func TestApplyBadChecksum(t *testing.T) {
	gh := fakeGitHub(t, []fakeRelease{{tag: "v0.2.0", files: releaseFiles(t, "v0.2.0", []byte("evil"), true)}})
	ctx := context.Background()
	var restarted atomic.Bool
	s, exe := newService(t, gh, "0.1.0", func() { restarted.Store(true) })
	s.Check(ctx)
	if err := s.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Status(ctx).State == "failed" })
	if st := s.Status(ctx); !strings.Contains(st.StateError, "checksum") {
		t.Fatalf("error: %q", st.StateError)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old program" || restarted.Load() {
		t.Fatal("a bad download must not be installed")
	}
	// Automatic updates don't retry the same version.
	s.maybeAuto(ctx)
	if s.Status(ctx).State != "failed" {
		t.Fatal("auto retried a failed version")
	}
}

func TestAutoUpdate(t *testing.T) {
	gh := fakeGitHub(t, []fakeRelease{{tag: "v0.2.0", files: releaseFiles(t, "v0.2.0", []byte("new program"), false)}})
	ctx := context.Background()
	var restarted atomic.Bool
	s, _ := newService(t, gh, "0.1.0", func() { restarted.Store(true) })
	s.SaveSettings(ctx, Settings{Channel: "stable", Auto: false})
	s.Check(ctx)
	s.maybeAuto(ctx)
	time.Sleep(50 * time.Millisecond)
	if s.Status(ctx).State != "idle" {
		t.Fatal("auto off must not install")
	}
	s.SaveSettings(ctx, Settings{Channel: "stable", Auto: true})
	s.maybeAuto(ctx)
	waitFor(t, restarted.Load)
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 300 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
