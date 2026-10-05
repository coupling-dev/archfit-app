package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Diagnostic is one problem of a policy text, worded for the person who wrote the text.
type Diagnostic struct {
	Line    int    // 1-based line in the text; 0 when the problem has no single place
	Column  int    // 1-based column; 0 when unknown
	Message string // one sentence; may quote keys of the text, at most 64 characters of each
}

const (
	maxDiagnostics = 100
	maxQuotedRunes = 64
	mergeTag       = "!!merge"
)

// Diagnose checks a policy text against the embedded engine config schema and the App's own readers. It returns
// nil exactly when the text is fit to save: then Validate, Waivers and Modules all accept it too. It is not engine
// validation: rule types, selectors and ownership are the engine's (`archfit config lint`). Unlike every other
// function here, its messages may quote keys of the input and the YAML library's syntax problem: they are for the
// author of the text and must never reach a log.
func Diagnose(policy []byte) []Diagnostic {
	root, err := engineSchema()
	if err != nil {
		return []Diagnostic{{Message: "The App cannot read its embedded engine schema."}}
	}
	keys, err := engineKeys()
	if err != nil {
		return []Diagnostic{{Message: "The App cannot read its embedded engine schema."}}
	}
	// Diagnose reports lines, so a line break the user's text area does not show is refused first. A lone CR
	// stays readable here: lineCount counts it like the library; Modules refuses it below when it matters.
	if bytes.ContainsAny(policy, "\u0085\u2028\u2029") {
		return []Diagnostic{readDiagnostic(errLineBreak, 0)}
	}
	top, ok, err := read(policy)
	switch {
	case err != nil:
		return []Diagnostic{readDiagnostic(err, lineCount(policy))}
	case !ok:
		var diags []Diagnostic
		for _, key := range root.Required {
			diags = append(diags, Diagnostic{Message: "Missing required key " + code(key) + "."})
		}
		return diags
	}
	c := checker{}
	c.value(root, top.root, "", true)
	if len(c.diags) > 0 {
		slices.SortStableFunc(c.diags, func(a, b Diagnostic) int {
			if a.Line != b.Line {
				return a.Line - b.Line
			}
			return a.Column - b.Column
		})
		if c.dropped {
			c.diags = append(c.diags, Diagnostic{Message: fmt.Sprintf("More problems are not listed. Fix the first %d and check again.", maxDiagnostics)})
		}
		return c.diags
	}
	// A schema-valid text can still fail the App's readers (module name length, a lone CR). Their errors name no
	// key or value of the input. They read the tree already parsed above.
	if _, err := waiversOf(top); err != nil {
		return []Diagnostic{readerDiagnostic(err)}
	}
	if _, err := modulesOf(policy, top); err != nil {
		return []Diagnostic{readerDiagnostic(err)}
	}
	if err := keys.checkTop(top, true); err != nil {
		return []Diagnostic{readerDiagnostic(err)}
	}
	return nil
}

// readDiagnostic words a reading error. lines bounds a syntax line: a problem at the end of the stream sits one
// past the last line.
func readDiagnostic(err error, lines int) Diagnostic {
	var se *syntaxError
	switch {
	case errors.As(err, &se):
		d := Diagnostic{Line: min(se.line, lines), Message: "The text is not valid YAML."}
		switch {
		case se.problem == "":
		case se.exact || d.Line == 0:
			d.Message = "YAML syntax: " + quoted(se.problem) + "."
		default:
			d.Message = fmt.Sprintf("YAML syntax: %s. The problem is at line %d or below it.", quoted(se.problem), d.Line)
		}
		return d
	case errors.Is(err, errTooLarge):
		return Diagnostic{Message: "The policy is larger than 1 MB."}
	case errors.Is(err, errNotUTF8):
		return Diagnostic{Message: "The policy is not valid UTF-8 text."}
	case errors.Is(err, errLineBreak):
		return Diagnostic{Message: "The policy has a NEL, LS or PS line break. Use ordinary line breaks."}
	case errors.Is(err, errTooComplex):
		return Diagnostic{Message: "The policy has too many YAML nodes, keys or alias expansions for the App to check."}
	case errors.Is(err, errNotOneDocument):
		return Diagnostic{Message: "The policy must be exactly one YAML document."}
	case errors.Is(err, errRootNotMapping):
		return Diagnostic{Line: 1, Message: "The top level must be a mapping of keys, such as `version: 2`."}
	}
	return Diagnostic{Message: "The text is not valid YAML."}
}

