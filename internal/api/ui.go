package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const uiMissing = `<!doctype html><meta charset="utf-8"><title>Harbor</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;padding:0 1rem;line-height:1.5">
<h1>Harbor is running</h1>
<p>The web interface was not built into this binary. Build it with <code>make web</code>
and rebuild Harbor, or use the API at <code>/api/v1</code>.</p></body>`

// uiHandler serves the single-page app: real files when they exist,
// index.html for every other path so client-side routes work on reload.
func uiHandler(ui fs.FS) http.Handler {
	if ui == nil {
		return missingUI()
	}
	if _, err := fs.Stat(ui, "index.html"); err != nil {
		return missingUI()
	}
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(ui, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Vite puts a content hash in asset names.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(p) != "" {
				http.NotFound(w, r) // a missing file, not an app route
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, ui, "index.html")
	})
}

func missingUI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(uiMissing))
	})
}
