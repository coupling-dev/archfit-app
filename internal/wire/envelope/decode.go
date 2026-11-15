package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	maxRawBytes = 8192

	sha256Prefix = "sha256:"
	shaHexLen    = 40
	digestHexLen = 64
)

// field is one envelope key: its JSON type.
type field struct {
	name string
	bits int // 0 = string; otherwise an integer of this size

}

// Errors name the field and the rule, never the offending value; encoding/json errors quote input, so none is passed on.
func fieldError(name, violated string) error {
	return errors.New("envelope: " + name + ": " + violated)
}

func errMalformed() error { return errors.New("envelope: malformed JSON") }

// checkShape is the strictness encoding/json lacks: one top-level object whose keys are exactly the contract's,
// each once, none null, each of the right JSON type, and nothing after the object.
func checkShape(raw []byte, fs []field) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("envelope: must be a JSON object")
	}
	seen := make(map[string]bool, len(fs))
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return errMalformed()
		}
		f, known := lookup(fs, key)
		if !known {
			return errors.New("envelope: unknown key")
		}
		if seen[f.name] {
			return fieldError(f.name, "duplicate key")
		}
		seen[f.name] = true

		value, err := dec.Token()
		if err != nil {
			return errMalformed()
		}
		if violated := checkType(f, value); violated != "" {
			return fieldError(f.name, violated)
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return errMalformed()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("envelope: trailing data after the object")
	}
	for _, f := range fs {
		if !seen[f.name] {
			return fieldError(f.name, "required")
		}
	}
	return nil
}

func lookup(fs []field, key json.Token) (field, bool) {
	for _, f := range fs {
		if key == f.name {
			return f, true
		}
	}
	return field{}, false
}

func checkType(f field, value json.Token) string {
	if value == nil {
		return "must not be null"
	}
	if f.bits == 0 {
		_, ok := value.(string)
		return rule(ok, "must be a string")
	}
	n, ok := value.(json.Number)
	if ok {
		_, err := strconv.ParseInt(n.String(), 10, f.bits)
		ok = err == nil
	}
	return rule(ok, "must be an integer without fraction or exponent, within "+strconv.Itoa(f.bits)+" bits")
}

func rule(ok bool, text string) string {
	if ok {
		return ""
	}
	return text
}

func prefixedDigest(digest string) string {
	hexPart, ok := strings.CutPrefix(digest, sha256Prefix)
	return rule(ok && isHex(hexPart, digestHexLen), "must be sha256: and 64 lowercase hex characters")
}

func isPlatform(s string) bool {
	parts := strings.Split(s, "/")
	if len(s) > 32 || len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if !isName(part, len(s), isLowerAlnum) {
			return false
		}
	}
	return true
}

func isHex(s string, n int) bool {
	return len(s) == n && isName(s, n, func(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' })
}

// isName reports whether s is 1 to limit bytes, all accepted by ok.
func isName(s string, limit int, ok func(byte) bool) bool {
	if len(s) < 1 || len(s) > limit {
		return false
	}
	for i := range len(s) {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool      { return '0' <= c && c <= '9' }
func isLower(c byte) bool      { return 'a' <= c && c <= 'z' }
func isAlnum(c byte) bool      { return isLower(c) || isDigit(c) || 'A' <= c && c <= 'Z' }
func isLowerAlnum(c byte) bool { return isLower(c) || isDigit(c) }

func isRepoChar(c byte) bool { return isAlnum(c) || c == '_' || c == '.' || c == '-' }
func isVersionChar(c byte) bool {
	return isAlnum(c) || c == '.' || c == '_' || c == '+' || c == '-'
}
