// Package release reads torrent release names
// ("Show.Name.S01E02.1080p.WEB-DL.DDP5.1.H.264-GROUP") and the text Stremio
// addons put around them, and turns them into structured fields that
// quality profiles can score.
package release

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/inluxc/harbor/internal/lang"
)

// Info is what a release name tells us.
type Info struct {
	Title      string   `json:"title"`
	Year       int      `json:"year,omitempty"`
	Seasons    []int    `json:"seasons,omitempty"`  // all seasons the release covers
	Episodes   []int    `json:"episodes,omitempty"` // episodes of Seasons[0], when known
	SeasonPack bool     `json:"seasonPack,omitempty"`
	Complete   bool     `json:"complete,omitempty"`
	Resolution string   `json:"resolution,omitempty"` // 2160p 1080p 720p 576p 480p 360p
	Source     string   `json:"source,omitempty"`     // REMUX BluRay BDRip WEB-DL WEBRip HDTV DVDRip HDRip CAM TS TC SCR
	Codec      string   `json:"codec,omitempty"`      // x265 x264 AV1 XviD VP9
	BitDepth   int      `json:"bitDepth,omitempty"`
	HDR        []string `json:"hdr,omitempty"`   // DV HDR10+ HDR10 HDR
	Audio      []string `json:"audio,omitempty"` // TrueHD Atmos DTS-HD DTS-X DTS DDP DD AAC FLAC LPCM Opus MP3
	Channels   string   `json:"channels,omitempty"`
	Languages  []string `json:"languages,omitempty"`
	Multi      bool     `json:"multi,omitempty"` // MULTI / DUAL audio
	Group      string   `json:"group,omitempty"`
	Proper     bool     `json:"proper,omitempty"`
	Repack     bool     `json:"repack,omitempty"`
	ThreeD     bool     `json:"threeD,omitempty"`
	Upscaled   bool     `json:"upscaled,omitempty"`
}

// Season returns the first season, or 0.
func (i *Info) Season() int {
	if len(i.Seasons) == 0 {
		return 0
	}
	return i.Seasons[0]
}

// HasEpisode reports whether the release contains season s episode e,
// either as that episode or as a pack covering the season.
func (i *Info) HasEpisode(s, e int) bool {
	if !containsInt(i.Seasons, s) {
		return i.Complete && len(i.Seasons) == 0
	}
	if len(i.Episodes) == 0 {
		return true // whole-season pack
	}
	return i.Seasons[0] == s && containsInt(i.Episodes, e)
}

// IsLowQuality reports camera/telesync style sources nobody wants by default.
func (i *Info) IsLowQuality() bool {
	switch i.Source {
	case "CAM", "TS", "TC", "SCR":
		return true
	}
	return false
}

