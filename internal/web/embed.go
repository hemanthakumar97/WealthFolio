package web

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// devFallback is served when no frontend has been built (e.g. running `go run` locally
// before `pnpm build`). Lets the API still be exercised via curl / http clients.
const devFallback = `<!doctype html>
<html><head><meta charset="utf-8"><title>WealthFolio (API only)</title>
<style>body{font-family:system-ui;padding:2rem;max-width:42rem;line-height:1.5}</style>
</head><body>
<h1>WealthFolio backend is running</h1>
<p>The frontend bundle hasn't been built into <code>internal/web/dist</code> yet.</p>
<p>For dev: run <code>cd web &amp;&amp; pnpm dev</code> and open the Vite URL.<br>
For prod: <code>make build-web</code> embeds the SPA into the binary.</p>
<p>API: <a href="/api/health">/api/health</a> · <a href="/api/auth/status">/api/auth/status</a></p>
</body></html>`

// SPA returns an http.Handler that serves the embedded Vite build, falling back to
// index.html for any path that doesn't match a static asset. /api/* requests are NOT
// handled here — wire this as the chi router's NotFound after mounting /api routes.
func SPA() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeFallback(w)
		})
	}
	indexBytes, indexErr := fs.ReadFile(sub, "index.html")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if indexErr != nil {
			writeFallback(w)
			return
		}
		// Try to serve as static asset; if file doesn't exist, fall back to SPA index.
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.Copy(w, bytes.NewReader(indexBytes))
	})
}

func writeFallback(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, devFallback)
}
