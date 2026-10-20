package feedback

import (
	"fmt"
	"strings"
)

// maxBody is the GitHub limit for a check summary; the comment uses the same budget.
const maxBody = 65535

const truncatedNote = "Output truncated."

// Folds keep the visible part short: the headline, the reasons and the findings stay open; evidence, identity,
// validation commands and agent tasks fold away. Agents read the raw Markdown, so the task markers still parse.
const (
	foldEvidence = "<details>\n<summary>Evidence and inputs</summary>"
	foldTasks    = "<details>\n<summary>For coding agents</summary>"
	foldClose    = "</details>"
	fenceClose   = "\n```"
)

// commandsBudget keeps a long command list from starving findings and agent tasks.
const (
	commandsBudget = 4096
	commandCap     = 20
)

// Engine-authored strings are untrusted for Markdown. Removing the marker parts and backticks first and
// escaping the angle brackets after makes any reassembled "<!--" inert.
var (
	stripper = strings.NewReplacer("<!--", "", "-->", "", "`", "", "\r", "", "\n", "")
	escaper  = strings.NewReplacer("<", "&lt;", ">", "&gt;")
)

func sanitize(s string) string {
	return escaper.Replace(stripper.Replace(s))
}

// text is sanitize for prose positions; code is sanitize for identifiers, paths and digests.
func text(s string) string {
	if s = sanitize(s); s != "" {
		return s
	}
	return "(none)"
}

func code(s string) string {
	if s = sanitize(s); s != "" {
		return "`" + s + "`"
	}
	return "(none)"
}

// layout holds the rendered blocks of one body. Empty blocks are left out.
type layout struct {
	head     string // blocks 1 to 3, with the comment markers
	decision string // the comment's reasons with their codes, first in the evidence fold
	findings string // block 5
	dims     string // block 6
	gaps     string // block 7
	inputs   string // block 8
	commands commandList
	tasks    []string // block 10, one encoded task each
	hasTasks bool
	receipt  string
}

func cost(block string) int {
	if block == "" {
		return 0
	}
	return len(block) + 2
}

// assemble joins the blocks under limit bytes. Head, decision, gaps, inputs and the receipt marker are budgeted
// first. Of the rest, agent tasks give way first (whole tasks), then findings are cut at a line end;
// dimensions and commands are cut only when even that is not enough.
func (l layout) assemble(limit int) string {
	rest := limit + 2 - cost(l.head) - cost(l.decision) - cost(l.inputs) - cost(l.receipt) - cost(foldEvidence) - cost(foldTasks) - 2*cost(foldClose)
	l.gaps = cutLines(l.gaps, rest-2) // last resort: only a pathological gap list does not fit
	rest -= cost(l.gaps)

	noTasks := ""
	if l.hasTasks {
		noTasks = tasksBlock(nil, len(l.tasks))
		rest -= cost(noTasks)
	}
	l.dims = cutLines(l.dims, rest-2)
	rest -= cost(l.dims)
	commands := l.commands.render(min(rest-2, commandsBudget))
	rest -= cost(commands)
	l.findings = cutLines(l.findings, rest-2)
	rest -= cost(l.findings)

	tasks := noTasks
	if n := fitTasks(l.tasks, rest); n > 0 {
		tasks = tasksBlock(l.tasks[:n], len(l.tasks)-n)
	}

	body := join(l.head, l.findings, fold(foldEvidence, join(l.decision, l.dims, l.gaps, l.inputs, commands)), fold(foldTasks, tasks))
	if l.receipt == "" {
		return cutLines(body, limit)
	}
	return cutLines(body, limit-cost(l.receipt)) + "\n\n" + l.receipt
}

// join joins the non-empty blocks with a blank line.
func join(blocks ...string) string {
	var kept []string
	for _, b := range blocks {
		if b != "" {
			kept = append(kept, b)
		}
	}
	return strings.Join(kept, "\n\n")
}

func fold(open, block string) string {
	if block == "" {
		return ""
	}
	return open + "\n\n" + block + "\n\n" + foldClose
}

// fitTasks returns how many leading tasks fit into extra bytes on top of the empty task block.
func fitTasks(tasks []string, extra int) int {
	n, size := 0, 2 // "[\n" and "\n]" instead of "[]"
	for n < len(tasks) && size+len(tasks[n])+2 <= extra {
		size += len(tasks[n]) + 2
		n++
	}
	return n
}

func tasksBlock(tasks []string, omitted int) string {
	array := "[]"
	if len(tasks) > 0 {
		array = "[\n" + strings.Join(tasks, ",\n") + "\n]"
	}
	block := headTasks + "\n\n" + tasksOpen + "\n```json\n" + array + "\n```\n" + tasksClose
	if omitted > 0 {
		block += fmt.Sprintf("\n%d more tasks did not fit.", omitted)
	}
	return block
}

// cutLines cuts a block at a line end so that it fits max bytes. A newline is never inside a UTF-8
// sequence or a marker, so neither can be split.
func cutLines(block string, limit int) string {
	if len(block) <= limit {
		return block
	}
	keep := limit - len(truncatedNote) - 2 - len(fenceClose) // room to close a code fence the cut opened
	if keep-len(foldClose)-2 <= 0 {
		return ""
	}
	i := strings.LastIndexByte(block[:keep-len(foldClose)-2], '\n')
	if i <= 0 {
		return ""
	}
	kept := strings.TrimRight(block[:i], "\n")
	if strings.Count("\n"+kept, "\n```")%2 == 1 { // nor a code fence: the rest of the body would render as code
		kept += fenceClose
	}
	if strings.Count(kept, "<details>") > strings.Count(kept, foldClose) { // a cut never leaves a fold open
		kept += "\n\n" + foldClose
	}
	return kept + "\n\n" + truncatedNote
}

// commandList is block 9: validation commands printed verbatim or not at all.
type commandList struct {
	items  []string
	unsafe int // commands that could close the code fence or forge a marker
}

func (c commandList) render(budget int) string {
	if len(c.items) == 0 && c.unsafe == 0 {
		return ""
	}
	more := func(n int) string { return fmt.Sprintf("%d more commands are not shown.", n) }
	unsafe := fmt.Sprintf("%d commands omitted: they contain marker or code fence characters.", c.unsafe)

	n, size := 0, len(headCommands)+len("\n\n```sh\n```\n")+len(more(len(c.items)))+1+len(unsafe)
	for n < len(c.items) && n < commandCap && size+len(c.items[n])+1 <= budget {
		size += len(c.items[n]) + 1
		n++
	}
	if size > budget {
		return ""
	}
	lines := []string{headCommands, ""}
	if n > 0 {
		lines = append(lines, "```sh")
		lines = append(lines, c.items[:n]...)
		lines = append(lines, "```")
	}
	if n < len(c.items) {
		lines = append(lines, more(len(c.items)-n))
	}
	if c.unsafe > 0 {
		lines = append(lines, unsafe)
	}
	return strings.Join(lines, "\n")
}
