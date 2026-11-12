package importer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/engine"
	"github.com/reelyard/server/internal/metadata"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

func TestAudioPlan(t *testing.T) {
	en, pt, und := AudioTrack{Lang: "en"}, AudioTrack{Lang: "pt"}, AudioTrack{}
	def := func(a AudioTrack) AudioTrack { a.Default = true; return a }
	ptBR := AudioTrack{Lang: "pt-BR"}
	cases := []struct {
		name   string
		tracks []AudioTrack
		prefs  []string
		pick   int
		want   bool
	}{
		{"one track", []AudioTrack{def(en)}, []string{"pt"}, -1, false},
		{"no preferences", []AudioTrack{def(en), pt}, nil, -1, false},
		{"preferred second", []AudioTrack{def(en), pt}, []string{"pt", "en"}, 1, true},
		{"preferred already first and default", []AudioTrack{def(pt), en}, []string{"pt", "en"}, -1, false},
		{"first, no defaults at all", []AudioTrack{pt, en}, []string{"pt"}, -1, false},
		{"first but another is default", []AudioTrack{pt, def(en)}, []string{"pt"}, 0, true},
		{"fall back to the second language", []AudioTrack{def(AudioTrack{Lang: "fr"}), en}, []string{"pt", "en"}, 1, true},
		{"none of the languages", []AudioTrack{def(en), AudioTrack{Lang: "fr"}}, []string{"pt"}, -1, false},
		{"regional variant matches", []AudioTrack{def(en), ptBR}, []string{"pt"}, 1, true},
		{"untagged never matches", []AudioTrack{def(en), und}, []string{"pt"}, -1, false},
		{"several of the language: the default one", []AudioTrack{AudioTrack{Lang: "fr"}, pt, def(pt)}, []string{"pt"}, 2, true},
		{"several of the language: else the first", []AudioTrack{def(en), pt, pt}, []string{"pt"}, 1, true},
	}
	for _, c := range cases {
		pick, want := audioPlan(c.tracks, c.prefs)
		if pick != c.pick || want != c.want {
			t.Errorf("%s: audioPlan = %d, %v; want %d, %v", c.name, pick, want, c.pick, c.want)
		}
	}
}

func TestRemuxArgs(t *testing.T) {
	pr := Probe{
		Streams:     []string{"video", "audio", "audio", "subtitle", "audio", "attachment"},
		AudioTracks: []AudioTrack{{Lang: "en", Default: true}, {Lang: "fr"}, {Lang: "pt"}},
	}
	got := remuxArgs("in.mkv", "out.mkv", pr, 2)
	want := []string{"-nostdin", "-v", "error", "-y", "-i", "in.mkv",
		"-map", "0:0", "-map", "0:4", "-map", "0:1", "-map", "0:2", "-map", "0:3", "-map", "0:5",
		"-c", "copy", "-max_muxing_queue_size", "9999", "-disposition:a:0", "default", "-disposition:a:1", "0", "out.mkv"}
	if !slices.Equal(got, want) {
		t.Fatalf("remuxArgs =\n%v\nwant\n%v", got, want)
	}
}

