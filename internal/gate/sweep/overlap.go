package sweep

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// edge is one observed module-to-module dependency of a report.
type edge struct{ from, to string }

// edgesOf is the directed module graph of a report. A seam within one module, or without a module at either end,
// is no edge.
func edgesOf(seams []report.Seam) []edge {
	var out []edge
	for _, s := range seams {
		if s.FromModule != "" && s.ToModule != "" && s.FromModule != s.ToModule {
			out = append(out, edge{s.FromModule, s.ToModule})
		}
	}
	return out
}

// cycles returns the strongly connected components of two or more modules of the graph, each sorted by module name.
func cycles(edges []edge) [][]string {
	next := map[string][]string{}
	for _, e := range edges {
		next[e.from] = append(next[e.from], e.to)
		if _, ok := next[e.to]; !ok {
			next[e.to] = nil
		}
	}
	nodes := make([]string, 0, len(next))
	for n := range next {
		nodes = append(nodes, n)
	}
	slices.Sort(nodes) // a fixed visiting order keeps the result, and so the published text, the same on every pass

	// Tarjan's algorithm. The graph has one node per module of a report: recursion depth is not a concern.
	var (
		out     [][]string
		index   = map[string]int{}
		low     = map[string]int{}
		onStack = map[string]bool{}
		stack   []string
		visit   func(string)
	)
	visit = func(n string) {
		index[n], low[n] = len(index), len(index)
		stack, onStack[n] = append(stack, n), true
		for _, m := range next[n] {
			if _, seen := index[m]; !seen {
				visit(m)
				low[n] = min(low[n], low[m])
			} else if onStack[m] {
				low[n] = min(low[n], index[m])
			}
		}
		if low[n] != index[n] {
			return
		}
		var comp []string
		for {
			m := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[m] = false
			comp = append(comp, m)
			if m == n {
				break
			}
		}
		if len(comp) > 1 {
			slices.Sort(comp)
			out = append(out, comp)
		}
	}
	for _, n := range nodes {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}
	return out
}

// closedCycle is the predicate of rule 5c. It returns, sorted, the modules of every cycle that the directed union of
// the tip's graph and the pull request's graph holds and that neither graph holds alone, member for member. A cycle
// that exists in the target already is not this pull request's doing, one the pull request closes by itself was
// measured by its own run, and a pull request that grows an existing cycle makes a new, larger one.
func closedCycle(tip, pr []edge) []string {
	known := map[string]bool{}
	for _, g := range [][]edge{tip, pr} {
		for _, c := range cycles(g) {
			known[strings.Join(c, "\x00")] = true
		}
	}
	var out []string
	for _, c := range cycles(slices.Concat(tip, pr)) {
		if !known[strings.Join(c, "\x00")] {
			out = append(out, c...)
		}
	}
	slices.Sort(out)
	return out
}

// edgeCacheSize bounds the graphs one instance keeps **[default]**: each is the module edges of one report, some
// kilobytes, and a repository with a hundred open pull requests holds a hundred.
const edgeCacheSize = 512

type edgeKey struct {
	repo   api.RepoRef
	digest string
}

// edgeCache keeps the module graph of a report under the digest its bytes were verified against. A report never
// changes, so an entry never goes stale; the oldest entry goes first when the cache is full.
type edgeCache struct {
	mu    sync.Mutex
	byKey map[edgeKey][]edge
	order []edgeKey
}

func (c *edgeCache) get(k edgeKey) ([]edge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	return e, ok
}

func (c *edgeCache) put(k edgeKey, e []edge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byKey[k]; ok {
		return
	}
	if c.byKey == nil {
		c.byKey = map[edgeKey][]edge{}
	}
	if len(c.order) == edgeCacheSize {
		delete(c.byKey, c.order[0])
		c.order = c.order[1:]
	}
	c.byKey[k] = e
	c.order = append(c.order, k)
}

