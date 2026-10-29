package decision

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxRawBytes      = 1 << 18
	maxWhyRunes      = 2000
	maxAlternatives  = 20
	maxAlternative   = 500
	maxPolicyRunes   = 65536
	maxPolicyBytes   = 65536
	maxRuleIDLen     = 128
	maxModuleNameLen = 200
	shaHexLen        = 40
	findingHexLen    = 32
	digestHexLen     = 64
)

// kind is the JSON type of a key.
type kind int

const (
	kindString kind = iota
	kindStrings
	kindObject
)

// spec is one key of an object: its JSON type and, for an object, its own keys.
type spec struct {
	name   string
	kind   kind
	nested []spec
}

var requestSpec = []spec{
	{"schema_version", kindString, nil},
	{"finding_id", kindString, nil},
	{"rule_id", kindString, nil},
	{"edge", kindObject, []spec{{"from", kindString, nil}, {"to", kindString, nil}}},
	{"head_sha", kindString, nil},
	{"policy_digest", kindString, nil},
	{"why", kindString, nil},
	{"alternatives_tried", kindStrings, nil},
	{"proposal", kindObject, []spec{{"kind", kindString, nil}, {"policy", kindString, nil}}},
}

// Decode parses and validates raw against the v1 JSON Schema. Unknown keys, duplicate keys, nulls, wrong types and
// every value outside its rule are errors. An error names the field and the rule, never the value.
func Decode(raw []byte) (Request, error) {
	if len(raw) > maxRawBytes {
		return Request{}, errors.New("decision: larger than " + strconv.Itoa(maxRawBytes) + " bytes")
	}
	// encoding/json replaces invalid UTF-8 silently.
	if !utf8.Valid(raw) {
		return Request{}, errors.New("decision: invalid UTF-8")
	}
	if err := checkShape(raw); err != nil {
		return Request{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return Request{}, errMalformed()
	}
	if name, violated := firstViolation(&req); violated != "" {
		return Request{}, fieldError(name, violated)
	}
	return req, nil
}

// Identify returns the finding ID and policy digest raw states when each is well formed, else Unknown. It validates
// nothing else: it names the status comment of a request that failed Decode.
func Identify(raw []byte) (findingID, policyDigest string) {
	var ids struct {
		FindingID    string `json:"finding_id"`
		PolicyDigest string `json:"policy_digest"`
	}
	if len(raw) <= maxRawBytes && json.Unmarshal(raw, &ids) == nil {
		findingID, policyDigest = ids.FindingID, ids.PolicyDigest
	}
	if !isHex(findingID, findingHexLen) {
		findingID = Unknown
	}
	if !isHex(policyDigest, digestHexLen) {
		policyDigest = Unknown
	}
	return findingID, policyDigest
}

func errMalformed() error { return errors.New("decision: malformed JSON") }

// Errors name the field and the rule, never the offending value; encoding/json errors quote input, so none is passed on.
func fieldError(name, violated string) error {
	return errors.New("decision: " + name + ": " + violated)
}

// checkShape is the strictness encoding/json lacks: one top-level object whose keys are exactly the contract's, each
// once, none null, each of the right JSON type, and nothing after the object.
func checkShape(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("decision: must be a JSON object")
	}
	if err := checkObject(dec, requestSpec, ""); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("decision: trailing data after the object")
	}
	return nil
}

// checkObject reads the members of an object whose opening brace is consumed, through its closing brace.
func checkObject(dec *json.Decoder, specs []spec, prefix string) error {
	seen := make(map[string]bool, len(specs))
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return errMalformed()
		}
		s, known := lookup(specs, key)
		if !known {
			return errors.New("decision: " + prefix + "unknown key")
		}
		name := prefix + s.name
		if seen[s.name] {
			return fieldError(name, "duplicate key")
		}
		seen[s.name] = true
		value, err := dec.Token()
		if err != nil {
			return errMalformed()
		}
		if violated := checkValue(dec, s, name, value); violated != nil {
			return violated
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return errMalformed()
	}
	for _, s := range specs {
		if !seen[s.name] {
			return fieldError(prefix+s.name, "required")
		}
	}
	return nil
}

