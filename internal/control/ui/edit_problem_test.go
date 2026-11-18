package ui

import "testing"

func TestEditProblemsReadInThePolicyWords(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{".archfit.yaml:4: policy: field foo not found in type policy3.File", "Line 4: `foo` is not a policy key."},
		{".archfit.yaml:6: policy: modules.a.paths: required: list at least one glob", "Line 6: module a needs at least one path glob."},
		{`.archfit.yaml:7: policy: modules.a.volatility: "wild" is not one of low, medium, high`, `Line 7: volatility must be low, medium or high (got "wild").`},
		{".archfit.yaml:2: policy: did not find expected node content", "Line 2: did not find expected node content."},
		{".archfit.yaml:3: policy: cannot unmarshal !!int `5` into []string", "Line 3: this value has the wrong type."},
		{".archfit.yaml:3: policy: modules.a.public: want a list", "Line 3: modules.a.public must be a list."},
		{".archfit.yaml:4: policy: field my key not found in type policy3.File", "Line 4: `my key` is not a policy key."},
		{".archfit.yaml:5: policy: a value that says policy3.File and !! stays a plain problem", "Line 5: a value that says policy3.File and !! stays a plain problem."},
		{"something without a line", "something without a line"},
	} {
		if got := editProblem(tt.in); got != tt.want {
			t.Errorf("editProblem(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEditProblemsDropARepeatedSentence(t *testing.T) {
	got := editProblems([]string{
		".archfit.yaml:3: policy: cannot unmarshal !!int `5` into []string",
		".archfit.yaml:3: policy: cannot unmarshal !!str `x` into []string",
	})
	if len(got) != 1 || got[0] != "Line 3: this value has the wrong type." {
		t.Fatalf("got %q", got)
	}
}
