package feedback

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The kinds of a record's next step (wire/result).
const (
	nextFix     = "fix"
	nextCommand = "command"
	nextWait    = "wait"
)

// requestCommand starts the engine's step that asks an owner: `archfit request -m "<why …>"`.
const requestCommand = "archfit request"

// The author and owner lines of design 12.5. A line names one step; the records whose step it names show no next.
const (
	authorFix  = "fix the code and push."
	authorAsk  = "reply `/archfit request <why you need it>`. Do not copy the code."
	authorEach = "do the `next` step of each record."
	ownerAsk   = "nothing to do until a request exists."
)

// waitStep is the engine's wait step, which names the request an owner answers.
var waitStep = regexp.MustCompile(`^wait for an owner: #([1-9][0-9]*)$`)

// fieldCap bounds each producer text the view shows, in runes. The engine writes each field as one short line; a
// longer one is cut with "…", so a record, a fence, a heading and an annotation message always fit GitHub's limits.
const fieldCap = 1000

// requestCap is the number of requests an author or owner line names before it counts the rest.
const requestCap = 10

// view is what every surface shows about one Archfit 4 result (design 8.2, 12.5): the status line, the records, one
// line for the author and one for the owner. The comment, the check title, the check summary and the annotations
// render from it, so they cannot say different things.
type view struct {
	result   result.Result // the status line (Summary), the errors and the unknown reasons, shown as given
	author   string        // "" when the author has nothing to do
	owner    string        // "" when the owner has nothing to do
	requests []int         // the requests the wait steps of the errors name, in record order, once each
	covered  string        // the next kind the author line names; a record of that kind shows no next in the comment
}

// newView builds the view of r, whose Status is the status the App gave it.
func newView(r result.Result) view {
	r = bounded(r)
	v := view{result: r}
	var asks, fixes bool
	for _, e := range r.Errors {
		asks = asks || asking(e.Next)
		fixes = fixes || e.Next.Kind == nextFix
		if n := request(e.Next); n > 0 && !slices.Contains(v.requests, n) {
			v.requests = append(v.requests, n)
		}
	}
	switch {
	case r.Status == result.StatusPass:
		return v
	case r.Status == result.StatusUnknown:
		v.author = authorEach
	case asks:
		v.author, v.covered = authorAsk, nextCommand
	case fixes:
		v.author, v.covered = authorFix, nextFix
	case len(v.requests) > 0:
		v.author, v.covered = "nothing to do. This check runs again when "+requestList(v.requests)+" "+plural(len(v.requests), "is", "are")+" answered.", nextWait
	default:
		v.author = authorEach
	}
	switch {
	case r.Status == result.StatusUnknown:
	case len(v.requests) > 0:
		v.owner = "answer " + requestList(v.requests) + "."
	case asks:
		v.owner = ownerAsk
	}
	return v
}

// asking reports whether a step asks an owner: the engine's request command, alone or after other text.
func asking(n result.Next) bool {
	return n.Kind == nextCommand && strings.Contains(n.Text, requestCommand)
}

