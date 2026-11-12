package requests

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

// The request data and the preview of each answer (design 10.4, 12.3, 19; M2 task 17). The owner decides on more
// than the worker's words: the record with its balance, the two modules, what the policy already says about them, and
// what each answer would write and change. The App runs no analysis: it reads the request, the pull records and the
// policy at the default branch's tip, and calls the policy writer.

var (
	// ErrNoRequest is a request number the App keeps no request for.
	ErrNoRequest = errors.New("requests: no such request")
	// ErrClosedRequest is a preview of a request that is not open: it was answered or closed.
	ErrClosedRequest = errors.New("requests: the request is not open")
	// ErrAnswer is an answer that cannot be written: an unknown kind, a missing or bad date, no instead.
	ErrAnswer = errors.New("requests: the answer is not complete")
	// ErrNoPolicy means the default branch has no policy of version 3 to write an answer into.
	ErrNoPolicy = errors.New("requests: the default branch has no policy of version 3")
	// ErrByHand is an answer the writer cannot place in this policy file; the owner edits it by hand.
	ErrByHand = errors.New("requests: the answer cannot be written into this policy")
)

// The rule ids an answer writes apart (wire/request's key treats the same ones apart).
const (
	ruleGraph       = "graph"
	ruleDuplication = "duplication"
	ruleUnowned     = "unowned"
)

const (
	dateLayout = "2006-01-02"
	// diffContext is the number of unchanged lines a diff shows around the change.
	diffContext = 3
)

// Answer is an owner's answer to a request (design 10.4).
type Answer struct {
	Kind    policy3.Answer // approve, approve-until or reject
	Until   string         // approve-until: the date, YYYY-MM-DD
	Instead string         // reject: what to do instead
}

// Change is the policy edit of one answer, with everything needed to write it again on another tip of the default
// branch. Its JSON is stable, so an approval can keep it (records.Approval.Change).
type Change struct {
	Request int            `json:"request"`
	Kind    policy3.Answer `json:"kind"`
	Rule    string         `json:"rule"`
	Path    string         `json:"path,omitempty"`
	From    string         `json:"from,omitempty"`
	To      string         `json:"to,omitempty"`
	Until   string         `json:"until,omitempty"`
	Instead string         `json:"instead,omitempty"`
}

// ChangeOf is the change an answer makes for request number. It checks the answer alone; whether the policy can take
// it is Edit's.
func ChangeOf(number int, req records.Request, a Answer) (Change, error) {
	c := Change{Request: number, Kind: a.Kind, Rule: req.Rule, Path: req.Path, From: req.From, To: req.To}
	switch a.Kind {
	case policy3.Approve:
	case policy3.ApproveUntil:
		if _, err := time.Parse(dateLayout, a.Until); err != nil {
			return Change{}, fmt.Errorf("%w: approve until needs a date, YYYY-MM-DD", ErrAnswer)
		}
		c.Until = a.Until
	case policy3.Reject:
		c.Instead = strings.TrimSpace(a.Instead)
		if c.Instead == "" {
			return Change{}, fmt.Errorf("%w: reject needs what to do instead", ErrAnswer)
		}
	default:
		return Change{}, fmt.Errorf("%w: unknown answer %q", ErrAnswer, a.Kind)
	}
	if c.Rule == "" {
		return Change{}, fmt.Errorf("%w: the request names no rule", ErrAnswer)
	}
	return c, nil
}

