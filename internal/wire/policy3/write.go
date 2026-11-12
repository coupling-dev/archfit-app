package policy3

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// ErrLayout means the writer cannot place an edit in this file without rewriting bytes it
// must keep, for example a module written as a flow mapping or a list split over several
// lines in flow style. The owner edits the file by hand.
var ErrLayout = errors.New("policy layout the writer does not edit")

// The writer adds an answer to a policy file by inserting text. It never re-encodes the
// file, because a YAML encoder rewrites indentation, quoting and blank lines. The YAML
// node tree gives the positions: the line and column of each key, so the inserted lines
// copy the indentation of their neighbours. Every edit is an insertion: the old file is
// the new file with the inserted text cut out, so no other byte changes, and so the digest
// changes exactly when the answer is written.
//
// Each edit decodes its own output and compares it with the old policy plus the answer. A
// value that would not read back as written (a name with a flow character, a layout the
// positions misread) is an error, never a different policy.

// AddDependsOn adds dependency to the depends_on of module. When the policy already
// allows it, the file comes back unchanged.
func AddDependsOn(file []byte, module, dependency string) ([]byte, error) {
	old, err := Decode(file)
	if err != nil {
		return nil, err
	}
	mod, ok := old.Modules[module]
	if !ok {
		return nil, fmt.Errorf("module %q is not in the policy", module)
	}
	if _, ok := old.Modules[dependency]; !ok {
		return nil, fmt.Errorf("module %q is not in the policy", dependency)
	}
	if slices.Contains(mod.DependsOn, dependency) {
		return file, nil
	}
	item, err := scalar(dependency)
	if err != nil {
		return nil, err
	}
	d, err := newDoc(file)
	if err != nil {
		return nil, err
	}
	_, modules := entry(d.root, "modules")
	modKey, node := entry(modules, module)
	if modules == nil || modules.Style&yaml.FlowStyle != 0 || node == nil || node.Kind != yaml.MappingNode || node.Style&yaml.FlowStyle != 0 ||
		len(node.Content) == 0 || !d.startsLine(modKey) || !d.startsLine(node.Content[0]) {
		return nil, fmt.Errorf("%w: module %q is not a block mapping", ErrLayout, module)
	}
	var out []byte
	switch depKey, deps := entry(node, "depends_on"); {
	case deps == nil:
		first := node.Content[0]
		line := d.blockEnd(modKey.Line, modKey.Column-1, false)
		out = d.insertAfter(line, spaces(first.Column-1)+"depends_on: ["+item+"]")
	case deps.Kind == yaml.SequenceNode && deps.Style&yaml.FlowStyle != 0:
		out, err = d.appendFlow(deps, item)
	default:
		out, err = d.appendItem(depKey, deps, func(dash, field int) []string {
			return itemLines(dash, field, []string{item})
		})
	}
	if err != nil {
		return nil, err
	}
	return verify(file, out, func(f *File) {
		m := f.Modules[module]
		m.DependsOn = append(slices.Clone(m.DependsOn), dependency)
		f.Modules[module] = m
	})
}

// AddException adds an exception to the policy. The reason (why) is the caller's and names
// the request, as "Request #431 is open." does. An exception for the rule unowned or for a
// custom only rule has no to: an only rule names its library itself. A to there is an error,
// never dropped. expires is a date, YYYY-MM-DD, or empty for an exception with no date.
func AddException(file []byte, e Exception) ([]byte, error) {
	if e.Expires != "" {
		if _, err := time.Parse(dateLayout, e.Expires); err != nil {
			return nil, fmt.Errorf("expires %q is not a date: use YYYY-MM-DD", e.Expires)
		}
	}
	q, err := quoted(e.Rule, e.From, e.To, e.Why)
	if err != nil {
		return nil, err
	}
	fields := []string{"rule: " + q[0], "from: " + q[1]}
	if e.To != "" {
		fields = append(fields, "to: "+q[2])
	}
	if e.Expires != "" {
		fields = append(fields, "expires: "+e.Expires)
	}
	fields = append(fields, "why: "+q[3])
	return addToList(file, "exceptions", fields, func(f *File) {
		f.Exceptions = append(slices.Clone(f.Exceptions), e)
	})
}

// AddDenyRule adds a custom deny rule from one module or path to another, with the reason
// and what to do instead. It answers later requests for the same dependency (design 6.3).
func AddDenyRule(file []byte, id, from, to, why, instead string) ([]byte, error) {
	q, err := quoted(id, from, to, why, instead)
	if err != nil {
		return nil, err
	}
	fields := []string{"id: " + q[0], "deny:", "  from: " + q[1], "  to: " + q[2], "why: " + q[3], "instead: " + q[4]}
	return addToList(file, "rules", fields, func(f *File) {
		f.Rules = append(slices.Clone(f.Rules), Rule{
			ID: id, Deny: &Deny{From: Names{from}, To: Names{to}}, Why: why, Instead: instead,
		})
	})
}

