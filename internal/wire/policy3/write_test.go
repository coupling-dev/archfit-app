package policy3_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

var update = flag.Bool("update", false, "write each writer golden out.yaml from the current output; read the diff before you commit it")

// answerFile is the answer.json of a writer golden: the kind of the answer and its fields.
type answerFile struct {
	Kind       policy3.Answer                               `json:"kind"`
	Loosens    bool                                         `json:"loosens"`
	Module     string                                       `json:"module"`
	Dependency string                                       `json:"dependency"`
	Exception  *policy3.Exception                           `json:"exception"`
	Rule       *struct{ ID, From, To, Why, Instead string } `json:"rule"`
}

func (a answerFile) apply(in []byte) ([]byte, error) {
	switch a.Kind {
	case policy3.Approve:
		return policy3.AddDependsOn(in, a.Module, a.Dependency)
	case policy3.ApproveUntil:
		return policy3.AddException(in, *a.Exception)
	case policy3.Reject:
		return policy3.AddDenyRule(in, a.Rule.ID, a.Rule.From, a.Rule.To, a.Rule.Why, a.Rule.Instead)
	case policy3.Confirm:
	}
	return nil, fmt.Errorf("answer %q writes nothing", a.Kind)
}

func readCase(t *testing.T, dir string) (in []byte, answer answerFile) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join(dir, "in.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "answer.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&answer); err != nil {
		t.Fatalf("%s/answer.json: %v", dir, err)
	}
	return in, answer
}

// isInsertion reports whether edited is old with one piece of text inserted: no byte of
// old was changed or removed.
func isInsertion(old, edited []byte) bool {
	n := len(edited) - len(old)
	if n < 0 {
		return false
	}
	for i := 0; i <= len(old); i++ {
		if bytes.Equal(edited[:i], old[:i]) && bytes.Equal(edited[i+n:], old[i:]) {
			return true
		}
	}
	return false
}

func TestWriterGoldens(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "writer", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no writer goldens: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			in, answer := readCase(t, dir)
			got, err := answer.apply(in)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			want := filepath.Join(dir, "out.yaml")
			if *update {
				if err := os.WriteFile(want, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			golden, err := os.ReadFile(want)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, golden) {
				t.Errorf("output differs from %s:\n%s", want, got)
			}
			if _, err := policy3.Decode(got); err != nil {
				t.Errorf("the output does not decode: %v", err)
			}
			if !isInsertion(in, got) {
				t.Error("the writer changed a byte of the old file; an edit may only insert")
			}
			if same := bytes.Equal(got, in); same != (policy3.Digest(got) == policy3.Digest(in)) {
				t.Errorf("bytes equal = %v but digests equal = %v", same, !same)
			}
			loosens, err := policy3.Loosens(answer.Kind)
			if err != nil || loosens != answer.Loosens {
				t.Errorf("Loosens(%s) = %v, %v; answer.json says %v", answer.Kind, loosens, err, answer.Loosens)
			}
		})
	}
}

// An answer that the policy already holds changes nothing, so its digest stays.
func TestWriterNoChangeKeepsTheDigest(t *testing.T) {
	in, answer := readCase(t, filepath.Join("testdata", "writer", "depends-on-already-allowed"))
	got, err := answer.apply(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, in) || policy3.Digest(got) != policy3.Digest(in) {
		t.Errorf("an allowed dependency changed the file:\n%s", got)
	}
}

// The digest is of the exact bytes: it changes with the edit and with any other byte, even
// one in a comment, and it does not change with a copy.
func TestDigestChangesOnlyWhenBytesChange(t *testing.T) {
	in, answer := readCase(t, filepath.Join("testdata", "writer", "depends-on-new-key"))
	edited, err := answer.apply(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		file []byte
		same bool
	}{
		{"a copy", bytes.Clone(in), true},
		{"the edit", edited, false},
		{"a changed comment", bytes.Replace(in, []byte("Shared helpers"), []byte("Shared helper"), 1), false},
		{"a trailing newline", append(bytes.Clone(in), '\n'), false},
		{"a trailing space", bytes.Replace(in, []byte("gate: required"), []byte("gate: required "), 1), false},
		{"windows line ends", bytes.ReplaceAll(in, []byte("\n"), []byte("\r\n")), false},
		{"no final newline", in[:len(in)-1], false},
	} {
		if same := policy3.Digest(tt.file) == policy3.Digest(in); same != tt.same {
			t.Errorf("%s: digest equal to the old file = %v, want %v", tt.name, same, tt.same)
		}
	}
	again, err := answer.apply(in)
	if err != nil || policy3.Digest(again) != policy3.Digest(edited) {
		t.Errorf("the same answer on the same file gave another digest (%v)", err)
	}
}

