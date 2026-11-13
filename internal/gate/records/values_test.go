package records_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
)

func TestKeyLayout(t *testing.T) {
	tests := []struct{ got, want string }{
		{records.RepoKey(repoName), "v4/repo/acme/shop"},
		{records.RequestKey(repoName, 431), "v4/request/acme/shop/431"},
		{records.RequestIndexKey(repoName), "v4/request-index/acme/shop"},
		{records.ApprovalKey(repoName, "ab12"), "v4/approval/acme/shop/ab12"},
		{records.ProposalKey(repoName, "cd34"), "v4/proposal/acme/shop/cd34"},
		{records.ProposalIndexKey(repoName), "v4/proposal-index/acme/shop"},
		{records.ProposalLeaseKey(repoName), "v4/proposal-lease/acme/shop"},
		{records.PolicyPushKey(repoName, records.Push{Before: "be", After: "af"}), "v4/policy-push/acme/shop/be..af"},
		{records.PolicyPushKey(repoName, records.Push{After: "af"}), "v4/policy-push/acme/shop/" + strings.Repeat("0", 40) + "..af"},
		{records.RecoveredPushKey(repoName, "merge"), "v4/recovered-push/acme/shop/merge"},
		{records.PullKey(repoName, 7), "v4/pull/acme/shop/7"},
		{records.PullIndexKey(repoName), "v4/pull-index/acme/shop"},
		{records.ReposKey, "v4/repos"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("key %q, want %q", tt.got, tt.want)
		}
		if !strings.HasPrefix(tt.got, records.Prefix) {
			t.Errorf("key %q is outside %q", tt.got, records.Prefix)
		}
	}
}

func TestNewIDIsFreshAndKeySafe(t *testing.T) {
	a, b := records.NewID(), records.NewID()
	if a == b || len(a) != 26 || strings.ContainsAny(a, "/ ") || strings.ToLower(a) != a {
		t.Errorf("NewID = %q, %q; want two different 26-character lower-case ids without '/'", a, b)
	}
}

func TestValuesRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		in     records.Value
		out    records.Value
		schema string
	}{
		{"repo", &records.Repo{ApprovedDigest: "sha256:aa", OutsideFlow: &records.OutsideFlow{Digest: "sha256:bb", Issue: 12},
			DriftIssue: 13, ExpiryIssues: map[string]int{"graph a b": 14}}, &records.Repo{}, records.SchemaRepo},
		{"request", &records.Request{Key: "graph a b", Reason: "a needs b <now>", State: records.StateOpen,
			Numbers: records.Numbers{Verified: true, Balance: &records.Balance{Score: 6, Max: 10, Min: 7}},
			Waiting: []int{5, 9}, Rule: "graph", From: "a", To: "b", Expires: "2027-01-01",
			Record: "pkg/a/x.go:3: graph: a -> b is not an allowed dependency\n"}, &records.Request{}, records.SchemaRequest},
		{"approval", &records.Approval{Kind: records.KindApproveUntil, Request: 431,
			Change: json.RawMessage(`{"allow":"a -> b","expires":"2027-01-01"}`), Owner: 99, Login: "alice", Credential: "cred-1",
			At: at, Proposal: "p1", State: records.StateOpen}, &records.Approval{}, records.SchemaApproval},
		{"proposal", &records.Proposal{Approvals: []string{"a1", "a2"}, PullRequest: 50, HeadSHA: "abc",
			ExpectedDigest: "sha256:cc", State: records.StateMerged, Commit: "def"}, &records.Proposal{}, records.SchemaProposal},
		{"pull", &records.Pull{HeadSHA: "abc", RunID: 123456789012, RunAttempt: 2, PayloadDigest: "sha256:dd",
			Status: "waiting", Errors: []string{"graph a b"}}, &records.Pull{}, records.SchemaPull},
		{"policy push", &records.PolicyPush{Digest: "sha256:ee", Proposal: "p1", Unguarded: true}, &records.PolicyPush{}, records.SchemaPolicyPush},
		{"index", &records.Index{Schema: records.SchemaRequestIndex, Entries: []records.Entry{{Key: "a", ID: "1"}, {Key: "b", ID: "2"}}},
			&records.Index{Schema: records.SchemaRequestIndex}, records.SchemaRequestIndex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := records.Encode(tt.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if !strings.HasPrefix(string(raw), `{"schema":"`+tt.schema+`"`) {
				t.Errorf("value %s does not start with its schema", raw)
			}
			if err := records.Decode(raw, tt.out); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(tt.in, tt.out) {
				t.Errorf("round trip\n got %+v\nwant %+v", tt.out, tt.in)
			}
		})
	}
}

