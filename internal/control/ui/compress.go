package ui

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// gzipMinBytes is the size below which a JSON body is sent as it is: a smaller one gains nothing that the header
// costs.
const gzipMinBytes = 1 << 10

// accepts reports whether the request takes the coding token. A q value of zero refuses it, and only the named coding
// counts, so "*" never selects one the client did not ask for by name.
func accepts(r *http.Request, token string) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), token) {
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return err == nil && q > 0
		}
		return true
	}
	return false
}

// gzipped is b compressed with gzip. Writing to a bytes.Buffer cannot fail, so the error is not checked.
func gzipped(b []byte) []byte {
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return out.Bytes()
}
