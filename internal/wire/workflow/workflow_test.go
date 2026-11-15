package workflow_test

import (
	"reflect"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/workflow"
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
      - uses: alexei-led/archfit-action@abc123
      - run: echo hello
  call:
    uses: owner/repo/.github/workflows/x.yaml@main
`,
			want: []string{"alexei-led/archfit-action@abc123", "owner/repo/.github/workflows/x.yaml@main"},
		},
		{
			name: "a run string and an env value are not calls",
			raw: `jobs:
  check:
    env:
      USES: alexei-led/archfit-action@abc123
    steps:
      - run: echo "uses: alexei-led/archfit-action@abc123"
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
		{"alexei-led/archfit-action@abc123", "alexei-led/archfit-action", "abc123", true},
		{"Alexei-Led/Archfit-Action@v1", "alexei-led/archfit-action", "v1", true},
		{"alexei-led/archfit-action@", "alexei-led/archfit-action", "", true},
		{"actions/checkout@v4", "alexei-led/archfit-action", "", false},
		{"alexei-led/archfit-action-fork@abc", "alexei-led/archfit-action", "", false},
	}
	for _, tt := range tests {
		ref, ok := workflow.ActionRef(tt.uses, tt.repository)
		if ok != tt.ok || ref != tt.ref {
			t.Errorf("ActionRef(%q) = %q, %v; want %q, %v", tt.uses, ref, ok, tt.ref, tt.ok)
		}
	}
}
