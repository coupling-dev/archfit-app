package expiry

import (
	"strconv"
	"time"
)

// The words of the issue and its notes: the App's words, the rule, the modules and the branch in code spans, dates as
// design 5 writes them, issue numbers. No digest (design 5), no "Not used" word.

func place(x Exception) string {
	p := "`" + x.From + "`"
	if x.To != "" {
		p += " -> `" + x.To + "`"
	}
	return p
}

func title(x Exception) string {
	p := x.From
	if x.To != "" {
		p += " -> " + x.To
	}
	return "Archfit: exception " + x.Rule + " " + p + " expires on " + x.Expires
}

// dateLeft writes the date with the days left, as the engine's record does: "2026-11-19 (6 days)", "(1 day)",
// "(expired)".
func dateLeft(x Exception, today time.Time) string {
	switch left := x.daysLeft(today); {
	case left < 0:
		return x.Expires + " (expired)"
	case left == 1:
		return x.Expires + " (1 day)"
	default:
		return x.Expires + " (" + strconv.Itoa(left) + " days)"
	}
}

func body(x Exception, branch string, today time.Time) string {
	b := "The exception for `" + x.Rule + "` from " + place(x) + " in the policy on `" + branch + "` holds until " +
		dateLeft(x, today) + ".\n\n"
	if x.Expired(today) {
		b = "The exception for `" + x.Rule + "` from " + place(x) + " in the policy on `" + branch + "` passed its date: " +
			dateLeft(x, today) + ".\n\n"
	}
	if x.Why != "" {
		b += "Its reason: " + x.Why + "\n\n"
	}
	return b + "After that date, while the code still breaks the rule there, every pull request into `" + branch +
		"` fails with the record of the expired exception, until an owner extends the exception or the code is fixed.\n\n" +
		"The App closes this issue when the policy no longer holds this exception with this date.\n"
}

func extendedNote(date string) string {
	return "The policy now holds this exception until " + date + ". Closed by the App.\n"
}

func removedNote(branch string) string {
	return "The policy on `" + branch + "` no longer holds this exception. Closed by the App.\n"
}

func duplicateNote(first int) string {
	return "Another pass opened #" + strconv.Itoa(first) + " for the same exception first. Closed by the App.\n"
}
