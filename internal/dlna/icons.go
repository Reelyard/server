package dlna

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/anacrolix/dms/dlna/dms"
)

// Reelyard's mark: a white film reel on the deep blue of the web UI.
var (
	iconBG  = color.RGBA{0x1f, 0x4b, 0x6e, 0xff}
	iconFG  = color.RGBA{0xff, 0xff, 0xff, 0xff}
	iconHub = color.RGBA{0xd9, 0x48, 0x1c, 0xff}
)

func drawIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// A film reel on a 32-unit grid, like the web UI's logo.
	type disc struct{ x, y, r float64 }
	var holes []disc
	for _, deg := range []float64{-90, -18, 54, 126, 198} {
		a := deg * math.Pi / 180
		holes = append(holes, disc{16 + 5*math.Cos(a), 16 + 5*math.Sin(a), 2.1})
	}
	in := func(d disc, x, y float64) bool { return (x-d.x)*(x-d.x)+(y-d.y)*(y-d.y) <= d.r*d.r }
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x := (float64(px) + 0.5) * 32 / float64(size)
			y := (float64(py) + 0.5) * 32 / float64(size)
			c := iconBG
			if in(disc{16, 16, 10}, x, y) {
				c = iconFG
				for _, h := range holes {
					if in(h, x, y) {
						c = iconBG
					}
				}
				if in(disc{16, 16, 1.7}, x, y) {
					c = iconHub
				}
			}
			img.SetRGBA(px, py, c)
		}
	}
	return img
}

// deviceIcons are the logos TVs show next to the server name.
var deviceIcons = sync.OnceValue(func() []dms.Icon {
	var out []dms.Icon
	for _, size := range []int{120, 48} {
		img := drawIcon(size)
		var p, j bytes.Buffer
		_ = png.Encode(&p, img)
		_ = jpeg.Encode(&j, img, &jpeg.Options{Quality: 90})
		out = append(out,
			dms.Icon{Width: size, Height: size, Depth: 24, Mimetype: "image/png", Bytes: p.Bytes()},
			dms.Icon{Width: size, Height: size, Depth: 24, Mimetype: "image/jpeg", Bytes: j.Bytes()},
		)
	}
	return out
})

// thumbs makes and caches video thumbnails with ffmpeg.
type thumbs struct {
	ffmpeg string // "" = not installed
	sem    chan struct{}
	mu     sync.Mutex
	cache  map[string][]byte
	order  []string
}

const thumbCacheSize = 300

func newThumbs(ffmpeg string) *thumbs {
	return &thumbs{ffmpeg: ffmpeg, sem: make(chan struct{}, 2), cache: map[string][]byte{}}
}

// serve answers /icon?path=<video>: a frame from the video, or Reelyard's icon.
func (t *thumbs) serve(w http.ResponseWriter, r *http.Request, root *os.Root, libraryDir string) {
	rel := cleanRel(r.URL.Query().Get("path"))
	var body []byte
	if fi, err := root.Stat(rel); rel != "" && err == nil && !fi.IsDir() {
		key := rel + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		body = t.get(r.Context(), key, filepath.Join(libraryDir, filepath.FromSlash(rel)))
	}
	if body == nil {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(deviceIcons()[1].Bytes) // 120px JPEG
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(body)
}

func (t *thumbs) get(ctx context.Context, key, file string) []byte {
	t.mu.Lock()
	if b, ok := t.cache[key]; ok {
		t.mu.Unlock()
		return b
	}
	t.mu.Unlock()
	if t.ffmpeg == "" {
		return nil
	}
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var b []byte
	for _, at := range []string{"120", "0"} { // skip opening credits; short clips fall back to the start
		out, err := exec.CommandContext(ctx, t.ffmpeg, "-nostdin", "-loglevel", "error", "-ss", at, "-i", file,
			"-frames:v", "1", "-vf", "scale=320:-2", "-f", "image2", "-c:v", "mjpeg", "pipe:1").Output()
		if err == nil && len(out) > 0 {
			b = out
			break
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	if b == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.cache[key]; !ok {
		t.cache[key] = b
		t.order = append(t.order, key)
		if len(t.order) > thumbCacheSize {
			delete(t.cache, t.order[0])
			t.order = t.order[1:]
		}
	}
	return b
}
