package request_test

import (
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/request"
)

const copyKey = "duplication pkg_tc pkg_util sha256:3f1c2a0e5b7d4c6a8e9f0b1d2c3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e"

func TestErrorRequestKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    request.Error
		want string
	}{
		{"graph drops the imported package", request.Error{Rule: "graph", Path: "pkg/tc/netem.go", From: "pkg_tc", To: "pkg_util", Key: "graph pkg_tc pkg_util github.com/alexei-led/pumba/pkg/util"}, "graph pkg_tc pkg_util"},
		{"coupling", request.Error{Rule: "coupling", From: "a", To: "b", Key: "coupling a b x"}, "coupling a b"},
		{"layers", request.Error{Rule: "layers", From: "a", To: "b"}, "layers a b"},
		{"a custom deny rule", request.Error{Rule: "no-chaos-to-runtime", From: "pkg_chaos", To: "pkg_runtime"}, "no-chaos-to-runtime pkg_chaos pkg_runtime"},
		{"an only rule names the library", request.Error{Rule: "process-in-adapters", From: "pkg_tc", To: "process"}, "process-in-adapters pkg_tc process"},
		{"a cycle keeps its direction", request.Error{Rule: "cycle", From: "b", To: "a", Key: "cycle b a"}, "cycle b a"},
		{"a copy is the engine's key", request.Error{Rule: "duplication", From: "pkg_tc", To: "pkg_util", Key: copyKey}, copyKey},
		{"unowned code is its path", request.Error{Rule: "unowned", Path: "tools/gen/main.go", Key: "unowned tools/gen/main.go"}, "unowned tools/gen/main.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.e.RequestKey()
			if err != nil || got != tc.want {
				t.Errorf("RequestKey() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestErrorWithoutRequestKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    request.Error
	}{
		{"no rule", request.Error{From: "a", To: "b"}},
		{"an expired exception", request.Error{Rule: "exception", From: "pkg_tc", To: "pkg_util", Key: "exception duplication pkg_tc pkg_util"}},
		{"a copy without the engine's key", request.Error{Rule: "duplication", From: "a", To: "b"}},
		{"unowned code without a path", request.Error{Rule: "unowned"}},
		{"a dependency without its target", request.Error{Rule: "graph", From: "a"}},
		{"a dependency without its source", request.Error{Rule: "only-net", To: "net"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.e.RequestKey(); !errors.Is(err, request.ErrNoKey) {
				t.Errorf("RequestKey() = %q, %v; want ErrNoKey", got, err)
			}
		})
	}
}

// TestAnswerAndErrorShareTheKey: a request opened from a `can` answer and the same dependency in a check are one request.
func TestAnswerAndErrorShareTheKey(t *testing.T) {
	fromAnswer, err := request.Answer{Rule: "graph", From: "pkg_tc", To: "pkg_util"}.Key()
	if err != nil {
		t.Fatal(err)
	}
	fromError, err := request.Error{Rule: "graph", From: "pkg_tc", To: "pkg_util", Key: "graph pkg_tc pkg_util github.com/alexei-led/pumba/pkg/util"}.RequestKey()
	if err != nil {
		t.Fatal(err)
	}
	if fromAnswer != fromError {
		t.Errorf("answer key %q, error key %q", fromAnswer, fromError)
	}
}

func TestAnswerWithoutKey(t *testing.T) {
	for _, a := range []request.Answer{
		{From: "a", To: "b"},
		{Rule: "duplication", From: "a", To: "b"},
		{Rule: "cycle", From: "a", To: "b"},
		{Rule: "unowned", From: "a", To: "b"},
		{Rule: "exception", From: "a", To: "b"},
		{Rule: "graph", From: "a"},
	} {
		if got, err := a.Key(); !errors.Is(err, request.ErrNoKey) {
			t.Errorf("%+v: Key() = %q, %v; want ErrNoKey", a, got, err)
		}
	}
}
