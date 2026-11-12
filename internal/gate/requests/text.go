package requests

import (
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/wire/request"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// recordCap bounds the record an issue shows and the App stores, in bytes. With a 500-character reason the issue body
// stays far below GitHub's 65,536 characters, and the stored request below the Store's 64 KiB.
const recordCap = 30_000

// Texts the App posts. None repeats what a worker wrote outside a fence, and no line starts with the request command,
// so the App never reads its own text as a request (wire/request).
const (
	textNoResult = "archfit: this pull request has no checked result for its head yet. Ask again when the check has run.\n"
	textNoPolicy = "archfit: the default branch has no policy of version 3 yet, so nothing can be asked.\n"
)

// records is the text record of errs as the check prints it, without the status line. Records past recordCap are
// left out after a line that counts them.
func recordsOf(errs []result.Error) string {
	var b strings.Builder
	for i, e := range errs {
		block := strings.TrimSuffix(result.Record(result.Result{Errors: []result.Error{e}}), "\n")
		if b.Len()+len(block) > recordCap {
			b.WriteString("(" + strconv.Itoa(len(errs)-i) + " more not shown)\n")
			break
		}
		b.WriteString(block)
	}
	return b.String()
}

// capRecord cuts a reported record to recordCap bytes at a line end.
func capRecord(s string) string {
	if len(s) <= recordCap {
		return s
	}
	cut := strings.LastIndex(s[:recordCap], "\n")
	return s[:cut+1] + "(more not shown)\n"
}

// issueOf is the request issue of a stored request.
func issueOf(req records.Request) request.Issue {
	return request.Issue{Rule: req.Rule, Path: req.Path, From: req.From, To: req.To, Reason: req.Reason,
		Expires: req.Expires, Record: req.Record, AsReported: !req.Numbers.Verified, Pulls: req.Waiting}
}

// outcome is what one request line did to the requests of a pull request, for the reply.
type outcome struct {
	opened   []int
	joined   []int
	answered []answered
	left     int // keys past MaxNewPerComment
}

// answered is a key a deny rule of the policy already answers.
type answered struct {
	what    string // "from -> to"
	rule    string
	instead string
}

// reply is the comment on the pull request after a request line.
func (o outcome) reply() string {
	var b strings.Builder
	b.WriteString("archfit: request received.\n\n")
	for _, n := range o.opened {
		b.WriteString("- #" + strconv.Itoa(n) + ": a new request; its owners are assigned.\n")
	}
	for _, n := range o.joined {
		b.WriteString("- #" + strconv.Itoa(n) + ": the same request is open; this pull request joins it.\n")
	}
	for _, a := range o.answered {
		b.WriteString("- " + codeSpan(a.what) + ": the rule " + codeSpan(a.rule) + " already answers it. Instead:\n\n")
		b.WriteString(indent(fenced(a.instead), "  ") + "\n")
	}
	if o.left > 0 {
		b.WriteString("- " + strconv.Itoa(o.left) + " more " + plural(o.left, "error asks", "errors ask") +
			" for an owner: one line opens at most " + strconv.Itoa(MaxNewPerComment) + " requests. Reply again with the same line.\n")
	}
	if len(o.opened)+len(o.joined) > 0 {
		b.WriteString("\nThe check shows `waiting` while open requests cover every error.\n")
	}
	return b.String()
}

// answeredReply is the comment on a request issue a deny rule already answers, before the App closes it.
func answeredReply(a answered) string {
	return "archfit: the rule " + codeSpan(a.rule) + " already answers this request. Instead:\n\n" + fenced(a.instead)
}

// duplicateReply is the comment on a request issue whose request is already open, before the App closes it.
func duplicateReply(open int) string {
	return "archfit: the same request is open: #" + strconv.Itoa(open) + ". This issue is closed; follow #" +
		strconv.Itoa(open) + ".\n"
}

// joinedNote is the comment on a request issue when a pull request joins it.
func joinedNote(pull int) string {
	return "archfit: #" + strconv.Itoa(pull) + " waits on this request.\n"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// fenced puts owner text in a `text` block its backticks cannot close, so no mention, link or Markdown in it takes
// effect.
func fenced(s string) string {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	fence := strings.Repeat("`", max(3, longestRun(s)+1))
	return fence + "text\n" + s + fence + "\n"
}

func indent(s, prefix string) string {
	lines := strings.SplitAfter(strings.TrimSuffix(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "") + "\n"
}

// codeSpan puts s on one line in a code span its backticks cannot close (CommonMark), so a module name cannot
// mention, link or format.
func codeSpan(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	ticks := strings.Repeat("`", longestRun(s)+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return ticks + s + ticks
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
