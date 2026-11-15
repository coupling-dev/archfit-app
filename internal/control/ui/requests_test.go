package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

// fakeRequestViews answers the request page from fixed values and records what it was asked.
type fakeRequestViews struct {
	mu      sync.Mutex
	view    gate.RequestView
	preview gate.RequestPreview
	err     error
	numbers []int
	answers []gate.RequestAnswer
	targets []gate.Target
}

func (f *fakeRequestViews) RequestView(_ context.Context, t gate.Target, number int) (gate.RequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets, f.numbers = append(f.targets, t), append(f.numbers, number)
	return f.view, f.err
}

func (f *fakeRequestViews) RequestPreview(_ context.Context, t gate.Target, number int, a gate.RequestAnswer) (gate.RequestPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets, f.numbers, f.answers = append(f.targets, t), append(f.numbers, number), append(f.answers, a)
	return f.preview, f.err
}

const requestAPI = "/api/repos/" + repoName + "/requests/431"

func TestRequestAPIShowsTheRequest(t *testing.T) {
	e := newEnv(t)
	e.requests.view = gate.RequestView{Number: 431, State: "open", Reason: "tc needs the retry helper", Rule: "graph",
		From: "pkg_tc", To: "pkg_util", Expires: "2026-12-10", Record: "pkg/tc/netem.go:12: graph: …\n  balance: 9/10 (min 7)\n",
		Numbers: gate.RequestNumbers{Verified: true, Balance: &gate.RequestBalance{Score: 9, Max: 10, Min: 7}}, Policy: true,
		Modules: []gate.RequestModule{{Name: "pkg_tc", Layer: "adapters", DependsOn: []string{"pkg_net"}}, {Name: "pkg_util"}},
		Earlier: []gate.RequestEarlier{{Kind: gate.EarlierDeny, Rule: "no-util-to-tc", From: "pkg_util", To: "pkg_tc",
			Why: "Util stays at the bottom.", Instead: "Pass a value in."}}}
	var body map[string]any
	e.as("alice", permRead).getJSON(requestAPI, &body)
	want := map[string]any{
		"dependencies": []any{}, "issue_href": "", "number": 431.0, "state": "open", "reason": "tc needs the retry helper", "rule": "graph", "from": "pkg_tc",
		"to": "pkg_util", "expires": "2026-12-10", "record": "pkg/tc/netem.go:12: graph: …\n  balance: 9/10 (min 7)\n",
		"verified": true, "balance": map[string]any{"value": 9.0, "max": 10.0, "min": 7.0}, "policy": true,
		"modules": []any{
			map[string]any{"name": "pkg_tc", "layer": "adapters", "depends_on": []any{"pkg_net"}},
			map[string]any{"name": "pkg_util", "depends_on": []any{}},
		},
		"layers": []any{}, "allowed": false,
		"earlier": []any{map[string]any{"kind": "deny", "rule": "no-util-to-tc", "from": "pkg_util", "to": "pkg_tc",
			"why": "Util stays at the bottom.", "instead": "Pass a value in."}},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body =\n%v\nwant\n%v", body, want)
	}
	if e.requests.numbers[0] != 431 || e.requests.targets[0].Repo.FullName != repoName {
		t.Errorf("asked for %v of %v", e.requests.numbers, e.requests.targets)
	}

	t.Run("as reported", func(t *testing.T) {
		e := newEnv(t)
		e.requests.view = gate.RequestView{Number: 431, State: "open", Rule: "graph", Record: "reported\n"}
		var body requestJSON
		e.as("alice", permRead).getJSON(requestAPI, &body)
		if body.Verified || body.Balance != nil || body.Modules == nil || body.Earlier == nil {
			t.Errorf("body = %+v", body)
		}
	})
}

