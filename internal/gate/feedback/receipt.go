package feedback

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	gateCheck  = "archfit/gate"
	stateCheck = "archfit/state"

	statusInProgress = "in_progress"
	statusCompleted  = "completed"

	markerClose   = " -->"
	commentMarker = "<!-- archfit:comment -->"
	headOpen      = "<!-- archfit:head "
)

// receiptV1 starts the external_id of a v1 check run.
const receiptV1 = "v1"

// externalRun reads the version, run and attempt of the external_id of a v1 or an Archfit 4 publication,
// "<version>:<run_id>:<attempt>:<digest>".
func externalRun(id string) (version string, runID int64, attempt int, ok bool) {
	parts := strings.SplitN(id, ":", 4)
	if len(parts) != 4 || parts[0] != receiptV1 && parts[0] != receiptV2 {
		return "", 0, 0, false
	}
	runID, errRun := strconv.ParseInt(parts[1], 10, 64)
	attempt, errAttempt := strconv.Atoi(parts[2])
	return parts[0], runID, attempt, errRun == nil && errAttempt == nil
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
