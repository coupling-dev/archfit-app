package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxRawBytes      = 8192
	eventPullRequest = "pull_request"
	sha256Prefix     = "sha256:"
	shaHexLen        = 40
	digestHexLen     = 64
)

// field is one envelope key: its JSON type and its v1 rule.
type field struct {
	name  string
	bits  int                      // 0 = string; otherwise an integer of this size
	check func(e *Envelope) string // the violated rule; "" when the value conforms
}

func fields() []field {
	return []field{
		{"schema_version", 0, func(e *Envelope) string {
			return rule(e.SchemaVersion == SchemaV1, "must equal "+SchemaV1)
		}},
		{"kind", 0, func(e *Envelope) string {
			return rule(e.Kind == KindReport || e.Kind == KindDiscovery || e.Kind == KindBaseline || e.Kind == KindReanchor,
				"must be report, discovery, baseline or reanchor")
		}},
		{"repository", 0, func(e *Envelope) string {
			owner, name, ok := strings.Cut(e.Repository, "/")
			return rule(ok && isName(owner, 100, isRepoChar) && isName(name, 100, isRepoChar), "must be owner/name")
		}},
		{"event", 0, func(e *Envelope) string {
			return rule(isName(e.Event, 64, isEventChar), "must be 1 to 64 characters of a-z and _")
		}},
		{"pull_request", 32, func(e *Envelope) string {
			if e.Event == eventPullRequest {
				return rule(e.PullRequest > 0, "must be > 0 when event is pull_request")
			}
			return rule(e.PullRequest == 0, "must be 0 unless event is pull_request")
		}},
		{"head_sha", 0, func(e *Envelope) string {
			return rule(isHex(e.HeadSHA, shaHexLen), "must be 40 lowercase hex characters")
		}},
		{"base_sha", 0, func(e *Envelope) string { return pullRequestSHA(e, e.BaseSHA) }},
		{"merge_base_sha", 0, func(e *Envelope) string { return pullRequestSHA(e, e.MergeBaseSHA) }},
		{"run_id", 64, func(e *Envelope) string { return rule(e.RunID > 0, "must be > 0") }},
		{"run_attempt", 32, func(e *Envelope) string { return rule(e.RunAttempt >= 1, "must be >= 1") }},
		{"engine_version", 0, func(e *Envelope) string {
			return rule(isName(e.EngineVersion, 64, isVersionChar), "must be 1 to 64 characters of A-Z a-z 0-9 . _ + -")
		}},
		{"image_digest", 0, func(e *Envelope) string { return prefixedDigest(e.ImageDigest) }},
		{"platform", 0, func(e *Envelope) string {
			return rule(isPlatform(e.Platform), "must be os/arch or os/arch/variant, at most 32 characters")
		}},
		{"payload_digest", 0, func(e *Envelope) string { return prefixedDigest(e.PayloadDigest) }},
		{"baseline_digest", 0, func(e *Envelope) string {
			// A baseline upload is the capture itself: the engine loaded no stored baseline to produce it.
			switch e.Kind {
			case KindBaseline:
				return rule(e.BaselineDigest == "", "must be empty when kind is baseline")
			case KindReanchor:
				// A re-anchor is checked against the protected baseline it carries forward: the digest must name one.
				return rule(isHex(e.BaselineDigest, digestHexLen), "must be 64 lowercase hex characters when kind is reanchor")
			}
			return inputDigest(e, e.BaselineDigest)
		}},
		{"labels_digest", 0, func(e *Envelope) string { return inputDigest(e, e.LabelsDigest) }},
	}
}

// Errors name the field and the rule, never the offending value; encoding/json errors quote input, so none is passed on.
func fieldError(name, violated string) error {
	return errors.New("envelope: " + name + ": " + violated)
}

// Decode parses and validates raw against the v1 JSON Schema. Unknown fields,
// a wrong schema_version, malformed SHAs or digests are errors.
func Decode(raw []byte) (Envelope, error) {
	if len(raw) > maxRawBytes {
		return Envelope{}, errors.New("envelope: larger than " + strconv.Itoa(maxRawBytes) + " bytes")
	}
	// encoding/json replaces invalid UTF-8 silently.
	if !utf8.Valid(raw) {
		return Envelope{}, errors.New("envelope: invalid UTF-8")
	}
	fs := fields()
	if err := checkShape(raw, fs); err != nil {
		return Envelope{}, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return Envelope{}, errMalformed()
	}
	for _, f := range fs {
		if violated := f.check(&env); violated != "" {
			return Envelope{}, fieldError(f.name, violated)
		}
	}
	return env, nil
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

func pullRequestSHA(e *Envelope, sha string) string {
	if e.Event == eventPullRequest {
		return rule(isHex(sha, shaHexLen), "must be 40 lowercase hex characters when event is pull_request")
	}
	return rule(sha == "", "must be empty unless event is pull_request")
}

func prefixedDigest(digest string) string {
	hexPart, ok := strings.CutPrefix(digest, sha256Prefix)
	return rule(ok && isHex(hexPart, digestHexLen), "must be sha256: and 64 lowercase hex characters")
}

func inputDigest(e *Envelope, digest string) string {
	if e.Kind == KindDiscovery {
		return rule(digest == "", "must be empty when kind is discovery")
	}
	return rule(digest == "" || isHex(digest, digestHexLen), "must be 64 lowercase hex characters, without prefix, or empty")
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
func isEventChar(c byte) bool  { return isLower(c) || c == '_' }
func isRepoChar(c byte) bool   { return isAlnum(c) || c == '_' || c == '.' || c == '-' }
func isVersionChar(c byte) bool {
	return isAlnum(c) || c == '.' || c == '_' || c == '+' || c == '-'
}