func TestDecodeRefuses(t *testing.T) {
	const marker = "SECRET-MARKER"
	tests := []struct {
		name string
		raw  string
		into records.Value
	}{
		{"another schema", `{"schema":"archfit-app.pull.v1","head_sha":"` + marker + `"}`, &records.Request{}},
		{"no schema", `{"key":"` + marker + `"}`, &records.Request{}},
		{"an unknown field", `{"schema":"archfit-app.request.v1","secret":"` + marker + `"}`, &records.Request{}},
		{"trailing data", `{"schema":"archfit-app.request.v1"}{"x":"` + marker + `"}`, &records.Request{}},
		{"not JSON", marker, &records.Request{}},
		{"a request index read as a pull index", `{"schema":"archfit-app.request-index.v1","entries":[{"key":"` + marker + `","id":"1"}]}`,
			&records.Index{Schema: records.SchemaPullIndex}},
		{"unsorted index", `{"schema":"archfit-app.repos.v1","entries":[{"key":"b"},{"key":"a"}]}`,
			&records.Index{Schema: records.SchemaRepos}},
		{"a key twice in an index", `{"schema":"archfit-app.repos.v1","entries":[{"key":"a"},{"key":"a"}]}`,
			&records.Index{Schema: records.SchemaRepos}},
		{"ten approvals", `{"schema":"archfit-app.proposal.v1","approvals":["1","2","3","4","5","6","7","8","9","10"],"expected_digest":"d","state":"open"}`,
			&records.Proposal{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := records.Decode([]byte(tt.raw), tt.into)
			if err == nil {
				t.Fatal("Decode = nil, want an error")
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("error %q quotes the value", err)
			}
		})
	}
}

func TestEncodeRefuses(t *testing.T) {
	big := &records.Request{Reason: strings.Repeat("x", 64<<10)}
	if _, err := records.Encode(big); !errors.Is(err, records.ErrTooLarge) {
		t.Errorf("Encode of a 64 KiB reason = %v, want ErrTooLarge", err)
	}
	if _, err := records.Encode(&records.Index{}); err == nil {
		t.Error("Encode of an index without a schema = nil, want an error")
	}
	full := &records.Proposal{Approvals: make([]string, records.MaxApprovals+1)}
	if _, err := records.Encode(full); err == nil {
		t.Error("Encode of a proposal over MaxApprovals = nil, want an error")
	}
}

func TestIndexAddRemoveLookup(t *testing.T) {
	x := records.Index{Schema: records.SchemaProposalIndex}
	for _, e := range []records.Entry{{Key: "9", ID: "p9"}, {Key: "10", ID: "p10"}, {Key: "1", ID: "p1"}} {
		if err := x.Add(e.Key, e.ID); err != nil {
			t.Fatalf("Add(%v): %v", e, err)
		}
	}
	if err := x.Add("9", "p9"); err != nil {
		t.Errorf("Add of the same entry = %v, want nil", err)
	}
	if err := x.Add("9", "other"); !errors.Is(err, records.ErrExists) {
		t.Errorf("Add of the key with another id = %v, want ErrExists", err)
	}
	want := []records.Entry{{Key: "1", ID: "p1"}, {Key: "10", ID: "p10"}, {Key: "9", ID: "p9"}}
	if !reflect.DeepEqual(x.Entries, want) {
		t.Errorf("entries %v, want %v", x.Entries, want)
	}
	if id, ok := x.Lookup("10"); !ok || id != "p10" {
		t.Errorf("Lookup(10) = %q, %v", id, ok)
	}
	x.Remove("10")
	x.Remove("missing")
	if _, ok := x.Lookup("10"); ok || len(x.Entries) != 2 {
		t.Errorf("after Remove: %v", x.Entries)
	}
}