// request is the request a wait step names; 0 when it names none.
func request(n result.Next) int {
	if n.Kind != nextWait {
		return 0
	}
	m := waitStep.FindStringSubmatch(n.Text)
	if m == nil {
		return 0
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return id
}

// says reports whether the author line already names a record's step. A command says more than the request only when
// it starts with something else, such as "use pkg_util, or run: archfit request …"; then the record keeps it.
func (v view) says(n result.Next) bool {
	if n.Kind != v.covered || v.covered == "" {
		return false
	}
	switch n.Kind {
	case nextCommand:
		return strings.HasPrefix(n.Text, requestCommand+" ")
	case nextWait:
		id := request(n)
		return id > 0 && slices.Contains(v.requests[:min(len(v.requests), requestCap)], id)
	}
	return true
}

// requestList is "#431", "#431 and #432", or "#431, #432 and #433"; after requestCap requests, "#1, …, #10 and 5
// more".
func requestList(ids []int) string {
	names := make([]string, min(len(ids), requestCap))
	for i := range names {
		names[i] = "#" + strconv.Itoa(ids[i])
	}
	if len(ids) > requestCap {
		return strings.Join(names, ", ") + " and " + strconv.Itoa(len(ids)-requestCap) + " more"
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// bounded is r with every text the view shows cut to fieldCap runes.
func bounded(r result.Result) result.Result {
	r.Summary = cut(r.Summary)
	r.Errors = slices.Clone(r.Errors)
	for i := range r.Errors {
		e := &r.Errors[i]
		e.Path, e.Rule, e.What, e.Why, e.Instead = cut(e.Path), cut(e.Rule), cut(e.What), cut(e.Why), cut(e.Instead)
		e.Next.Text, e.Next.Note = cut(e.Next.Text), cut(e.Next.Note)
	}
	r.Unknown = slices.Clone(r.Unknown)
	for i := range r.Unknown {
		u := &r.Unknown[i]
		u.Path, u.Cause, u.What, u.Why = cut(u.Path), cut(u.Cause), cut(u.What), cut(u.Why)
		u.Next.Text, u.Next.Note = cut(u.Next.Text), cut(u.Next.Note)
	}
	return r
}

func cut(s string) string {
	if utf8.RuneCountInString(s) <= fieldCap {
		return s
	}
	return string([]rune(s)[:fieldCap]) + "…"
}

// title is the check title: the status line without "archfit: ", because the check's name already says archfit
// ("fail, 1 error", "waiting, request #431"; design 11.6).
func (v view) title() string {
	return strings.TrimPrefix(v.result.Summary, statusPrefix)
}

const statusPrefix = "archfit: "

// summary is the check summary: the text record (design 8.2) in a fence no record can close. Records that do not
// fit in a check summary are left out after a line that counts them; the status line always stays.
func (v view) summary() string {
	blocks := textRecords(v.result)
	fence := fenceFor(strings.Join(blocks, "") + v.result.Summary)
	frame := len(fence) + len("text\n") + len("\n") + len(fence)
	kept := fit(blocks, maxBody-frame-len(v.result.Summary)-1, func(n int) string { return moreLine(n) + "\n" })
	return fence + "text\n" + strings.Join(kept, "") + v.result.Summary + "\n" + fence
}

// textRecords is the text record of each error and unknown reason, in record order, without the status line.
func textRecords(r result.Result) []string {
	var blocks []string
	for _, e := range r.Errors {
		blocks = append(blocks, strings.TrimSuffix(result.Record(result.Result{Errors: []result.Error{e}}), "\n"))
	}
	for _, u := range r.Unknown {
		blocks = append(blocks, strings.TrimSuffix(result.Record(result.Result{Unknown: []result.Unknown{u}}), "\n"))
	}
	return blocks
}

// annotations is one failure annotation per error that has a line, on that line, with the error's text record; at
// most annotationCap. The check summary holds every record that fits in it.
func (v view) annotations() []api.Annotation {
	var out []api.Annotation
	for _, e := range v.result.Errors {
		if e.Line <= 0 || len(out) == annotationCap {
			continue
		}
		msg := strings.TrimSuffix(result.Record(result.Result{Errors: []result.Error{e}}), "\n\n")
		out = append(out, api.Annotation{Path: e.Path, Line: e.Line, Level: "failure", Message: msg})
	}
	return out
}

// comment is the comment of design 12.5, without its markers: the status line as the heading, one Markdown record
// per error and unknown reason, then the author line and the owner line. Records that do not fit in budget are left
// out after a line that counts them.
func (v view) comment(budget int) string {
	p := v.page()
	head := "## " + p.StatusLine + "\n"
	var tail strings.Builder
	if p.Author != "" {
		tail.WriteString("\n**Author:** " + p.Author + "\n")
	}
	if p.Owner != "" {
		if p.Author == "" {
			tail.WriteString("\n")
		}
		tail.WriteString("**Owner:** " + p.Owner + "\n")
	}
	var blocks []string
	for _, r := range p.Records {
		blocks = append(blocks, "\n"+r.markdown())
	}
	kept := fit(blocks, budget-len(head)-tail.Len(), func(n int) string { return "\n" + moreLine(n) + "\n" })
	return head + strings.Join(kept, "") + tail.String()
}

// heading is the status line with "Archfit" as the first word of a heading.
func heading(line string) string {
	if rest, ok := strings.CutPrefix(line, "archfit"); ok {
		return "Archfit" + prose(rest)
	}
	return prose(line)
}

// markdown is one record of the comment: where in code, the rule in bold, what; then its fields as a list.
func (r Record) markdown() string {
	where := place(r.Path, r.Line)
	var b strings.Builder
	if where != "" {
		b.WriteString(codeSpan(where) + " ")
	}
	b.WriteString("**" + prose(r.Rule) + "**")
	if r.What != "" {
		b.WriteString(" " + prose(r.What))
	}
	b.WriteString("\n")
	if r.Why != "" {
		b.WriteString("- why: " + prose(r.Why) + "\n")
	}
	if r.Instead != "" {
		b.WriteString("- instead: " + prose(r.Instead) + "\n")
	}
	if r.Balance != nil {
		fmt.Fprintf(&b, "- balance: %d/10 (min %d)\n", r.Balance.Value, r.Balance.Min)
	}
	if r.ShowNext {
		next := r.Next
		step := prose(next.Text)
		if next.Kind != nextFix && next.Kind != nextWait {
			step = codeSpan(next.Text)
		}
		if next.Note != "" {
			step += " (" + prose(next.Note) + ")"
		}
		b.WriteString("- next: " + step + "\n")
	}
	return b.String()
}

// place is "path:line", or the path alone without a line.
func place(path string, line int) string {
	if line <= 0 {
		return path
	}
	return path + ":" + strconv.Itoa(line)
}

// prose makes producer text one inert Markdown line: line breaks become spaces and "<" cannot open HTML or a comment
// marker. Other Markdown stays, as the engine wrote it.
func prose(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "<", "&lt;").Replace(s)
}

// codeSpan puts s in a code span its backticks cannot close (CommonMark: a longer backtick string, padded when s
// starts or ends with a backtick).
func codeSpan(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	ticks := strings.Repeat("`", longestRun(s, '`')+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return ticks + s + ticks
}

// fenceFor is a code fence no line of s can close: at least three backticks, one more than the longest run in s.
func fenceFor(s string) string {
	return strings.Repeat("`", max(3, longestRun(s, '`')+1))
}

func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}

// fit keeps the leading blocks that fit in budget bytes. When some do not, it keeps room for the line more(n) and
// ends with it, n being the number left out.
func fit(blocks []string, budget int, more func(n int) string) []string {
	total := 0
	for _, b := range blocks {
		total += len(b)
	}
	if total <= budget {
		return blocks
	}
	budget -= len(more(len(blocks)))
	kept, used := 0, 0
	for _, b := range blocks {
		if used+len(b) > budget {
			break
		}
		used += len(b)
		kept++
	}
	return append(blocks[:kept:kept], more(len(blocks)-kept))
}

func moreLine(n int) string {
	return "(" + strconv.Itoa(n) + " more not shown)"
}