// makeVideo writes a short file with one audio track per language, the
// first one marked default.
func makeVideo(t *testing.T, p string, langs ...string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:duration=2:rate=5"}
	for i := range langs {
		args = append(args, "-f", "lavfi", "-i", "sine=duration=2:frequency="+[]string{"440", "660", "880"}[i%3])
	}
	args = append(args, "-map", "0:v")
	for i, l := range langs {
		args = append(args, "-map", itoa(i+1)+":a", "-metadata:s:a:"+itoa(i), "language="+l)
	}
	args = append(args, "-c:v", "mpeg4", "-c:a", "mp2", "-disposition:a:0", "default")
	for i := 1; i < len(langs); i++ {
		args = append(args, "-disposition:a:"+itoa(i), "0")
	}
	args = append(args, "-shortest", p)
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a test file here: %v %s", err, out)
	}
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestPlaceAudioFirst(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "a.mkv")
	os.MkdirAll(filepath.Dir(src), 0o755)
	makeVideo(t, src, "eng", "por", "fra")
	pr, _, err := ProbeFile(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(src)

	dst := filepath.Join(dir, "lib", "Movies", "A (2000)", "A (2000).mkv")
	method, lead, note, err := placeAudioFirst(ctx, src, dst, pr, []string{"pt", "en"})
	if err != nil || method != MethodRemux || lead != "pt" || note != "" {
		t.Fatalf("placeAudioFirst = %q, %q, %q, %v", method, lead, note, err)
	}
	got, _, _ := ProbeFile(ctx, dst)
	want := []AudioTrack{{Lang: "pt", Default: true}, {Lang: "en"}, {Lang: "fr"}}
	if !slices.Equal(got.AudioTracks, want) || got.VideoCodec != "mpeg4" {
		t.Fatalf("library file tracks = %+v (video %q)", got.AudioTracks, got.VideoCodec)
	}
	// The download is untouched (it may still be shared), and no temp files are left.
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Fatal("the download changed")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".reelyard-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	// Importing again accepts the earlier copy.
	if m, _, _, err := placeAudioFirst(ctx, src, dst, pr, []string{"pt", "en"}); err != nil || m != MethodRemux {
		t.Fatalf("again = %q, %v", m, err)
	}
	// A different language order no longer matches it: that's a different file.
	if _, _, _, err := placeAudioFirst(ctx, src, dst, pr, []string{"fr"}); err != ErrExists {
		t.Fatalf("other order err = %v", err)
	}

	// Nothing to change: a hardlink as before.
	dst2 := filepath.Join(dir, "lib", "B.mkv")
	if m, lead, _, err := placeAudioFirst(ctx, src, dst2, pr, []string{"en"}); err != nil || m != MethodHardlink || lead != "" {
		t.Fatalf("english first = %q, %q, %v", m, lead, err)
	}
	// An older hardlink is replaced by a reordered copy.
	if m, _, _, err := placeAudioFirst(ctx, src, dst2, pr, []string{"pt"}); err != nil || m != MethodRemux {
		t.Fatalf("replace hardlink = %q, %v", m, err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dst2)
	if os.SameFile(si, di) {
		t.Fatal("still a hardlink")
	}

	// A container that can't keep the order: hardlinked, unchanged.
	avi := filepath.Join(dir, "dl", "c.avi")
	os.Link(src, avi)
	if m, _, _, err := placeAudioFirst(ctx, avi, filepath.Join(dir, "lib", "C.avi"), pr, []string{"pt"}); err != nil || m != MethodHardlink {
		t.Fatalf("avi = %q, %v", m, err)
	}
}

func TestPlaceAudioFirstFallsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	makeVideo(t, src, "eng", "por")
	pr, _, _ := ProbeFile(ctx, src)
	// A probe that doesn't match the file makes ffmpeg fail: the file is
	// still imported, as a hardlink, with a note.
	pr.Streams = append(pr.Streams, "audio")
	pr.AudioTracks = append(pr.AudioTracks, AudioTrack{Lang: "pt"})
	dst := filepath.Join(dir, "lib", "A.mkv")
	m, lead, note, err := placeAudioFirst(ctx, src, dst, pr, []string{"pt"})
	if err != nil || m != MethodHardlink || lead != "" || note == "" {
		t.Fatalf("fallback = %q, %q, %q, %v", m, lead, note, err)
	}
}

func TestImportAudioFirst(t *testing.T) {
	ctx := context.Background()
	srv := addons(t)
	base := t.TempDir()
	dlDir, libDir := filepath.Join(base, "downloads"), filepath.Join(base, "library")
	src := filepath.Join(dlDir, "aaa", "Night.of.the.Living.Dead.1968.MULTI.mkv")
	os.MkdirAll(filepath.Dir(src), 0o755)
	makeVideo(t, src, "eng", "por")
	st, err := store.Open(ctx, filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client := stremio.New("test")
	meta := metadata.New(client)
	meta.Base = srv.URL + "/cinemeta"
	d, err := st.CreateDownload(ctx, store.NewDownload{InfoHash: strings.Repeat("a", 40), Name: "Night.of.the.Living.Dead.1968.MULTI"})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(src)
	fd := &fakeDownloads{d: map[int64]*engine.Status{d.ID: {Download: store.Download{ID: d.ID, State: store.StateSeeding,
		Name: d.Name, FilePath: "aaa/Night.of.the.Living.Dead.1968.MULTI.mkv", Size: fi.Size(), DoneBytes: fi.Size(),
		TitleType: "movie", TitleID: "tt0063350", TitleName: "Night of the Living Dead"}}}, states: map[int64]string{}, msgs: map[int64]string{}}
	im := New(Options{Store: st, Downloads: fd, Meta: meta, Suppliers: &suppliers.Service{Store: st, Client: client}, Client: client,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LibraryDir: libDir, DownloadDir: dlDir,
		AudioLangsFor: func(context.Context, string) ([]string, bool) { return []string{"pt", "en"}, true }})

	if err := im.Import(ctx, d.ID); err != nil || fd.states[d.ID] != store.ImportImported {
		t.Fatalf("import: %v %q %q", err, fd.states[d.ID], fd.msgs[d.ID])
	}
	files, _ := st.ListMediaFiles(ctx, "tt0063350")
	if len(files) != 1 || files[0].Method != MethodRemux || !slices.Equal(files[0].AudioLangs, []string{"pt", "en"}) {
		t.Fatalf("media rows: %+v", files)
	}
	acts, _ := st.ListActivity(ctx, 10)
	found := false
	for _, a := range acts {
		found = found || strings.Contains(a.Message, "copied with the Portuguese audio first")
	}
	if !found {
		t.Fatalf("no activity about the audio: %+v", acts)
	}

	// Switched off in Settings → Library: a plain hardlink.
	if _, err := SaveSettings(ctx, st, Settings{SubtitleLangs: []string{}, AudioFirst: false}); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(libDir)
	if err := im.Import(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	files, _ = st.ListMediaFiles(ctx, "tt0063350")
	if len(files) != 1 || files[0].Method != MethodHardlink {
		t.Fatalf("with the setting off: %+v", files)
	}
}
