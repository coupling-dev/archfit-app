package gate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/approvals"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

var overlapRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}, DefaultBranch: "main"}

// overlapFaults fails a read by key: a store key, or "gh:" plus the name of a GitHub read.
type overlapFaults map[string]error

// boundGauge records the most reads in flight at once.
type boundGauge struct{ active, peak atomic.Int64 }

func (g *boundGauge) run(d time.Duration, read func() error) error {
	a := g.active.Add(1)
	for old := g.peak.Load(); a > old && !g.peak.CompareAndSwap(old, a); old = g.peak.Load() {
	}
	defer g.active.Add(-1)
	time.Sleep(d)
	return read()
}

type faultyStore struct {
	store.Store
	faults overlapFaults
	gauge  *boundGauge
}

func (s faultyStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	read := func() error {
		if err := s.faults[key]; err != nil {
			return err
		}
		return nil
	}
	if s.gauge != nil {
		var raw []byte
		err := s.gauge.run(50*time.Millisecond, func() error {
			if err := read(); err != nil {
				return err
			}
			var err error
			raw, err = s.Store.Get(ctx, t, key)
			return err
		})
		return raw, err
	}
	if err := read(); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, t, key)
}

type faultyGitHub struct {
	faults overlapFaults
	pulls  []api.Pull
	gauge  *boundGauge
}

// call runs one GitHub read: a fault keyed by name, then the read itself, both inside the gauge when one is set.
func (g faultyGitHub) call(name string, read func() error) error {
	body := func() error {
		if err := g.faults["gh:"+name]; err != nil {
			return err
		}
		return read()
	}
	if g.gauge == nil {
		return body()
	}
	return g.gauge.run(100*time.Millisecond, body)
}

func (g faultyGitHub) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	var out string
	err := g.call("tip", func() error { out = "immutable-tip"; return nil })
	return out, err
}

func (g faultyGitHub) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	var raw []byte
	var err error
	switch {
	case ref == "immutable-tip" && path == homePolicyPath:
		err = g.call("policy", func() error { raw = []byte(workflowsTestPolicy); return nil })
	case ref == "immutable-tip" && path == approvals.WorkflowPath:
		err = g.call("current", func() error { raw = []byte("workflow-current"); return nil })
	case ref == "sha-201" && path == approvals.WorkflowPath:
		err = g.call("candidate", func() error { raw = []byte("workflow-new"); return nil })
	case path == homePolicyPath:
		err = g.call("candidate", func() error { raw = []byte(workflowsTestPolicy); return nil })
	default:
		err = g.call("missing", func() error { return api.ErrNotFound })
	}
	return raw, "", err
}

func (g faultyGitHub) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) {
	var out []api.Pull
	err := g.call("pulls", func() error { out = g.pulls; return nil })
	return out, err
}

// newOverlapHome is an approved repository with one request, one open pull request that changes its workflow, one
// pull record and one proposal. approvedDigest is the digest the repository was approved at.
func newOverlapHome(t *testing.T, faults overlapFaults, approvedDigest string, gauge *boundGauge) HomeViews {
	t.Helper()
	mem := store.NewMemory()
	put := func(key string, v records.Value) {
		t.Helper()
		raw, err := records.Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := mem.Put(t.Context(), 1, key, raw); err != nil {
			t.Fatal(err)
		}
	}
	put(records.RepoKey("acme/app"), &records.Repo{ApprovedDigest: approvedDigest})
	put(records.RequestIndexKey("acme/app"), &records.Index{Schema: records.SchemaRequestIndex, Entries: []records.Entry{{Key: "1", ID: "1"}}})
	put(records.RequestKey("acme/app", 1), &records.Request{State: records.StateOpen, Rule: "graph", From: "app", To: "other"})
	put(records.PullKey("acme/app", 201), &records.Pull{HeadSHA: "sha-201"})
	put(records.ProposalIndexKey("acme/app"), &records.Index{Schema: records.SchemaProposalIndex, Entries: []records.Entry{{Key: "101", ID: "101"}}})
	put(records.ProposalKey("acme/app", "101"), &records.Proposal{State: records.StateOpen, Kind: records.ProposalFirst, PullRequest: 999, HeadSHA: "sha-999"})
	pulls := []api.Pull{{Number: 201, State: "open", BaseRef: "main", HeadRepo: "acme/app", HeadSHA: "sha-201", BaseSHA: "base"}}
	gh := faultyGitHub{faults: faults, pulls: pulls, gauge: gauge}
	return NewHome(gh, faultyStore{Store: mem, faults: faults, gauge: gauge}, NewAdmissions(mem, []string{"acme"}))
}

