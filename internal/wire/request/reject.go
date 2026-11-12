package request

import (
	"strconv"
	"time"
)

// Kind names why a request line is not a request.
type Kind string

// The rejections of M2 plan D3, each with one reply.
const (
	KindNoReason      Kind = "no-reason"
	KindReasonTooLong Kind = "reason-too-long"
	KindDate          Kind = "date"
	KindNothingToAsk  Kind = "nothing-to-ask"
	KindNoPermission  Kind = "no-permission"
)

// Rejection is a request the App does not take. Reply is the comment the App posts. Neither repeats the text the
// author wrote.
type Rejection struct {
	Kind  Kind
	reply string
}

func (r *Rejection) Error() string { return "request: " + string(r.Kind) }

// Reply returns the Markdown comment that answers the request.
func (r *Rejection) Reply() string { return r.reply }

func noReason() *Rejection {
	return &Rejection{KindNoReason, "archfit: a request needs a reason: reply `" + Command + " <why you need it>`.\n"}
}

func reasonTooLong(n int) *Rejection {
	return &Rejection{KindReasonTooLong, "archfit: the reason has " + strconv.Itoa(n) + " characters; the limit is " +
		strconv.Itoa(MaxReason) + ". Ask again with a shorter reason.\n"}
}

func badDate(today time.Time) *Rejection {
	from := dateOf(today)
	first, last := from.AddDate(0, 0, 1).Format(time.DateOnly), from.AddDate(0, 0, MaxDaysAhead).Format(time.DateOnly)
	return &Rejection{KindDate, "archfit: `" + expiresFlag + "` needs a date from " + first + " to " + last +
		", written as YYYY-MM-DD.\n"}
}

// NothingToAsk answers a request when no error of the pull request asks for an owner. records are the records of
// its errors as the check prints them, without the status line; empty when it has no error.
func NothingToAsk(records string) *Rejection {
	if records == "" {
		return &Rejection{KindNothingToAsk, "archfit: nothing to ask: no error here needs an owner.\n"}
	}
	return &Rejection{KindNothingToAsk, "archfit: nothing to ask: each error says what to do.\n\n" + textBlock(records)}
}

// NoPermission answers a request whose author may not ask (M2 plan D21).
func NoPermission() *Rejection {
	return &Rejection{KindNoPermission, "archfit: a request counts only from the author of the pull request or from " +
		"a person with write access to the repository.\n"}
}
