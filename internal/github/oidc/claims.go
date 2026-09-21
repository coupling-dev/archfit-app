package oidc

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// payload holds the claims read from a token whose signature is already verified. Unknown claims are ignored.
type payload struct {
	Iss               string          `json:"iss"`
	Aud               json.RawMessage `json:"aud"`
	Exp               *float64        `json:"exp"`
	Nbf               *float64        `json:"nbf"`
	Iat               *float64        `json:"iat"`
	Repository        string          `json:"repository"`
	RepositoryID      json.RawMessage `json:"repository_id"`
	RunID             json.RawMessage `json:"run_id"`
	RunAttempt        json.RawMessage `json:"run_attempt"`
	SHA               string          `json:"sha"`
	Ref               string          `json:"ref"`
	EventName         string          `json:"event_name"`
	JobWorkflowRef    string          `json:"job_workflow_ref"`
	RunnerEnvironment string          `json:"runner_environment"`
}

// readClaims checks issuer, audience, time, then the required claims, in that order.
func readClaims(payloadJSON []byte, audience string, now time.Time) (Claims, error) {
	var p payload
	if json.Unmarshal(payloadJSON, &p) != nil {
		return Claims{}, failed("payload")
	}
	if p.Iss != issuer {
		return Claims{}, failed("issuer")
	}
	if !slices.Contains(audiences(p.Aud), audience) {
		return Claims{}, failed("audience")
	}
	if check := timeCheck(p, now); check != "" {
		return Claims{}, failed(check)
	}

	repositoryID, okRepositoryID := positiveInt(p.RepositoryID, 64)
	runID, okRunID := positiveInt(p.RunID, 64)
	runAttempt, okRunAttempt := positiveInt(p.RunAttempt, 32)
	for _, required := range []struct {
		claim string
		ok    bool
	}{
		{"repository", p.Repository != ""},
		{"repository_id", okRepositoryID},
		{"run_id", okRunID},
		{"run_attempt", okRunAttempt},
		{"sha", p.SHA != ""},
		{"ref", p.Ref != ""},
		{"event_name", p.EventName != ""},
		{"job_workflow_ref", p.JobWorkflowRef != ""},
	} {
		if !required.ok {
			return Claims{}, failed("claim " + required.claim)
		}
	}
	return Claims{
		Repository:        p.Repository,
		RepositoryID:      repositoryID,
		RunID:             runID,
		RunAttempt:        int(runAttempt),
		SHA:               p.SHA,
		Ref:               p.Ref,
		EventName:         p.EventName,
		JobWorkflowRef:    p.JobWorkflowRef,
		RunnerEnvironment: p.RunnerEnvironment,
	}, nil
}

// audiences reads aud as a JSON string or an array of strings; anything else has no values.
func audiences(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// timeCheck returns the name of the failed time check, or "".
func timeCheck(p payload, now time.Time) string {
	at, l := float64(now.Unix()), leeway.Seconds()
	switch {
	case p.Exp == nil:
		return "claim exp"
	case p.Iat == nil:
		return "claim iat"
	case at > *p.Exp+l:
		return "expired"
	case *p.Iat > at+l, p.Nbf != nil && at < *p.Nbf-l:
		return "not yet valid"
	}
	return ""
}

// positiveInt reads a claim GitHub sends as a JSON string of digits; a JSON integer is accepted too.
func positiveInt(raw json.RawMessage, bits int) (int64, bool) {
	text := string(raw)
	if strings.HasPrefix(text, `"`) && json.Unmarshal(raw, &text) != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(text, 10, bits)
	return n, err == nil && n > 0
}

// WorkflowPath extracts "<path>" from a job_workflow_ref for repository fullName; "" if it points elsewhere.
func WorkflowPath(jobWorkflowRef, fullName string) string {
	prefix := fullName + "/"
	if fullName == "" || len(jobWorkflowRef) < len(prefix) || !equalFoldASCII(jobWorkflowRef[:len(prefix)], prefix) {
		return ""
	}
	rest := jobWorkflowRef[len(prefix):]
	// Cutting at the last "@" can only lengthen the path, so "x.yml@evil.yml" never reads as "x.yml".
	at := strings.LastIndex(rest, "@")
	if at < 0 || strings.Contains(rest[:at], "@") {
		return ""
	}
	return rest[:at]
}

// equalFoldASCII compares equal-length strings; strings.EqualFold would also fold non-ASCII look-alikes.
func equalFoldASCII(a, b string) bool {
	for i := range len(a) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
