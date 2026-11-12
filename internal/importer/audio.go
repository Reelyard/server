package importer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/reelyard/server/internal/lang"
)

// MethodRemux is a library copy whose audio tracks were reordered so the
// preferred language plays first.
const MethodRemux = "remux"

// remuxExts are containers whose default-track flag players respect.
var remuxExts = map[string]bool{".mkv": true, ".webm": true, ".mp4": true, ".m4v": true, ".mov": true}

// preferredTrack returns the audio track to play first: one in the first
// preferred language the file has (the one already marked default if
// there are several), or -1.
func preferredTrack(tracks []AudioTrack, prefs []string) int {
	for _, want := range prefs {
		want = lang.Normalize(want)
		found := -1
		for i, t := range tracks {
			if t.Lang == "" || !sameBase(want, t.Lang) {
				continue
			}
			if found < 0 || (t.Default && !tracks[found].Default) {
				found = i
			}
		}
		if found >= 0 {
			return found
		}
	}
	return -1
}

// audioPlan says which track to move to the front, if any. Nothing
// changes when there's one audio track, no track in a preferred
// language, or the preferred one already plays first.
func audioPlan(tracks []AudioTrack, prefs []string) (int, bool) {
	if len(tracks) < 2 {
		return -1, false
	}
	pick := preferredTrack(tracks, prefs)
	if pick < 0 {
		return -1, false
	}
	if pick == 0 {
		others := false
		for _, t := range tracks[1:] {
			others = others || t.Default
		}
		if tracks[0].Default || !others {
			return -1, false
		}
	}
	return pick, true
}

func sameBase(a, b string) bool {
	x, _, _ := strings.Cut(a, "-")
	y, _, _ := strings.Cut(b, "-")
	return x == y
}

// remuxArgs builds the ffmpeg arguments that copy every stream of src to
// out unchanged, except that audio track pick comes first among the audio
// tracks and is the only one marked default.
func remuxArgs(src, out string, pr Probe, pick int) []string {
	args := []string{"-nostdin", "-v", "error", "-y", "-i", src}
	var audio []int // global index of each audio stream
	for g, typ := range pr.Streams {
		if typ == "audio" {
			audio = append(audio, g)
		}
	}
	placed := false
	for g, typ := range pr.Streams {
		if typ == "audio" {
			if !placed {
				args = append(args, "-map", "0:"+strconv.Itoa(audio[pick]))
				placed = true
			}
			if g == audio[pick] {
				continue
			}
		}
		args = append(args, "-map", "0:"+strconv.Itoa(g))
	}
	args = append(args, "-c", "copy", "-max_muxing_queue_size", "9999", "-disposition:a:0", "default")
	// Output audio order: pick, then the rest in their old order. Clear
	// the default flag on the others, leaving their other flags alone.
	n := 1
	for i, t := range pr.AudioTracks {
		if i == pick {
			continue
		}
		if t.Default {
			args = append(args, "-disposition:a:"+strconv.Itoa(n), "0")
		}
		n++
	}
	return append(args, out)
}

// remuxAudioFirst writes dst as a copy of src with track pick first and
// default. A half-written file never appears under dst.
func remuxAudioFirst(ctx context.Context, ffmpeg, src, dst string, pr Probe, pick int) error {
	if len(pr.Streams) == 0 || pick < 0 || pick >= len(pr.AudioTracks) {
		return fmt.Errorf("no audio track %d", pick)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".reelyard-remux-*"+filepath.Ext(dst))
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, remuxArgs(src, tmp.Name(), pr, pick)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", msg)
	}
	// Check the result before it replaces anything.
	got, ok, err := ProbeFile(ctx, tmp.Name())
	if ok && err == nil {
		if len(got.AudioTracks) != len(pr.AudioTracks) || len(got.Streams) != len(pr.Streams) {
			return fmt.Errorf("the rewritten file has %d streams instead of %d", len(got.Streams), len(pr.Streams))
		}
		if d := got.DurationSec - pr.DurationSec; d > 2 || d < -2 {
			return fmt.Errorf("the rewritten file is %ds long instead of %ds", got.DurationSec, pr.DurationSec)
		}
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), dst)
}

// placeAudioFirst puts src at dst like Place, but when the file has
// several audio tracks and the preferred language isn't the one that
// plays first, it writes a copy with that track first instead of a
// hardlink. lead is the language now playing first ("" when nothing was
// reordered); note explains a reorder that was wanted but failed.
func placeAudioFirst(ctx context.Context, src, dst string, pr Probe, prefs []string) (method, lead, note string, err error) {
	pick, want := audioPlan(pr.AudioTracks, prefs)
	ffmpeg, ferr := exec.LookPath("ffmpeg")
	if !want || ferr != nil || !remuxExts[strings.ToLower(filepath.Ext(dst))] {
		method, err = Place(src, dst)
		return method, "", "", err
	}
	lead = pr.AudioTracks[pick].Lang
	if di, serr := os.Stat(dst); serr == nil {
		si, err := os.Stat(src)
		if err != nil || !os.SameFile(si, di) {
			// An earlier reordered copy of this file is fine as it is.
			if got, ok, perr := ProbeFile(ctx, dst); ok && perr == nil && len(got.AudioTracks) == len(pr.AudioTracks) {
				if _, again := audioPlan(got.AudioTracks, prefs); !again && abs(got.DurationSec-pr.DurationSec) <= 2 {
					return MethodRemux, lead, "", nil
				}
			}
			method, err = Place(src, dst)
			return method, "", "", err
		}
		// dst is a hardlink from before: replace it with a reordered copy.
	}
	if rerr := remuxAudioFirst(ctx, ffmpeg, src, dst, pr, pick); rerr != nil {
		if ctx.Err() != nil {
			return "", "", "", ctx.Err()
		}
		method, err = Place(src, dst)
		return method, "", fmt.Sprintf("couldn't put the %s audio first (%v)", lang.Name(lead), rerr), err
	}
	return MethodRemux, lead, "", nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