// edges is the module graph of the report that run uploaded, proved to be the report digest names. ok is false when
// there is no such report: no run, an expired artifact, an artifact that is not one file, bytes that are not the
// digest's or not a report. Those stay so on every read. An error is a failed read, which proves nothing either way.
func (s *sweeper) edges(ctx context.Context, r api.RepoRef, runID int64, digest string) (edges []edge, ok bool, err error) {
	if runID == 0 || digest == "" {
		return nil, false, nil
	}
	key := edgeKey{r, digest}
	if e, hit := s.graphs.get(key); hit {
		return e, true, nil
	}
	payload, err := s.actions.Artifact(ctx, r, runID, report.ArtifactName, report.MaxBytes)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrArtifactInvalid) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rep, err := report.DecodeArtifact(payload, digest)
	if err != nil {
		return nil, false, nil
	}
	e := edgesOf(rep.Seams)
	s.graphs.put(key, e)
	return e, true, nil
}

// tipOutcome is what a pass learned about the report of a default-branch tip.
type tipOutcome int

const (
	tipUndecided tipOutcome = iota // no report to compare with, and not for long enough to call it missing
	tipRead                        // edges is the tip's module graph
	tipOverdue                     // no verifiable report for longer than MissingUploadAfter
)

// tipView is the default-branch tip of one repository and its report, read at most once per pass.
type tipView struct {
	sha     string // "" when the default branch does not exist
	loaded  bool
	outcome tipOutcome
	edges   []edge
}

// tip is the default-branch head of r, read once per pass.
func (p *pass) tip(ctx context.Context, r api.Repo) (*tipView, error) {
	if v, ok := p.tips[r.RepoRef]; ok {
		return v, nil
	}
	sha, err := p.repos.BranchHead(ctx, r.RepoRef, r.DefaultBranch)
	if errors.Is(err, api.ErrNotFound) {
		sha, err = "", nil
	}
	if err != nil {
		return nil, err
	}
	v := &tipView{sha: sha}
	p.remember(r.RepoRef, v)
	return v, nil
}

func (p *pass) remember(r api.RepoRef, v *tipView) {
	if p.tips == nil {
		p.tips = map[api.RepoRef]*tipView{}
	}
	p.tips[r] = v
}

// load reads the report of the tip: the archfit/state check on the tip commit names a run and a report digest, the
// run's artifact must be that report, and the run must be one the App decided (Decided): a rejected upload (unknown
// engine, unapproved workflow, mismatched envelope) is no measurement of the tip, however well it decodes. No check
// yet is no decision, as is a check without a verifiable report until MissingUploadAfter has passed on its clock; the clock of a check that starts in the future reads as run out, so a
// skewed or forged time never keeps the recheck off.
func (p *pass) load(ctx context.Context, r api.Repo, v *tipView) error {
	if v.loaded {
		return nil
	}
	state, err := p.publisher.AdvisoryState(ctx, inputs.Target{Repo: r, HeadSHA: v.sha, BaseRef: r.DefaultBranch})
	if err != nil {
		return err
	}
	if state.Exists {
		rc := state.Receipt
		var edges []edge
		ok := false
		if Decided(rc) {
			var err error
			if edges, ok, err = p.edges(ctx, r.RepoRef, rc.RunID, rc.ReportDigest); err != nil {
				return err
			}
		}
		age := p.now.Sub(state.StartedAt)
		switch {
		case ok:
			v.outcome, v.edges = tipRead, edges
		case age < 0 || age >= p.cfg.MissingUploadAfter:
			v.outcome = tipOverdue
		}
	}
	v.loaded = true
	return nil
}

