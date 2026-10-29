package decision

import (
	"errors"
	"strings"
)

// The block an agent posts. The fenced JSON between the markers is the request.
const (
	OpenMarker  = "<!-- archfit:decision-request -->"
	CloseMarker = "<!-- /archfit:decision-request -->"

	fence = "```"
	// maxCommentBytes bounds what Extract scans. GitHub limits a comment to 65536 characters, 262144 bytes at most.
	maxCommentBytes = 1 << 18
)

// ErrNoRequest means the comment holds no request block at all. Every other Extract error means a block that is
// there and cannot be used.
var ErrNoRequest = errors.New("decision: no decision request block")

// HasRequest is the cheap precheck: whether the comment opens a request block.
func HasRequest(commentBody string) bool {
	return strings.Contains(scanned(commentBody), OpenMarker)
}

// Extract returns the JSON of the one request block in a comment. No block is ErrNoRequest; a block that is not
// closed, not a ```json fence, or a second block is an error that names the rule.
func Extract(commentBody string) ([]byte, error) {
	body := scanned(commentBody)
	switch opens := strings.Count(body, OpenMarker); {
	case opens == 0:
		return nil, ErrNoRequest
	case opens > 1:
		return nil, errors.New("decision: more than one request block")
	}
	switch closes := strings.Count(body, CloseMarker); {
	case closes == 0:
		return nil, errors.New("decision: request block is not closed")
	case closes > 1:
		return nil, errors.New("decision: more than one request block end")
	}
	_, afterOpen, _ := strings.Cut(body, OpenMarker)
	inner, _, ok := strings.Cut(afterOpen, CloseMarker)
	if !ok {
		return nil, errors.New("decision: request block ends before it begins")
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(inner), fence+"json")
	if !ok || !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return nil, errors.New("decision: request block must hold one ```json fence")
	}
	rest, ok = strings.CutSuffix(rest, fence)
	if !ok {
		return nil, errors.New("decision: request block must hold one ```json fence")
	}
	return []byte(rest), nil
}

func scanned(body string) string { return body[:min(len(body), maxCommentBytes)] }
