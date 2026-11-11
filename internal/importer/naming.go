package importer

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Library layout, following Jellyfin/Plex/Emby conventions:
//
//	Movies/Title (Year)/Title (Year) - 1080p.mkv
//	TV/Show (Year)/Season 01/Show (Year) - S01E02 - Episode Title - 1080p.mkv
//
// Paths here use '/' and are relative to the library folder.

// MoviePath returns the library path for a movie file.
func MoviePath(name string, year int, resolution, ext string) string {
	folder := withYear(SafeName(name), year)
	file := folder
	if resolution != "" {
		file += " - " + resolution
	}
	return path.Join("Movies", folder, file+cleanExt(ext))
}

// EpisodePath returns the library path for an episode file.
func EpisodePath(show string, year, season, episode int, episodeTitle, resolution, ext string) string {
	folder := withYear(SafeName(show), year)
	file := fmt.Sprintf("%s - S%02dE%02d", folder, season, episode)
	if t := SafeName(episodeTitle); t != "" && !genericEpisodeTitle(t, episode) {
		file += " - " + t
	}
	if resolution != "" {
		file += " - " + resolution
	}
	return path.Join("TV", folder, fmt.Sprintf("Season %02d", season), trimBytes(file, 200)+cleanExt(ext))
}

// SubtitlePath returns the path of a subtitle next to a video:
// "Movie (1968) - 720p.mp4" → "Movie (1968) - 720p.pt-BR.srt".
func SubtitlePath(videoPath, lang string) string {
	return strings.TrimSuffix(videoPath, path.Ext(videoPath)) + "." + lang + ".srt"
}

func withYear(name string, year int) string {
	if name == "" {
		name = "Unknown"
	}
	if year > 0 {
		return fmt.Sprintf("%s (%d)", name, year)
	}
	return name
}

// "Episode 3" adds nothing to "S01E03".
func genericEpisodeTitle(t string, episode int) bool {
	return strings.EqualFold(t, fmt.Sprintf("Episode %d", episode)) || strings.EqualFold(t, fmt.Sprintf("Episode %02d", episode))
}

func cleanExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeName makes one path component safe on Windows, macOS and Linux:
// no reserved characters or control characters, no trailing dots or
// spaces, not a reserved device name, at most 150 bytes.
func SafeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ':':
			b.WriteString(" -") // "Star Wars: Episode IV" → "Star Wars - Episode IV"
		case strings.ContainsRune(`<>"/\|?*`, r):
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimRight(out, ". ")
	out = trimBytes(out, 150)
	out = strings.TrimRight(out, ". ")
	if reserved[strings.ToUpper(strings.SplitN(out, ".", 2)[0])] {
		out += "_"
	}
	return out
}

// trimBytes shortens s to at most n bytes without splitting a character.
func trimBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// YearFrom reads the first year from Cinemeta's year/releaseInfo
// ("2008–2013", "1968").
func YearFrom(s string) int {
	for i := 0; i+4 <= len(s); i++ {
		y := s[i : i+4]
		ok := true
		for _, c := range y {
			if c < '0' || c > '9' {
				ok = false
				break
			}
		}
		if ok && (y[0] == '1' || y[0] == '2') {
			n := 0
			fmt.Sscanf(y, "%d", &n)
			return n
		}
	}
	return 0
}
