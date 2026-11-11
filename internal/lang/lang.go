// Package lang normalizes language identifiers from the different places
// Harbor meets them — ISO 639-2 codes from subtitle addons (eng, pob),
// flag emojis in addon descriptions (🇬🇧), and words in release names
// (ENG, PT-BR, LATINO) — into one short form: ISO 639-1 ("en"), plus
// "pt-BR" and "es-419" where the variant matters to viewers.
package lang

import (
	"sort"
	"strings"
)

// Lang is a normalized language code.
type Lang = string

var names = map[Lang]string{
	"en": "English", "pt": "Portuguese", "pt-BR": "Portuguese (Brazil)", "es": "Spanish",
	"es-419": "Spanish (Latin America)", "fr": "French", "de": "German", "it": "Italian",
	"ru": "Russian", "uk": "Ukrainian", "pl": "Polish", "nl": "Dutch", "sv": "Swedish",
	"da": "Danish", "no": "Norwegian", "fi": "Finnish", "cs": "Czech", "sk": "Slovak",
	"hu": "Hungarian", "ro": "Romanian", "bg": "Bulgarian", "el": "Greek", "tr": "Turkish",
	"ar": "Arabic", "he": "Hebrew", "fa": "Persian", "hi": "Hindi", "ta": "Tamil",
	"te": "Telugu", "bn": "Bengali", "ja": "Japanese", "ko": "Korean", "zh": "Chinese",
	"th": "Thai", "vi": "Vietnamese", "id": "Indonesian", "ms": "Malay", "hr": "Croatian",
	"sr": "Serbian", "sl": "Slovenian", "et": "Estonian", "lv": "Latvian", "lt": "Lithuanian",
	"ca": "Catalan", "eu": "Basque", "gl": "Galician", "is": "Icelandic", "tl": "Filipino",
}

// Name returns the English name of a code, or the code itself.
func Name(l Lang) string {
	if n, ok := names[l]; ok {
		return n
	}
	return l
}

// Known lists all codes with names, sorted by name.
func Known() []Lang {
	out := make([]Lang, 0, len(names))
	for k := range names {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return names[out[i]] < names[out[j]] })
	return out
}

// iso2 maps ISO 639-2/B, 639-2/T and OpenSubtitles extras to our codes.
var iso2 = map[string]Lang{
	"eng": "en", "por": "pt", "pob": "pt-BR", "pb": "pt-BR", "spa": "es", "spn": "es-419", "ea": "es-419",
	"fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "ita": "it", "rus": "ru", "ukr": "uk",
	"pol": "pl", "dut": "nl", "nld": "nl", "swe": "sv", "dan": "da", "nor": "no", "nob": "no",
	"fin": "fi", "cze": "cs", "ces": "cs", "slo": "sk", "slk": "sk", "hun": "hu", "rum": "ro",
	"ron": "ro", "bul": "bg", "gre": "el", "ell": "el", "tur": "tr", "ara": "ar", "heb": "he",
	"per": "fa", "fas": "fa", "hin": "hi", "tam": "ta", "tel": "te", "ben": "bn", "jpn": "ja",
	"kor": "ko", "chi": "zh", "zho": "zh", "zht": "zh", "zhe": "zh", "ze": "zh", "tha": "th",
	"vie": "vi", "ind": "id", "may": "ms", "msa": "ms", "hrv": "hr", "srp": "sr", "scc": "sr",
	"slv": "sl", "est": "et", "lav": "lv", "lit": "lt", "cat": "ca", "baq": "eu", "eus": "eu",
	"glg": "gl", "ice": "is", "isl": "is", "fil": "tl", "tgl": "tl",
}

// Normalize turns a code from any source (en, eng, pob, pt-br, PT_BR,
// es-MX…) into Harbor's form. It returns "" when unknown.
func Normalize(code string) Lang {
	c := strings.ToLower(strings.TrimSpace(code))
	c = strings.ReplaceAll(c, "_", "-")
	switch c {
	case "":
		return ""
	case "pt-br", "ptbr", "br":
		return "pt-BR"
	case "pt-pt":
		return "pt"
	case "es-419", "es-mx", "es-la", "es-ar", "es-co", "es-cl":
		return "es-419"
	case "es-es":
		return "es"
	case "iw":
		return "he"
	case "nb", "nn":
		return "no"
	}
	if l, ok := iso2[c]; ok {
		return l
	}
	if _, ok := names[c]; ok {
		return c
	}
	if base, _, ok := strings.Cut(c, "-"); ok {
		return Normalize(base)
	}
	return ""
}

