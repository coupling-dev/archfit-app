package ui

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
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

	// noncePlaceholder is the nonce web/index.html and web/csp.txt carry; the shell replaces it on every response.
	noncePlaceholder = "__CSP_NONCE__"
)

func builtWeb() fs.FS {
	web, err := fs.Sub(spaFS, "spa")
	if err != nil {
		panic(err) // the embedded folder name is a compile-time constant
	}
	return web
}

// shell serves the web app's index.html for a page route. It holds no tenant data: the app reads that from /api.
//
// The policy editor (CodeMirror) mounts its styles as a <style> element, which style-src 'self' blocks. Each response
// gets a fresh nonce for that element alone: the shell's CSP allows it in style-src, the page reads it from a meta
// tag, and script-src stays 'self'. A nonce must never be reused, so the shell is never stored.
func shell(web fs.FS) http.HandlerFunc {
	index, err := fs.ReadFile(web, spaIndex)
	if err != nil {
		slog.Warn("ui web app not built; pages show a notice")
		index = []byte(unbuiltPage)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		nonce := newNonce()
		w.Header().Set("Content-Security-Policy", shellPolicy(nonce))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(bytes.ReplaceAll(index, []byte(noncePlaceholder), []byte(nonce)))
	}
}

// newNonce is 16 random bytes in base64, which a CSP nonce and an HTML attribute both take as they are.
func newNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	return base64.StdEncoding.EncodeToString(b[:])
}

// shellPolicy is contentSecurityPolicy with the shell's style nonce added to style-src, and nothing else changed.
func shellPolicy(nonce string) string {
	return cspHead + " 'nonce-" + nonce + "'" + cspTail
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
