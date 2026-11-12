package request

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Label marks a request issue.
const Label = "archfit-request"

// Issue is what the App shows in a request issue. Its number is the request id.
type Issue struct {
	// Rule, Path, From and To name what is asked, as in the key: Path for unowned code, From and To otherwise.
	Rule string `json:"rule"`
	Path string `json:"path,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Reason is the worker's reason, shown in a text block so that nothing in it renders.
	Reason string `json:"reason"`
	// Expires is the date the worker asked for, YYYY-MM-DD; empty when none.
	Expires string `json:"expires,omitempty"`
	// Record is the record of the asked errors as the check prints them, without the status line, or the reported
	// `can` answer. It is shown in a text block.
	Record string `json:"record"`
	// AsReported is true while no pull request confirms the numbers of Record.
	AsReported bool `json:"as_reported,omitempty"`
	// Pulls are the linked pull requests, in the order they joined.
	Pulls []int `json:"pulls,omitempty"`
}

// Title returns the issue title: "Request: rule from -> to", "Request: duplication a <-> b" with the two modules
// sorted, or "Request: unowned path".
func (i Issue) Title() string {
	switch i.Rule {
	case ruleDuplication:
		a, b := min(i.From, i.To), max(i.From, i.To)
		return "Request: " + i.Rule + " " + a + " <-> " + b
	case ruleUnowned:
		return "Request: " + i.Rule + " " + i.Path
	}
	return "Request: " + i.Rule + " " + i.From + " -> " + i.To
}

// Body returns the issue body in Markdown: the reason, the record, then the linked pull requests and the date asked.
func (i Issue) Body() string {
	var b strings.Builder
	b.WriteString("**Reason**\n\n" + textBlock(i.Reason) + "\n**Record**")
	if i.AsReported {
		b.WriteString(" (as reported: no pull request confirms these numbers yet)")
	}
	b.WriteString("\n\n" + textBlock(i.Record))
	var list []string
	if len(i.Pulls) > 0 {
		refs := make([]string, len(i.Pulls))
		for n, pull := range i.Pulls {
			refs[n] = "#" + strconv.Itoa(pull)
		}
		list = append(list, "- Pull requests: "+strings.Join(refs, ", ")+"\n")
	}
	if i.Expires != "" {
		list = append(list, "- Expires: "+i.Expires+"\n")
	}
	if len(list) > 0 {
		b.WriteString("\n" + strings.Join(list, ""))
	}
	return b.String()
}

// textBlock fences text as a `text` code block. The fence is longer than any run of backticks in text, so no line of
// text can close it and no mention, link or Markdown inside takes effect.
func textBlock(text string) string {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	fence := strings.Repeat("`", fenceLength(text))
	return fence + "text\n" + text + fence + "\n"
}

// fenceLength is one more than the longest run of backticks in text, and at least three.
func fenceLength(text string) int {
	longest, run := 0, 0
	for _, r := range text {
		if r != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return max(3, longest+1)
}

// Form is an issue body the CLI wrote: the request line, then the reported record.
type Form struct {
	Line
	// Record is the first text block after the line, verbatim with LF line ends; empty when there is none.
	Record string `json:"record,omitempty"`
	// Answer is the `can` answer Record reports; nil when Record is not one.
	Answer *Answer `json:"answer,omitempty"`
}

// ParseIssue reads the body of an issue. ok is false when the first line that is not blank is not a request line.
// A request line that cannot be used returns a *Rejection.
func ParseIssue(body string, today time.Time) (form Form, ok bool, err error) {
	all := lines(body)
	first := 0
	for first < len(all) && strings.TrimSpace(all[first]) == "" {
		first++
	}
	if first == len(all) {
		return Form{}, false, nil
	}
	rest, found := command(all[first])
	if !found {
		return Form{}, false, nil
	}
	if form.Line, err = parse(rest, today); err != nil {
		return Form{}, true, err
	}
	form.Record = firstTextBlock(all[first+1:])
	form.Answer = parseAnswer(form.Record)
	return form, true, nil
}

var openFence = regexp.MustCompile("^(`{3,})text[ \t]*$")

// firstTextBlock returns the content of the first closed `text` block in lines.
func firstTextBlock(lines []string) string {
	for i, line := range lines {
		m := openFence.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if closes(lines[j], len(m[1])) {
				return strings.Join(lines[i+1:j], "\n") + "\n"
			}
		}
		return ""
	}
	return ""
}

// closes reports whether line closes a fence of n backticks: n or more backticks and nothing else but trailing blanks.
func closes(line string, n int) bool {
	run := strings.TrimRight(line, " \t")
	return len(run) >= n && strings.Trim(run, "`") == ""
}

var (
	answerFields = regexp.MustCompile(`^  (why|instead|balance|next): `)
	balanceLine  = regexp.MustCompile(`^  balance: (10|[1-9])/10 \(min (10|[1-9])\)$`)
)

// parseAnswer reads one `can` answer of the engine's grammar: "from -> to: rule: what", then indented fields.
func parseAnswer(record string) *Answer {
	all := strings.Split(strings.TrimSuffix(record, "\n"), "\n")
	head, fields, ok := strings.Cut(all[0], ": ")
	if !ok {
		return nil
	}
	from, to, ok := strings.Cut(head, " -> ")
	rule, _, hasWhat := strings.Cut(fields, ": ")
	if !ok || !hasWhat || !word(from) || !word(to) || !word(rule) {
		return nil
	}
	switch rule {
	case "yes", "no", "unknown":
		return nil
	}
	a := &Answer{Rule: rule, From: from, To: to}
	for _, line := range all[1:] {
		if !answerFields.MatchString(line) {
			return nil
		}
		if m := balanceLine.FindStringSubmatch(line); m != nil {
			value, _ := strconv.Atoi(m[1])
			least, _ := strconv.Atoi(m[2])
			a.Balance = &Balance{Value: value, Min: least}
		} else if strings.HasPrefix(line, "  balance: ") {
			return nil
		}
	}
	return a
}

// word reports whether s is a non-empty name without blanks at its ends.
func word(s string) bool {
	return s != "" && strings.TrimSpace(s) == s
}
