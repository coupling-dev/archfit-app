package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	targetRefPrefix = "refs/heads/"
	maxBranchBytes  = 244 // a target_ref is at most 255 bytes
)

// fieldV2 is one envelope 2 key: its JSON type (0 = string, otherwise an integer of this size) and its rule.
type fieldV2 struct {
	name  string
	bits  int
	check func(e *EnvelopeV2) string // the violated rule; "" when the value conforms
}

func fieldsV2() []fieldV2 {
	sha := func(v string) string { return rule(isHex(v, shaHexLen), "must be 40 lowercase hex characters") }
	return []fieldV2{
		{"schema_version", 0, func(e *EnvelopeV2) string {
			return rule(e.SchemaVersion == SchemaV2, "must equal "+SchemaV2)
		}},
		{"repository", 0, func(e *EnvelopeV2) string {
			owner, name, ok := strings.Cut(e.Repository, "/")
			return rule(ok && isName(owner, 100, isRepoChar) && isName(name, 100, isRepoChar), "must be owner/name")
		}},
		{"event", 0, func(e *EnvelopeV2) string {
			return rule(e.Event == EventPullRequest || e.Event == EventPush || e.Event == EventMergeGroup,
				"must be pull_request, push or merge_group")
		}},
		{"pull_request", 32, func(e *EnvelopeV2) string {
			if e.Event == EventPullRequest {
				return rule(e.PullRequest > 0, "must be > 0 when event is pull_request")
			}
			return rule(e.PullRequest == 0, "must be 0 unless event is pull_request")
		}},
		{"head_sha", 0, func(e *EnvelopeV2) string { return sha(e.HeadSHA) }},
		{"merge_base_sha", 0, func(e *EnvelopeV2) string { return sha(e.MergeBaseSHA) }},
		{"target_ref", 0, func(e *EnvelopeV2) string {
			branch, ok := strings.CutPrefix(e.TargetRef, targetRefPrefix)
			return rule(ok && isName(branch, maxBranchBytes, isRefChar),
				"must be refs/heads/ and 1 to 244 printable ASCII characters without a space")
		}},
		{"target_sha", 0, func(e *EnvelopeV2) string { return sha(e.TargetSHA) }},
		{"policy_digest", 0, func(e *EnvelopeV2) string { return prefixedDigest(e.PolicyDigest) }},
		{"run_id", 64, func(e *EnvelopeV2) string { return rule(e.RunID > 0, "must be > 0") }},
		{"run_attempt", 32, func(e *EnvelopeV2) string { return rule(e.RunAttempt >= 1, "must be >= 1") }},
		{"engine_version", 0, func(e *EnvelopeV2) string {
			return rule(isName(e.EngineVersion, 64, isVersionChar), "must be 1 to 64 characters of A-Z a-z 0-9 . _ + -")
		}},
		{"image_digest", 0, func(e *EnvelopeV2) string { return prefixedDigest(e.ImageDigest) }},
		{"platform", 0, func(e *EnvelopeV2) string {
			return rule(isPlatform(e.Platform), "must be os/arch or os/arch/variant, at most 32 characters")
		}},
		{"payload_digest", 0, func(e *EnvelopeV2) string { return prefixedDigest(e.PayloadDigest) }},
	}
}

// DecodeV2 parses raw strictly against the envelope 2 JSON Schema and checks that payload, the result JSON v1 read
// as bytes and never parsed here, is what payload_digest names. Unknown keys (every v1-only key among them),
// duplicate, null or absent keys, a wrong schema_version, malformed values and a payload of other bytes are errors.
// The caller bounds the payload size.
func DecodeV2(raw, payload []byte) (EnvelopeV2, error) {
	if len(raw) > maxRawBytes {
		return EnvelopeV2{}, errors.New("envelope: larger than " + strconv.Itoa(maxRawBytes) + " bytes")
	}
	// encoding/json replaces invalid UTF-8 silently.
	if !utf8.Valid(raw) {
		return EnvelopeV2{}, errors.New("envelope: invalid UTF-8")
	}
	fs := fieldsV2()
	// The v1 pre-pass reads only each key's name and JSON type; envelope 2 has no object-valued key.
	shape := make([]field, len(fs))
	for i, f := range fs {
		shape[i] = field{name: f.name, bits: f.bits}
	}
	if err := checkShape(raw, shape); err != nil {
		return EnvelopeV2{}, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env EnvelopeV2
	if err := dec.Decode(&env); err != nil {
		return EnvelopeV2{}, errMalformed()
	}
	for _, f := range fs {
		if violated := f.check(&env); violated != "" {
			return EnvelopeV2{}, fieldError(f.name, violated)
		}
	}
	if PayloadDigest(payload) != env.PayloadDigest {
		return EnvelopeV2{}, fieldError("payload_digest", "does not match the payload")
	}
	return env, nil
}

// isRefChar accepts printable ASCII without the space. Git forbids more; GitHub's value is compared in ingest.
func isRefChar(c byte) bool { return '!' <= c && c <= '~' }
