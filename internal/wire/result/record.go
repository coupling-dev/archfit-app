package result

import (
	"strconv"
	"strings"
)

// noteGap separates a next step from its note, as in design 8.1.
const noteGap = "     "

// Record returns the text record of r with the grammar of the engine's internal/output/record:
//
//	record      = { error } { unknown } status-line
//	error       = path [ ":" line ] ": " rule-id ": " what NL fields next-line
//	unknown     = [ path [ ":" line ] ": " ] cause ": " what NL fields next-line
//	fields      = [ "  why: " text NL ] [ "  instead: " text NL ]
//	              [ "  balance: " n "/10 (min " m ")" NL ]
//	next-line   = "  next: " text [ "     (" note ")" ] NL
//	status-line = summary NL
//
// It prints the producer's text as given and decides nothing. It is the record without --verbose: digests and the
// engine version stay out of the check and the comment (design 5).
func Record(r Result) string {
	var b strings.Builder
	for _, e := range r.Errors {
		writeHead(&b, location(e.Path, e.Line), e.Rule, e.What)
		writeDetails(&b, e.Why, e.Instead, e.Balance, e.Next)
	}
	for _, u := range r.Unknown {
		writeHead(&b, location(u.Path, u.Line), u.Cause, u.What)
		writeDetails(&b, u.Why, "", nil, u.Next)
	}
	b.WriteString(r.Summary + "\n")
	return b.String()
}

func location(path string, line int) string {
	if line == 0 {
		return path
	}
	return path + ":" + strconv.Itoa(line)
}

// writeHead writes "where: label: what"; an empty where or what drops its part, as the engine does.
func writeHead(b *strings.Builder, where, label, what string) {
	if where != "" {
		b.WriteString(where + ": ")
	}
	b.WriteString(label)
	if what != "" {
		b.WriteString(": " + what)
	}
	b.WriteString("\n")
}

func writeDetails(b *strings.Builder, why, instead string, balance *Balance, next Next) {
	if why != "" {
		b.WriteString("  why: " + why + "\n")
	}
	if instead != "" {
		b.WriteString("  instead: " + instead + "\n")
	}
	if balance != nil {
		b.WriteString("  balance: " + strconv.Itoa(balance.Value) + "/10 (min " + strconv.Itoa(balance.Min) + ")\n")
	}
	b.WriteString("  next: " + next.Text)
	if next.Note != "" {
		b.WriteString(noteGap + "(" + next.Note + ")")
	}
	b.WriteString("\n")
}
