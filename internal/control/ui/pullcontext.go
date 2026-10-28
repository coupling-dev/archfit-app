package ui

import (
	"encoding/hex"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

// checkContextJSON is display-only context from this App's check, never report admission.
// RunID is deliberately separate from pullBody.RunID, which authorizes artifact lookup.
type checkContextJSON struct {
	RunID          int64             `json:"run_id"`
	RunAttempt     int               `json:"run_attempt"`
	HeadSHA        string            `json:"head_sha"`
	CheckStartedAt string            `json:"check_started_at"`
	ReportDigest   string            `json:"report_digest"`
	Reason         string            `json:"reason"`
	Next           string            `json:"next"`
	Detail         string            `json:"detail"`
	Also           []string          `json:"also"`
	Producer       string            `json:"producer"` // claimed identity, not execution proof
	PolicyDigest   string            `json:"policy_digest"`
	BaselineDigest string            `json:"baseline_digest"`
	LabelsDigest   string            `json:"labels_digest"`
	Schema         string            `json:"schema"` // only from an admitted, digest-matched artifact
	Profile        string            `json:"profile"`
	Approved       *producerPinsJSON `json:"approved,omitempty"`

	// reportDerived: every reason this App recorded for the check is a known, report-derived one, so the report may be
	// shown under an action_required conclusion, as the comment shows it. False for anything unknown or unparsed.
	reportDerived bool
}

type producerPinsJSON struct {
	EngineVersion string `json:"engine_version"`
	ImageDigest   string `json:"image_digest"`
	Platform      string `json:"platform"`
	ActionRef     string `json:"action_ref"`
}

// actionContext projects the App-authored recovery prelude and identity section only.
// Findings and HTML folds may contain repository-controlled text; they are not metadata.
// Historical missing fields stay empty. Current manifest pins are recovery guidance only.
func actionContext(run api.CheckRun) *checkContextJSON {
	out := &checkContextJSON{HeadSHA: run.HeadSHA, Also: []string{}}
	out.RunID, _, _ = parseExternalID(run.ExternalID)
	if out.RunID < 0 {
		out.RunID = 0
	}
	if parts := strings.SplitN(run.ExternalID, ":", 4); len(parts) == 4 && out.RunID > 0 {
		if attempt, err := strconv.Atoi(parts[2]); err == nil && attempt > 0 {
			out.RunAttempt = attempt
		}
		digest := parts[3]
		if raw, ok := strings.CutPrefix(digest, "sha256:"); ok {
			if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
				out.ReportDigest = digest
			}
		}
	}
	if !run.StartedAt.IsZero() {
		out.CheckStartedAt = run.StartedAt.UTC().Format(time.RFC3339)
	}
	if len(run.Summary) > 65535 {
		return out
	}
	prelude, secondary, inputs := true, false, false
	plain := func(s string) string { return html.UnescapeString(strings.ReplaceAll(s, "`", "")) }
	for line := range strings.Lines(run.Summary) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "<") {
			prelude = false
			inputs = line == "### Inputs and identity"
			continue
		}
		if inputs {
			fields := []struct {
				prefix string
				dst    *string
			}{
				{"- Producer identity, claimed: ", &out.Producer},
				{"- Producer identity: ", &out.Producer},
				{"- Policy digest: ", &out.PolicyDigest},
				{"- Baseline digest: ", &out.BaselineDigest},
				{"- Labels digest: ", &out.LabelsDigest},
				{"- Measurement profile: ", &out.Profile},
			}
			for _, f := range fields {
				if value, ok := strings.CutPrefix(line, f.prefix); ok && *f.dst == "" {
					*f.dst = plain(value)
				}
			}
		}
		if !prelude {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- **Next:** "):
			out.Next = plain(strings.TrimPrefix(line, "- **Next:** "))
		case strings.HasPrefix(line, "- Reason: "):
			out.Reason = plain(strings.TrimPrefix(line, "- Reason: "))
		case strings.HasPrefix(line, "- Detail: "):
			out.Detail = plain(strings.TrimPrefix(line, "- Detail: "))
		case line == "Also reported:":
			secondary = true
		case secondary && strings.HasPrefix(line, "- "):
			out.Also = append(out.Also, plain(strings.TrimPrefix(line, "- ")))
		}
	}
	if qualifyHistoricalRecovery(out) {
		current := manifest.Current()
		out.Approved = &producerPinsJSON{EngineVersion: current.EngineVersion, ImageDigest: current.ImageDigest,
			Platform: current.Platform, ActionRef: current.ActionRef}
	}
	return out
}

// recordedRecovery is a display projection of an App-authored prelude, not an admitted report fact.
type recordedRecovery struct {
	reason, detail, next, raw, code string
	reportDerived, known            bool
}

func classifyRecordedRecovery(reason, detail, next string) recordedRecovery {
	out := recordedRecovery{reason: reason, detail: detail, next: next}
	code, rest, opened := strings.Cut(reason, " (")
	category, tail, closed := strings.Cut(rest, ")")
	if opened && closed && tail == "" {
		out.code = code
		out.reportDerived, out.known = gate.ClassifyDisplayReason(code, category)
	}
	return out
}

// qualifyHistoricalRecovery keeps source-independent diagnostics but never promotes rejected report repair claims.
// It mutates only the newly allocated projection; the stored CheckRun and its historical outcome remain unchanged.
func qualifyHistoricalRecovery(out *checkContextJSON) (needsPins bool) {
	records := []recordedRecovery{classifyRecordedRecovery(out.Reason, out.Detail, out.Next)}
	for _, raw := range out.Also {
		reason, detail, _ := strings.Cut(raw, ": ")
		next := ""
		if i := strings.LastIndex(detail, ". Next: "); i >= 0 {
			detail, next = detail[:i], detail[i+len(". Next: "):]
		}
		record := classifyRecordedRecovery(reason, detail, next)
		record.raw = raw
		records = append(records, record)
	}
	sourceAdmissible := true
	for _, record := range records {
		if !record.known || !record.reportDerived {
			sourceAdmissible = false
		}
		if record.known && (record.code == "unknown_engine_identity" || record.code == "workflow_not_approved") {
			needsPins = true
		}
	}
	if sourceAdmissible {
		out.reportDerived = true
		return needsPins
	}
	var diagnostics []recordedRecovery
	withheld := false
	for _, record := range records {
		if record.known && !record.reportDerived {
			diagnostics = append(diagnostics, record)
		} else if record.reason != "" || record.next != "" || record.detail != "" {
			withheld = true
		}
	}
	out.Also = []string{}
	if len(diagnostics) > 0 {
		first := diagnostics[0]
		out.Reason, out.Detail, out.Next = first.reason, first.detail, first.next
		for _, record := range diagnostics[1:] {
			out.Also = append(out.Also, record.raw)
		}
	} else if withheld {
		out.Reason = "Historical recovery context is unclassified"
		out.Detail = ""
		out.Next = "Read the exact check and re-run an approved workflow to obtain usable evidence."
	}
	if withheld {
		out.Also = append(out.Also, "Historical report-derived claims and repair actions are withheld because source authority was not established.")
	}
	if out.Profile != "" {
		out.Profile = "not available for display"
	}
	return needsPins
}
