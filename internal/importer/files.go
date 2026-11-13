package importer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/reelyard/server/internal/diskfree"
	"github.com/reelyard/server/internal/lang"
)

// Placement methods.
const (
	MethodHardlink = "hardlink"
	MethodCopy     = "copy"
)

// ErrExists means a different file already sits at the library path.
var ErrExists = errors.New("a different file already has this name in the library")

// ErrNoRoom means a copy into the library wouldn't fit on its disk.
var ErrNoRoom = errors.New("not enough free disk space in the library folder")

// copyHeadroom is kept free on the library's disk by copies and remuxes
// (hardlinks take no space).
const copyHeadroom = 1 << 30

// freeSpace reports free bytes on the disk holding a path (tests override).
var freeSpace = diskfree.Free

// roomForCopy checks that a need-byte file written into dir leaves
// copyHeadroom free. Unknown free space doesn't block.
func roomForCopy(dir string, need int64) error {
	// The folder may not exist yet: ask about the nearest one that does.
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	free, err := freeSpace(dir)
	if err != nil || need <= 0 {
		return nil
	}
	if uint64(need)+copyHeadroom > free {
		return fmt.Errorf("%w: %.1f GB free, the copy needs %.1f GB plus 1 GB kept free",
			ErrNoRoom, float64(free)/(1<<30), float64(need)/(1<<30))
	}
	return nil
}

// Place puts src at dst: a hardlink when both are on the same disk (no
// extra space, and seeding keeps working), else a copy. An existing dst
// that is already the same file is fine.
func Place(src, dst string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if di, err := os.Stat(dst); err == nil {
		si, serr := os.Stat(src)
		if serr == nil && os.SameFile(si, di) {
			return MethodHardlink, nil
		}
		if serr == nil && si.Size() == di.Size() && sameStart(src, dst) {
			return MethodCopy, nil // an earlier copy
		}
		return "", ErrExists
	}
	if err := os.Link(src, dst); err == nil {
		return MethodHardlink, nil
	}
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return MethodCopy, nil
}

func sameStart(a, b string) bool {
	ha, err1 := headBytes(a)
	hb, err2 := headBytes(b)
	return err1 == nil && err2 == nil && string(ha) == string(hb)
}

func headBytes(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, 64<<10)
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return b[:n], nil
}

// copyFile copies through a temporary file so a half-written copy never
// appears under the final name.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err == nil {
		if err := roomForCopy(filepath.Dir(dst), fi.Size()); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".reelyard-copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), dst)
}

// OSHash is the OpenSubtitles hash: file size plus the 64-bit little-endian
// sums of the first and last 64 KiB, as 16 hex digits.
func OSHash(p string) (string, error) {
	const chunk = 64 << 10
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	if size < chunk {
		return "", fmt.Errorf("file too small to hash (%d bytes)", size)
	}
	h := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := f.ReadAt(buf, off); err != nil {
			return "", err
		}
		for i := 0; i < chunk; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

// Probe is what ffprobe says about a video file.
type Probe struct {
	Resolution   string
	VideoCodec   string
	DurationSec  int
	AudioLangs   []string
	EmbeddedSubs []string
	// AudioCodecs lists the audio streams' codecs (ffprobe names, e.g.
	// "aac", "eac3"), never nil after a successful probe.
	AudioCodecs []string
	// AudioTracks are the audio streams in file order.
	AudioTracks []AudioTrack
	// Streams are the codec types of every stream in file order
	// ("video", "audio", "subtitle", "attachment", "data").
	Streams []string
}

// AudioTrack is one audio stream.
type AudioTrack struct {
	Lang    string // normalized; "" when untagged
	Default bool
}

// ProbeFile runs ffprobe if it is installed. ok is false when it isn't.
func ProbeFile(ctx context.Context, p string) (pr Probe, ok bool, err error) {
	bin, lerr := exec.LookPath("ffprobe")
	if lerr != nil {
		return pr, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", p).Output()
	if err != nil {
		return pr, true, fmt.Errorf("ffprobe: %w", err)
	}
	var body struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Tags      struct {
				Language string `json:"language"`
			} `json:"tags"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
				Default     int `json:"default"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return pr, true, fmt.Errorf("ffprobe output: %w", err)
	}
	pr.AudioCodecs = []string{}
	for _, s := range body.Streams {
		l := lang.Normalize(s.Tags.Language)
		pr.Streams = append(pr.Streams, s.CodecType)
		switch s.CodecType {
		case "video":
			if pr.VideoCodec == "" && s.Disposition.AttachedPic == 0 {
				pr.VideoCodec = s.CodecName
				pr.Resolution = resolutionFor(s.Width, s.Height)
			}
		case "audio":
			if s.CodecName != "" && !slices.Contains(pr.AudioCodecs, s.CodecName) {
				pr.AudioCodecs = append(pr.AudioCodecs, s.CodecName)
			}
			if l != "" {
				pr.AudioLangs = lang.Merge(pr.AudioLangs, []string{l})
			}
			pr.AudioTracks = append(pr.AudioTracks, AudioTrack{Lang: l, Default: s.Disposition.Default == 1})
		case "subtitle":
			if l != "" {
				pr.EmbeddedSubs = lang.Merge(pr.EmbeddedSubs, []string{l})
			}
		}
	}
	if d, err := strconv.ParseFloat(body.Format.Duration, 64); err == nil {
		pr.DurationSec = int(d)
	}
	return pr, true, nil
}

// resolutionFor names a frame size by its width as well as height, so
// letterboxed films (1920×800) still count as 1080p.
func resolutionFor(w, h int) string {
	switch {
	case w >= 3200 || h >= 1800:
		return "2160p"
	case w >= 1700 || h >= 1000:
		return "1080p"
	case w >= 1200 || h >= 700:
		return "720p"
	case h >= 560:
		return "576p"
	case h >= 400:
		return "480p"
	case h > 0:
		return fmt.Sprintf("%dp", h)
	}
	return ""
}
