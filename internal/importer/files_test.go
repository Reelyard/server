package importer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPlace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "a.mkv")
	os.MkdirAll(filepath.Dir(src), 0o755)
	data := bytes.Repeat([]byte("reelyard"), 50000)
	os.WriteFile(src, data, 0o644)

	dst := filepath.Join(dir, "lib", "Movies", "A (2000)", "A (2000).mkv")
	m, err := Place(src, dst)
	if err != nil || m != MethodHardlink {
		t.Fatalf("Place = %q, %v", m, err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dst)
	if !os.SameFile(si, di) {
		t.Fatal("not a hardlink")
	}
	// Again: already in place.
	if m, err := Place(src, dst); err != nil || m != MethodHardlink {
		t.Fatalf("second Place = %q, %v", m, err)
	}
	// A different file at the destination is refused.
	other := filepath.Join(dir, "dl", "b.mkv")
	os.WriteFile(other, []byte("something else"), 0o644)
	if _, err := Place(other, dst); err != ErrExists {
		t.Fatalf("conflict err = %v", err)
	}

	// copyFile writes the same bytes, through a temp file.
	cp := filepath.Join(dir, "copy", "c.mkv")
	os.MkdirAll(filepath.Dir(cp), 0o755)
	if err := copyFile(src, cp); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cp); !bytes.Equal(got, data) {
		t.Fatal("copy differs")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "copy", ".reelyard-copy-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestOSHash(t *testing.T) {
	// Reference: a file of 131072 zero bytes hashes to its size.
	p := filepath.Join(t.TempDir(), "z")
	os.WriteFile(p, make([]byte, 131072), 0o644)
	if h, err := OSHash(p); err != nil || h != "0000000000020000" {
		t.Fatalf("OSHash = %s, %v", h, err)
	}
	// One byte at the start adds its value.
	b := make([]byte, 131072)
	b[0] = 1
	os.WriteFile(p, b, 0o644)
	if h, _ := OSHash(p); h != "0000000000020001" {
		t.Fatalf("OSHash = %s", h)
	}
	os.WriteFile(p, []byte("tiny"), 0o644)
	if _, err := OSHash(p); err == nil {
		t.Fatal("tiny file hashed")
	}
}

func TestResolutionFor(t *testing.T) {
	cases := map[[2]int]string{{1920, 800}: "1080p", {1280, 932}: "720p", {3840, 1600}: "2160p", {720, 576}: "576p", {640, 480}: "480p"}
	for wh, want := range cases {
		if got := resolutionFor(wh[0], wh[1]); got != want {
			t.Errorf("%v → %s, want %s", wh, got, want)
		}
	}
}

func TestProbeFile(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	p := filepath.Join(t.TempDir(), "t.mkv")
	cmd := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=1280x720:duration=1:rate=5",
		"-f", "lavfi", "-i", "sine=duration=1", "-c:v", "mpeg4", "-c:a", "mp2", "-metadata:s:a:0", "language=por", "-shortest", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a test file here: %v %s", err, out)
	}
	pr, ok, err := ProbeFile(context.Background(), p)
	if !ok || err != nil {
		t.Fatalf("ProbeFile ok=%v err=%v", ok, err)
	}
	if pr.Resolution != "720p" || pr.VideoCodec != "mpeg4" || len(pr.AudioLangs) != 1 || pr.AudioLangs[0] != "pt" {
		t.Fatalf("probe = %+v", pr)
	}
}

func TestCopyNeedsRoom(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	os.WriteFile(src, bytes.Repeat([]byte("x"), 1<<20), 0o644)
	old := freeSpace
	t.Cleanup(func() { freeSpace = old })

	freeSpace = func(string) (uint64, error) { return copyHeadroom + 512<<10, nil } // less than the file plus headroom
	dst := filepath.Join(dir, "lib", "a.mkv")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := copyFile(src, dst); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("copy without room: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("partial copy left behind")
	}
	// A folder that doesn't exist yet is checked on its nearest parent.
	var asked string
	freeSpace = func(p string) (uint64, error) { asked = p; return 1 << 40, nil }
	if err := roomForCopy(filepath.Join(dir, "lib", "new", "deeper"), 1<<20); err != nil || asked != filepath.Join(dir, "lib") {
		t.Fatalf("roomForCopy: %v, asked %q", err, asked)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy with room: %v", err)
	}
}