// countryLang maps the country of a flag emoji to the language it signals
// in addon descriptions.
var countryLang = map[string]Lang{
	"GB": "en", "US": "en", "AU": "en", "CA": "en", "IE": "en", "NZ": "en",
	"PT": "pt", "BR": "pt-BR", "ES": "es", "MX": "es-419", "AR": "es-419", "CO": "es-419", "CL": "es-419",
	"FR": "fr", "DE": "de", "AT": "de", "IT": "it", "RU": "ru", "UA": "uk", "PL": "pl",
	"NL": "nl", "SE": "sv", "DK": "da", "NO": "no", "FI": "fi", "CZ": "cs", "SK": "sk",
	"HU": "hu", "RO": "ro", "BG": "bg", "GR": "el", "TR": "tr", "SA": "ar", "AE": "ar",
	"EG": "ar", "IL": "he", "IR": "fa", "IN": "hi", "JP": "ja", "KR": "ko", "CN": "zh",
	"TW": "zh", "HK": "zh", "TH": "th", "VN": "vi", "ID": "id", "MY": "ms", "HR": "hr",
	"RS": "sr", "SI": "sl", "EE": "et", "LV": "lv", "LT": "lt", "IS": "is", "PH": "tl",
}

// FromFlags finds flag emojis (pairs of regional indicator symbols) in s
// and returns the languages they stand for, in order, without repeats.
func FromFlags(s string) []Lang {
	var out []Lang
	rs := []rune(s)
	for i := 0; i+1 < len(rs); i++ {
		a, b := rs[i], rs[i+1]
		if isRI(a) && isRI(b) {
			cc := string([]rune{'A' + (a - 0x1F1E6), 'A' + (b - 0x1F1E6)})
			if l, ok := countryLang[cc]; ok {
				out = appendUnique(out, l)
			}
			i++
		}
	}
	return out
}

func isRI(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// releaseWords maps tokens seen in release names to languages. Tokens are
// matched case-insensitively on word boundaries by the release parser.
var releaseWords = map[string]Lang{
	"ENG": "en", "ENGLISH": "en", "EN": "en",
	"ITA": "it", "ITALIAN": "it",
	"FRE": "fr", "FRENCH": "fr", "VFF": "fr", "VFQ": "fr", "VFI": "fr", "TRUEFRENCH": "fr", "VF": "fr", "VF2": "fr",
	"GER": "de", "GERMAN": "de", "DEU": "de",
	"SPA": "es", "ES": "es", "SPANISH": "es", "ESP": "es", "CASTELLANO": "es",
	"LATINO": "es-419", "LAT": "es-419",
	"POR": "pt", "PORTUGUESE": "pt", "PT": "pt",
	"PTBR": "pt-BR", "PT-BR": "pt-BR", "DUBLADO": "pt-BR", "NACIONAL": "pt-BR",
	"RUS": "ru", "RUSSIAN": "ru", "UKR": "uk", "UKRAINIAN": "uk",
	"POL": "pl", "POLISH": "pl", "PL": "pl", "DUTCH": "nl", "NL": "nl",
	"SWE": "sv", "SWEDISH": "sv", "DAN": "da", "DANISH": "da", "NOR": "no", "NORWEGIAN": "no",
	"FIN": "fi", "FINNISH": "fi", "CZE": "cs", "CZECH": "cs", "HUN": "hu", "HUNGARIAN": "hu",
	"ROM": "ro", "RUM": "ro", "ROMANIAN": "ro", "BUL": "bg", "GREEK": "el", "TUR": "tr", "TURKISH": "tr",
	"ARABIC": "ar", "HEB": "he", "HEBREW": "he", "HIN": "hi", "HINDI": "hi", "TAMIL": "ta", "TELUGU": "te",
	"JAP": "ja", "JPN": "ja", "JAPANESE": "ja", "KOR": "ko", "KOREAN": "ko",
	"CHI": "zh", "CHINESE": "zh", "MANDARIN": "zh", "CANTONESE": "zh",
	"THAI": "th", "VIE": "vi", "VIETNAMESE": "vi", "IND": "id",
}

// FromWord returns the language a release-name token stands for, or "".
func FromWord(tok string) Lang {
	return releaseWords[strings.ToUpper(tok)]
}

func appendUnique(list []Lang, l Lang) []Lang {
	for _, v := range list {
		if v == l {
			return list
		}
	}
	return append(list, l)
}

// Merge combines language lists without repeats, keeping order.
func Merge(lists ...[]Lang) []Lang {
	var out []Lang
	for _, l := range lists {
		for _, v := range l {
			out = appendUnique(out, v)
		}
	}
	return out
}
