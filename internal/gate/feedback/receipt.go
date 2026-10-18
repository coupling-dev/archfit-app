package feedback

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	gateCheck  = "archfit/gate"
	stateCheck = "archfit/state"

	statusInProgress = "in_progress"
	statusCompleted  = "completed"

	receiptOpen   = "<!-- archfit:receipt "
	markerClose   = " -->"
	commentMarker = "<!-- archfit:comment -->"
	headOpen      = "<!-- archfit:head "
	waiverOpen    = "<!-- archfit:waiver "
	waiverClose   = "<!-- /archfit:waiver -->"
	tasksOpen     = "<!-- archfit:agent-tasks -->"
	tasksClose    = "<!-- /archfit:agent-tasks -->"
)

func externalID(r Receipt) string {
	return fmt.Sprintf("v1:%d:%d:%s", r.RunID, r.RunAttempt, r.ReportDigest)
}

// receiptMarker relies on encoding/json escaping "<" and ">": no field value can close the marker.
func receiptMarker(r Receipt) string {
	raw, err := json.Marshal(r)
	if err != nil { // unreachable: Receipt holds only strings, integers and a time
		raw = []byte("{}")
	}
	return receiptOpen + string(raw) + markerClose
}

// parseReceipt reads the last receipt marker of a summary.
func parseReceipt(summary string) (Receipt, bool) {
	i := strings.LastIndex(summary, receiptOpen)
	if i < 0 {
		return Receipt{}, false
	}
	raw, _, ok := strings.Cut(summary[i+len(receiptOpen):], markerClose)
	if !ok {
		return Receipt{}, false
	}
	var r Receipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Receipt{}, false
	}
	return r, true
}

// supersedes reports whether the existing check run holds a newer run than r and must be left alone.
func supersedes(existing api.CheckRun, r Receipt) bool {
	if existing.Status != statusCompleted {
		return false
	}
	old, ok := parseReceipt(existing.Summary)
	if !ok {
		return false
	}
	return newerRun(old.RunID, old.RunAttempt, r)
}

func newerRun(runID int64, attempt int, r Receipt) bool {
	return runID > r.RunID || runID == r.RunID && attempt > r.RunAttempt
}

func headMarker(headSHA string, r Receipt) string {
	return fmt.Sprintf("%s%s run %d.%d%s", headOpen, sanitize(headSHA), r.RunID, r.RunAttempt, markerClose)
}

// parseHead reads the head marker on the second line of a comment.
func parseHead(body string) (headSHA string, runID int64, attempt int, ok bool) {
	lines := strings.SplitN(body, "\n", 3)
	if len(lines) < 2 || !strings.HasPrefix(lines[1], headOpen) {
		return "", 0, 0, false
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(lines[1], headOpen), markerClose))
	if len(fields) != 3 || fields[1] != "run" {
		return "", 0, 0, false
	}
	run, att, ok := strings.Cut(fields[2], ".")
	if !ok {
		return "", 0, 0, false
	}
	runID, errRun := strconv.ParseInt(run, 10, 64)
	attempt, errAttempt := strconv.Atoi(att)
	return fields[0], runID, attempt, errRun == nil && errAttempt == nil
}

func waiverDate(expiry time.Time) string {
	return expiry.UTC().Format(time.DateOnly)
}

func waiverSection(date string) string {
	return waiverOpen + date + markerClose + "\n" +
		"> **Waiver expiry:** a waiver ends on " + date + ". After that date its findings block again. Renew the waiver or fix the findings before then.\n" +
		waiverClose
}

// currentWaiver returns the date of the waiver section in a comment body.
func currentWaiver(body string) (string, bool) {
	_, rest, ok := strings.Cut(body, "\n"+waiverOpen)
	if !ok {
		return "", false
	}
	date, _, ok := strings.Cut(rest, markerClose)
	return date, ok
}

// placeWaiver puts the section after the verdict block: before the first "### " heading, else at the end.
// Publish and Warn both use it, so they agree byte for byte.
func placeWaiver(body, section string) string {
	if section == "" {
		return body
	}
	i := strings.Index(body, "\n\n### ")
	if j := strings.Index(body, "\n\n<details>"); j >= 0 && (i < 0 || j < i) { // the notice stays outside folds
		i = j
	}
	if i < 0 {
		return body + "\n\n" + section
	}
	return body[:i] + "\n\n" + section + body[i:]
}

func removeWaiver(body string) string {
	i := strings.Index(body, "\n\n"+waiverOpen)
	if i < 0 {
		return body
	}
	j := strings.Index(body[i:], waiverClose)
	if j < 0 {
		return body
	}
	return body[:i] + body[i+j+len(waiverClose):]
}
