package suppliers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
)

func TestCatalog(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := map[string]string{
			"/addon_catalog/all/official.json":  "addon-catalog-official.json",
			"/addon_catalog/all/community.json": "addon-catalog-community.json",
		}[r.URL.Path]
		if file == "" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		b, err := os.ReadFile(filepath.Join("..", "stremio", "testdata", file))
		if err != nil {
			t.Error(err)
		}
		w.Write(b)
	}))
	defer srv.Close()

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Pretend OpenSubtitles v3 is already a supplier.
	if _, err := st.CreateSupplier(ctx, "OpenSubtitles v3", "https://opensubtitles-v3.strem.io/manifest.json", "{}"); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: st, Client: stremio.New("test"), CatalogBase: srv.URL}

	list, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 15 {
		t.Fatalf("only %d usable addons", len(list))
	}
	var sawOpenSubs, sawConfigure bool
	for i, e := range list {
		if strings.Contains(e.ManifestURL, "127.0.0.1") || !strings.HasSuffix(e.ManifestURL, "/manifest.json") {
			t.Errorf("unusable addon kept: %s", e.ManifestURL)
		}
		if strings.Contains(e.Description, "<a ") || strings.Contains(e.Description, "&amp;") {
			t.Errorf("%s: description still has markup: %q", e.Name, e.Description)
		}
		if len(e.Resources) == 0 {
			t.Errorf("%s has no stream/subtitle resource", e.Name)
		}
		if e.Name == "Cinemeta" || e.Name == "YouTube" {
			t.Errorf("%s offers neither streams nor subtitles", e.Name)
		}
		if i > 0 && e.Official && !list[i-1].Official {
			t.Error("official addons are not listed first")
		}
		if e.ManifestURL == "https://opensubtitles-v3.strem.io/manifest.json" {
			sawOpenSubs = true
			if !e.Installed || !e.Official {
				t.Errorf("OpenSubtitles v3: %+v", e)
			}
		}
		if e.ConfigurationRequired {
			sawConfigure = true
			if !strings.HasSuffix(e.ConfigureURL, "/configure") {
				t.Errorf("%s: configure URL %q", e.Name, e.ConfigureURL)
			}
		}
	}
	if !sawOpenSubs || !sawConfigure {
		t.Errorf("expected OpenSubtitles v3 and a configure-first addon (opensubs=%v configure=%v)", sawOpenSubs, sawConfigure)
	}

	// Second call is served from the cache.
	before := hits.Load()
	if _, err := s.Catalog(ctx); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Error("catalog fetched again within the cache window")
	}
}
