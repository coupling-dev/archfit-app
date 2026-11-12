package requests

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const (
	previewTip = "0123456789abcdef0123456789abcdef01234567"
	// previewPolicy has the request's two modules, a deny rule and an exception about them (earlier answers), and a
	// rule and an exception about other modules.
	previewPolicy = `version: 3
owners: [olivia]
modules:
  pkg_tc:
    paths: [pkg/tc/**]
    layer: adapters
    volatility: high
    depends_on: [pkg_net]
  pkg_util:
    paths: [pkg/util/**]
    subdomain: generic
  pkg_net:
    paths: [pkg/net/**]
rules:
  - id: no-util-to-tc
    deny:
      from: pkg_util
      to: pkg_tc
    why: Util stays at the bottom.
    instead: Pass a value in.
  - id: no-net-to-tc
    deny:
      from: pkg_net
      to: pkg_tc
    why: Net stays below tc.
    instead: Call back through an interface.
  - id: no-docker
    only:
      use: github.com/docker/docker
      in: [pkg_net]
    why: One adapter owns the Docker API.
exceptions:
  - rule: coupling
    from: pkg_tc
    to: pkg_util
    expires: 2026-12-01
    why: 'Approved in request #12.'
  - rule: graph
    from: pkg_net
    to: pkg_util
    why: Migration in progress.
`
	graphKey = "graph pkg_tc pkg_util"
)

var previewRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, DefaultBranch: "main"}

// previewGH is the GitHub the view and the preview read: the default branch's tip and its policy.
type previewGH struct {
	GitHub // nil: a call the view or the preview must not make panics
	policy []byte
}

func (g *previewGH) BranchHead(_ context.Context, _ api.RepoRef, branch string) (string, error) {
	if branch != previewRepo.DefaultBranch {
		return "", api.ErrNotFound
	}
	return previewTip, nil
}

func (g *previewGH) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	if ref != previewTip || path != policyPath || g.policy == nil {
		return nil, "", api.ErrNotFound
	}
	return g.policy, "blob", nil
}

type previewWorld struct {
	t   *testing.T
	gh  *previewGH
	mem store.Store
	svc *Service
}

func newPreviewWorld(t *testing.T) *previewWorld {
	t.Helper()
	gh := &previewGH{policy: []byte(previewPolicy)}
	mem := store.NewMemory()
	return &previewWorld{t: t, gh: gh, mem: mem, svc: New(gh, mem, nil)}
}

func graphError() result.Error {
	return result.Error{Path: "pkg/tc/netem.go", Line: 12, Rule: "graph", What: "pkg_tc -> pkg_util is not an allowed dependency",
		Why: "a new dependency between modules needs an owner", Balance: &result.Balance{Value: 9, Min: 7},
		Next: result.Next{Kind: nextCommand, Text: `archfit request -m "<why you need it>"`, Note: "do not copy the code"},
		From: "pkg_tc", To: "pkg_util", Key: "graph pkg_tc pkg_util example.com/pkg/util"}
}

// verifiedRequest stores request 431 from a pull request's verified result, waiting pull requests 4 and 9.
func (w *previewWorld) verifiedRequest() records.Request {
	w.t.Helper()
	e := graphError()
	return w.put(431, records.Request{Key: graphKey, Reason: "tc needs the retry helper", Rule: e.Rule, Path: e.Path,
		From: e.From, To: e.To, Expires: "2026-12-10", Record: recordsOf([]result.Error{e}), Waiting: []int{4, 9},
		Numbers: records.Numbers{Verified: true, Balance: balance(e.Balance)}})
}

func (w *previewWorld) put(number int, req records.Request) records.Request {
	w.t.Helper()
	if _, err := put(context.Background(), w.mem, previewRepo, number, req); err != nil {
		w.t.Fatal(err)
	}
	req.State = records.StateOpen
	return req
}

// pull stores the pull record of an open pull request with the request key of each of its errors.
func (w *previewWorld) pull(number int, status string, requestKeys ...string) {
	w.t.Helper()
	rec := records.Pull{HeadSHA: previewTip, RunID: int64(number), Status: status, RequestKeys: requestKeys}
	for i := range requestKeys {
		rec.Errors = append(rec.Errors, "engine-key-"+string(rune('a'+i)))
	}
	err := records.Indexed(context.Background(), w.mem, tenant(previewRepo), records.PullKey(previewRepo.FullName, number),
		records.PullIndexKey(previewRepo.FullName), records.SchemaPullIndex, func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(records.PullEntry(number), previewTip); err != nil {
				return nil, err
			}
			return records.Encode(&rec)
		})
	if err != nil {
		w.t.Fatal(err)
	}
}

