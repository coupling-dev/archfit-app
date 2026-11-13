package drift

import (
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The bounds of the issue body, far below GitHub's 65,536 characters: the records of the drift errors, as the check
// prints them, and the places the check could not read. What does not fit is counted.
const (
	maxRecordBytes = 30000
	maxUnread      = 50
)

// The words of the issue and its notes: the App's words, the branch in a code span, issue numbers and commit SHAs,
// and the engine's records as the check prints them. No digest (design 5).

func title(branch string) string { return "Archfit: drift on " + branch }

// body is the issue body for the repository block of run: its drift errors and the places the check could not read.
func body(run Run) string {
	branch, head := "`"+run.Repo.DefaultBranch+"`", run.Head
	drift, unread := run.State.Drift, run.State.Unread
	var b strings.Builder
	if len(drift) > 0 {
		b.WriteString(branch + " has errors that no exception covers and that no open change added. This is drift: " +
			"it stays until the code is fixed or an owner adds an exception. Pull requests are not affected.\n\n")
		b.WriteString("At commit " + head + ", " + errorCount(len(drift)) + ":\n\n```text\n" + errorRecords(drift) + "```\n")
		if len(unread) > 0 {
			b.WriteString("\nThe check could not read these places, so the list may be incomplete:\n\n" + places(unread))
		}
	} else {
		b.WriteString("At commit " + head + ", " + branch + " has no error outside the exceptions in what the check " +
			"read. The check could not read these places, so drift there is unknown:\n\n" + places(unread))
	}
	b.WriteString("\nThe App edits this issue after each push to " + branch + ", and closes it when no error is left " +
		"and every place was read.\n")
	return b.String()
}

// errorRecords prints the drift errors as the check prints them, without a status line, as many as fit.
func errorRecords(errs []result.Error) string {
	var b strings.Builder
	for i, e := range errs {
		rec := strings.TrimSuffix(result.Record(result.Result{Errors: []result.Error{e}}), "\n")
		if b.Len()+len(rec) > maxRecordBytes {
			b.WriteString("and " + strconv.Itoa(len(errs)-i) + " more\n")
			break
		}
		b.WriteString(rec)
	}
	return b.String()
}

// places lists the places the check could not read, with the step that makes each readable.
func places(unread []result.UnreadPlace) string {
	var b strings.Builder
	for i, u := range unread {
		if i == maxUnread {
			b.WriteString("- and " + strconv.Itoa(len(unread)-i) + " more places\n")
			break
		}
		where := "the whole tree"
		if u.Path != "" {
			where = "`" + u.Path + "`"
			if u.Line > 0 {
				where = "`" + u.Path + ":" + strconv.Itoa(u.Line) + "`"
			}
		}
		b.WriteString("- " + where + ": " + u.Reason)
		if u.Fix != "" {
			b.WriteString(". To read it: " + u.Fix)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func errorCount(n int) string {
	if n == 1 {
		return "1 error"
	}
	return strconv.Itoa(n) + " errors"
}

func closedNote(branch, head string) string {
	return "At commit " + head + ", `" + branch + "` has no error outside the exceptions, and the check read every place. " +
		"Closed by the App.\n"
}

func duplicateNote(first int) string {
	return "Another delivery of the same push opened #" + strconv.Itoa(first) + " first. Closed by the App.\n"
}
