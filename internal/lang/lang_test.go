package lang

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"eng": "en", "EN": "en", "pob": "pt-BR", "pt_BR": "pt-BR", "por": "pt", "pt-PT": "pt",
		"spn": "es-419", "es-MX": "es-419", "es": "es", "fre": "fr", "iw": "he", "zh-TW": "zh",
		"klingon": "", "": "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromFlags(t *testing.T) {
	got := FromFlags("👤 4 💾 7.23 GB ⚙️ EXT\n🇬🇧 / 🇵🇹 / 🇧🇷 / 🇬🇧")
	if want := []string{"en", "pt", "pt-BR"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FromFlags = %v, want %v", got, want)
	}
	if got := FromFlags("no flags here"); got != nil {
		t.Errorf("FromFlags = %v", got)
	}
}

func TestFromWord(t *testing.T) {
	if FromWord("latino") != "es-419" || FromWord("PTBR") != "pt-BR" || FromWord("x265") != "" {
		t.Error("FromWord mapping wrong")
	}
}