// Edit writes the change into a policy file (design 10.4): approve adds the dependency to depends_on for the rule
// graph and an exception without a date for any other rule; approve-until adds an exception with the date; reject
// adds a deny rule with instead. A reject of a copy, of unowned code or of an only rule writes no line, and the file
// comes back unchanged, as it does for a dependency the policy already allows. An exception for unowned code names the
// path; one for an only rule has no to. A comment, such as "request #431", ends each line the change writes. A writer
// error wraps ErrByHand.
func Edit(file []byte, c Change, comment string) ([]byte, error) {
	if strings.ContainsAny(comment, "\r\n") {
		return nil, fmt.Errorf("%w: a comment on two lines", ErrAnswer)
	}
	pol, err := policy3.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrByHand, err)
	}
	n := strconv.Itoa(c.Request)
	exception := policy3.Exception{Rule: c.Rule, From: c.From, To: c.To, Why: "Approved in request #" + n + "."}
	switch {
	case c.Rule == ruleUnowned:
		exception.From, exception.To = c.Path, ""
	case onlyRule(pol, c.Rule):
		exception.To = ""
	}
	var out []byte
	switch c.Kind {
	case policy3.Approve:
		if c.Rule == ruleGraph {
			out, err = policy3.AddDependsOn(file, c.From, c.To)
		} else {
			out, err = policy3.AddException(file, exception)
		}
	case policy3.ApproveUntil:
		exception.Expires = c.Until
		out, err = policy3.AddException(file, exception)
	case policy3.Reject:
		if c.Rule == ruleDuplication || c.Rule == ruleUnowned || onlyRule(pol, c.Rule) || c.From == "" || c.To == "" {
			return file, nil
		}
		out, err = policy3.AddDenyRule(file, "request-"+n, c.From, c.To, "Rejected in request #"+n+".", c.Instead)
	default:
		return nil, fmt.Errorf("%w: unknown answer %q", ErrAnswer, c.Kind)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrByHand, err)
	}
	if comment == "" {
		return out, nil
	}
	return annotate(file, out, comment)
}

func onlyRule(pol policy3.File, id string) bool {
	return slices.ContainsFunc(pol.Rules, func(r policy3.Rule) bool { return r.ID == id && r.Only != nil })
}

// annotate ends each line edited changes against old with "# comment", and checks that the comment changes no value.
func annotate(old, edited []byte, comment string) ([]byte, error) {
	before, after := lines(old), lines(edited)
	head, tail := common(before, after)
	for i := head; i < len(after)-tail; i++ {
		body, eol := cutEOL(after[i])
		after[i] = body + " # " + comment + eol
	}
	out := []byte(strings.Join(after, ""))
	want, err := policy3.Decode(edited)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrByHand, err)
	}
	got, err := policy3.Decode(out)
	if err != nil || !reflect.DeepEqual(got, want) {
		return nil, fmt.Errorf("%w: the comment would change the policy", ErrByHand)
	}
	return out, nil
}

