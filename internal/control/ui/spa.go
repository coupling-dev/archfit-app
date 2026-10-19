package ui

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
)

// spaFS is the build of the web app (web/, `npm run build`). Without a build it holds only its .gitignore, and the
// shell answers with unbuiltPage, so `go build` and `go test` need no Node.
//
//go:embed all:spa
var spaFS embed.FS

const (
	spaIndex = "index.html"

	// Asset names carry a content hash, so a browser may keep them for a year; the shell must be revalidated.
	immutableCache = "public, max-age=31536000, immutable"

	unbuiltPage = "<!doctype html><title>Archfit</title><p>The web app is not built. Run <code>make web</code>, then restart."
)

func builtWeb() fs.FS {
	web, err := fs.Sub(spaFS, "spa")
	if err != nil {
		panic(err) // the embedded folder name is a compile-time constant
	}
	return web
}

// shell serves the web app's index.html for a page route. It holds no tenant data: the app reads that from /api.
func shell(web fs.FS) http.HandlerFunc {
	index, err := fs.ReadFile(web, spaIndex)
	if err != nil {
		slog.Warn("ui web app not built; pages show a notice")
		index = []byte(unbuiltPage)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}
}

// assets serves the hashed build files under /assets/: no directory listing, no other folder of the build.
func assets(web fs.FS) http.HandlerFunc {
	files := http.FileServerFS(web)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(web, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", immutableCache)
		files.ServeHTTP(w, r)
	}
}
