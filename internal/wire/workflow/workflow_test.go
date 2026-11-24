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

func TestActionEndpoint(t *testing.T) {
	const action = "coupling-dev/archfit-action"
	step := func(uses, with string) string {
		return "jobs:\n  archfit:\n    steps:\n      - uses: actions/checkout@v4\n      - uses: " + uses + "\n" + with
	}
	tests := []struct {
		name     string
		raw      string
		endpoint string
		ok       bool
	}{
		{"literal", step(action+"@abc", "        with:\n          endpoint: https://app.coupling.dev\n          discover: true\n"),
			"https://app.coupling.dev", true},
		{"quoted", step(action+"@abc", "        with:\n          endpoint: \"https://app-staging.coupling.dev\"\n"),
			"https://app-staging.coupling.dev", true},
		{"repository case", step("Coupling-Dev/Archfit-Action@abc", "        with:\n          endpoint: https://x.test\n"),
			"https://x.test", true},
		{"expression", step(action+"@abc", "        with:\n          endpoint: ${{ vars.ARCHFIT_URL }}\n"),
			"${{ vars.ARCHFIT_URL }}", true},
		{"empty input", step(action+"@abc", "        with:\n          endpoint: \"\"\n"), "", false},
		{"no input", step(action+"@abc", "        with:\n          audience: https://x.test\n"), "", false},
		{"no with", step(action+"@abc", ""), "", false},
		{"another action", step("actions/setup-go@v5", "        with:\n          endpoint: https://x.test\n"), "", false},
		{"first action step in job-name order", "jobs:\n  b:\n    steps:\n      - uses: " + action + "@abc\n" +
			"        with:\n          endpoint: https://b.test\n  a:\n    steps:\n      - uses: " + action + "@abc\n" +
			"        with:\n          endpoint: https://a.test\n", "https://a.test", true},
		{"run text is not a call", "jobs:\n  check:\n    steps:\n      - run: |\n          uses: " + action +
			"@abc\n          endpoint: https://x.test\n", "", false},
		{"non-scalar inputs of steps", "jobs:\n  check:\n    steps:\n      - uses: actions/cache@v4\n" +
			"        with:\n          path: [a, b]\n      - uses: " + action + "@abc\n        with:\n" +
			"          endpoint: https://x.test\n          extra: {k: v}\n", "https://x.test", true},
		{"a non-scalar endpoint", step(action+"@abc", "        with:\n          endpoint: [https://x.test]\n"), "", false},
		{"a step without an endpoint does not hide a later one", "jobs:\n  a:\n    steps:\n      - uses: " + action +
			"@abc\n  b:\n    steps:\n      - uses: " + action + "@abc\n        with:\n          endpoint: https://b.test\n",
			"https://b.test", true},
		{"not yaml", "\t- broken: [", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, ok := workflow.ActionEndpoint([]byte(tt.raw), action)
			if endpoint != tt.endpoint || ok != tt.ok {
				t.Errorf("ActionEndpoint = %q, %v; want %q, %v", endpoint, ok, tt.endpoint, tt.ok)
			}
		})
	}
}