// checkValue checks the value of one key, whose first token is value, and reads the rest of it.
func checkValue(dec *json.Decoder, s spec, name string, value json.Token) error {
	if value == nil {
		return fieldError(name, "must not be null")
	}
	switch s.kind {
	case kindString:
		if _, ok := value.(string); !ok {
			return fieldError(name, "must be a string")
		}
	case kindObject:
		if value != json.Delim('{') {
			return fieldError(name, "must be an object")
		}
		return checkObject(dec, s.nested, name+".")
	case kindStrings:
		if value != json.Delim('[') {
			return fieldError(name, "must be an array of strings")
		}
		for dec.More() {
			item, err := dec.Token()
			if err != nil {
				return errMalformed()
			}
			if _, ok := item.(string); !ok {
				return fieldError(name, "must be an array of strings")
			}
		}
		if _, err := dec.Token(); err != nil { // the closing bracket
			return errMalformed()
		}
	}
	return nil
}

func lookup(specs []spec, key json.Token) (spec, bool) {
	for _, s := range specs {
		if key == s.name {
			return s, true
		}
	}
	return spec{}, false
}

// firstViolation returns the first key, in contract order, whose value breaks its rule.
func firstViolation(r *Request) (name, violated string) {
	checks := []struct {
		name     string
		violated string
	}{
		{"schema_version", rule(r.SchemaVersion == SchemaV1, "must equal "+SchemaV1)},
		{"finding_id", rule(isHex(r.FindingID, findingHexLen), "must be 32 lowercase hex characters")},
		{"rule_id", identifier(r.RuleID, maxRuleIDLen)},
		{"edge.from", identifier(r.Edge.From, maxModuleNameLen)},
		{"edge.to", identifier(r.Edge.To, maxModuleNameLen)},
		{"head_sha", rule(isHex(r.HeadSHA, shaHexLen), "must be 40 lowercase hex characters")},
		{"policy_digest", rule(isHex(r.PolicyDigest, digestHexLen), "must be 64 lowercase hex characters")},
		{"why", text(r.Why, maxWhyRunes)},
		{"alternatives_tried", alternatives(r.AlternativesTried)},
		{"proposal.kind", rule(r.Proposal.Kind == ProposalPolicyPatch, "must equal "+ProposalPolicyPatch)},
		{"proposal.policy", policyText(r.Proposal.Policy)},
	}
	for _, c := range checks {
		if c.violated != "" {
			return c.name, c.violated
		}
	}
	return "", ""
}

func alternatives(list []string) string {
	if len(list) > maxAlternatives {
		return "must have at most " + strconv.Itoa(maxAlternatives) + " entries"
	}
	for _, s := range list {
		if violated := text(s, maxAlternative); violated != "" {
			return "every entry " + violated
		}
	}
	return ""
}

func policyText(s string) string {
	if violated := text(s, maxPolicyRunes); violated != "" {
		return violated
	}
	return rule(len(s) <= maxPolicyBytes, "must be at most "+strconv.Itoa(maxPolicyBytes)+" bytes")
}

// text is the rule of free text: 1 to limit characters, and no control character but newline and tab. U+2028 and
// U+2029 count as control: a store that escapes them would grow a value past its limit.
func text(s string, limit int) string {
	if n := utf8.RuneCountInString(s); n < 1 || n > limit {
		return "must be 1 to " + strconv.Itoa(limit) + " characters"
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || r == ' ' || r == ' ' {
			return "must not contain control characters other than newline and tab"
		}
	}
	return ""
}

func identifier(s string, limit int) string {
	return rule(isName(s, limit, isIdentifierChar), "must be 1 to "+strconv.Itoa(limit)+" characters of A-Z a-z 0-9 _ . : / @ + -")
}

func rule(ok bool, text string) string {
	if ok {
		return ""
	}
	return text
}

func isHex(s string, n int) bool {
	return len(s) == n && isName(s, n, func(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' })
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

func isIdentifierChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("_.:/@+-", c) >= 0
}

// ValidIDs reports whether findingID and policyDigest have the shape a request states them in: 32 and 64 lowercase
// hex characters. A caller that takes them from a URL checks them before it names a record.
func ValidIDs(findingID, policyDigest string) bool {
	return isHex(findingID, findingHexLen) && isHex(policyDigest, digestHexLen)
}
