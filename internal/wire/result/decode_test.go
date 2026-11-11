package result_test

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// leak is seeded into rejected input; no error may quote it.
const leak = "LEAK-7f3a"

const hex64 = "1a954628a960aaef81d7b2d4521929579f3541e61a954628a960aaef81d7b2d4"

// edit replaces old, which must occur exactly once in the vendored example name, with new.
func edit(t *testing.T, name, old, new string) []byte {
	t.Helper()
	raw := string(read(t, resultDir+"/"+name+".json"))
	if n := strings.Count(raw, old); n != 1 {
		t.Fatalf("%s.json holds %q %d times, want once", name, old, n)
	}
	return []byte(strings.Replace(raw, old, new, 1))
}

func TestDecodeRejects(t *testing.T) {
	const (
		graph   = "graph"
		pass    = "pass"
		expired = "expired_exception"
		differs = "policy_differs"
		line    = `"line": 12`
		status  = `"status": "fail"`
		schema  = `"schema": "archfit.result.v1"`
	)
	for _, tc := range []struct {
		name     string
		example  string
		old, new string
		want     string
	}{
		{"an unknown key", graph, `"engine": "4.0.0"`, `"engine": "4.0.0", "` + leak + `": 1`, "result: unknown key"},
		{"an unknown nested key", graph, `"note": "do not copy the code"`, `"note": "n", "` + leak + `": 1`, "result: errors[0].next: unknown key"},
		{"a key in another case", graph, status, `"Status": "fail"`, "result: unknown key"},
		{"a duplicate key", graph, status, status + `, "status": "pass"`, "result: status: duplicate key"},
		{"a null value", graph, `"summary": "archfit: fail, 1 error"`, `"summary": null`, "result: summary: must not be null"},
		{"a null list", pass, `"errors": []`, `"errors": null`, "result: errors: must not be null"},
		{"a null nested value", graph, `"why": "a new dependency between modules needs an owner"`, `"why": null`, "result: errors[0].why: must not be null"},
		{"a missing key", pass, `"summary": "archfit: pass",`, ``, "result: summary: required"},
		{"a missing next kind", graph, `"kind": "command",`, ``, "result: errors[0].next.kind: required"},
		{"a missing policy flag", graph, `"protected": true,`, ``, "result: policy.protected: required"},
		{"a string for an integer", graph, line, `"line": "` + leak + `"`, "result: errors[0].line: must be an integer"},
		{"a string for a boolean", graph, `"from_change": true`, `"from_change": "` + leak + `"`, "result: errors[0].from_change: must be a boolean"},
		{"an object for a list", pass, `"errors": []`, `"errors": {}`, "result: errors: must be an array"},
		{"a list for an object", graph, `"balance": {`, `"balance": [{`, "result: errors[0].balance: must be an object"},
		{"line 0", graph, line, `"line": 0`, "result: errors[0].line: must be an integer >= 1"},
		{"a negative line", graph, line, `"line": -1`, "result: errors[0].line: must be an integer >= 1"},
		{"a fractional line", graph, line, `"line": 1.5`, "result: errors[0].line: must be an integer >= 1"},
		{"an exponent line", graph, line, `"line": 1e1`, "result: errors[0].line: must be an integer >= 1"},
		{"an empty summary", graph, `"summary": "archfit: fail, 1 error"`, `"summary": ""`, "result: summary: must not be empty"},
		{"an empty key", graph, `"key": "graph pkg_tc pkg_util github.com/alexei-led/pumba/pkg/util"`, `"key": ""`, "result: errors[0].key: must not be empty"},
		{"an empty next text", graph, `"text": "archfit request -m \"<why you need it>\""`, `"text": ""`, "result: errors[0].next.text: must not be empty"},
		{"a balance above 10", graph, `"value": 9`, `"value": 11`, "result: errors[0].balance.value: must be <= 10"},
		{"a balance of 0", graph, `"value": 9`, `"value": 0`, "result: errors[0].balance.value: must be an integer >= 1"},
		{"a can answer", graph, schema, `"schema": "archfit.can.v1"`, "result: schema: must be one of archfit.result.v1"},
		{"another version", graph, schema, `"schema": "archfit.result.v2"`, "result: schema: must be one of archfit.result.v1"},
		{"an unknown status", graph, status, `"status": "` + leak + `"`, "result: status: must be one of pass, fail, waiting, unknown"},
		{"an unknown next kind", graph, `"kind": "command"`, `"kind": "` + leak + `"`, "result: errors[0].next.kind: must be one of fix, command, wait, restore"},
		{"an unknown cause", differs, `"cause": "policy"`, `"cause": "` + leak + `"`, "result: unknown[0].cause: must be one of policy, base, tool, import"},
		{"a bare policy digest", graph, `"digest": "sha256:`, `"digest": "` + leak, "result: policy.digest: must match ^sha256:[0-9a-f]{64}$"},
		{"a short head", graph, `"head": "1a954628a960aaef81d7b2d4521929579f3541e6"`, `"head": "1a95` + leak + `"`, "result: head: must match ^[0-9a-f]{40}([0-9a-f]{24})?$"},
		{"an empty base", graph, `"base": "1405df66cbe219b0bf6355bc3d60361a8376b6b4"`, `"base": ""`, "result: base: must match ^[0-9a-f]{40}([0-9a-f]{24})?$"},
		{"a malformed date", graph, `"date": "2026-11-10"`, `"date": "` + leak + `"`, "result: date: must match ^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
		{"a malformed expiry", expired, `"expires": "2026-11-19"`, `"expires": "19.11.2026` + leak + `"`, "result: errors[0].exception.expires: must match ^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
		{"trailing data", pass, `"engine": "4.0.0"
}`, `"engine": "4.0.0"
} {}`, "result: trailing data after the object"},
		{"a truncated document", pass, `"engine": "4.0.0"
}`, `"engine": "4.0.0"`, "result: malformed JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := result.Decode(edit(t, tc.example, tc.old, tc.new))
			if err == nil {
				t.Fatalf("Decode accepted it: %+v", r)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), leak) {
				t.Errorf("the error quotes the input: %q", err)
			}
		})
	}
}

