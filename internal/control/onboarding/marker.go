package onboarding

import (
	"fmt"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	markerPrefix = "<!-- archfit:managed "
	markerSuffix = " -->"

	markerRunning  = "running"
	markerFailed   = "failed"
	markerComplete = "complete"
)

// Failure codes shown in the managed PR body and the Progress diagnostic.
const (
	codeToken       = "token_error"
	codeUnavailable = "sandbox_unavailable"
	codeTimeout     = "timeout"
	codeSandbox     = "sandbox_failed"
	codeDraft       = "invalid_draft"
	codeGitHub      = "github_error"
)

// marker is the managed job state: one hidden line in the onboarding PR body.
type marker struct {
	State   string
	Started time.Time // running only
	Code    string    // failed only
}

func (m marker) String() string {
	fields := "state=" + m.State
	switch m.State {
	case markerRunning:
		fields += " started=" + m.Started.UTC().Format(time.RFC3339)
	case markerFailed:
		fields += " code=" + m.Code
	}
	return markerPrefix + fields + markerSuffix
}

// parseMarker reads the last marker line of body. A marker that does not parse is found with a zero
// value, which every reader treats as stalled.
func parseMarker(body string) (m marker, found bool) {
	lines := strings.Split(body, "\n")
	for i := len(lines) - 1; i >= 0 && !found; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, markerPrefix) || !strings.HasSuffix(line, markerSuffix) {
			continue
		}
		found = true
		for _, field := range strings.Fields(line[len(markerPrefix) : len(line)-len(markerSuffix)]) {
			key, value, _ := strings.Cut(field, "=")
			switch key {
			case "state":
				m.State = value
			case "started":
				m.Started, _ = time.Parse(time.RFC3339, value)
			case "code":
				m.Code = value
			}
		}
	}
	ok := false
	switch m.State {
	case markerRunning:
		ok = !m.Started.IsZero() && m.Code == ""
	case markerFailed:
		ok = knownCode(m.Code) && m.Started.IsZero()
	case markerComplete:
		ok = m.Code == "" && m.Started.IsZero()
	}
	if !ok {
		return marker{}, found
	}
	return m, found
}

func knownCode(code string) bool {
	switch code {
	case codeToken, codeUnavailable, codeTimeout, codeSandbox, codeDraft, codeGitHub:
		return true
	}
	return false
}

// running reports a job that may still be alive: no second job is started for it.
func (m marker) running(now time.Time) bool {
	return m.State == markerRunning && now.Sub(m.Started) < stalledAfter
}

// managedDiagnostic is "" unless p is an open pull request whose body carries the managed marker.
func managedDiagnostic(p api.Pull, now time.Time) string {
	m, found := parseMarker(p.Body)
	if p.State != "open" || !found {
		return ""
	}
	switch {
	case m.running(now):
		return "Managed discovery running since " + m.Started.UTC().Format(time.RFC3339)
	case m.State == markerFailed:
		return "Managed discovery failed (`" + m.Code + "`) — retry"
	case m.State == markerComplete:
		return fmt.Sprintf("Review and merge PR #%d", p.Number)
	}
	return "Managed discovery stalled — retry"
}
