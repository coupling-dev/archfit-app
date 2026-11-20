package result_test

import (
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/result"
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
			name: "a v4.0.0 balance, then a 0.9.0 balance with its inputs and the surface of the target",
			r: result.Result{
				Summary: "archfit: fail, 2 errors",
				Errors: []result.Error{
					{Path: "a.go", Line: 1, Rule: "graph", What: "a -> b is not an allowed dependency", Balance: &result.Balance{Value: 9, Min: 7}, To: "b", Next: fix},
					{
						Path: "c.go", Line: 2, Rule: "coupling", What: "internal/assessment/score is past the surface", To: "assessment-repair",
						Balance: &result.Balance{Value: 2, Min: 7, Strength: result.StrengthIntrusive, Volatility: result.VolatilityHigh},
						Surface: &result.Surface{Public: []string{"internal/assessment/finding"}, Model: []string{"internal/assessment/result"}},
						Next:    fix,
					},
				},
			},
			want: "a.go:1: graph: a -> b is not an allowed dependency\n  balance: 9/10 (min 7)\n  next: fix the code\n" +
				"c.go:2: coupling: internal/assessment/score is past the surface\n" +
				"  balance: 2/10 (min 7): intrusive, assessment-repair is high\n" +
				"  surface: internal/assessment/finding (public), internal/assessment/result (model)\n" +
				"  next: fix the code\n" +
				"archfit: fail, 2 errors\n",
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