// targetMoved is rule 5c: a success receipt measured on a base that is not the default branch tip any more goes
// stale when the tip's report and the pull request's report together close a module cycle that neither holds alone.
// It reports whether it ended the evaluation (it published, or found the check changed). A read that fails decides
// nothing, whichever report it was for. A report that is missing or defective for good proves nothing about the tip
// (nothing is decided until MissingUploadAfter), but for the pull request's own report it goes stale at once, because
// an update of the branch makes a new report.
func (p *pass) targetMoved(ctx context.Context, t inputs.Target, tr inputs.Trusted, rc feedback.Receipt) (bool, error) {
	if rc.Conclusion != verdict.Success || tr.Settings.Recheck() != settings.RecheckOverlap {
		return false, nil
	}
	tip, err := p.tip(ctx, t.Repo)
	if err != nil || tip.sha == "" || rc.BaseSHA == tip.sha {
		return false, err
	}
	if err := p.load(ctx, t.Repo, tip); err != nil {
		return false, err
	}
	var cycle []string
	switch tip.outcome {
	case tipUndecided:
		return false, nil
	case tipRead:
		mine, ok, err := p.edges(ctx, t.Repo.RepoRef, rc.RunID, rc.ReportDigest)
		if err != nil {
			return false, err
		}
		if ok {
			if cycle = closedCycle(tip.edges, mine); len(cycle) == 0 {
				return false, nil
			}
		}
	}
	return true, p.publishMoved(ctx, "5c", t, rc, cycle, tip.sha, func(tr inputs.Trusted) bool {
		return tr.Settings.Recheck() == settings.RecheckOverlap && !stale(rc, tr)
	})
}

// retarget is the base-behind branch of rules 5b, 6b, 7a, 7b and 7c, which would otherwise ask for a re-run. A re-run
// reuses the commit the old run measured, and the upload of a commit whose base is not the tip fails envelope_mismatch,
// so only a new run on an updated branch can bring evidence: target_moved, without a cycle, names that recovery in
// place of the rule's own reason. It reports whether it ended the evaluation. A receipt without a base (written before
// the field existed) and one measured on the tip are not behind, and neither is any receipt when the tip cannot be
// read: the rule then does what it did before, and only a rate limit or a cancelled ctx stops the pass.
// replaceStale is for rule 5b, whose receipt is stale by definition; the other rules leave a stale receipt to it.
func (p *pass) retarget(ctx context.Context, rule string, t inputs.Target, rc feedback.Receipt, replaceStale bool) (bool, error) {
	if rc.BaseSHA == "" {
		return false, nil
	}
	tip, err := p.tip(ctx, t.Repo)
	if err != nil {
		if errors.Is(err, api.ErrRateLimited) || ctx.Err() != nil {
			return false, err
		}
		return false, nil
	}
	if tip.sha == "" || tip.sha == rc.BaseSHA {
		return false, nil
	}
	return true, p.publishMoved(ctx, rule, t, rc, nil, tip.sha, func(tr inputs.Trusted) bool {
		return replaceStale || !stale(rc, tr)
	})
}

// publishMoved writes target_moved over rc inside the head's publication lease. The decision is made again there from
// the check and the inputs as they are: a newer run, a withdrawn approval, a repository that left required mode or
// that admits answers false are left to the rule that owns them.
func (p *pass) publishMoved(ctx context.Context, rule string, t inputs.Target, rc feedback.Receipt, cycle []string, tip string, admits func(inputs.Trusted) bool) error {
	var pub *feedback.Publication
	err := p.publisher.Reconcile(ctx, t, func(ctx context.Context, current feedback.CheckState) (*feedback.Publication, error) {
		if current.Receipt != rc {
			return nil, nil
		}
		tr, err := p.resolver.Resolve(ctx, t, p.now)
		if err != nil {
			return nil, err
		}
		if tr.Settings.Gate != settings.GateRequired || Withdrawn(rc, tr) || !admits(tr) {
			return nil, nil
		}
		moved := withOutcome(verdict.TargetMoved(cycle, rc.BaseSHA, tip), verdict.ReasonTargetMoved, t, tr, rc)
		pub = &moved
		return pub, nil
	})
	if err != nil || pub == nil {
		return err
	}
	p.logPublished(rule, *pub, rc)
	return nil
}
