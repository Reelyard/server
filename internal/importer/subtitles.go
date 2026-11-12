package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/stremio"
)

// ErrNotSubtitle means downloaded content isn't SRT or WebVTT.
var ErrNotSubtitle = errors.New("not a subtitle file")

var (
	vttTime  = regexp.MustCompile(`^((?:\d+:)?\d{1,2}:\d{2})[.,](\d{3})\s+-->\s+((?:\d+:)?\d{1,2}:\d{2})[.,](\d{3})`)
	tagStrip = regexp.MustCompile(`</?c[^>]*>|</?v[^>]*>|</?lang[^>]*>|</?ruby>|</?rt>`)
)

// ToSRT returns the subtitle as UTF-8 SRT with \n line endings. It accepts
// SRT or WebVTT, in UTF-8 (with or without BOM) or Windows-1252.
func ToSRT(b []byte) ([]byte, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) {
		dec, err := charmap.Windows1252.NewDecoder().Bytes(b)
		if err != nil {
			return nil, err
		}
		b = dec
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	if !strings.Contains(text, "-->") {
		return nil, ErrNotSubtitle
	}
	isVTT := strings.HasPrefix(strings.TrimSpace(text), "WEBVTT")

	// Re-emit cue by cue: number, normalized timing line, text.
	var out strings.Builder
	n := 0
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		ti := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				ti = i
				break
			}
		}
		if ti < 0 {
			continue // WEBVTT header, NOTE, STYLE, or junk
		}
		m := vttTime.FindStringSubmatch(strings.TrimSpace(lines[ti]))
		if m == nil {
			continue
		}
		body := lines[ti+1:]
		if isVTT {
			for i := range body {
				body[i] = tagStrip.ReplaceAllString(body[i], "")
			}
		}
		n++
		fmt.Fprintf(&out, "%d\n%s,%s --> %s,%s\n%s\n\n", n, srtClock(m[1]), m[2], srtClock(m[3]), m[4], strings.Join(body, "\n"))
	}
	if n == 0 {
		return nil, ErrNotSubtitle
	}
	return []byte(out.String()), nil
}

// srtClock pads "1:02" or "01:02" (mm:ss) or "1:02:03" to hh:mm:ss.
func srtClock(s string) string {
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		parts = append([]string{"0"}, parts...)
	}
	for i, p := range parts {
		v, _ := strconv.Atoi(p)
		parts[i] = fmt.Sprintf("%02d", v)
	}
	return strings.Join(parts, ":")
}

// candidate is a subtitle offered by a supplier.
type candidate struct {
	Supplier string
	Sub      stremio.Subtitle
	Lang     string // normalized
}

// matches says whether an offered language satisfies a wanted one:
// exact, or the regional variant of the same language (pt ↔ pt-BR,
// es ↔ es-419) as a fallback.
func matches(wanted, offered string) (exact, ok bool) {
	if wanted == offered {
		return true, true
	}
	base := func(l string) string { b, _, _ := strings.Cut(l, "-"); return b }
	return false, base(wanted) == base(offered)
}

// pick orders the candidates for one wanted language: exact matches
// first, then regional variants, each in supplier order.
func pick(wanted string, cands []candidate) []candidate {
	var exact, near []candidate
	for _, c := range cands {
		if e, ok := matches(wanted, c.Lang); ok {
			if e {
				exact = append(exact, c)
			} else {
				near = append(near, c)
			}
		}
	}
	return append(exact, near...)
}

// fetchSubtitle downloads one subtitle (at most 5 MiB) and converts it.
func fetchSubtitle(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	return ToSRT(b)
}

// normalizeLangs cleans a list of wanted languages.
func normalizeLangs(in []string) []string {
	var out []string
	for _, l := range in {
		if n := lang.Normalize(l); n != "" {
			out = lang.Merge(out, []string{n})
		}
	}
	return out
}

// ToVTT returns a subtitle (SRT or WebVTT, any encoding ToSRT reads) as
// WebVTT, the only format HTML video and TV web players take.
func ToVTT(b []byte) ([]byte, error) {
	srt, err := ToSRT(b)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, block := range strings.Split(strings.TrimRight(string(srt), "\n"), "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) < 2 {
			continue
		}
		// Drop the cue number; "00:00:01,000" becomes "00:00:01.000".
		out.WriteString(strings.ReplaceAll(lines[1], ",", "."))
		out.WriteByte('\n')
		for _, l := range lines[2:] {
			if l == "" {
				continue // a blank line would end the cue early
			}
			out.WriteString(strings.ReplaceAll(l, "-->", "->"))
			out.WriteByte('\n')
		}
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}