// addToList appends one item, with the given fields, to the top-level list key. A missing
// key is added at the end of the file.
func addToList(file []byte, key string, fields []string, mutate func(*File)) ([]byte, error) {
	if _, err := Decode(file); err != nil {
		return nil, err
	}
	d, err := newDoc(file)
	if err != nil {
		return nil, err
	}
	var out []byte
	switch k, seq := entry(d.root, key); {
	case seq == nil:
		out = d.insertAfter(len(d.starts), append([]string{key + ":"}, itemLines(2, 4, fields)...)...)
	case !d.startsLine(k):
		return nil, fmt.Errorf("%w: %s is not a block list", ErrLayout, key)
	default:
		out, err = d.appendItem(k, seq, func(dash, field int) []string { return itemLines(dash, field, fields) })
	}
	if err != nil {
		return nil, err
	}
	return verify(file, out, mutate)
}

// verify decodes the edited file and checks that it is the old policy plus the answer.
func verify(old, edited []byte, mutate func(*File)) ([]byte, error) {
	want, err := Decode(old)
	if err != nil {
		return nil, err
	}
	mutate(&want)
	got, err := Decode(edited)
	if err != nil {
		return nil, fmt.Errorf("the edit would make the policy invalid: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return nil, errors.New("the edit would change more than the answer")
	}
	return edited, nil
}

// quoted writes each string as one YAML line.
func quoted(values ...string) ([]string, error) {
	out := make([]string, len(values))
	for i, v := range values {
		text, err := scalar(v)
		if err != nil {
			return nil, err
		}
		out[i] = text
	}
	return out, nil
}

// scalar writes a string as one YAML line, quoted when it needs quotes.
func scalar(s string) (string, error) {
	out, err := yaml.Marshal(s)
	if err != nil {
		return "", err
	}
	text := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(text, "\n") {
		return "", fmt.Errorf("%q cannot be written on one line", s)
	}
	return text, nil
}

// itemLines writes the fields of one list item: the first after the dash, the others
// at the same indent as the first.
func itemLines(dash, field int, fields []string) []string {
	lines := make([]string, len(fields))
	for i, f := range fields {
		if i == 0 {
			lines[i] = spaces(dash) + "-" + spaces(field-dash-1) + f
			continue
		}
		lines[i] = spaces(field) + f
	}
	return lines
}

func spaces(n int) string { return strings.Repeat(" ", n) }

// entry returns the key and the value of a mapping entry, or nil, nil.
func entry(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// doc is a policy file with the position of each of its lines.
type doc struct {
	data   []byte
	starts []int // byte offset of the first byte of each line
	eol    string
	root   *yaml.Node
}

func newDoc(data []byte) (*doc, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: the policy is not one block mapping", ErrLayout)
	}
	if err := checkLayout(data, node.Content[0]); err != nil {
		return nil, err
	}
	d := &doc{data: data, root: node.Content[0], eol: "\n", starts: []int{0}}
	for i, b := range data {
		if b == '\n' && i+1 < len(data) {
			d.starts = append(d.starts, i+1)
		}
	}
	if i := strings.IndexByte(string(data), '\n'); i > 0 && data[i-1] == '\r' {
		d.eol = "\r\n"
	}
	return d, nil
}

// checkLayout refuses what node positions cannot place an edit in. A line break other than
// LF or CRLF moves the node lines away from the lines the writer counts. A document end
// marker or a second document leaves the end of the file outside the policy. An anchor, an
// alias or a merge key puts a key's value somewhere other than under the key.
func checkLayout(data []byte, root *yaml.Node) error {
	for i, b := range data {
		if b == '\r' && (i+1 == len(data) || data[i+1] != '\n') {
			return fmt.Errorf("%w: a CR line break without LF", ErrLayout)
		}
	}
	if strings.ContainsAny(string(data), "\u0085\u2028\u2029") {
		return fmt.Errorf("%w: a NEL, LS or PS line break", ErrLayout)
	}
	seenContent := false
	for line := range strings.Lines(string(data)) {
		text := strings.TrimRight(line, "\r\n")
		switch {
		case isMarker(text, "..."):
			return fmt.Errorf("%w: a document end marker", ErrLayout)
		case isMarker(text, "---") && seenContent:
			return fmt.Errorf("%w: a second document", ErrLayout)
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			seenContent = true
		}
	}
	var special func(*yaml.Node) bool
	special = func(n *yaml.Node) bool {
		return n.Anchor != "" || n.Kind == yaml.AliasNode || n.ShortTag() == "!!merge" || slices.ContainsFunc(n.Content, special)
	}
	if special(root) {
		return fmt.Errorf("%w: an anchor, an alias or a merge key", ErrLayout)
	}
	return nil
}

// isMarker reports whether a line is a YAML document marker: three characters at the start
// of the line, then the end of the line or a space or a tab, so "... # end" counts.
func isMarker(line, marker string) bool {
	rest, ok := strings.CutPrefix(line, marker)
	return ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t')
}

// text returns line n (counted from 1) without its line end.
func (d *doc) text(n int) string {
	end := len(d.data)
	if n < len(d.starts) {
		end = d.starts[n]
	}
	return strings.TrimRight(string(d.data[d.starts[n-1]:end]), "\r\n")
}

func (d *doc) indent(n int) int {
	t := d.text(n)
	return len(t) - len(strings.TrimLeft(t, " "))
}

// startsLine reports whether the node is the first thing on its line, so its column is
// the indent of the block it opens.
func (d *doc) startsLine(n *yaml.Node) bool { return d.indent(n.Line) == n.Column-1 }

// blockEnd returns the last line of the block that opens at line: the lines after it
// that are indented deeper than base or, when the list's dashes sit at the key's own
// indent, dash lines at base. Blank lines and comments never end a block, and the ones
// at its end are not part of it.
func (d *doc) blockEnd(line, base int, dashesAtBase bool) int {
	last := line
	for n := line + 1; n <= len(d.starts); n++ {
		text := d.text(n)
		trimmed := strings.TrimSpace(text)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case d.indent(n) > base, dashesAtBase && d.indent(n) == base && (trimmed == "-" || strings.HasPrefix(trimmed, "- ")):
			last = n
		default:
			return last
		}
	}
	return last
}

