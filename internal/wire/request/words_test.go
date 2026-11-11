package request_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// wordingFile is the engine's internal/testutil/wording.go, vendored byte for byte and pinned in SCHEMA_SOURCE.
const wordingFile = "testdata/wording.go.txt"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestVendoredFilesMatchSchemaSource holds the vendored word list to its SCHEMA_SOURCE line, and the line to a file.
func TestVendoredFilesMatchSchemaSource(t *testing.T) {
	header := map[string][]string{}
	listed := map[string]string{}
	for line := range strings.Lines(string(read(t, "SCHEMA_SOURCE"))) {
		line = strings.TrimSpace(line)
		if key, value, ok := strings.Cut(line, ": "); ok {
			header[key] = append(header[key], value)
			continue
		}
		sum, path, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("SCHEMA_SOURCE line %q is neither key: value nor <sha256>  <path>", line)
		}
		listed[path] = sum
	}
	if got := header["repository"]; !slices.Equal(got, []string{"github.com/alexei-led/archfit"}) {
		t.Errorf("repository = %q", got)
	}
	if got := header["commit"]; len(got) != 1 || !commitPattern.MatchString(got[0]) {
		t.Errorf("commit = %q, want one 40-hex commit", got)
	}
	if got := header["from"]; !slices.Equal(got, []string{wordingFile + " <- internal/testutil/wording.go"}) {
		t.Errorf("from = %q", got)
	}
	if got := slices.Sorted(maps.Keys(listed)); !slices.Equal(got, []string{wordingFile}) {
		t.Fatalf("SCHEMA_SOURCE lists %q, want only %s", got, wordingFile)
	}
	sum := sha256.Sum256(read(t, wordingFile))
	if got := hex.EncodeToString(sum[:]); got != listed[wordingFile] {
		t.Errorf("%s: sha256 %s, SCHEMA_SOURCE says %s", wordingFile, got, listed[wordingFile])
	}
}

// TestGoldensUseNoNotUsedWord: no golden uses a word of the "Not used" column of design section 5.
func TestGoldensUseNoNotUsedWord(t *testing.T) {
	notUsed := loadNotUsed(t)
	for _, path := range allGoldens(t) {
		if path == wordingFile {
			continue
		}
		if found := notUsed.in(string(read(t, path))); len(found) > 0 {
			t.Errorf("%s uses %q", path, found)
		}
	}
}

func TestNotUsedMatchesAsTheEngine(t *testing.T) {
	notUsed := loadNotUsed(t)
	for _, tc := range []struct {
		text string
		want []string
	}{
		{text: "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n"},
		{text: "a decision request", want: []string{"decision", "decision request"}},
		{text: "needs\n  attention", want: []string{"needs attention"}},
		{text: "config_file edge_count tasks"},
		{text: "the check archfit/gate failed; gate: required"},
		{text: "the gate is closed", want: []string{"gate"}},
	} {
		if got := notUsed.in(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("in(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// notUsedWords matches as the engine's testutil.NotUsedIn: whole words in any case, phrases across white space,
// with the gate names removed first.
type notUsedWords struct {
	words     []string
	patterns  []*regexp.Regexp
	gateNames *regexp.Regexp
}

func (n notUsedWords) in(text string) []string {
	text = n.gateNames.ReplaceAllString(text, " ")
	var found []string
	for i, p := range n.patterns {
		if p.MatchString(text) {
			found = append(found, n.words[i])
		}
	}
	return found
}

// loadNotUsed reads notUsedWords and the gateNames pattern from the vendored engine file.
func loadNotUsed(t *testing.T) notUsedWords {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), wordingFile, read(t, wordingFile), 0)
	if err != nil {
		t.Fatal(err)
	}
	var n notUsedWords
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			v, ok := spec.(*ast.ValueSpec)
			if !ok || len(v.Values) != 1 {
				continue
			}
			switch v.Names[0].Name {
			case "notUsedWords":
				if list, ok := v.Values[0].(*ast.CompositeLit); ok {
					for _, elt := range list.Elts {
						n.words = append(n.words, unquote(t, elt))
					}
				}
			case "gateNames":
				if call, ok := v.Values[0].(*ast.CallExpr); ok && len(call.Args) == 1 {
					n.gateNames = regexp.MustCompile(unquote(t, call.Args[0]))
				}
			}
		}
	}
	if len(n.words) == 0 || n.gateNames == nil {
		t.Fatalf("%s has no notUsedWords or gateNames", wordingFile)
	}
	for _, word := range n.words {
		phrase := strings.Join(strings.Fields(regexp.QuoteMeta(word)), `\s+`)
		n.patterns = append(n.patterns, regexp.MustCompile(`(?i)(^|[^\pL\pN_])`+phrase+`($|[^\pL\pN_])`))
	}
	return n
}

func unquote(t *testing.T, expr ast.Expr) string {
	t.Helper()
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		t.Fatalf("%s: want a string literal", wordingFile)
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
