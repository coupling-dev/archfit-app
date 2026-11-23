package policy3_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// requireBase is a policy with comments and unrelated keys, so a change to any byte but the
// gate value shows in the expected output.
const requireBase = `version: 1
owners: [olivia, oscar]
# a comment between top-level keys
modules:
  pkg_util:
    paths: [pkg/util/**]
  pkg_net:
    paths: [pkg/net/**]
`

func withGate(value string) string {
	return requireBase + "gate: " + value + "\n"
}

func TestRequireGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		want    string
		wantErr error
		anyErr  bool
	}{
		{name: "missing gate adds the line", file: requireBase, want: withGate("required")},
		{name: "advisory is replaced", file: withGate("advisory"), want: withGate("required")},
		{name: "quoted advisory is replaced", file: withGate(`"advisory"`), want: withGate("required")},
		{name: "required stays as it is", file: withGate("required"), want: withGate("required")},
		{name: "invalid YAML is an error", file: "version: 1\nowners: [\n", anyErr: true},
		{name: "missing version is an error", file: "owners: [olivia]\nmodules: {}\n", anyErr: true},
		{
			name:    "a gate on its own line is refused",
			file:    requireBase + "gate:\n  advisory\n",
			wantErr: policy3.ErrLayout,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policy3.RequireGate([]byte(tc.file))
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatal("err = nil, want one")
				}
				return
			case err != nil:
				t.Fatalf("RequireGate: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("file =\n%s\nwant\n%s", got, tc.want)
			}
			pol, err := policy3.Decode(got)
			if err != nil || pol.Gate != policy3.GateRequired {
				t.Fatalf("decoded gate = %q, %v", pol.Gate, err)
			}
		})
	}
}

func TestRequireGateKeepsTheCommentOnTheGateLine(t *testing.T) {
	got, err := policy3.RequireGate([]byte(withGate("advisory  # the release gate")))
	if err != nil {
		t.Fatal(err)
	}
	if want := withGate("required  # the release gate"); string(got) != want {
		t.Fatalf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestRequireGateReturnsTheSameBytesWhenRequired(t *testing.T) {
	file := withGate("required")
	got, err := policy3.RequireGate([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(file)) {
		t.Fatalf("file changed: %q", got)
	}
}
