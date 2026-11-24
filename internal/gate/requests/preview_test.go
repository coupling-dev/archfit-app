package requests

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
	"github.com/coupling-dev/archfit-app/internal/wire/wording"
)

const (
	previewTip = "0123456789abcdef0123456789abcdef01234567"
	// previewPolicy has the request's two modules, a deny rule and an exception about them (earlier answers), and a
	// rule and an exception about other modules.
	previewPolicy = `version: 1
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
	return &previewWorld{t: t, gh: gh, mem: mem, svc: New(gh, mem, nil, "")}
}

func graphError() result.Error {
	return result.Error{Path: "pkg/tc/netem.go", Line: 12, Rule: "graph", What: "pkg_tc -> pkg_util is not an allowed dependency",
		Why: "a new dependency between modules needs an owner", Balance: &result.Balance{Value: 9, Min: 7, Strength: result.StrengthContract, Volatility: result.VolatilityLow},
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
	if !v.Numbers.Verified || *v.Numbers.Balance != (records.Balance{Score: 9, Max: 10, Min: 7, Strength: "contract", Volatility: "low"}) {
		t.Errorf("numbers = %+v", v.Numbers)
	}
	wantRecord := "pkg/tc/netem.go:12: graph: pkg_tc -> pkg_util is not an allowed dependency\n" +
		"  why: a new dependency between modules needs an owner\n  balance: 9/10 (min 7): contract, pkg_util is low\n" +
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
+      - pkg_util  # request #431
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
+  - rule: graph  # request #431
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
+  - rule: graph  # request #431
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
+  - id: request-431  # request #431
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
			p, err := w.svc.Preview(context.Background(), previewRepo, 431, tt.answer, testToday)
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
		{name: "approve until today", number: 431, answer: Answer{Kind: policy3.ApproveUntil, Until: "2026-11-12"}, want: ErrAnswer},
		{name: "approve until a past date", number: 431, answer: Answer{Kind: policy3.ApproveUntil, Until: "2020-01-01"}, want: ErrAnswer},
		{name: "approve until more than a year ahead", number: 431, answer: Answer{Kind: policy3.ApproveUntil, Until: "2027-11-13"}, want: ErrAnswer},
		{name: "approve until the asked date, now past", number: 10, answer: Answer{Kind: policy3.ApproveUntil}, want: ErrAnswer},
		{name: "an unknown answer", number: 431, answer: Answer{Kind: "confirm"}, want: ErrAnswer},
		{name: "no such request", number: 999, answer: Answer{Kind: policy3.Approve}, want: ErrNoRequest},
		{name: "an answered request", number: 9, answer: Answer{Kind: policy3.Approve}, want: ErrClosedRequest},
		{name: "no policy", number: 431, answer: Answer{Kind: policy3.Approve}, want: ErrNoPolicy,
			setup: func(w *previewWorld) { w.gh.policy = nil }},
		{name: "a layout the writer does not edit", number: 431, answer: Answer{Kind: policy3.Approve}, want: ErrByHand,
			setup: func(w *previewWorld) {
				w.gh.policy = []byte("version: 1\nowners: [olivia]\nmodules:\n  pkg_tc: {paths: [pkg/tc/**]}\n  pkg_util:\n    paths: [pkg/util/**]\n")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newPreviewWorld(t)
			w.verifiedRequest()
			w.put(8, records.Request{Key: "graph pkg_net pkg_tc", Rule: "graph", From: "pkg_net", To: "pkg_tc"})
			w.put(10, records.Request{Key: "graph pkg_net pkg_util", Rule: "graph", From: "pkg_net", To: "pkg_util", Expires: "2026-11-01"})
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
			if _, err := w.svc.Preview(context.Background(), previewRepo, tt.number, tt.answer, testToday); !errors.Is(err, tt.want) {
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

	approve, err := w.svc.Preview(context.Background(), previewRepo, 431, Answer{Kind: policy3.Approve}, testToday)
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

	reject, err := w.svc.Preview(context.Background(), previewRepo, 431, Answer{Kind: policy3.Reject, Instead: "Keep tc apart."}, testToday)
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
	for version, policy := range map[string]string{"version 3": previewPolicy, "version 1": previewPolicyV1} {
		for _, tt := range tests {
			t.Run(version+"/"+tt.name, func(t *testing.T) {
				out, err := Edit([]byte(policy), tt.c, "")
				if err != nil {
					t.Fatal(err)
				}
				if got := changed(policy, string(out)); got != tt.want {
					t.Errorf("changed lines =\n%s\nwant\n%s", got, tt.want)
				}
			})
		}
	}
}

// previewPolicyV1 is previewPolicy at version 1 of engine 0.9.0, with a model part.
var previewPolicyV1 = strings.Replace(strings.Replace(previewPolicy, "version: 1\n", "version: 1\n", 1),
	"    paths: [pkg/util/**]\n", "    paths: [pkg/util/**]\n    model: [pkg/util/model]\n", 1)

func TestEditKeepsTheRequestCommentInAVersion1Policy(t *testing.T) {
	out, err := Edit([]byte(previewPolicyV1), Change{Request: 2, Kind: policy3.Approve, Rule: "graph", From: "pkg_tc", To: "pkg_util"}, "request #2")
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(previewPolicyV1, string(out)); got != "    depends_on:\n      - pkg_net\n      - pkg_util  # request #2\n" {
		t.Errorf("changed lines = %q", got)
	}
	if p := mustPolicy(t, string(out)); p.Version != policy3.Version1 || len(p.Modules["pkg_util"].Model) != 1 {
		t.Errorf("policy = version %d, pkg_util %+v", p.Version, p.Modules["pkg_util"])
	}
}

// common counts the equal lines at the start and at the end of a and b, the end not overlapping the start. Lines
// compare without their line ends: a last line the writer ends with a newline, because it adds lines after it, is not
// a line the edit wrote.
func common(a, b []string) (head, tail int) {
	same := func(x, y string) bool {
		x, _ = cutEOL(x)
		y, _ = cutEOL(y)
		return x == y
	}
	for head < len(a) && head < len(b) && same(a[head], b[head]) {
		head++
	}
	for tail < len(a)-head && tail < len(b)-head && same(a[len(a)-1-tail], b[len(b)-1-tail]) {
		tail++
	}
	return head, tail
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
			want: "--- a/p\n+++ b/p\n@@ -1,2 +1,3 @@\n a\n-b\n\\ No newline at end of file\n+b\n+c\n\\ No newline at end of file\n"},
		{name: "CRLF line ends", old: "a\r\nb\r\n", edited: "a\r\nx\r\nb\r\n",
			want: "--- a/p\n+++ b/p\n@@ -1,2 +1,3 @@\n a\n+x\n\\ Line ending: CRLF\n b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff("p", []byte(tt.old), []byte(tt.edited)); got != tt.want {
				t.Errorf("diff =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestEditAtTheEndOfAPolicyWithoutAFinalNewline(t *testing.T) {
	file := strings.TrimSuffix(previewPolicy, "\n")
	c := Change{Request: 5, Kind: policy3.ApproveUntil, Rule: "graph", From: "pkg_tc", To: "pkg_util", Until: "2026-12-01"}
	out, err := Edit([]byte(file), c, RequestComment(5))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "    why: Migration in progress.\n  - rule: graph  # request #5\n") {
		t.Errorf("an old line took the comment, or the new one lacks it:\n%s", out)
	}
	if got := Diff(policyPath, []byte(file), out); !strings.Contains(got, "-    why: Migration in progress.\n\\ No newline at end of file\n+    why: Migration in progress.\n") {
		t.Errorf("the diff hides the added final newline:\n%s", got)
	}
}

func TestAnExceptionClearsWhatItCovers(t *testing.T) {
	tests := []struct {
		name  string
		req   records.Request
		keys  []string // the stored request keys of an open pull request that does not wait
		clear int
	}{
		{name: "a copy: every copy between the two modules",
			req:  records.Request{Key: "duplication pkg_tc pkg_util sha256:aa", Rule: "duplication", From: "pkg_util", To: "pkg_tc"},
			keys: []string{"duplication pkg_tc pkg_util sha256:bb", "duplication pkg_tc pkg_net sha256:aa", "graph pkg_tc pkg_util"}, clear: 1},
		{name: "an only rule: every library of the rule from the module",
			req:  records.Request{Key: "no-docker pkg_tc github.com/docker/docker", Rule: "no-docker", From: "pkg_tc", To: "github.com/docker/docker"},
			keys: []string{"no-docker pkg_tc github.com/docker/docker/client", "no-docker pkg_util github.com/docker/docker"}, clear: 1},
		{name: "another rule: its own key only",
			req:  records.Request{Key: "layers pkg_util pkg_net", Rule: "layers", From: "pkg_util", To: "pkg_net"},
			keys: []string{"layers pkg_util pkg_net", "layers pkg_util pkg_tc"}, clear: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newPreviewWorld(t)
			w.put(20, tt.req)
			w.pull(6, "fail", tt.keys...)
			for _, a := range []Answer{{Kind: policy3.Approve}, {Kind: policy3.ApproveUntil, Until: "2026-12-01"}} {
				p, err := w.svc.Preview(context.Background(), previewRepo, 20, a, testToday)
				if err != nil {
					t.Fatal(err)
				}
				want := []PullEffect{{Number: 6, Status: "fail", Clears: tt.clear, Left: len(tt.keys) - tt.clear}}
				if !slices.Equal(p.Pulls, want) {
					t.Errorf("%s touches %+v, want %+v", a.Kind, p.Pulls, want)
				}
			}
		})
	}
}

func TestViewLayersAndVolatilityUseOnePolicy(t *testing.T) {
	w := newPreviewWorld(t)
	w.verifiedRequest()
	w.gh.policy = []byte(strings.Replace(previewPolicy, "modules:\n", "layers: [core, adapters, outer]\nmodules:\n", 1))
	v, err := w.svc.View(context.Background(), previewRepo, 431)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.Layers, []string{"outer", "adapters", "core"}) || len(v.Modules) != 2 || v.Modules[0].Volatility != "high" {
		t.Fatalf("layers %v, modules %+v", v.Layers, v.Modules)
	}
}

func TestViewKeepsStructuredDenyAndExceptionEndpoints(t *testing.T) {
	w := newPreviewWorld(t)
	w.verifiedRequest()
	v, err := w.svc.View(t.Context(), previewRepo, 431)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dependency{{Kind: EarlierException, Rule: "coupling", From: "pkg_tc", To: "pkg_util", Expires: "2026-12-01"}, {Kind: EarlierDeny, Rule: "no-util-to-tc", From: "pkg_util", To: "pkg_tc"}}
	if !slices.Equal(v.Dependencies, want) {
		t.Fatalf("dependencies = %+v, want %+v", v.Dependencies, want)
	}
	pol := policy3.File{Rules: []policy3.Rule{{ID: "deny", Deny: &policy3.Deny{From: []string{"a, b", "other"}, To: []string{"c"}}}}}
	if got := dependencies(pol, "a, b", "c"); !slices.Equal(got, []Dependency{{Kind: EarlierDeny, Rule: "deny", From: "a, b", To: "c"}}) {
		t.Fatalf("comma module: %+v", got)
	}
}

// A surface request (the surfaces design 6.9) takes declare-public, declare-model, approve-until or reject. A use past
// the surface is never approved as such, and a request that names no package past the surface gets no declaration.
func TestChangeOfASurfaceRequest(t *testing.T) {
	today := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	surface := records.Request{Rule: "coupling", From: "pkg_tc", To: "pkg_util",
		Surface: &records.Surface{Public: []string{"pkg/util"}, Model: []string{"pkg/util/model"}, Package: "pkg/util/retry"}}
	noPackage := surface
	noPackage.Surface = &records.Surface{Public: []string{"pkg/util"}, Model: []string{}}
	plain := records.Request{Rule: "coupling", From: "pkg_tc", To: "pkg_util"}
	for _, tc := range []struct {
		name     string
		req      records.Request
		a        Answer
		wantGlob string
		wantErr  bool
	}{
		{"declare public", surface, Answer{Kind: policy3.DeclarePublic}, "pkg/util/retry", false},
		{"declare a model part", surface, Answer{Kind: policy3.DeclareModel}, "pkg/util/retry", false},
		{"allow until a date", surface, Answer{Kind: policy3.ApproveUntil, Until: "2026-12-20"}, "", false},
		{"reject with instead", surface, Answer{Kind: policy3.Reject, Instead: "Use pkg/util."}, "", false},
		{"approve a use past the surface", surface, Answer{Kind: policy3.Approve}, "", true},
		{"declare without a package", noPackage, Answer{Kind: policy3.DeclarePublic}, "", true},
		{"declare on another request", plain, Answer{Kind: policy3.DeclareModel}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ChangeOf(7, tc.req, tc.a, today)
			if tc.wantErr {
				if !errors.Is(err, ErrAnswer) {
					t.Fatalf("err = %v, want ErrAnswer", err)
				}
				return
			}
			if err != nil || c.Glob != tc.wantGlob || c.Kind != tc.a.Kind {
				t.Fatalf("ChangeOf = %+v, %v", c, err)
			}
		})
	}
}

func TestEditDeclaresThePackageInTheSurface(t *testing.T) {
	const v1 = "version: 1\nowners: [olivia]\nmodules:\n  pkg_tc:\n    paths: [pkg/tc/**]\n  pkg_util:\n    paths: [pkg/util/**]\n    public: [pkg/util]\n"
	declare := func(kind policy3.Answer) Change {
		return Change{Request: 7, Kind: kind, Rule: "coupling", From: "pkg_tc", To: "pkg_util", Glob: "pkg/util/retry"}
	}
	out, err := Edit([]byte(v1), declare(policy3.DeclarePublic), "request #7")
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(v1, string(out)); got != "    public:\n      - pkg/util\n      - pkg/util/retry  # request #7\n" {
		t.Errorf("declare public: changed lines = %q", got)
	}
	out, err = Edit([]byte(v1), declare(policy3.DeclareModel), "request #7")
	if err != nil {
		t.Fatal(err)
	}
	if got := changed(v1, string(out)); got != "    model:\n      - pkg/util/retry  # request #7\n" {
		t.Errorf("declare a model part: changed lines = %q", got)
	}
	v3 := strings.Replace(v1, "version: 1", "version: 3", 1)
	if _, err := Edit([]byte(v3), declare(policy3.DeclareModel), "request #7"); !errors.Is(err, ErrByHand) {
		t.Errorf("a version 3 policy: err = %v, want ErrByHand", err)
	}
}

// A request keeps the surface of a coupling error past it, with the package the error names; another rule keeps none.
func TestSurfaceOfACouplingError(t *testing.T) {
	s := &result.Surface{Public: []string{"pkg/util"}, Model: []string{}}
	if got := surfaceOf(result.Error{Rule: "coupling", Surface: s}); got == nil || got.Package != "" || !slices.Equal(got.Public, s.Public) {
		t.Errorf("surfaceOf(coupling) = %+v", got)
	}
	if got := surfaceOf(result.Error{Rule: "graph", Surface: s}); got != nil {
		t.Errorf("surfaceOf(graph) = %+v, want nil", got)
	}
	if got := surfaceOf(result.Error{Rule: "coupling"}); got != nil {
		t.Errorf("surfaceOf(no surface) = %+v, want nil", got)
	}
}

// A request is a surface request when any of its errors is a use past the surface, not only the first.
func TestGroupSurfaceTakesTheFirstErrorWithASurface(t *testing.T) {
	past := result.Error{Rule: "coupling", Surface: &result.Surface{Public: []string{"pkg/util"}, Model: []string{}}}
	if got := groupSurface([]result.Error{{Rule: "coupling"}, past}); got == nil || !slices.Equal(got.Public, []string{"pkg/util"}) {
		t.Errorf("groupSurface = %+v", got)
	}
	if got := groupSurface([]result.Error{{Rule: "coupling"}}); got != nil {
		t.Errorf("groupSurface without a surface = %+v, want nil", got)
	}
}

// A verified result can make a request a surface request, or give it the package; it never makes it stop being one,
// and the package a request keeps is only ever one a verified result names.
func TestMergeSurface(t *testing.T) {
	bare := &records.Surface{Public: []string{"a"}, Model: []string{}}
	retry := &records.Surface{Public: []string{"a"}, Model: []string{}, Package: "a/retry"}
	backoff := &records.Surface{Public: []string{"a"}, Model: []string{}, Package: "a/backoff"}
	contested := &records.Surface{Public: []string{"a"}, Model: []string{}, Contested: true}
	for _, tc := range []struct {
		name        string
		kept, s     *records.Surface
		verified    bool
		want        *records.Surface
		wantChanged bool
	}{
		{"no surface either side", nil, nil, true, nil, false},
		{"a verified result past the surface", nil, bare, true, bare, true},
		{"a verified result names the package", bare, retry, true, retry, true},
		{"a verified result without a surface keeps it", retry, nil, true, retry, false},
		{"a package is never dropped for none", retry, bare, true, retry, false},
		{"the same package again", retry, retry, true, retry, false},
		{"a reported package is replaced by the verified one", retry, backoff, false, backoff, true},
		{"a reported package is dropped by a verified result without a surface", retry, nil, false, bare, true},
		{"two verified packages leave none", retry, backoff, true, contested, true},
		{"a contested request never takes a package again", contested, retry, true, contested, false},
		{"a contested request keeps its mark against a bare surface", contested, bare, true, contested, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := mergeSurface(tc.kept, tc.s, tc.verified)
			if !reflect.DeepEqual(got, tc.want) || changed != tc.wantChanged {
				t.Errorf("mergeSurface = %+v, %v; want %+v, %v", got, changed, tc.want, tc.wantChanged)
			}
		})
	}
}

func TestBoundedSurface(t *testing.T) {
	many := make([]string, maxSurfaceGlobs+5)
	for i := range many {
		many[i] = strings.Repeat("x", maxSurfaceGlob+10)
	}
	got := boundedSurface(many, nil, strings.Repeat("p", maxSurfaceGlob+1))
	if len(got.Public) != maxSurfaceGlobs || len(got.Public[0]) != maxSurfaceGlob || got.Model == nil || len(got.Model) != 0 {
		t.Errorf("public %d globs of %d bytes, model %v", len(got.Public), len(got.Public[0]), got.Model)
	}
	if got.Package != "" {
		t.Error("a package past the bound is cut, not dropped: a declaration would write another glob")
	}
}

// A surface request with its package previews each declaration against the version 1 policy at the tip, and refuses
// a plain approve.
func TestPreviewOfASurfaceRequest(t *testing.T) {
	w := newPreviewWorld(t)
	w.gh.policy = []byte("version: 1\nowners: [olivia]\nmodules:\n  pkg_tc:\n    paths: [pkg/tc/**]\n  pkg_util:\n    paths: [pkg/util/**]\n    public: [pkg/util]\n")
	w.put(432, records.Request{Key: "coupling pkg_tc pkg_util", Reason: "retry is shared", Rule: "coupling", From: "pkg_tc", To: "pkg_util",
		Numbers: records.Numbers{Verified: true, Balance: &records.Balance{Score: 2, Max: 10, Min: 7, Strength: "intrusive", Volatility: "high"}},
		Surface: &records.Surface{Public: []string{"pkg/util"}, Model: []string{}, Package: "pkg/util/retry"}})
	ctx := context.Background()
	for kind, want := range map[policy3.Answer]string{
		policy3.DeclarePublic: "+      - pkg/util/retry  # request #432\n",
		policy3.DeclareModel:  "+    model:\n+      - pkg/util/retry  # request #432\n",
	} {
		p, err := w.svc.Preview(ctx, previewRepo, 432, Answer{Kind: kind}, testToday)
		if err != nil || !p.Loosens || !strings.Contains(p.Diff, want) {
			t.Errorf("%s: preview %+v, %v; want a diff with %q", kind, p, err, want)
		}
	}
	if _, err := w.svc.Preview(ctx, previewRepo, 432, Answer{Kind: policy3.Approve}, testToday); !errors.Is(err, ErrAnswer) {
		t.Errorf("approve: err = %v, want ErrAnswer", err)
	}
}