// lines splits text after each LF, keeping the line ends; a last line without one is kept as it is.
func lines(text []byte) []string {
	out := strings.SplitAfter(string(text), "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func cutEOL(line string) (body, eol string) {
	for _, end := range []string{"\r\n", "\n"} {
		if b, ok := strings.CutSuffix(line, end); ok {
			return b, end
		}
	}
	return line, ""
}

// common counts the equal lines at the start and at the end of a and b, the end not overlapping the start.
func common(a, b []string) (head, tail int) {
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	return head, tail
}

// Diff is the change from old to edited of the file at path as a unified diff of one hunk, the edit of one answer
// being one insertion; "" when the two are the same.
func Diff(path string, old, edited []byte) string {
	if string(old) == string(edited) {
		return ""
	}
	a, b := lines(old), lines(edited)
	head, tail := common(a, b)
	from, to := max(0, head-diffContext), tail-min(tail, diffContext)
	oldN, newN := len(a)-to-from, len(b)-to-from
	var w strings.Builder
	w.WriteString("--- a/" + path + "\n+++ b/" + path + "\n")
	w.WriteString("@@ -" + hunkRange(from, oldN) + " +" + hunkRange(from, newN) + " @@\n")
	write := func(mark string, ls []string) {
		for _, l := range ls {
			body, _ := cutEOL(l)
			w.WriteString(mark + body + "\n")
		}
	}
	write(" ", a[from:head])
	write("-", a[head:len(a)-tail])
	write("+", b[head:len(b)-tail])
	write(" ", a[len(a)-tail:len(a)-to])
	return w.String()
}

// hunkRange is "start,count" of a unified diff hunk; an empty range starts at the line before it.
func hunkRange(from, n int) string {
	if n == 0 {
		return strconv.Itoa(from) + ",0"
	}
	return strconv.Itoa(from+1) + "," + strconv.Itoa(n)
}

// Module is one of the two modules of a request as the policy at the default branch's tip declares it.
type Module struct {
	Name       string
	Layer      string
	Volatility string // as written; empty when the module leaves it to its subdomain or the default
	Subdomain  string
	DependsOn  []string // its allowed dependencies
}

// Earlier is what the policy already says about the request's two modules: a deny rule, an answer of an earlier
// reject, or an exception, an answer of an earlier approve. Every answer in effect is a line of the policy.
type Earlier struct {
	Kind    string // EarlierDeny or EarlierException
	Rule    string // the deny rule's id, or the rule the exception is for
	From    string // the exception's from; for a deny rule, its from names joined with ", "
	To      string
	Why     string
	Instead string // a deny rule's
	Expires string // an exception's date, YYYY-MM-DD; empty for none
}

// The kinds of an Earlier.
const (
	EarlierDeny      = "deny"
	EarlierException = "exception"
)

// View is a request as the owner reads it before answering.
type View struct {
	Number  int
	State   string // records.StateOpen, StateAnswered or StateClosed
	Reason  string // the worker's text
	Rule    string
	Path    string // unowned code
	From    string
	To      string
	Expires string // the date the worker asked for, YYYY-MM-DD
	// Record is the record the request issue shows: the asked errors as the check prints them, or the reported `can`
	// answer. Numbers says whether they are verified or "as reported".
	Record  string
	Numbers records.Numbers
	// Policy is false when the default branch has no policy of version 3; Modules, Allowed and Earlier are then empty.
	Policy  bool
	Modules []Module // the request's modules the policy declares, From first
	Allowed bool     // the policy already allows From to depend on To
	Earlier []Earlier
}

// PullEffect is what an answer does to one open pull request, by its stored errors (M2 D6). An answer clears an
// error whose request key is the request's key.
type PullEffect struct {
	Number int
	Status string // the status of its latest verified result
	Waits  bool   // it waits on this request
	Clears int    // its errors the answer clears
	Left   int    // its errors the answer does not clear; 0 means the answer leaves it no error
}

// Preview is what one answer to an open request would write and change.
type Preview struct {
	Answer Answer // with the date it would use
	Change Change
	// Diff is the policy change as a unified diff against the policy at PolicyDigest; "" when the answer writes no line.
	Diff         string
	PolicyDigest string
	Loosens      bool // the answer needs the passkey step (design 9.2)
	// Pulls are the open pull requests the answer touches, by number: those waiting on the request, and the others it
	// clears errors of. A reject clears nothing.
	Pulls []PullEffect
}

// View reads request number of the repository with the policy at the default branch's tip.
func (s *Service) View(ctx context.Context, r api.Repo, number int) (View, error) {
	req, err := load(ctx, s, r, number)
	if err != nil {
		return View{}, err
	}
	v := View{Number: number, State: req.State, Reason: req.Reason, Rule: req.Rule, Path: req.Path, From: req.From,
		To: req.To, Expires: req.Expires, Record: req.Record, Numbers: req.Numbers}
	pol, _, found, err := s.policyAt(ctx, r)
	if err != nil || !found {
		return v, err
	}
	v.Policy = true
	for _, name := range []string{req.From, req.To} {
		if m, ok := pol.Modules[name]; ok && name != "" && !slices.ContainsFunc(v.Modules, func(x Module) bool { return x.Name == name }) {
			v.Modules = append(v.Modules, Module{Name: name, Layer: m.Layer, Volatility: m.Volatility, Subdomain: m.Subdomain,
				DependsOn: slices.Clone(m.DependsOn)})
		}
	}
	v.Allowed = req.From != "" && slices.Contains(pol.Modules[req.From].DependsOn, req.To)
	v.Earlier = earlier(pol, req.From, req.To)
	return v, nil
}

// earlier lists the deny rules and exceptions of the policy that name both modules, either way, in policy order.
func earlier(pol policy3.File, a, b string) []Earlier {
	if a == "" || b == "" {
		return nil
	}
	pair := func(from, to []string) bool {
		return slices.Contains(from, a) && slices.Contains(to, b) || slices.Contains(from, b) && slices.Contains(to, a)
	}
	var out []Earlier
	for _, r := range pol.Rules {
		if r.Deny != nil && pair(r.Deny.From, r.Deny.To) {
			out = append(out, Earlier{Kind: EarlierDeny, Rule: r.ID, From: strings.Join(r.Deny.From, ", "),
				To: strings.Join(r.Deny.To, ", "), Why: r.Why, Instead: r.Instead})
		}
	}
	for _, e := range pol.Exceptions {
		if pair([]string{e.From}, []string{e.To}) {
			out = append(out, Earlier{Kind: EarlierException, Rule: e.Rule, From: e.From, To: e.To, Why: e.Why,
				Expires: e.Expires})
		}
	}
	return out
}

// Preview is what answer a to the open request number would write into the policy at the default branch's tip, and
// which open pull requests it touches. Approve until takes the date the worker asked for when a gives none.
func (s *Service) Preview(ctx context.Context, r api.Repo, number int, a Answer) (Preview, error) {
	req, err := load(ctx, s, r, number)
	if err != nil {
		return Preview{}, err
	}
	if req.State != records.StateOpen {
		return Preview{}, ErrClosedRequest
	}
	if a.Kind == policy3.ApproveUntil && a.Until == "" {
		a.Until = req.Expires
	}
	c, err := ChangeOf(number, req, a)
	if err != nil {
		return Preview{}, err
	}
	loosens, err := policy3.Loosens(a.Kind)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: %w", ErrAnswer, err)
	}
	_, raw, found, err := s.policyAt(ctx, r)
	if err != nil {
		return Preview{}, err
	}
	if !found {
		return Preview{}, ErrNoPolicy
	}
	edited, err := Edit(raw, c, "")
	if err != nil {
		return Preview{}, err
	}
	pulls, err := effects(ctx, s, r, number, req, a.Kind != policy3.Reject)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Answer: a, Change: c, Diff: Diff(policyPath, raw, edited), PolicyDigest: policy3.Digest(raw),
		Loosens: loosens, Pulls: pulls}, nil
}

