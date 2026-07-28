package adminweb

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The SPA build output (web/ → `npm run build` → dist/) is embedded so the
// gateway binary stays self-contained: no Node toolchain at runtime, no CDN.
// The committed dist/ is the build artifact; regenerate it whenever web/src
// changes.
//
//go:embed all:dist
var distFS embed.FS

// spaHandler serves the embedded SPA: real files (hashed assets, immutable
// cache) and index.html for everything else, so client-side routes like
// /connectors deep-link correctly. The bundle is public by design — the login
// page is part of it; every privileged action goes through /api.
func spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// The embed directive guarantees dist exists; a failure here is a
		// build-system bug, surfaced on every request rather than by panic.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "adminweb: embedded UI bundle missing", http.StatusInternalServerError)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(sub, name); err != nil || info.IsDir() {
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			// Vite emits content-hashed asset names — safe to cache forever.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		http.ServeFileFS(w, r, sub, name)
	})
}
