package api

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestSettingsExportImport(t *testing.T) {
	addons := fakeAddons(t)
	a := newMediaServer(t, addons.URL)

	// Server A: a supplier, a profile, a viewer and library settings.
	if code, body := a.do("POST", "/api/v1/suppliers", `{"url":"`+addons.URL+`/torrentio/manifest.json"}`); code != 201 {
		t.Fatalf("add supplier: %d %v", code, body)
	}
	if code, body := a.do("POST", "/api/v1/profiles", `{"name":"Custom","resolutions":["1080p","720p"],"minSeeders":3,"seedRatio":1,"seedHours":24}`); code != 201 && code != 200 {
		t.Fatalf("add profile: %d %v", code, body)
	}
	if code, body := a.do("POST", "/api/v1/viewers", `{"name":"Kid","allTitles":false,"titles":["tt0000002"],"dlna":false,"tvs":[]}`); code != 201 && code != 200 {
		t.Fatalf("add viewer: %d %v", code, body)
	}
	if code, body := a.do("PUT", "/api/v1/settings/library", `{"subtitleLangs":["pt","en"]}`); code != 200 {
		t.Fatalf("library settings: %d %v", code, body)
	}

	resp, err := a.c.Get(a.srv.URL + "/api/v1/settings/export")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "reelyard-settings-") {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	text := string(raw)
	for _, secret := range []string{"long enough pw", "passwordHash", "stream_link_secret", "addon_keys", "server_id"} {
		if strings.Contains(text, secret) {
			t.Fatalf("export holds %q", secret)
		}
	}
	var ex Export
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatal(err)
	}
	if ex.Kind != exportKind || len(ex.Suppliers) != 1 || len(ex.Suppliers[0].Manifest) == 0 || len(ex.Profiles) != 1 || len(ex.Viewers) != 1 {
		t.Fatalf("export content: %+v", ex)
	}

	// Server B: a fresh install.
	b := newMediaServer(t, addons.URL)
	body := `{"data":` + text + `,"dryRun":true}`
	code, got := b.do("POST", "/api/v1/settings/import", body)
	sections, _ := got["sections"].(map[string]any)
	if code != 200 || sections["suppliers"] != 1.0 || sections["viewers"] != 1.0 {
		t.Fatalf("dry run: %d %v", code, got)
	}
	if _, s := b.do("GET", "/api/v1/suppliers", ""); len(s["suppliers"].([]any)) != 0 {
		t.Fatal("a dry run must not change anything")
	}

	code, got = b.do("POST", "/api/v1/settings/import", `{"data":`+text+`}`)
	if code != 200 || got["suppliers"].(map[string]any)["added"] != 1.0 || got["profiles"].(map[string]any)["added"] != 1.0 ||
		got["viewers"].(map[string]any)["added"] != 1.0 || len(got["problems"].([]any)) != 0 {
		t.Fatalf("import: %d %v", code, got)
	}
	if _, s := b.do("GET", "/api/v1/suppliers", ""); len(s["suppliers"].([]any)) != 1 {
		t.Fatalf("supplier not imported: %v", s)
	}
	if _, s := b.do("GET", "/api/v1/settings/library", ""); !strings.Contains(toJSON(s), `"pt"`) {
		t.Fatalf("library settings not imported: %v", s)
	}

	// Again: updated in place, nothing doubled.
	_, got = b.do("POST", "/api/v1/settings/import", `{"data":`+text+`}`)
	if got["suppliers"].(map[string]any)["updated"] != 1.0 || got["profiles"].(map[string]any)["updated"] != 1.0 || got["viewers"].(map[string]any)["updated"] != 1.0 {
		t.Fatalf("second import: %v", got)
	}
	if _, s := b.do("GET", "/api/v1/suppliers", ""); len(s["suppliers"].([]any)) != 1 {
		t.Fatalf("supplier doubled: %v", s)
	}

	// Only the sections asked for.
	c := newMediaServer(t, addons.URL)
	_, got = c.do("POST", "/api/v1/settings/import", `{"data":`+text+`,"sections":["viewers"]}`)
	if got["viewers"].(map[string]any)["added"] != 1.0 || got["suppliers"].(map[string]any)["added"] != 0.0 {
		t.Fatalf("sections: %v", got)
	}

	if code, _ := b.do("POST", "/api/v1/settings/import", `{"data":{"hello":"world"}}`); code != 400 {
		t.Fatalf("not a settings file: %d", code)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