var overlapDigest = policy3.Digest([]byte(workflowsTestPolicy))

// overlapStages lists Home's reads in the order the serial code made them.
var overlapStages = []struct{ name, key string }{
	{"repo record", records.RepoKey("acme/app")},
	{"tip", "gh:tip"},
	{"policy", "gh:policy"},
	{"request index", records.RequestIndexKey("acme/app")},
	{"request record", records.RequestKey("acme/app", 1)},
	{"open pulls", "gh:pulls"},
	{"pull record", records.PullKey("acme/app", 201)},
	{"current workflow", "gh:current"},
	{"proposal index", records.ProposalIndexKey("acme/app")},
	{"proposal record", records.ProposalKey("acme/app", "101")},
	{"candidate workflow", "gh:candidate"},
}

func TestHomeOverlapKeepsTheSerialErrorOrder(t *testing.T) {
	for i, stage := range overlapStages {
		t.Run("only "+stage.name, func(t *testing.T) {
			faults := overlapFaults{stage.key: fmt.Errorf("fails at %s", stage.name)}
			_, err := newOverlapHome(t, faults, overlapDigest, nil).Home(t.Context(), overlapRepo)
			if want := faults[stage.key]; !errors.Is(err, want) {
				t.Fatalf("Home error = %v, want %v", err, want)
			}
		})
		t.Run("earlier than later failures "+stage.name, func(t *testing.T) {
			faults := overlapFaults{}
			for _, later := range overlapStages[i:] {
				faults[later.key] = fmt.Errorf("fails at %s", later.name)
			}
			_, err := newOverlapHome(t, faults, overlapDigest, nil).Home(t.Context(), overlapRepo)
			if want := faults[stage.key]; !errors.Is(err, want) {
				t.Fatalf("Home error = %v, want %v", err, want)
			}
		})
	}
}

func TestHomeOverlapReadsEverythingOnTheSuccessPath(t *testing.T) {
	out, err := newOverlapHome(t, nil, overlapDigest, nil).Home(t.Context(), overlapRepo)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{201}; !reflect.DeepEqual(out.WorkflowPulls, want) {
		t.Fatalf("workflow pulls = %v, want %v", out.WorkflowPulls, want)
	}
	if len(out.Requests) != 1 || out.OutsideFlow {
		t.Fatalf("out = %+v", out)
	}
}

// A repository whose approval no longer matches its policy is outside the flow. Its workflow and proposal reads may
// run ahead, but a failure in them is not the answer, as in the serial code.
func TestHomeOverlapIgnoresSpeculativeFailuresOutsideTheFlow(t *testing.T) {
	faults := overlapFaults{
		"gh:current":                         errors.New("current workflow unavailable"),
		"gh:candidate":                       errors.New("candidate workflow unavailable"),
		records.ProposalIndexKey("acme/app"): errors.New("proposal index unavailable"),
	}
	out, err := newOverlapHome(t, faults, "stale-digest", nil).Home(t.Context(), overlapRepo)
	if err != nil {
		t.Fatalf("Home error = %v, want nil", err)
	}
	if !out.OutsideFlow || len(out.WorkflowPulls) != 0 {
		t.Fatalf("out = %+v, want outside the flow with no workflow pulls", out)
	}
}

// The bound of four reads holds across the whole request, not per stage.
func TestHomeOverlapSharesOneBoundOfFourReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var gauge boundGauge
		if _, err := newOverlapHome(t, nil, overlapDigest, &gauge).Home(t.Context(), overlapRepo); err != nil {
			t.Fatal(err)
		}
		if peak := gauge.peak.Load(); peak != homeReadConcurrency {
			t.Fatalf("peak reads in flight = %d, want %d", peak, homeReadConcurrency)
		}
	})
}