func TestWriterKeepsLineEndingsAndFinalNewline(t *testing.T) {
	in, answer := readCase(t, filepath.Join("testdata", "writer", "exception-append"))
	want, err := os.ReadFile(filepath.Join("testdata", "writer", "exception-append", "out.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func([]byte) []byte{
		"windows line ends": func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n")) },
		"no final newline":  func(b []byte) []byte { return bytes.TrimSuffix(b, []byte("\n")) },
	}
	for name, transform := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := answer.apply(transform(in))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, transform(want)) {
				t.Errorf("got\n%q\nwant\n%q", got, transform(want))
			}
		})
	}
}

func TestWriterRefuses(t *testing.T) {
	const base = "version: 3\nowners: [o]\nmodules:\n  app:\n    paths: [cmd/**]\n  lib:\n    paths: [lib/**]\n"
	tests := []struct {
		name    string
		do      func() ([]byte, error)
		wantErr string
		layout  bool
	}{
		{
			name:    "an unknown module",
			do:      func() ([]byte, error) { return policy3.AddDependsOn([]byte(base), "nope", "lib") },
			wantErr: `module "nope" is not in the policy`,
		},
		{
			name:    "an unknown dependency",
			do:      func() ([]byte, error) { return policy3.AddDependsOn([]byte(base), "app", "nope") },
			wantErr: `module "nope" is not in the policy`,
		},
		{
			name:    "a policy that does not decode",
			do:      func() ([]byte, error) { return policy3.AddDependsOn([]byte("version: 3\n"), "app", "lib") },
			wantErr: "owners: required",
		},
		{
			name: "a module written as a flow mapping",
			do: func() ([]byte, error) {
				return policy3.AddDependsOn([]byte("version: 3\nowners: [o]\nmodules:\n  app: {paths: [cmd/**]}\n  lib: {paths: [lib/**]}\n"), "app", "lib")
			},
			wantErr: "not a block mapping", layout: true,
		},
		{
			name: "a flow list over several lines",
			do: func() ([]byte, error) {
				return policy3.AddDependsOn([]byte(base+"  db:\n    paths: [db/**]\n  cli:\n    paths: [x/**]\n    depends_on: [lib,\n      app]\n"), "cli", "db")
			},
			wantErr: "several lines", layout: true,
		},
		{
			name: "an empty flow list of exceptions",
			do: func() ([]byte, error) {
				return policy3.AddException([]byte(base+"exceptions: []\n"), policy3.Exception{Rule: "graph", From: "app", To: "lib", Why: "w"})
			},
			wantErr: "not a block list", layout: true,
		},
		{
			name: "a module name a flow list would split",
			do: func() ([]byte, error) {
				return policy3.AddDependsOn([]byte(base+"  \"a, b\":\n    paths: [x/**]\n"), "app", "a, b")
			},
			wantErr: "more than the answer",
		},
		{
			name: "a date that is not a date",
			do: func() ([]byte, error) {
				return policy3.AddException([]byte(base), policy3.Exception{Rule: "graph", From: "app", To: "lib", Expires: "19-11-2026", Why: "w"})
			},
			wantErr: `expires "19-11-2026" is not a date`,
		},
		{
			name: "a reason on two lines",
			do: func() ([]byte, error) {
				return policy3.AddException([]byte(base), policy3.Exception{Rule: "graph", From: "app", To: "lib", Why: "one\ntwo"})
			},
			wantErr: "cannot be written on one line",
		},
		{
			name: "an exception with no target",
			do: func() ([]byte, error) {
				return policy3.AddException([]byte(base), policy3.Exception{Rule: "graph", From: "app", Why: "w"})
			},
			wantErr: "exceptions[0].to: required",
		},
		{
			name: "an unowned exception with a target",
			do: func() ([]byte, error) {
				return policy3.AddException([]byte(base), policy3.Exception{Rule: "unowned", From: "x/**", To: "lib", Why: "w"})
			},
			wantErr: "unowned has no target module",
		},
		{
			name: "a deny rule with no reason",
			do: func() ([]byte, error) {
				return policy3.AddDenyRule([]byte(base), "no-app-to-lib", "app", "lib", "", "Use a port.")
			},
			wantErr: "rules[0].why: empty string: give a value or remove the key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.do()
			if err == nil {
				t.Fatalf("the writer accepted it and wrote\n%s", got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want %q", err, tt.wantErr)
			}
			if errors.Is(err, policy3.ErrLayout) != tt.layout {
				t.Errorf("errors.Is(err, ErrLayout) = %v, want %v", !tt.layout, tt.layout)
			}
		})
	}
}
