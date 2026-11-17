package ui

import "testing"

func TestEditProblemsReadInThePolicyWords(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{".archfit.yaml:4: policy: field foo not found in type policy3.File", "Line 4: `foo` is not a policy key."},
		{".archfit.yaml:6: policy: modules.a.paths: required: list at least one glob", "Line 6: module a needs at least one path glob."},
		{`.archfit.yaml:7: policy: modules.a.volatility: "wild" is not one of low, medium, high`, `Line 7: volatility must be low, medium or high (got "wild").`},
		{".archfit.yaml:2: policy: did not find expected node content", "Line 2: did not find expected node content."},
		{"something without a line", "something without a line"},
	} {
		if got := editProblem(tt.in); got != tt.want {
			t.Errorf("editProblem(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