func TestViewOfAVerifiedRequest(t *testing.T) {
	w := newPreviewWorld(t)
	w.verifiedRequest()
	v, err := w.svc.View(context.Background(), previewRepo, 431)
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 431 || v.State != records.StateOpen || v.Reason != "tc needs the retry helper" || v.Expires != "2026-12-10" {
		t.Errorf("view = %+v", v)
	}
	if !v.Numbers.Verified || *v.Numbers.Balance != (records.Balance{Score: 9, Max: 10, Min: 7}) {
		t.Errorf("numbers = %+v", v.Numbers)
	}
	wantRecord := "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n" +
		"  why: a new dependency between modules needs an owner\n  balance: 9/10 (min 7)\n" +
		"  next: archfit request -m \"<why you need it>\"     (do not copy the code)\n"
	if v.Record != wantRecord {
		t.Errorf("record =\n%s\nwant\n%s", v.Record, wantRecord)
	}
	wantModules := []Module{
		{Name: "pkg_tc", Layer: "adapters", Volatility: "high", DependsOn: []string{"pkg_net"}},
		{Name: "pkg_util", Subdomain: "generic"},
	}
	if !v.Policy || v.Allowed || !equalModules(v.Modules, wantModules) {
		t.Errorf("policy %v, allowed %v, modules %+v", v.Policy, v.Allowed, v.Modules)
	}
}

func equalModules(a, b []Module) bool {
	return slices.EqualFunc(a, b, func(x, y Module) bool {
		return x.Name == y.Name && x.Layer == y.Layer && x.Volatility == y.Volatility && x.Subdomain == y.Subdomain &&
			slices.Equal(x.DependsOn, y.DependsOn)
	})
}

func TestViewOfARequestAsReported(t *testing.T) {
	w := newPreviewWorld(t)
	reported := "pkg_tc -> pkg_util: graph: not an allowed dependency\n  balance: 8/10 (min 7)\n"
	w.put(77, records.Request{Key: graphKey, Reason: "before a pull request", Rule: "graph", From: "pkg_tc", To: "pkg_util",
		Record: reported, Numbers: records.Numbers{Balance: &records.Balance{Score: 8, Max: 10, Min: 7}}})
	v, err := w.svc.View(context.Background(), previewRepo, 77)
	if err != nil {
		t.Fatal(err)
	}
	if v.Numbers.Verified || v.Numbers.Balance.Score != 8 || v.Record != reported || len(v.Modules) != 2 {
		t.Errorf("view = %+v", v)
	}
}

func TestViewOfARequestWithEarlierAnswers(t *testing.T) {
	w := newPreviewWorld(t)
	w.verifiedRequest()
	v, err := w.svc.View(context.Background(), previewRepo, 431)
	if err != nil {
		t.Fatal(err)
	}
	want := []Earlier{
		{Kind: EarlierDeny, Rule: "no-util-to-tc", From: "pkg_util", To: "pkg_tc", Why: "Util stays at the bottom.",
			Instead: "Pass a value in."},
		{Kind: EarlierException, Rule: "coupling", From: "pkg_tc", To: "pkg_util", Why: "Approved in request #12.",
			Expires: "2026-12-01"},
	}
	if !slices.Equal(v.Earlier, want) {
		t.Errorf("earlier =\n%+v\nwant\n%+v", v.Earlier, want)
	}
}