// lineCount counts lines the way the YAML library does once Diagnose has refused NEL, LS and PS: LF, CRLF and a
// lone CR each end one.
func lineCount(text []byte) int {
	return bytes.Count(text, []byte("\n")) + bytes.Count(text, []byte("\r")) - bytes.Count(text, []byte("\r\n")) + 1
}

func readerDiagnostic(err error) Diagnostic {
	return Diagnostic{Message: "The App cannot read this policy: " + strings.TrimPrefix(err.Error(), "policy: ") + "."}
}

type checker struct {
	diags   []Diagnostic
	dropped bool
	silent  bool // a oneOf trial: count problems, record none
	failed  bool
	// site is the alias or merge key whose expansion is being checked. A problem inside an expansion is reported
	// there, once per use, not at the anchor that every use shares.
	site *yaml.Node
}

// record counts a problem and reports whether it is to be listed. Callers build the message only after it.
func (c *checker) record() bool {
	c.failed = true
	if c.silent {
		return false
	}
	if len(c.diags) == maxDiagnostics {
		c.dropped = true
		return false
	}
	return true
}

func (c *checker) add(n *yaml.Node, msg string) {
	if c.record() {
		c.put(n, msg)
	}
}

// addSuggest is add with "Did you mean" when one of candidates is close to got.
func (c *checker) addSuggest(n *yaml.Node, msg, got string, candidates []string) {
	if !c.record() {
		return
	}
	if s := suggest(got, candidates); s != "" {
		msg += " Did you mean " + code(s) + "?"
	}
	c.put(n, msg)
}

func (c *checker) put(n *yaml.Node, msg string) {
	if c.site != nil {
		n = c.site
	}
	c.diags = append(c.diags, Diagnostic{Line: n.Line, Column: n.Column, Message: msg})
}

// within checks with site as the place of every problem found, unless an outer expansion already set one.
func (c *checker) within(site *yaml.Node, check func()) {
	prev := c.site
	if prev == nil {
		c.site = site
	}
	check()
	c.site = prev
}

// value checks node n against schema s. name is the key that holds n, "" for a list item. A YAML null is the
// engine decoder's default for a key, so it passes every check here; mapping decides whether a required key
// with a null value is missing.
func (c *checker) value(s *schemaNode, n *yaml.Node, name string, root bool) {
	s = s.resolved()
	if n.Kind == yaml.AliasNode {
		c.within(n, func() { c.value(s, n.Alias, name, root) })
		return
	}
	if isNull(n) {
		return
	}
	if len(s.OneOf) > 0 {
		matched := 0
		for _, alt := range s.OneOf {
			trial := checker{silent: true}
			trial.value(alt, n, name, false)
			if !trial.failed {
				matched++
			}
		}
		if matched != 1 {
			c.add(n, subject(name)+" must be "+alternatives(s.OneOf)+".")
		}
		return
	}
	if (len(s.Const) > 0 || len(s.Enum) > 0) && n.Kind != yaml.ScalarNode {
		c.add(n, subject(name)+" must be "+allowedWords(s)+".")
		return
	}
	if !hasType(n, s.Type) {
		msg := subject(name) + " must be " + typeWords(s.Type)
		if _, err := strconv.ParseFloat(n.Value, 64); err == nil && n.ShortTag() == "!!str" && (s.Type == "integer" || s.Type == "number") {
			msg += " without quotes"
		}
		c.add(n, msg+".")
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		c.mapping(s, n, root)
	case yaml.SequenceNode:
		c.sequence(s, n, name)
	case yaml.ScalarNode:
		c.scalar(s, n, name)
	}
}

