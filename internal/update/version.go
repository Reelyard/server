package update

import (
	"regexp"
	"strconv"
	"strings"
)

// semver is a parsed release version: 1.2.3 or 1.2.3-beta.4.
type semver struct {
	nums [3]int
	pre  []string
}

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// devSuffix matches what `git describe` adds after a tag: -18-gb15fd6d,
// optionally -dirty.
var devSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]{6,}(-dirty)?$|-dirty$`)

func parse(v string) (semver, bool) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}, false
	}
	var s semver
	for i := range 3 {
		s.nums[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] != "" {
		s.pre = strings.Split(m[4], ".")
	}
	return s, true
}

// IsDev reports whether a version is a development build: "dev", or a
// release tag plus commits on top (git describe).
func IsDev(v string) bool {
	if v == "" || v == "dev" {
		return true
	}
	return devSuffix.MatchString(v)
}

// Base strips a development build's git describe suffix, so
// v0.1.0-beta.2-18-gb15fd6d becomes v0.1.0-beta.2.
func Base(v string) string {
	return devSuffix.ReplaceAllString(v, "")
}

// IsPrerelease reports whether v is a prerelease (1.0.0-beta.1).
func IsPrerelease(v string) bool {
	s, ok := parse(Base(v))
	return ok && len(s.pre) > 0
}

// Newer reports whether a is a later version than b. Unparseable versions
// are never newer.
func Newer(a, b string) bool {
	sa, ok1 := parse(a)
	sb, ok2 := parse(Base(b))
	if !ok1 {
		return false
	}
	if !ok2 {
		return true // b is "dev" or unknown: any release is newer
	}
	return compare(sa, sb) > 0
}

func compare(a, b semver) int {
	for i := range 3 {
		if a.nums[i] != b.nums[i] {
			return cmpInt(a.nums[i], b.nums[i])
		}
	}
	// A release outranks its prereleases.
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		nx, errx := strconv.Atoi(x)
		ny, erry := strconv.Atoi(y)
		switch {
		case errx == nil && erry == nil:
			if nx != ny {
				return cmpInt(nx, ny)
			}
		case errx == nil: // numeric identifiers sort before text
			return -1
		case erry == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(a.pre), len(b.pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
