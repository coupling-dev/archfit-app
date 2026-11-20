package workflow_test

import (
	"reflect"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/workflow"
)

func TestUsesRefs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "step and job uses",
			raw: `jobs:
  check:
    steps:
      - uses: coupling-dev/archfit-action@abc123
      - run: echo hello
  call:
    uses: owner/repo/.github/workflows/x.yaml@main
`,
			want: []string{"coupling-dev/archfit-action@abc123", "owner/repo/.github/workflows/x.yaml@main"},
		},
		{
			name: "a run string and an env value are not calls",
			raw: `jobs:
  check:
    env:
      USES: coupling-dev/archfit-action@abc123
    steps:
      - run: echo "uses: coupling-dev/archfit-action@abc123"
`,
			want: nil,
		},
		{name: "not yaml", raw: "\t- broken: [", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflow.UsesRefs([]byte(tt.raw)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UsesRefs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestActionRef(t *testing.T) {
	tests := []struct {
		uses, repository, ref string
		ok                    bool
	}{
		{"coupling-dev/archfit-action@abc123", "coupling-dev/archfit-action", "abc123", true},
		{"Coupling-Dev/Archfit-Action@v1", "coupling-dev/archfit-action", "v1", true},
		{"coupling-dev/archfit-action@", "coupling-dev/archfit-action", "", true},
		{"actions/checkout@v4", "coupling-dev/archfit-action", "", false},
		{"coupling-dev/archfit-action-fork@abc", "coupling-dev/archfit-action", "", false},
	}
	for _, tt := range tests {
		ref, ok := workflow.ActionRef(tt.uses, tt.repository)
		if ok != tt.ok || ref != tt.ref {
			t.Errorf("ActionRef(%q) = %q, %v; want %q, %v", tt.uses, ref, ok, tt.ref, tt.ok)
		}
	}
}
