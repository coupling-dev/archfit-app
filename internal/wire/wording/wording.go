// Package wording holds the words of design section 5 that Archfit output never uses, as the engine checks them.
//
// wording.go.txt is the engine's internal/testutil/wording.go, a byte copy pinned in SCHEMA_SOURCE. It is read, not
// compiled: NotUsedIn takes the engine's list and its gate names from that file, so the App and the engine check the
// same words. Tests of every package that renders text for a person call NotUsedIn on their goldens.
package wording

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

//go:embed wording.go.txt
var vendored []byte

var notUsed = load(vendored)

type words struct {
	list      []string
	patterns  []*regexp.Regexp
	gateNames *regexp.Regexp
}

// NotUsedIn returns the words of the "Not used" column of design section 5 that text uses, in list order: whole words
// in any case, a phrase across any white space, after the two gate names (the check archfit/gate and the policy key
// gate:) are removed. It matches as the engine's testutil.NotUsedIn.
func NotUsedIn(text string) []string {
	text = notUsed.gateNames.ReplaceAllString(text, " ")
	var found []string
	for i, p := range notUsed.patterns {
		if p.MatchString(text) {
			found = append(found, notUsed.list[i])
		}
	}
	return found
}

// load reads the engine's notUsedWords and gateNames from the vendored source. The file is fixed at build time, so a
// file it cannot read is a defect of this package and panics; TestNotUsedMatchesAsTheEngine runs it.
func load(src []byte) words {
	file, err := parser.ParseFile(token.NewFileSet(), "wording.go.txt", src, 0)
	if err != nil {
		panic(fmt.Sprintf("wording: %v", err))
	}
	var w words
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
						w.list = append(w.list, unquote(elt))
					}
				}
			case "gateNames":
				if call, ok := v.Values[0].(*ast.CallExpr); ok && len(call.Args) == 1 {
					w.gateNames = regexp.MustCompile(unquote(call.Args[0]))
				}
			}
		}
	}
	if len(w.list) == 0 || w.gateNames == nil {
		panic("wording: wording.go.txt has no notUsedWords or gateNames")
	}
	for _, word := range w.list {
		phrase := strings.Join(strings.Fields(regexp.QuoteMeta(word)), `\s+`)
		w.patterns = append(w.patterns, regexp.MustCompile(`(?i)(^|[^\pL\pN_])`+phrase+`($|[^\pL\pN_])`))
	}
	return w
}

func unquote(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		panic("wording: wording.go.txt: want a string literal")
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		panic(fmt.Sprintf("wording: %v", err))
	}
	return s
}
