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
//	              [ "  balance: " n "/10 (min " m ")" [ ": " strength ", " to " is " volatility ] NL ]
//	              [ "  surface: " ( glob " (" role ")" { ", " glob " (" role ")" } | "none declared" ) NL ]
//	next-line   = "  next: " text [ "     (" note ")" ] NL
//	status-line = summary NL
//
// It prints the producer's text as given and decides nothing. It is the record without --verbose: digests and the
// engine version stay out of the check and the comment (design 5).
func Record(r Result) string {
	var b strings.Builder
	for _, e := range r.Errors {
		writeHead(&b, location(e.Path, e.Line), e.Rule, e.What)
		writeDetails(&b, e.Why, e.Instead, e.Balance, e.To, e.Surface, e.Next)
	}
	for _, u := range r.Unknown {
		writeHead(&b, location(u.Path, u.Line), u.Cause, u.What)
		writeDetails(&b, u.Why, "", nil, "", nil, u.Next)
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

func writeDetails(b *strings.Builder, why, instead string, balance *Balance, to string, surface *Surface, next Next) {
	if why != "" {
		b.WriteString("  why: " + why + "\n")
	}
	if instead != "" {
		b.WriteString("  instead: " + instead + "\n")
	}
	if balance != nil {
		b.WriteString("  balance: " + BalanceText(*balance, to) + "\n")
	}
	if surface != nil {
		b.WriteString("  surface: " + surfaceText(*surface) + "\n")
	}
	b.WriteString("  next: " + next.Text)
	if next.Note != "" {
		b.WriteString(noteGap + "(" + next.Note + ")")
	}
	b.WriteString("\n")
}

// BalanceText is the balance as the record prints it after "balance: ": "9/10 (min 7)" for a v4.0.0 balance, and
// "2/10 (min 7): intrusive, assessment-repair is high" for a 0.9.0 balance, whose subject is the target module to.
func BalanceText(b Balance, to string) string {
	text := strconv.Itoa(b.Value) + "/10 (min " + strconv.Itoa(b.Min) + ")"
	if b.HasInputs() {
		text += ": " + string(b.Strength) + ", " + to + " is " + string(b.Volatility)
	}
	return text
}

// noSurface is the surface line of a module that declares no part another module may use.
const noSurface = "none declared"

// surfaceText lists the public globs, then the model globs, each with its role; "none declared" when both are empty.
func surfaceText(s Surface) string {
	if len(s.Public)+len(s.Model) == 0 {
		return noSurface
	}
	parts := make([]string, 0, len(s.Public)+len(s.Model))
	for _, g := range s.Public {
		parts = append(parts, g+" (public)")
	}
	for _, g := range s.Model {
		parts = append(parts, g+" (model)")
	}
	return strings.Join(parts, ", ")
}