// insertAfter inserts lines after line n. A file without a final line end stays without one.
func (d *doc) insertAfter(n int, lines ...string) []byte {
	block := strings.Join(lines, d.eol)
	end := len(d.data)
	if n < len(d.starts) {
		end = d.starts[n]
	}
	out := make([]byte, 0, len(d.data)+len(block)+len(d.eol))
	out = append(out, d.data[:end]...)
	if end == len(d.data) && !strings.HasSuffix(string(d.data), "\n") {
		return append(append(out, d.eol...), block...)
	}
	out = append(out, block...)
	out = append(out, d.eol...)
	return append(out, d.data[end:]...)
}

// appendItem adds one item after the last item of the block list seq, which key holds.
// lines gets the indent of the dash and of the item's fields.
func (d *doc) appendItem(key, seq *yaml.Node, lines func(dash, field int) []string) ([]byte, error) {
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 || len(seq.Content) == 0 || !d.startsLine(seq) {
		return nil, fmt.Errorf("%w: %s is not a block list", ErrLayout, key.Value)
	}
	dash, field := seq.Column-1, seq.Column+1
	if first := seq.Content[0]; first.Kind == yaml.MappingNode && first.Style&yaml.FlowStyle == 0 &&
		first.Line == seq.Line && len(first.Content) > 0 {
		field = first.Content[0].Column - 1
	}
	if field <= dash {
		return nil, fmt.Errorf("%w: the items of %s are not indented under their dash", ErrLayout, key.Value)
	}
	last := d.blockEnd(key.Line, key.Column-1, dash == key.Column-1)
	return d.insertAfter(last, lines(dash, field)...), nil
}

// appendFlow adds an item to a flow list written on one line: [a, b] becomes [a, b, c].
func (d *doc) appendFlow(seq *yaml.Node, item string) ([]byte, error) {
	line := d.text(seq.Line)
	open := d.starts[seq.Line-1] + byteOffset(line, seq.Column)
	if open >= len(d.data) || d.data[open] != '[' {
		return nil, fmt.Errorf("%w: cannot find the start of a flow list", ErrLayout)
	}
	closing := -1
	depth, quote := 0, byte(0)
scan:
	for i := open; i < d.starts[seq.Line-1]+len(line); i++ {
		c := d.data[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				closing = i
				break scan
			}
		}
	}
	if closing < 0 {
		return nil, fmt.Errorf("%w: a flow list that spans several lines", ErrLayout)
	}
	at := closing
	for at > open+1 && d.data[at-1] == ' ' {
		at--
	}
	insert := ", " + item
	switch d.data[at-1] {
	case '[':
		insert = item
	case ',':
		insert = " " + item
	}
	out := make([]byte, 0, len(d.data)+len(insert))
	out = append(out, d.data[:at]...)
	out = append(out, insert...)
	return append(out, d.data[at:]...), nil
}

// byteOffset converts a column, counted in characters from 1, into a byte offset in the line.
func byteOffset(line string, column int) int {
	i := 0
	for n := 1; n < column && i < len(line); n++ {
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
	}
	return i
}