func (c *checker) mapping(s *schemaNode, n *yaml.Node, root bool) {
	present := map[string]bool{}
	nulls := map[string]*yaml.Node{} // a key whose value is null, and where to report it
	note := func(key, val, at *yaml.Node) {
		present[key.Value] = true
		if isNull(resolve(val)) {
			nulls[key.Value] = at
		}
	}
	var merges [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := resolve(n.Content[i]), n.Content[i+1]
		if key.ShortTag() == mergeTag {
			if root {
				// Validate never looks into a merge at the top level; neither does the engine's key set.
				c.add(key, "The top level cannot use a merge key `<<`. Write the keys out.")
				continue
			}
			merges = append(merges, [2]*yaml.Node{key, val})
			continue
		}
		note(key, val, val)
		c.entry(s, key, val, root)
	}
	// YAML merge semantics: the mapping's own keys win, then the merged mappings in order. A merged key is
	// checked against this mapping's schema and reported at the merge key.
	for _, m := range merges {
		for _, pair := range mergedPairs(m[1], 0) {
			key, val := resolve(pair[0]), pair[1]
			if present[key.Value] {
				continue
			}
			note(key, val, m[0])
			c.within(m[0], func() { c.entry(s, key, val, false) })
		}
	}
	for _, req := range s.Required {
		switch at, null := nulls[req]; {
		case !present[req]:
			c.add(n, "Missing required key "+code(req)+".")
		case null:
			c.add(at, code(req)+" needs a value.")
		}
	}
}

