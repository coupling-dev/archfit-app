package result_test

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The goldens cover each field of an error and an unknown reason with a path; these cover the rest of the grammar.
func TestRecordLines(t *testing.T) {
	fix := result.Next{Kind: "fix", Text: "fix the code"}
	for _, tc := range []struct {
		name string
		r    result.Result
		want string
	}{
		{
			name: "errors in the given order, then unknown reasons, then the status line",
			r: result.Result{
				Summary: "archfit: unknown, no verdict",
				Errors: []result.Error{
					{Path: "b.go", Line: 2, Rule: "graph", What: "second in file order", Next: fix},
					{Path: "a.go", Line: 1, Rule: "cycle", What: "first in file order", Next: fix},
				},
				Unknown: []result.Unknown{{Path: "c.go", Line: 3, Cause: "import", What: "an import cannot be resolved", Next: result.Next{Kind: "restore", Text: "fix the import"}}},
			},
			want: "b.go:2: graph: second in file order\n  next: fix the code\n" +
				"a.go:1: cycle: first in file order\n  next: fix the code\n" +
				"c.go:3: import: an import cannot be resolved\n  next: fix the import\n" +
				"archfit: unknown, no verdict\n",
		},
		{
			name: "an unknown reason without a path",
			r: result.Result{
				Summary: "archfit: unknown, no verdict",
				Unknown: []result.Unknown{{Cause: "base", What: "no merge base", Why: "nothing to compare with", Next: result.Next{Kind: "restore", Text: "git fetch --unshallow"}}},
			},
			want: "base: no merge base\n  why: nothing to compare with\n  next: git fetch --unshallow\narchfit: unknown, no verdict\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := result.Record(tc.r); got != tc.want {
				t.Errorf("record:\n--- got\n%s--- want\n%s", got, tc.want)
			}
		})
	}
}
