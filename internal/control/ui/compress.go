package ui

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// tokenBearer is an answer that carries a CSRF or step-up token. It is never compressed, whatever its size: a compressed
// body's length moves with the text around the token, which is how BREACH reads a secret. Its size does not matter, so
// the rule is explicit rather than a side effect of a size threshold.
type tokenBearer interface{ carriesToken() }

func (sessionJSON) carriesToken()      {}
func (answerJSON) carriesToken()       {}
func (editJSON) carriesToken()         {}
func (ownerJSON) carriesToken()        {}
func (passkeyTokenJSON) carriesToken() {}
func (stepUpTokenJSON) carriesToken()  {}

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
		if v, ok := cutQ(strings.TrimSpace(params)); ok {
			q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return err == nil && q > 0
		}
		return true
	}
	return false
}

// cutQ returns the value of a q parameter; the name is case-insensitive.
func cutQ(params string) (string, bool) {
	if len(params) < 2 || !strings.EqualFold(params[:2], "q=") {
		return "", false
	}
	return params[2:], true
}

// gzipped is b compressed with gzip. Writing to a bytes.Buffer cannot fail, so the error is not checked.
func gzipped(b []byte) []byte {
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return out.Bytes()
}