// mergedPairs lists the key and value nodes a merge value brings in, in merge order: a mapping's own keys, then
// its own merges. The full decode in read already refused alias cycles and merge values that are not mappings.
func mergedPairs(n *yaml.Node, depth int) [][2]*yaml.Node {
	n = resolve(n)
	if depth > maxSchemaDepth {
		return nil
	}
	var out, later [][2]*yaml.Node
	switch n.Kind {
	case yaml.SequenceNode:
		for _, item := range n.Content {
			out = append(out, mergedPairs(item, depth+1)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if resolve(n.Content[i]).ShortTag() == mergeTag {
				later = append(later, mergedPairs(n.Content[i+1], depth+1)...)
			} else {
				out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
			}
		}
	}
	return append(out, later...)
}

func (c *checker) entry(s *schemaNode, key, val *yaml.Node, root bool) {
	name := key.Value
	if root && key.ShortTag() != "!!str" {
		c.add(key, "Unknown top-level key "+code(name)+".")
		return
	}
	if prop, ok := s.Properties[name]; ok {
		c.value(prop, val, name, false)
		return
	}
	matched := false
	for _, p := range s.patterns {
		if p.match(name) {
			matched = true
			c.value(p.schema, val, name, false)
		}
	}
	switch {
	case matched, s.Additional == nil:
	case s.Additional.closed:
		c.addSuggest(key, "Unknown key "+code(name)+".", name, keysOf(s.Properties))
	case s.Additional.schema != nil:
		c.value(s.Additional.schema, val, name, false)
	}
}

func (c *checker) sequence(s *schemaNode, n *yaml.Node, name string) {
	if s.MinItems != nil && len(n.Content) < *s.MinItems {
		c.add(n, fmt.Sprintf("%s must list at least %d %s.", subject(name), *s.MinItems, plural(*s.MinItems, "item", "items")))
	}
	if s.Items == nil {
		return
	}
	for _, item := range n.Content {
		c.value(s.Items, item, "", false)
	}
}

func (c *checker) scalar(s *schemaNode, n *yaml.Node, name string) {
	if len(s.Const) > 0 && !scalarEquals(n, s.Const) {
		c.add(n, subject(name)+" must be "+literal(s.Const)+".")
		return
	}
	if len(s.Enum) > 0 {
		for _, v := range s.Enum {
			if scalarEquals(n, v) {
				return
			}
		}
		texts := make([]string, len(s.Enum))
		for i, v := range s.Enum {
			texts[i] = strings.Trim(string(v), `"`)
		}
		c.addSuggest(n, subject(name)+" must be "+allowedWords(s)+".", n.Value, texts)
		return
	}
	if s.Minimum != nil {
		if f, ok := number(n); ok && f < *s.Minimum {
			c.add(n, subject(name)+" must be at least "+strconv.FormatFloat(*s.Minimum, 'f', -1, 64)+".")
		}
	}
	if s.MinLength != nil && utf8.RuneCountInString(n.Value) < *s.MinLength {
		c.add(n, fmt.Sprintf("%s must be at least %d %s long.", subject(name), *s.MinLength,
			plural(*s.MinLength, "character", "characters")))
		return
	}
	// date-time is a hint here: the engine also reads a bare date there. link refuses every other format.
	if s.Format == "date" {
		if _, err := time.Parse(time.DateOnly, n.Value); err != nil || len(n.Value) != len(time.DateOnly) {
			c.add(n, subject(name)+" must be a date written YYYY-MM-DD.")
		}
	}
}

// hasType follows the YAML tags, with one leniency the engine's decoder has: a text field takes any scalar
// (`owner: 123`). Numbers and booleans need their own tag: the engine refuses a quoted number in some places
// (`metrics.function_loc_threshold`), so a quoted number is refused everywhere.
func hasType(n *yaml.Node, want string) bool {
	switch want {
	case "":
		return true
	case "object":
		return n.Kind == yaml.MappingNode
	case "array":
		return n.Kind == yaml.SequenceNode
	}
	if n.Kind != yaml.ScalarNode {
		return false
	}
	switch want {
	case "string":
		return true
	case "boolean":
		return n.ShortTag() == "!!bool"
	case "integer":
		return n.ShortTag() == "!!int"
	case "number":
		return n.ShortTag() == "!!int" || n.ShortTag() == "!!float"
	}
	return false
}

// number decodes a number scalar the way the library reads its tag (-0x1, 0o7, 1_000).
func number(n *yaml.Node) (float64, bool) {
	if tag := n.ShortTag(); tag != "!!int" && tag != "!!float" {
		return 0, false
	}
	var f float64
	return f, n.Decode(&f) == nil
}

func typeWords(t string) string {
	switch t {
	case "object":
		return "a mapping of keys"
	case "array":
		return "a list"
	case "string":
		return "text"
	case "boolean":
		return "`true` or `false`"
	case "integer":
		return "a whole number"
	case "number":
		return "a number"
	}
	return t
}

// allowedWords names the values a const or enum schema allows.
func allowedWords(s *schemaNode) string {
	if len(s.Const) > 0 {
		return literal(s.Const)
	}
	words := make([]string, len(s.Enum))
	for i, v := range s.Enum {
		words[i] = literal(v)
	}
	if len(words) == 1 {
		return words[0]
	}
	return "one of " + strings.Join(words, ", ")
}

func alternatives(alts []*schemaNode) string {
	var words []string
	for _, alt := range alts {
		alt = alt.resolved()
		if len(alt.Const) > 0 || len(alt.Enum) > 0 {
			words = append(words, allowedWords(alt))
		} else {
			words = append(words, typeWords(alt.Type))
		}
	}
	return strings.Join(words, " or ")
}

// scalarEquals compares a scalar with a JSON enum or const value: text with a string, the number with a number,
// a boolean with a boolean.
func scalarEquals(n *yaml.Node, v json.RawMessage) bool {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return n.Value == s
	}
	var want float64
	if json.Unmarshal(v, &want) == nil {
		got, ok := number(n)
		return ok && got == want
	}
	var b, got bool
	if json.Unmarshal(v, &b) == nil {
		return n.ShortTag() == "!!bool" && n.Decode(&got) == nil && got == b
	}
	return false
}

func literal(v json.RawMessage) string {
	return code(strings.Trim(string(bytes.TrimSpace(v)), `"`))
}

func subject(name string) string {
	if name == "" {
		return "Each item"
	}
	return code(name)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// code renders text as a Markdown-style code span. The page escapes it; quoted bounds it to one short line.
func code(text string) string {
	return "`" + quoted(text) + "`"
}

func quoted(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '`' {
			return ' '
		}
		return r
	}, text)
	if utf8.RuneCountInString(text) > maxQuotedRunes {
		text = string([]rune(text)[:maxQuotedRunes-1]) + "…"
	}
	return text
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// suggest returns the closest candidate within a small edit distance, "" when none is close. Ties go to the
// first candidate in the given order.
func suggest(got string, candidates []string) string {
	best, bestDist := "", 0
	for _, cand := range candidates {
		d := editDistance(got, cand)
		if d == 0 || d > 2 || d*3 > utf8.RuneCountInString(cand) {
			continue
		}
		if best == "" || d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best
}

// editDistance is the optimal string alignment distance: insertions, deletions, substitutions and swaps of
// two neighbours each cost one.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > 4*maxQuotedRunes || len(rb) > 4*maxQuotedRunes {
		return len(ra) + len(rb)
	}
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