// effects lists the open pull requests of the pull index that wait on the request or, when clears, hold an error with
// its key, by number.
func effects(ctx context.Context, s *Service, r api.Repo, number int, req records.Request, clears bool) ([]PullEffect, error) {
	x := &records.Index{Schema: records.SchemaPullIndex}
	err := records.Load(ctx, s.store, tenant(r), records.PullIndexKey(r.FullName), x)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return nil, fmt.Errorf("requests: read the pull index: %w", err)
	}
	var out []PullEffect
	for _, entry := range x.Entries {
		pull, err := strconv.Atoi(entry.Key)
		if err != nil {
			continue
		}
		var rec records.Pull
		err = records.Load(ctx, s.store, tenant(r), records.PullKey(r.FullName, pull), &rec)
		if errors.Is(err, records.ErrNoRecord) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("requests: read the pull record: %w", err)
		}
		e := PullEffect{Number: pull, Status: rec.Status, Waits: slices.Contains(req.Waiting, pull)}
		if clears {
			for _, k := range rec.RequestKeys {
				if k == req.Key {
					e.Clears++
				}
			}
		}
		e.Left = len(rec.Errors) - e.Clears
		if e.Waits || e.Clears > 0 {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b PullEffect) int { return a.Number - b.Number })
	return out, nil
}

// load reads request number; a missing one is ErrNoRequest.
func load(ctx context.Context, s *Service, r api.Repo, number int) (records.Request, error) {
	var req records.Request
	err := records.Load(ctx, s.store, tenant(r), records.RequestKey(r.FullName, number), &req)
	if errors.Is(err, records.ErrNoRecord) {
		return req, ErrNoRequest
	}
	if err != nil {
		return req, fmt.Errorf("requests: read request %d: %w", number, err)
	}
	return req, nil
}
