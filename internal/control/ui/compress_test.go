package ui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetsSendTheCodingTheClientTakes(t *testing.T) {
	const raw, brotli, gz = "console.log(1)", "BR-BYTES", "GZ-BYTES"
	web := fstest.MapFS{
		"assets/app.js":    {Data: []byte(raw)},
		"assets/app.js.br": {Data: []byte(brotli)},
		"assets/app.js.gz": {Data: []byte(gz)},
		"assets/plain.js":  {Data: []byte("only raw")},
	}
	tests := []struct {
		name, path, accept, wantBody, wantEncoding string
	}{
		{"brotli first", "/assets/app.js", "gzip, br", brotli, "br"},
		{"gzip when brotli is refused", "/assets/app.js", "br;q=0, gzip", gz, "gzip"},
		{"raw when nothing is accepted", "/assets/app.js", "", raw, ""},
		{"raw for identity", "/assets/app.js", "identity", raw, ""},
		{"a wildcard names no coding", "/assets/app.js", "*", raw, ""},
		{"no sibling, raw", "/assets/plain.js", "br, gzip", "only raw", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Accept-Encoding", tt.accept)
			res := httptest.NewRecorder()
			assets(web).ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d", res.Code)
			}
			if got := res.Header().Get("Content-Encoding"); got != tt.wantEncoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if got := res.Body.String(); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if got := res.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("Vary = %q, want Accept-Encoding", got)
			}
		})
	}
}

func TestAssetContentTypeIsTheRawFilesNotTheSiblings(t *testing.T) {
	web := fstest.MapFS{
		"assets/app.js":    {Data: []byte("x")},
		"assets/app.js.br": {Data: []byte("y")},
	}
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	res := httptest.NewRecorder()
	assets(web).ServeHTTP(res, req)
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") && !strings.HasPrefix(got, "application/javascript") {
		t.Errorf("Content-Type = %q, want the script type", got)
	}
}

func TestJSONIsGzippedOnlyWhenLargeAndAccepted(t *testing.T) {
	large := map[string]string{"text": strings.Repeat("policy line\n", 200)}
	small := map[string]string{"text": "ok"}
	tests := []struct {
		name, accept string
		body         any
		wantGzip     bool
	}{
		{"large and accepted", "gzip", large, true},
		{"large, not accepted", "br", large, false},
		{"small and accepted", "gzip", small, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			req.Header.Set("Accept-Encoding", tt.accept)
			res := httptest.NewRecorder()
			writeJSON(res, req, http.StatusOK, tt.body)

			if got := res.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("Vary = %q", got)
			}
			if tt.wantGzip != (res.Header().Get("Content-Encoding") == "gzip") {
				t.Fatalf("Content-Encoding = %q, want gzip %v", res.Header().Get("Content-Encoding"), tt.wantGzip)
			}
			body := res.Body.Bytes()
			if tt.wantGzip {
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(zr)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(string(body), `"text"`) {
				t.Errorf("body does not decode to the JSON: %.80s", body)
			}
		})
	}
}
