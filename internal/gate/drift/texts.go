package drift

import (
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/markdown"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The bounds of the issue: the records of the drift errors as the check prints them, and the places the check could
// not read, each line of those clipped. Together with the App's words they stay far below GitHub's 65,536 characters,
// whatever the engine's block holds. What does not fit is counted.
const (
	maxRecordBytes = 30000
	maxPlacesBytes = 20000
	maxPlaceBytes  = 500
	maxUnread      = 50
	maxTitleBytes  = 200
	maxBranchBytes = 500
)

// The words of the issue and its notes: the App's words and commit SHAs. The branch is in a code span, and the engine's
// records and places, which come from the customer's tree, are in text blocks their content cannot close, so no part
// of them mentions, links or formats. No digest (design 5).

func title(branch string) string {
	return markdown.Clip("Archfit: drift on "+markdown.OneLine(branch), maxTitleBytes)
}

// body is the issue body for the repository block of run: its drift errors and the places the check could not read.
func body(run Run) string {
	branch, head := markdown.CodeSpan(markdown.Clip(run.Repo.DefaultBranch, maxBranchBytes)), run.Head
	drift, unread := run.State.Drift, run.State.Unread
	var b strings.Builder
	if len(drift) > 0 {
		b.WriteString(branch + " has errors that no exception covers and that no open change added. This is drift: " +
			"it stays until the code is fixed or an owner adds an exception. Pull requests are not affected.\n\n")
		b.WriteString("At commit " + head + ", " + errorCount(len(drift)) + ":\n\n" + markdown.Fenced(errorRecords(drift)))
		if len(unread) > 0 {
			b.WriteString("\nThe check could not read these places, so the list may be incomplete:\n\n" +
				markdown.Fenced(places(unread)))
		}
	} else {
		b.WriteString("At commit " + head + ", " + branch + " has no error outside the exceptions in what the check " +
			"read. The check could not read these places, so drift there is unknown:\n\n" + markdown.Fenced(places(unread)))
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
		more := "and " + strconv.Itoa(len(errs)-i) + " more\n"
		if len(markdown.Fenced(b.String()+rec+more)) > maxRecordBytes {
			b.WriteString(more)
			break
		}
		b.WriteString(rec)
	}
	return b.String()
}

// places lists the places the check could not read, one clipped line each, with the step that makes each readable.
func places(unread []result.UnreadPlace) string {
	var b strings.Builder
	for i, u := range unread {
		where := "the whole tree"
		if u.Path != "" {
			where = u.Path
			if u.Line > 0 {
				where += ":" + strconv.Itoa(u.Line)
			}
		}
		line := where + ": " + u.Reason
		if u.Fix != "" {
			line += " (to read it: " + u.Fix + ")"
		}
		line = markdown.Clip(markdown.OneLine(line), maxPlaceBytes) + "\n"
		more := "and " + strconv.Itoa(len(unread)-i) + " more places\n"
		if i == maxUnread || len(markdown.Fenced(b.String()+line+more)) > maxPlacesBytes {
			b.WriteString(more)
			break
		}
		b.WriteString(line)
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
	return "At commit " + head + ", " + markdown.CodeSpan(markdown.Clip(branch, maxBranchBytes)) + " has no error outside the exceptions, and the " +
		"check read every place. Closed by the App.\n"
}

func duplicateNote(first int) string {
	return "Another delivery of the same push opened #" + strconv.Itoa(first) + " first. Closed by the App.\n"
}
