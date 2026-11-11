package release

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/reelyard/server/internal/lang"
)

// Stream is what Reelyard knows about one addon stream after parsing.
type Stream struct {
	TorrentName string `json:"torrentName"`
	FilePath    string `json:"filePath,omitempty"`
	Info        Info   `json:"info"`            // torrent name and file name combined
	Seeders     int    `json:"seeders"`         // -1 when the addon doesn't say
	Size        int64  `json:"size,omitempty"`  // bytes of the file (or torrent) as reported
	Site        string `json:"site,omitempty"`  // tracker/indexer the addon found it on
	Label       string `json:"label,omitempty"` // the addon's short quality label ("4k HDR")
}

var (
	seedRe = regexp.MustCompile(`(?:👤|🌱|👥|⬆️|S:|Seeders?:)\s*(\d+)`)
	sizeRe = regexp.MustCompile(`(?i)(?:💾|📦|Size:)\s*([\d.,]+)\s*([KMGT]i?B)`)
	siteRe = regexp.MustCompile(`(?:⚙️|⚙|🔍|🔗|🌐|Source:)\s*([^\n👤💾🇦-🇿/|]+)`)
	// Any line built from these markers is metadata, not a name.
	metaLine = regexp.MustCompile(`👤|💾|⚙|🔍|🌱|📦|🔗|🌐`)
)

// ParseStream reads an addon stream: name ("Torrentio\n4k HDR"), the
// descriptive text (title/description, several lines), and the filename
// hint from behaviorHints.
func ParseStream(name, text, filenameHint string, videoSize int64) Stream {
	out := Stream{Seeders: -1}
	if _, label, ok := strings.Cut(name, "\n"); ok {
		out.Label = strings.TrimSpace(label)
	}

	var nameLines []string
	var flags []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case metaLine.MatchString(line):
			if m := seedRe.FindStringSubmatch(line); m != nil {
				out.Seeders, _ = strconv.Atoi(m[1])
			}
			if m := sizeRe.FindStringSubmatch(line); m != nil {
				out.Size = parseSize(m[1], m[2])
			}
			if m := siteRe.FindStringSubmatch(line); m != nil {
				out.Site = strings.TrimSpace(m[1])
			}
			flags = append(flags, lang.FromFlags(line)...)
		case onlyFlags(line):
			flags = append(flags, lang.FromFlags(line)...)
		default:
			nameLines = append(nameLines, line)
		}
	}
	if len(nameLines) > 0 {
		out.TorrentName = nameLines[0]
	}
	if len(nameLines) > 1 {
		out.FilePath = nameLines[len(nameLines)-1]
	} else if filenameHint != "" && filenameHint != out.TorrentName {
		out.FilePath = filenameHint
	}
	if out.TorrentName == "" {
		out.TorrentName = filenameHint
	}
	if videoSize > 0 {
		out.Size = videoSize
	}

	t := Parse(out.TorrentName)
	if out.FilePath != "" {
		f := ParseFile(out.FilePath)
		t = merge(t, f)
	}
	if t.Resolution == "" {
		t.Resolution = labelResolution(out.Label)
	}
	t.Languages = lang.Merge(t.Languages, flags)
	out.Info = t
	return out
}

// merge fills gaps in the torrent-name info with the file-name info. The
// file decides which episode this is; the torrent says whether it's a pack.
func merge(t, f Info) Info {
	if len(f.Episodes) > 0 {
		pack := t.SeasonPack || len(t.Seasons) > 1 || (len(t.Episodes) > 1 && len(f.Episodes) == 1)
		t.Seasons, t.Episodes = f.Seasons, f.Episodes
		if len(t.Seasons) == 0 {
			t.Seasons = f.Seasons
		}
		t.SeasonPack = pack
	}
	if t.Resolution == "" {
		t.Resolution = f.Resolution
	}
	if t.Source == "" {
		t.Source = f.Source
	}
	if t.Codec == "" {
		t.Codec = f.Codec
	}
	if t.BitDepth == 0 {
		t.BitDepth = f.BitDepth
	}
	if t.Year == 0 {
		t.Year = f.Year
	}
	if t.Channels == "" {
		t.Channels = f.Channels
	}
	if t.Group == "" {
		t.Group = f.Group
	}
	t.HDR = mergeStr(t.HDR, f.HDR)
	t.Audio = mergeStr(t.Audio, f.Audio)
	t.Languages = mergeStr(t.Languages, f.Languages)
	t.Multi = t.Multi || f.Multi
	return t
}

func mergeStr(a, b []string) []string {
	for _, v := range b {
		a = appendStr(a, v)
	}
	return a
}

func labelResolution(label string) string {
	l := strings.ToLower(label)
	switch {
	case strings.Contains(l, "4k") || strings.Contains(l, "2160"):
		return "2160p"
	case strings.Contains(l, "1080"):
		return "1080p"
	case strings.Contains(l, "720"):
		return "720p"
	case strings.Contains(l, "480"):
		return "480p"
	}
	return ""
}

func onlyFlags(line string) bool {
	seen := false
	for _, r := range line {
		switch {
		case r >= 0x1F1E6 && r <= 0x1F1FF:
			seen = true
		case unicode.IsSpace(r), r == '/', r == '|', r == ',':
		default:
			return false
		}
	}
	return seen
}

func parseSize(num, unit string) int64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", "."), 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[strings.ToUpper(unit[:1])]
	return int64(f * mult)
}
