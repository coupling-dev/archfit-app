package expiry

import (
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/markdown"
)

// The words of the issue and its notes: the App's words, the policy's names and reason in code spans, dates as design 5
// writes them, issue numbers. No digest (design 5), no "Not used" word.

// maxWhyBytes and maxTitleBytes bound what the policy's text adds to an issue.
const (
	maxWhyBytes   = 1000
	maxNameBytes  = 500
	maxTitleBytes = 200
)

func place(x Exception) string {
	p := markdown.CodeSpan(markdown.Clip(x.From, maxNameBytes))
	if x.To != "" {
		p += " -> " + markdown.CodeSpan(markdown.Clip(x.To, maxNameBytes))
	}
	return p
}

func title(x Exception) string {
	p := x.From
	if x.To != "" {
		p += " -> " + x.To
	}
	return markdown.Clip(markdown.OneLine("Archfit: exception "+x.Rule+" "+p+" expires on "+x.Expires), maxTitleBytes)
}

// dateLeft writes the date with the days left, as the engine's record does: "2026-11-19 (6 days)", "(1 day)",
// "(expired)".
func dateLeft(x Exception, today time.Time) string {
	switch left, _ := x.daysLeft(today); {
	case left < 0:
		return x.Expires + " (expired)"
	case left == 1:
		return x.Expires + " (1 day)"
	default:
		return x.Expires + " (" + strconv.Itoa(left) + " days)"
	}
}

// body is the issue's text. The rule, the modules, the branch and the owner's reason are the policy's text: each is in
// a code span, so none of it mentions, links or formats.
func body(x Exception, branch string, today time.Time) string {
	head := "The exception for " + markdown.CodeSpan(markdown.Clip(x.Rule, maxNameBytes)) + " from " + place(x) + " in the policy on " +
		markdown.CodeSpan(markdown.Clip(branch, maxNameBytes))
	b := head + " holds until " + dateLeft(x, today) + ".\n\n"
	if x.Expired(today) {
		b = head + " passed its date: " + dateLeft(x, today) + ".\n\n"
	}
	if x.Why != "" {
		b += "Its reason: " + markdown.CodeSpan(markdown.Clip(x.Why, maxWhyBytes)) + "\n\n"
	}
	return b + "After that date, while the code still breaks the rule there, every pull request into " +
		markdown.CodeSpan(markdown.Clip(branch, maxNameBytes)) + " fails with the record of the expired exception, until an owner extends the " +
		"exception or the code is fixed.\n\nThe App closes this issue when the policy no longer holds this exception " +
		"with this date.\n"
}

func extendedNote(date string) string {
	return "The policy now holds this exception until " + date + ". Closed by the App.\n"
}

func removedNote(branch string) string {
	return "The policy on " + markdown.CodeSpan(markdown.Clip(branch, maxNameBytes)) + " no longer holds this exception. Closed by the App.\n"
}

func duplicateNote(first int) string {
	return "Another pass opened #" + strconv.Itoa(first) + " for the same exception first. Closed by the App.\n"
}
