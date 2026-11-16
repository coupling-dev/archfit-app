package requests

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
)

const (
	diffContext = 3
	// Bound Myers's quadratic trace on unrelated files. Beyond this budget, show the
	// entire remaining replacement, never omit bytes or mistake failure for equality.
	maxDiffTrace = 1 << 20
)

type diffLine struct {
	mark byte
	text string
}

// Diff shows exact byte changes with unified hunks and three lines of context.
// Empty output means byte-identical inputs. Line-ending annotations and escaped
// control characters are display notation; consumers must render this as text, not HTML.
func Diff(path string, old, edited []byte) string {
	if bytes.Equal(old, edited) {
		return ""
	}
	edits := diffEdits(lines(old), lines(edited))
	labelEOL := bytes.Contains(old, []byte("\r\n")) || bytes.Contains(edited, []byte("\r\n"))
	var w strings.Builder
	w.WriteString("--- a/" + visibleDiffText(path) + "\n+++ b/" + visibleDiffText(path) + "\n")
	oldPos, newPos, scanned := 0, 0, 0
	for i := 0; i < len(edits); {
		if edits[i].mark == ' ' {
			i++
			continue
		}
		start, last := max(0, i-diffContext), i
		j := i + 1
		for ; j < len(edits); j++ {
			if edits[j].mark != ' ' {
				last = j
			} else if j-last > 2*diffContext {
				break
			}
		}
		end := min(len(edits), last+diffContext+1)
		a, b := diffCounts(edits[scanned:start])
		oldPos, newPos = oldPos+a, newPos+b
		oldN, newN := diffCounts(edits[start:end])
		w.WriteString("@@ -" + hunkRange(oldPos, oldN) + " +" + hunkRange(newPos, newN) + " @@\n")
		for _, line := range edits[start:end] {
			body, eol := cutEOL(line.text)
			w.WriteByte(line.mark)
			w.WriteString(visibleDiffText(body) + "\n")
			switch {
			case eol == "":
				w.WriteString("\\ No newline at end of file\n")
			case labelEOL && line.mark != ' ':
				if eol == "\r\n" {
					w.WriteString("\\ Line ending: CRLF\n")
				} else {
					w.WriteString("\\ Line ending: LF\n")
				}
			}
		}
		oldPos, newPos, scanned = oldPos+oldN, newPos+newN, end
		i = end
	}
	return w.String()
}

func diffCounts(edits []diffLine) (oldN, newN int) {
	for _, line := range edits {
		if line.mark != '+' {
			oldN++
		}
		if line.mark != '-' {
			newN++
		}
	}
	return oldN, newN
}

func diffEdits(a, b []string) []diffLine {
	head, tail := 0, 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	out := make([]diffLine, 0, len(a)+len(b))
	for _, line := range a[:head] {
		out = append(out, diffLine{' ', line})
	}
	out = append(out, diffMiddle(a[head:len(a)-tail], b[head:len(b)-tail])...)
	for _, line := range a[len(a)-tail:] {
		out = append(out, diffLine{' ', line})
	}
	return out
}

// diffMiddle uses Myers's shortest edit path, comparing whole lines including
// their terminators. The bounded trace keeps hostile inputs from exhausting memory.
func diffMiddle(a, b []string) []diffLine {
	var trace [][]int
	for d := 0; d <= len(a)+len(b) && (d+1)*(d+1) <= maxDiffTrace; d++ {
		next := make([]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			x := 0
			if d > 0 {
				prev := trace[d-1]
				if k == -d || k != d && frontierX(prev, k-1) < frontierX(prev, k+1) {
					x = frontierX(prev, k+1)
				} else {
					x = frontierX(prev, k-1) + 1
				}
			}
			y := x - k
			for x < len(a) && y < len(b) && a[x] == b[y] {
				x++
				y++
			}
			next[k+d] = x
			if x == len(a) && y == len(b) {
				return traceEdits(a, b, append(trace, next))
			}
		}
		trace = append(trace, next)
	}
	out := make([]diffLine, 0, len(a)+len(b))
	for _, line := range a {
		out = append(out, diffLine{'-', line})
	}
	for _, line := range b {
		out = append(out, diffLine{'+', line})
	}
	return out
}

func frontierX(frontier []int, k int) int {
	i := k + len(frontier)/2
	if i < 0 || i >= len(frontier) {
		return -1
	}
	return frontier[i]
}

func traceEdits(a, b []string, trace [][]int) []diffLine {
	x, y := len(a), len(b)
	out := make([]diffLine, 0, x+y)
	for d := len(trace) - 1; d > 0; d-- {
		prev, k := trace[d-1], x-y
		prevK := k - 1
		if k == -d || k != d && frontierX(prev, k-1) < frontierX(prev, k+1) {
			prevK = k + 1
		}
		prevX := frontierX(prev, prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			out = append(out, diffLine{' ', a[x]})
		}
		if x == prevX {
			y--
			out = append(out, diffLine{'+', b[y]})
		} else {
			x--
			out = append(out, diffLine{'-', a[x]})
		}
	}
	for x > 0 {
		x--
		out = append(out, diffLine{' ', a[x]})
	}
	slices.Reverse(out)
	return out
}

// visibleDiffText keeps HTML and ordinary Unicode literal, but makes tabs,
// backslashes, invalid UTF-8 and non-printing controls (including bidi) visible.
func visibleDiffText(text string) string {
	quoted := strconv.Quote(text)
	return strings.ReplaceAll(quoted[1:len(quoted)-1], `\"`, `"`)
}

// lines retains terminators, including the absence of a final newline.
func lines(text []byte) []string {
	out := strings.SplitAfter(string(text), "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func cutEOL(line string) (body, eol string) {
	for _, end := range []string{"\r\n", "\n"} {
		if b, ok := strings.CutSuffix(line, end); ok {
			return b, end
		}
	}
	return line, ""
}

// hunkRange is "start,count"; an empty range starts at the line before it.
func hunkRange(from, n int) string {
	if n == 0 {
		return strconv.Itoa(from) + ",0"
	}
	return strconv.Itoa(from+1) + "," + strconv.Itoa(n)
}
