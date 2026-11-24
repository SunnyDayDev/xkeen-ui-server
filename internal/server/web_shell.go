package server

import (
	"bytes"
	"embed"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed webdist
var webAssets embed.FS

func NewWebShellHandler(origin WebOrigin, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if name != "/ui" && name != "/ui/" && name != "/ui/templates" && name != "/ui/settings" && !strings.HasPrefix(name, "/ui/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if !origin.allows(r, false) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		switch name {
		case "/ui":
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
		case "/ui/", "/ui/templates", "/ui/settings":
			w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'")
			serveWebAsset(w, r, "index.html")
		default:
			asset := strings.TrimPrefix(name, "/ui/assets/")
			if asset == "" || asset != path.Base(asset) || asset == "." || asset == ".." {
				http.NotFound(w, r)
				return
			}
			serveWebAsset(w, r, "assets/"+asset)
		}
	})
}

func serveWebAsset(w http.ResponseWriter, r *http.Request, name string) {
	content, err := webAssets.ReadFile("webdist/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(content))
}
