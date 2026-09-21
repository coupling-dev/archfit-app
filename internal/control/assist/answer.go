package assist

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	maxAnswerBytes = 64 << 10
	codeFence      = "```"
)

// normalizeAnswer turns raw model output into the new policy file, or reports ErrInvalidProposal.
// The reason names the failed check only; the text itself never enters an error.
func normalizeAnswer(text string, draftPolicy []byte) ([]byte, error) {
	reason := ""
	body, fenceOK := stripFence(strings.TrimSpace(text))
	answer := []byte(body + "\n")
	switch {
	case body == "":
		reason = "empty"
	case len(answer) > maxAnswerBytes:
		reason = "too large"
	case !utf8.Valid(answer) || bytes.IndexByte(answer, 0) >= 0:
		reason = "not text"
	case !fenceOK:
		reason = "not a bare YAML file"
	case bytes.Equal(bytes.TrimSpace(answer), bytes.TrimSpace(draftPolicy)):
		reason = "identical to the draft"
	default:
		if _, err := policy.Waivers(answer); err != nil {
			reason = "not a valid policy"
		}
	}
	if reason != "" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidProposal, reason)
	}
	return answer, nil
}

// stripFence removes one Markdown code fence that surrounds the whole text. ok is false when a
// fence line is left over: prose around a fence, a second fence, or a fence that never closes.
func stripFence(s string) (body string, ok bool) {
	if strings.HasPrefix(s, codeFence) && strings.HasSuffix(s, codeFence) {
		_, rest, found := strings.Cut(s, "\n")
		if !found {
			return "", true
		}
		s = strings.TrimSpace(strings.TrimSuffix(rest, codeFence))
	}
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), codeFence) {
			return s, false
		}
	}
	return s, true
}