func TestViewAllowedAndWithoutPolicy(t *testing.T) {
	w := newPreviewWorld(t)
	w.put(5, records.Request{Key: "graph pkg_tc pkg_net", Rule: "graph", From: "pkg_tc", To: "pkg_net", Numbers: records.Numbers{Verified: true}})
	v, err := w.svc.View(context.Background(), previewRepo, 5)
	if err != nil || !v.Allowed || len(v.Earlier) != 1 || v.Earlier[0].Rule != "no-net-to-tc" {
		t.Errorf("view = %+v, %v; want allowed, and the deny rule the other way as an earlier answer", v, err)
	}
	w.gh.policy = nil
	v, err = w.svc.View(context.Background(), previewRepo, 5)
	if err != nil || v.Policy || v.Modules != nil || v.Allowed {
		t.Errorf("view without a policy = %+v, %v", v, err)
	}
	if _, err := w.svc.View(context.Background(), previewRepo, 6); !errors.Is(err, ErrNoRequest) {
		t.Errorf("a missing request: %v, want ErrNoRequest", err)
	}
}

func TestPreviewOfEachAnswer(t *testing.T) {
	tests := []struct {
		name    string
		answer  Answer
		loosens bool
		until   string
		diff    string
	}{
		{name: "approve", answer: Answer{Kind: policy3.Approve}, loosens: true, diff: `--- a/.archfit.yaml
+++ b/.archfit.yaml
@@ -5,7 +5,9 @@
     paths: [pkg/tc/**]
     layer: adapters
     volatility: high
-    depends_on: [pkg_net]
+    depends_on:
+      - pkg_net
+      - pkg_util
   pkg_util:
     paths: [pkg/util/**]
     subdomain: generic
`},
		{name: "approve until the date asked for", answer: Answer{Kind: policy3.ApproveUntil}, loosens: true,
			until: "2026-12-10", diff: `--- a/.archfit.yaml
+++ b/.archfit.yaml
@@ -39,3 +39,8 @@
     from: pkg_net
     to: pkg_util
     why: Migration in progress.
+  - rule: graph
+    from: pkg_tc
+    to: pkg_util
+    expires: 2026-12-10
+    why: 'Approved in request #431.'
`},
		{name: "approve until another date", answer: Answer{Kind: policy3.ApproveUntil, Until: "2026-11-30"}, loosens: true,
			until: "2026-11-30", diff: `--- a/.archfit.yaml
+++ b/.archfit.yaml
@@ -39,3 +39,8 @@
     from: pkg_net
     to: pkg_util
     why: Migration in progress.
+  - rule: graph
+    from: pkg_tc
+    to: pkg_util
+    expires: 2026-11-30
+    why: 'Approved in request #431.'
`},
		{name: "reject with instead", answer: Answer{Kind: policy3.Reject, Instead: "Copy the two lines with a test."}, diff: `--- a/.archfit.yaml
+++ b/.archfit.yaml
@@ -29,6 +29,12 @@
       use: github.com/docker/docker
       in: [pkg_net]
     why: One adapter owns the Docker API.
+  - id: request-431
+    deny:
+      from: pkg_tc
+      to: pkg_util
+    why: 'Rejected in request #431.'
+    instead: Copy the two lines with a test.
 exceptions:
   - rule: coupling
     from: pkg_tc
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newPreviewWorld(t)
			w.verifiedRequest()
			p, err := w.svc.Preview(context.Background(), previewRepo, 431, tt.answer)
			if err != nil {
				t.Fatal(err)
			}
			if p.Diff != tt.diff {
				t.Errorf("diff =\n%s\nwant\n%s", p.Diff, tt.diff)
			}
			if p.Loosens != tt.loosens || p.Answer.Until != tt.until || p.Change.Request != 431 ||
				p.PolicyDigest != policy3.Digest([]byte(previewPolicy)) {
				t.Errorf("preview = %+v", p)
			}
			if words := wording.NotUsedIn(p.Diff); len(words) > 0 {
				t.Errorf("the diff uses %v", words)
			}
		})
	}
}

func TestPreviewRefuses(t *testing.T) {
	tests := []struct {
		name   string
		number int
		answer Answer
		setup  func(w *previewWorld)
		want   error
	}{
		{name: "reject without instead", number: 431, answer: Answer{Kind: policy3.Reject, Instead: "  "}, want: ErrAnswer},
		{name: "approve until without a date", number: 8, answer: Answer{Kind: policy3.ApproveUntil}, want: ErrAnswer},
		{name: "approve until a bad date", number: 431, answer: Answer{Kind: policy3.ApproveUntil, Until: "10-12-2026"}, want: ErrAnswer},
		{name: "an unknown answer", number: 431, answer: Answer{Kind: "confirm"}, want: ErrAnswer},
		{name: "no such request", number: 999, answer: Answer{Kind: policy3.Approve}, want: ErrNoRequest},
		{name: "an answered request", number: 9, answer: Answer{Kind: policy3.Approve}, want: ErrClosedRequest},
		{name: "no policy", number: 431, answer: Answer{Kind: policy3.Approve}, want: ErrNoPolicy,
			setup: func(w *previewWorld) { w.gh.policy = nil }},
		{name: "a layout the writer does not edit", number: 431, answer: Answer{Kind: policy3.Approve}, want: ErrByHand,
			setup: func(w *previewWorld) {
				w.gh.policy = []byte("version: 3\nowners: [olivia]\nmodules:\n  pkg_tc: {paths: [pkg/tc/**]}\n  pkg_util:\n    paths: [pkg/util/**]\n")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newPreviewWorld(t)
			w.verifiedRequest()
			w.put(8, records.Request{Key: "graph pkg_net pkg_tc", Rule: "graph", From: "pkg_net", To: "pkg_tc"})
			answered := w.put(9, records.Request{Key: "graph pkg_util pkg_net", Rule: "graph", From: "pkg_util", To: "pkg_net"})
			answered.State = records.StateAnswered
			raw, err := records.Encode(&answered)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.mem.Put(context.Background(), tenant(previewRepo), records.RequestKey(previewRepo.FullName, 9), raw); err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(w)
			}
			if _, err := w.svc.Preview(context.Background(), previewRepo, tt.number, tt.answer); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPreviewListsThePullRequestsTheAnswerTouches(t *testing.T) {
	w := newPreviewWorld(t)
	w.verifiedRequest()                                     // pull requests 4 and 9 wait on it
	w.pull(4, "waiting", graphKey)                          // waits; the answer leaves it no error
	w.pull(5, "fail", graphKey, "", "layers a b", graphKey) // does not wait; two cleared, two left
	w.pull(6, "fail", "graph pkg_tc pkg_net")               // another key: untouched
	w.pull(7, "pass")                                       // no errors
	// Pull request 9 waits on the request but is closed: it is not in the pull index.

	approve, err := w.svc.Preview(context.Background(), previewRepo, 431, Answer{Kind: policy3.Approve})
	if err != nil {
		t.Fatal(err)
	}
	want := []PullEffect{
		{Number: 4, Status: "waiting", Waits: true, Clears: 1, Left: 0},
		{Number: 5, Status: "fail", Clears: 2, Left: 2},
	}
	if !slices.Equal(approve.Pulls, want) {
		t.Errorf("approve touches %+v, want %+v", approve.Pulls, want)
	}

	reject, err := w.svc.Preview(context.Background(), previewRepo, 431, Answer{Kind: policy3.Reject, Instead: "Keep tc apart."})
	if err != nil {
		t.Fatal(err)
	}
	want = []PullEffect{{Number: 4, Status: "waiting", Waits: true, Clears: 0, Left: 1}}
	if !slices.Equal(reject.Pulls, want) {
		t.Errorf("reject touches %+v, want %+v", reject.Pulls, want)
	}
}

func TestEditWritesEachAnswerOfEachRule(t *testing.T) {
	tests := []struct {
		name string
		c    Change
		want string // the lines the edit adds or changes, "" when the file stays the same
	}{
		{name: "approve graph", c: Change{Request: 4, Kind: policy3.Approve, Rule: "graph", From: "pkg_tc", To: "pkg_util"},
			want: "    depends_on:\n      - pkg_net\n      - pkg_util\n"},
		{name: "approve a dependency already allowed", c: Change{Request: 4, Kind: policy3.Approve, Rule: "graph", From: "pkg_tc", To: "pkg_net"}},
		{name: "approve another rule", c: Change{Request: 4, Kind: policy3.Approve, Rule: "layers", From: "pkg_util", To: "pkg_net"},
			want: "  - rule: layers\n    from: pkg_util\n    to: pkg_net\n    why: 'Approved in request #4.'\n"},
		{name: "approve unowned code until a date", c: Change{Request: 4, Kind: policy3.ApproveUntil, Rule: "unowned", Path: "tools/gen.go", Until: "2026-12-01"},
			want: "  - rule: unowned\n    from: tools/gen.go\n    expires: 2026-12-01\n    why: 'Approved in request #4.'\n"},
		{name: "approve an only rule until a date", c: Change{Request: 4, Kind: policy3.ApproveUntil, Rule: "no-docker", From: "pkg_tc", To: "github.com/docker/docker", Until: "2026-12-01"},
			want: "  - rule: no-docker\n    from: pkg_tc\n    expires: 2026-12-01\n    why: 'Approved in request #4.'\n"},
		{name: "reject a copy", c: Change{Request: 4, Kind: policy3.Reject, Rule: "duplication", From: "pkg_tc", To: "pkg_util", Instead: "Use pkg_util."}},
		{name: "reject unowned code", c: Change{Request: 4, Kind: policy3.Reject, Rule: "unowned", Path: "tools/gen.go", Instead: "Add it to a module."}},
		{name: "reject an only rule", c: Change{Request: 4, Kind: policy3.Reject, Rule: "no-docker", From: "pkg_tc", To: "github.com/docker/docker", Instead: "Go through pkg_net."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Edit([]byte(previewPolicy), tt.c, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := changed(previewPolicy, string(out)); got != tt.want {
				t.Errorf("changed lines =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// changed is the lines of edited that differ from old.
func changed(old, edited string) string {
	a, b := lines([]byte(old)), lines([]byte(edited))
	head, tail := common(a, b)
	return strings.Join(b[head:len(b)-tail], "")
}

func TestEditEndsTheWrittenEntryWithTheComment(t *testing.T) {
	c := Change{Request: 431, Kind: policy3.Reject, Rule: "graph", From: "pkg_tc", To: "pkg_util", Instead: "Keep tc apart."}
	out, err := Edit([]byte(previewPolicy), c, "request #431")
	if err != nil {
		t.Fatal(err)
	}
	want := "  - id: request-431  # request #431\n    deny:\n      from: pkg_tc\n" +
		"      to: pkg_util\n    why: 'Rejected in request #431.'\n    instead: Keep tc apart.\n"
	if got := changed(previewPolicy, string(out)); got != want {
		t.Errorf("changed lines =\n%s\nwant\n%s", got, want)
	}
	plain, _ := Edit([]byte(previewPolicy), c, "")
	if a, b := mustPolicy(t, string(out)), mustPolicy(t, string(plain)); !reflect.DeepEqual(a, b) {
		t.Error("the comment changed the policy")
	}

	crlf := strings.ReplaceAll(previewPolicy, "\n", "\r\n")
	out, err = Edit([]byte(crlf), Change{Request: 2, Kind: policy3.Approve, Rule: "graph", From: "pkg_tc", To: "pkg_util"}, "request #2")
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(crlf, string(out)); got != "    depends_on:\r\n      - pkg_net\r\n      - pkg_util  # request #2\r\n" {
		t.Errorf("CRLF line = %q", got)
	}

	if _, err := Edit([]byte(previewPolicy), c, "request\n#431"); !errors.Is(err, ErrAnswer) {
		t.Errorf("a comment on two lines: %v, want ErrAnswer", err)
	}
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name, old, edited, want string
	}{
		{name: "the same file", old: "a\nb\n", edited: "a\nb\n"},
		{name: "an insertion at the end", old: "a\nb\n", edited: "a\nb\nc\n",
			want: "--- a/p\n+++ b/p\n@@ -1,2 +1,3 @@\n a\n b\n+c\n"},
		{name: "a changed line with context", old: "1\n2\n3\n4\n5\n6\n7\n8\n9\n", edited: "1\n2\n3\n4\n5x\n6\n7\n8\n9\n",
			want: "--- a/p\n+++ b/p\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+5x\n 6\n 7\n 8\n"},
		{name: "no final newline", old: "a\nb", edited: "a\nb\nc",
			want: "--- a/p\n+++ b/p\n@@ -1,2 +1,3 @@\n a\n-b\n+b\n+c\n"},
		{name: "CRLF line ends", old: "a\r\nb\r\n", edited: "a\r\nx\r\nb\r\n",
			want: "--- a/p\n+++ b/p\n@@ -1,2 +1,3 @@\n a\n+x\n b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff("p", []byte(tt.old), []byte(tt.edited)); got != tt.want {
				t.Errorf("diff =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