func TestRequestPreviewAPIPassesTheAnswer(t *testing.T) {
	e := newEnv(t)
	e.requests.preview = gate.RequestPreview{Answer: gate.RequestAnswer{Kind: policy3.ApproveUntil, Until: "2026-12-10"},
		Diff: "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n", PolicyDigest: "sha256:" + sha(1), Loosens: true,
		Pulls: []gate.RequestPullEffect{{Number: 4, Status: "waiting", Waits: true, Clears: 1}}}
	var body previewJSON
	e.as("alice", permRead).getJSON(requestAPI+"/preview?answer=approve-until&until=2026-12-10&instead=x", &body)
	want := previewJSON{Answer: "approve-until", Until: "2026-12-10", Diff: "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n",
		PolicyDigest: "sha256:" + sha(1), Loosens: true, Pulls: []pullEffectJSON{{Number: 4, Status: "waiting", Waits: true, Clears: 1}}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %+v, want %+v", body, want)
	}
	if got := e.requests.answers[0]; got != (gate.RequestAnswer{Kind: policy3.ApproveUntil, Until: "2026-12-10", Instead: "x"}) {
		t.Errorf("answer = %+v", got)
	}
}

func TestRequestAPIRefusals(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		err    error
		status int
	}{
		{name: "no such request", path: requestAPI, err: gate.ErrNoRequest, status: http.StatusNotFound},
		{name: "not a number", path: "/api/repos/" + repoName + "/requests/x", status: http.StatusNotFound},
		{name: "an incomplete answer", path: requestAPI + "/preview?answer=reject", err: gate.ErrAnswer, status: http.StatusUnprocessableEntity},
		{name: "a closed request", path: requestAPI + "/preview?answer=approve", err: gate.ErrClosedRequest, status: http.StatusConflict},
		{name: "no policy", path: requestAPI + "/preview?answer=approve", err: gate.ErrNoPolicy, status: http.StatusConflict},
		{name: "by hand", path: requestAPI + "/preview?answer=approve", err: gate.ErrByHand, status: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.requests.err = tt.err
			res := e.as("alice", permRead).get(tt.path)
			wantStatus(t, res, tt.status)
			var body errorJSON
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if words := wording.NotUsedIn(body.Error); len(words) > 0 {
				t.Errorf("%q uses %v", body.Error, words)
			}
		})
	}

	t.Run("no permission", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.signIn("mallory").get(requestAPI), http.StatusNotFound)
		if len(e.requests.numbers) != 0 {
			t.Errorf("read for a user without access: %v", e.requests.numbers)
		}
	})
}

func TestRequestAPIGoldens(t *testing.T) {
	base := gate.RequestView{IssueHref: "https://github.com/" + repoName + "/issues/431", Number: 431, State: "open", Reason: "Need the helper.", Rule: "graph", From: "app", To: "util",
		Record: "app.go:3 graph app -> util\n  balance: 9/10 (min 7)\n", Numbers: gate.RequestNumbers{Verified: true, Balance: &gate.RequestBalance{Score: 9, Max: 10, Min: 7}},
		Dependencies: []gate.RequestDependency{{Kind: gate.EarlierDeny, Rule: "no-util-to-app", From: "util", To: "app"}},
		Policy:       true, Layers: []string{"outer", "core"}, Modules: []gate.RequestModule{{Name: "app", Layer: "outer", Volatility: "high"}, {Name: "util", Layer: "core", Volatility: "low"}}}
	for _, tc := range []struct {
		name string
		view gate.RequestView
	}{
		{"open", base},
		{"as-reported", func() gate.RequestView { v := base; v.Numbers.Verified = false; return v }()},
		{"joined", func() gate.RequestView {
			v := base
			v.State = "answered"
			v.Answer = &gate.RequestAnswer{Kind: policy3.ApproveUntil, Until: "2026-12-10"}
			return v
		}()},
		{"answered", func() gate.RequestView {
			v := base
			v.State = "answered"
			v.Answer = &gate.RequestAnswer{Kind: policy3.Reject, Instead: "Use a port."}
			return v
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			e.requests.view = tc.view
			res := e.as("alice", permRead).get(requestAPI)
			wantStatus(t, res, http.StatusOK)
			if words := wording.NotUsedIn(res.Body.String()); len(words) > 0 {
				t.Fatalf("words: %v", words)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			path := filepath.Join("testdata", "request", tc.name+".json")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pretty.Bytes(), want) {
				t.Fatalf("got %s, want %s", pretty.Bytes(), want)
			}
		})
	}
}