var videoExt = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|ts|m2ts|wmv|mov|webm|mpg|mpeg|iso)$`)

type rule struct {
	re  *regexp.Regexp
	val string
}

func ci(p string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + p) }

// Boundaries: Go's \b treats '_' as a word character, so use explicit
// separators instead.
const (
	lb = `(?:^|[^a-z0-9])`
	rb = `(?:$|[^a-z0-9])`
)

var (
	resRules = []rule{
		{ci(lb + `(?:2160[pi]|4k|uhd|3840x2160)` + rb), "2160p"},
		{ci(lb + `(?:1080[pi]|1920x1080|fhd)` + rb), "1080p"},
		{ci(lb + `(?:720p|1280x720)` + rb), "720p"},
		{ci(lb + `576[pi]` + rb), "576p"},
		{ci(lb + `480[pi]` + rb), "480p"},
		{ci(lb + `360p` + rb), "360p"},
	}
	sourceRules = []rule{
		{ci(`(?:^|[^a-z0-9]|bd|uhd)-?remux` + rb), "REMUX"},
		{ci(lb + `(?:bdrip|brrip|bd-rip|br-rip)` + rb), "BDRip"},
		{ci(lb + `(?:blu-?ray|bluray|bd25|bd50|uhd-?bd|bdmv|bd)` + rb), "BluRay"},
		{ci(lb + `(?:web-?dl|webdl|amzn|nf|dsnp|hmax|atvp)` + rb), "WEB-DL"},
		{ci(lb + `web-?rip` + rb), "WEBRip"},
		{ci(lb + `web` + rb), "WEB-DL"},
		{ci(lb + `(?:hdtv|pdtv|dsr|tvrip|satrip)` + rb), "HDTV"},
		{ci(lb + `(?:hd-?cam|cam-?rip|cam|hqcam)` + rb), "CAM"},
		{ci(lb + `(?:hd-?ts|telesync|ts-?rip|ts)` + rb), "TS"},
		{ci(lb + `(?:hd-?tc|telecine|tc)` + rb), "TC"},
		{ci(lb + `(?:dvd-?scr|screener|scr)` + rb), "SCR"},
		{ci(lb + `(?:dvd-?rip|dvd-?r|dvd5|dvd9|dvd)` + rb), "DVDRip"},
		{ci(lb + `hd-?rip` + rb), "HDRip"},
	}
	codecRules = []rule{
		{ci(lb + `(?:[xh][ .]?265|hevc)` + rb), "x265"},
		{ci(lb + `(?:[xh][ .]?264|avc)` + rb), "x264"},
		{ci(lb + `av1` + rb), "AV1"},
		{ci(lb + `vp9` + rb), "VP9"},
		{ci(lb + `(?:xvid|divx)` + rb), "XviD"},
	}
	audioRules = []rule{
		{ci(lb + `true-?hd`), "TrueHD"},
		{ci(lb + `atmos`), "Atmos"},
		{ci(lb + `dts-?hd`), "DTS-HD"},
		{ci(lb + `dts-?x` + rb), "DTS-X"},
		{ci(lb + `dts(?:[^a-z-]|$)`), "DTS"},
		{ci(lb + `(?:ddp|dd\+|e-?ac-?3|eac3)`), "DDP"},
		{ci(lb + `(?:dd|ac-?3)(?:[^a-z]|$)`), "DD"},
		{ci(lb + `aac`), "AAC"},
		{ci(lb + `flac`), "FLAC"},
		{ci(lb + `l?pcm` + rb), "LPCM"},
		{ci(lb + `opus` + rb), "Opus"},
		{ci(lb + `mp3` + rb), "MP3"},
	}
	hdrDV      = ci(lb + `(?:dv|dovi|dolby[ .-]?vision)` + rb)
	hdr10plus  = ci(lb + `hdr10(?:\+|plus)`)
	hdr10      = ci(lb + `hdr10` + rb)
	hdrPlain   = ci(lb + `hdr` + rb)
	bit10      = ci(lb + `10[ .-]?bits?` + rb)
	channelsRe = ci(`(?:^|[^0-9a-z]|ma|aac|ac3|ddp|dd|dts|flac|opus|atmos|pcm)([1-7])[ .]([01])(?:ch)?` + rb)
	yearRe     = regexp.MustCompile(`(?:19[2-9]|20[0-4])[0-9]`)
	multiRe    = ci(lb + `(?:multi|dual(?:[ .-]?audio)?|multi[ .-]?audio)` + rb)
	completeRe = ci(lb + `(?:complete|integrale|collection|full[ .]series)` + rb)
	properRe   = ci(lb + `proper` + rb)
	repackRe   = ci(lb + `(?:repack|rerip)` + rb)
	threeDRe   = ci(lb + `(?:3d|h-?sbs|h-?ou)` + rb)
	upscaleRe  = ci(`upscal`)

	// Episodes and seasons, most specific first.
	sxxexx      = ci(lb + `s(\d{1,2})[ ._-]?e(\d{1,4})((?:[ ._]?-[ ._]?e?\d{1,3}|e\d{1,3})*)`)
	nxnn        = ci(lb + `(\d{1,2})x(\d{2,3})` + rb)
	seasonRange = ci(lb + `s(\d{1,2})[ ._]?-[ ._]?s?(\d{1,2})` + rb)
	seasonsList = ci(lb + `(?:seasons?|temporadas?|saisons?|sezon|сезон|staffel|stagione)[ ._:]*((?:\d{1,2}(?:[ ._,&+-]+(?:and[ ._]+)?)?)+)`)
	seasonFirst = ci(lb + `(\d{1,2})(?:st|nd|rd|th|ª|a)?[ ._]?(?:temporada|season|saison|stagione|staffel)` + rb)
	sxx         = ci(lb + `s(\d{1,2})` + rb)
	episodeWord = ci(lb + `(?:episode|ep|cap[ií]tulo)[ ._]?(\d{1,4})` + rb)
	moreEps     = regexp.MustCompile(`\d{1,4}`)
	// "05 - Title.mkv" inside a season folder.
	looseEpisode = regexp.MustCompile(`^(\d{1,3})(?:[ ._-]|$)`)

	groupRe     = regexp.MustCompile(`-\s*([A-Za-z0-9][A-Za-z0-9_]{0,24})(?:\[[^\]]*\]|\([^)]*\))?\s*$`)
	bracketTail = regexp.MustCompile(`[\[(]([A-Za-z0-9][A-Za-z0-9.\-_]{1,20})[\])]\s*$`)
	leadingTag  = regexp.MustCompile(`^\s*(?:\[[^\]]*\]|【[^】]*】)\s*-?\s*`)
	splitRe     = regexp.MustCompile(`[^A-Za-z0-9+\-]+`)
	spacesRe    = regexp.MustCompile(`\s+`)
)

// Parse reads one release name or file name.
func Parse(name string) Info {
	var in Info
	s := strings.TrimSpace(name)
	s = videoExt.ReplaceAllString(s, "")
	s = leadingTag.ReplaceAllString(s, "")

	cut := len(s) // where the title ends

	mark := func(idx []int) {
		if idx != nil && idx[0] < cut && idx[0] > 0 {
			cut = idx[0]
		}
	}

	// Seasons / episodes.
	if m := sxxexx.FindStringSubmatchIndex(s); m != nil {
		season := atoi(s[m[2]:m[3]])
		in.Seasons = []int{season}
		in.Episodes = []int{atoi(s[m[4]:m[5]])}
		// "S01E01 - 1080p": the trailing digits run on, so they're not episodes.
		trailOK := m[7] == len(s) || !isAlnum(s[m[7]])
		if trailOK {
			for _, e := range moreEps.FindAllString(s[m[6]:m[7]], -1) {
				in.Episodes = appendInt(in.Episodes, atoi(e))
			}
			if len(in.Episodes) == 2 && strings.Contains(s[m[6]:m[7]], "-") {
				in.Episodes = rangeInts(in.Episodes[0], in.Episodes[1])
			}
		}
		mark(m)
	} else if m := nxnn.FindStringSubmatchIndex(s); m != nil {
		in.Seasons = []int{atoi(s[m[2]:m[3]])}
		in.Episodes = []int{atoi(s[m[4]:m[5]])}
		mark(m)
	} else {
		if m := seasonRange.FindStringSubmatchIndex(s); m != nil {
			in.Seasons = rangeInts(atoi(s[m[2]:m[3]]), atoi(s[m[4]:m[5]]))
			mark(m)
		} else if m := seasonsList.FindStringSubmatchIndex(s); m != nil {
			for _, n := range moreEps.FindAllString(s[m[2]:m[3]], -1) {
				in.Seasons = appendInt(in.Seasons, atoi(n))
			}
			if strings.Contains(s[m[2]:m[3]], "-") && len(in.Seasons) == 2 {
				in.Seasons = rangeInts(in.Seasons[0], in.Seasons[1])
			}
			mark(m)
		} else if m := seasonFirst.FindStringSubmatchIndex(s); m != nil {
			in.Seasons = []int{atoi(s[m[2]:m[3]])}
			mark(m)
		} else if m := sxx.FindStringSubmatchIndex(s); m != nil {
			in.Seasons = []int{atoi(s[m[2]:m[3]])}
			mark(m)
		}
		if m := episodeWord.FindStringSubmatchIndex(s); m != nil {
			in.Episodes = []int{atoi(s[m[2]:m[3]])}
			mark(m)
		}
		if len(in.Seasons) > 0 && len(in.Episodes) == 0 {
			in.SeasonPack = true
		}
	}

	seasonCut := cut
	in.Resolution = firstRule(resRules, s, mark)
	in.Source = firstRule(sourceRules, s, mark)
	in.Codec = firstRule(codecRules, s, mark)
	for _, r := range audioRules {
		if r.re.MatchString(s) {
			in.Audio = append(in.Audio, r.val)
			mark(r.re.FindStringIndex(s))
		}
	}
	if m := channelsRe.FindStringSubmatch(s); m != nil {
		in.Channels = m[1] + "." + m[2]
	}
	if bit10.MatchString(s) {
		in.BitDepth = 10
	}
	if hdrDV.MatchString(s) {
		in.HDR = append(in.HDR, "DV")
	}
	switch {
	case hdr10plus.MatchString(s):
		in.HDR = append(in.HDR, "HDR10+")
	case hdr10.MatchString(s):
		in.HDR = append(in.HDR, "HDR10")
	case hdrPlain.MatchString(s):
		in.HDR = append(in.HDR, "HDR")
	}
	if m := multiRe.FindStringIndex(s); m != nil {
		in.Multi = true
		mark(m)
	}
	in.Complete = completeRe.MatchString(s) || len(in.Seasons) > 1
	if in.Complete && len(in.Episodes) == 0 && len(in.Seasons) > 0 {
		in.SeasonPack = true
	}
	in.Proper = properRe.MatchString(s)
	in.Repack = repackRe.MatchString(s)
	in.ThreeD = threeDRe.MatchString(s)
	in.Upscaled = upscaleRe.MatchString(s)
	for _, r := range []*regexp.Regexp{properRe, repackRe, upscaleRe, completeRe, hdrPlain, hdr10} {
		mark(r.FindStringIndex(s))
	}

	// qcut is where quality tags start (season markers don't count, so
	// "Show S01 2008 1080p" still finds 2008).
	qcut := cut
	if seasonCut < len(s) && qcut == seasonCut {
		qcut = len(s)
		for _, rs := range [][]rule{resRules, sourceRules, codecRules} {
			for _, r := range rs {
				if loc := r.re.FindStringIndex(s); loc != nil && loc[0] < qcut {
					qcut = loc[0]
				}
			}
		}
	}

	// Year: the last plausible year before the quality tags. A year at the
	// very start is part of the title ("2012", "1917") unless nothing else
	// follows.
	yearAt := 0
	for _, m := range years(s) {
		if m[2] >= qcut {
			break
		}
		if m[2] == 0 {
			continue
		}
		in.Year = atoi(s[m[2]:m[3]])
		yearAt = m[2]
		if m[3] < len(s) && s[m[3]] == '-' {
			break // first year of a range "2008-2013"
		}
	}
	if yearAt > 0 {
		mark([]int{yearAt, yearAt + 4})
	}

	// Language words only count after the title.
	tail := s[cut:]
	for _, tok := range splitRe.Split(tail, -1) {
		for _, part := range splitLangToken(tok) {
			if l := lang.FromWord(part); l != "" {
				in.Languages = appendStr(in.Languages, l)
			}
		}
	}

	in.Group = group(s)
	in.Title = cleanTitle(s[:cut])
	return in
}

// splitLangToken splits "EN-PTBR" style tokens but keeps "PT-BR" whole.
func splitLangToken(tok string) []string {
	if strings.EqualFold(tok, "PT-BR") || !strings.Contains(tok, "-") {
		return []string{tok}
	}
	return strings.Split(tok, "-")
}

func firstRule(rules []rule, s string, mark func([]int)) string {
	for _, r := range rules {
		if loc := r.re.FindStringIndex(s); loc != nil {
			mark(loc)
			return r.val
		}
	}
	return ""
}

func group(s string) string {
	if m := groupRe.FindStringSubmatch(s); m != nil && plausibleGroup(m[1]) {
		return m[1]
	}
	if m := bracketTail.FindStringSubmatch(s); m != nil && plausibleGroup(m[1]) {
		return m[1]
	}
	return ""
}

var notGroup = ci(`^(?:imdb.*|tt\d+|\d+p?|dl|hd|rip|ma|x|br|sbs|avc|hevc|x26[45]|h26[45]|web|subs?|esub|multi|dual|complete|extras|hdr|sdr|dv)$`)

// plausibleGroup rejects tokens that are really language, quality or
// numbering tags ("ES-EN", "1080p", "Eng").
func plausibleGroup(g string) bool {
	if notGroup.MatchString(g) {
		return false
	}
	for _, part := range strings.FieldsFunc(g, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		if lang.FromWord(part) == "" && !notGroup.MatchString(part) {
			return true
		}
	}
	return false
}

func cleanTitle(t string) string {
	t = strings.NewReplacer(".", " ", "_", " ").Replace(t)
	t = spacesRe.ReplaceAllString(t, " ")
	t = strings.Trim(t, " -[(:,/|*")
	// "Русское название / English Title": keep the Latin-script part.
	if strings.Contains(t, " / ") {
		for _, part := range strings.Split(t, " / ") {
			if part = strings.TrimSpace(part); latin(part) && !seasonOnly.MatchString(part) {
				return part
			}
		}
	}
	return t
}

// years finds 4-digit years not touching other digits. Matches are
// returned in submatch-index form (m[2]:m[3] is the year).
func years(s string) [][]int {
	var out [][]int
	for i := 0; i+4 <= len(s); i++ {
		loc := yearRe.FindStringIndex(s[i:])
		if loc == nil {
			break
		}
		a, b := i+loc[0], i+loc[1]
		if (a == 0 || !isDigit(s[a-1])) && (b == len(s) || !isDigit(s[b])) {
			out = append(out, []int{a, b, a, b})
		}
		i = a
	}
	return out
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

var seasonOnly = ci(`^(?:season|сезон|серии|series)`)

func latin(s string) bool {
	letters, ascii := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if r < 128 {
				ascii++
			}
		}
	}
	return letters > 0 && ascii*10 >= letters*8
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func rangeInts(a, b int) []int {
	if b < a || b-a > 200 {
		return []int{a, b}
	}
	out := make([]int, 0, b-a+1)
	for i := a; i <= b; i++ {
		out = append(out, i)
	}
	return out
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func appendInt(list []int, n int) []int {
	if containsInt(list, n) {
		return list
	}
	return append(list, n)
}

func appendStr(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// ParseFile reads a file path inside a torrent ("Season 1/Show S01E01.mkv").
// The file name decides; folders only fill in a missing season.
func ParseFile(p string) Info {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	dir, file := path.Split(p)
	in := Parse(file)
	if len(in.Seasons) == 0 && dir != "" {
		for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
			if d := Parse(part); len(d.Seasons) == 1 {
				in.Seasons = d.Seasons
			}
		}
		if len(in.Seasons) > 0 && len(in.Episodes) == 0 {
			if m := looseEpisode.FindStringSubmatch(file); m != nil {
				in.Episodes = []int{atoi(m[1])}
			}
		}
	}
	return in
}
