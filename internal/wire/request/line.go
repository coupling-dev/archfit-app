package request

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Command starts a request line.
const Command = "/archfit request"

const (
	expiresFlag = "--expires"
	// MaxReason is the longest reason, in characters.
	MaxReason = 500
	// MaxDaysAhead is how far after the App's date --expires may be.
	MaxDaysAhead = 365
)

// Line is a parsed request line: the fields the worker wrote.
type Line struct {
	Reason  string `json:"reason"`
	Expires string `json:"expires,omitempty"` // YYYY-MM-DD; empty when not asked
}

// ParseComment reads the request of a pull-request comment. ok is false when no line is a request line: the comment
// is not a request and gets no reply. A request line that cannot be used returns a *Rejection. today is the App's
// date; its year, month and day are used.
func ParseComment(body string, today time.Time) (line Line, ok bool, err error) {
	for _, l := range lines(body) {
		if rest, found := command(l); found {
			line, err = parse(rest, today)
			return line, true, err
		}
	}
	return Line{}, false, nil
}

// lines splits text at LF, CRLF and CR.
func lines(text string) []string {
	return strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
}

// command returns what follows the command on a request line.
func command(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, Command)
	if !ok {
		return "", false
	}
	if r, _ := utf8.DecodeRuneInString(rest); rest != "" && !unicode.IsSpace(r) {
		return "", false
	}
	return rest, true
}

func parse(rest string, today time.Time) (Line, error) {
	text := strings.TrimSpace(rest)
	reason, date, flagged := text, "", false
	head, last := lastWord(text)
	if last == expiresFlag {
		reason, flagged = head, true
	} else if before, prev := lastWord(head); prev == expiresFlag {
		reason, date, flagged = before, last, true
	}
	reason = strings.TrimSpace(reason)
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		return Line{}, noReason()
	case n > MaxReason:
		return Line{}, reasonTooLong(n)
	}
	if flagged && !inRange(date, today) {
		return Line{}, badDate(today)
	}
	return Line{Reason: reason, Expires: date}, nil
}

// lastWord splits s, trimmed on the right, into what comes before its last word and that word.
func lastWord(s string) (head, last string) {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	i := strings.LastIndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return "", s
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return strings.TrimRightFunc(s[:i], unicode.IsSpace), s[i+size:]
}

// inRange reports whether date is a real day after today and at most MaxDaysAhead days after it.
func inRange(date string, today time.Time) bool {
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return false
	}
	from := dateOf(today)
	return day.After(from) && !day.After(from.AddDate(0, 0, MaxDaysAhead))
}

func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
