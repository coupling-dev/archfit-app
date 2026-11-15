// Package markdown writes text the App does not own into the Markdown of an issue the App authors: a policy's names
// and reasons, and the engine's records, which come from the customer's tree. Such text must not mention, link, format
// or close the block around it, and must keep the body under GitHub's limit.
package markdown

import (
	"strings"
	"unicode/utf8"
)

// ellipsis ends a string Clip shortened.
const ellipsis = "…"

// CodeSpan puts s on one line in a code span its backticks cannot close (CommonMark), so a name cannot mention, link
// or format.
func CodeSpan(s string) string {
	s = OneLine(s)
	ticks := strings.Repeat("`", longestRun(s)+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return ticks + s + ticks
}

// Fenced puts s in a text code block whose fence no line of s can close.
func Fenced(s string) string {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	fence := strings.Repeat("`", max(3, longestRun(s)+1))
	return fence + "text\n" + s + fence + "\n"
}

// OneLine replaces line breaks with spaces.
func OneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}

// Clip keeps at most n bytes of s, cut at a rune boundary and ended with an ellipsis when it was cut and n has room for it.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	suffix := ellipsis
	if n < len(suffix) {
		suffix = ""
	}
	cut := max(0, n-len(suffix))
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

func longestRun(s string) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}