func TestDecodeRejectsWhatIsNotOneObject(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"empty input", ``, "result: malformed JSON"},
		{"a top-level array", `[]`, "result: must be an object"},
		{"a top-level string", `"` + leak + `"`, "result: must be an object"},
		{"invalid UTF-8", "{\"schema\": \"\xff\"}", "result: invalid UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := result.Decode([]byte(tc.raw))
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeAcceptsAbsentOptionalKeys(t *testing.T) {
	for _, tc := range []struct {
		name, example, old, new string
		check                   func(result.Result) bool
	}{
		{
			"no base and no head", "pass",
			`"base": "1405df66cbe219b0bf6355bc3d60361a8376b6b4",
  "head": "1a954628a960aaef81d7b2d4521929579f3541e6",`, ``,
			func(r result.Result) bool { return r.Base == "" && r.Head == "" },
		},
		{
			"a head of a SHA-256 repository", "graph",
			`"head": "1a954628a960aaef81d7b2d4521929579f3541e6"`, `"head": "` + hex64 + `"`,
			func(r result.Result) bool { return r.Head == hex64 },
		},
		{
			"no why, balance or note", "graph",
			`"why": "a new dependency between modules needs an owner",
      "balance": {
        "value": 9,
        "min": 7
      },`, ``,
			func(r result.Result) bool { e := r.Errors[0]; return e.Why == "" && e.Balance == nil && e.Line == 12 },
		},
		{
			"no policy target", "pass", `"differs": false,
    "target": "origin/main"`, `"differs": false`,
			func(r result.Result) bool { return r.Policy.Target == "" && r.Policy.Protected },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := result.Decode(edit(t, tc.example, tc.old, tc.new))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !tc.check(r) {
				t.Errorf("decoded %+v", r)
			}
		})
	}
}

func TestDecodeReadsEveryField(t *testing.T) {
	r, err := result.Decode(read(t, resultDir+"/expired_exception.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := r.Errors[0]
	if r.Status != result.StatusFail || r.Date != "2026-11-20" || r.Engine != "4.0.0" || len(r.Unknown) != 0 {
		t.Errorf("result = %+v", r)
	}
	if e.Path != ".archfit.yaml" || e.Line != 0 || e.Rule != "exception" || e.FromChange || e.Key != "exception duplication pkg_tc pkg_util" {
		t.Errorf("error = %+v", e)
	}
	if e.Exception == nil || *e.Exception != (result.Exception{Rule: "duplication", Expires: "2026-11-19"}) {
		t.Errorf("exception = %+v", e.Exception)
	}
	if e.Next != (result.Next{Kind: "wait", Text: "wait for an owner: #431"}) {
		t.Errorf("next = %+v", e.Next)
	}
	if !r.Policy.Protected || r.Policy.Differs || r.Policy.Target != "origin/main" {
		t.Errorf("policy = %+v", r.Policy)
	}
}
