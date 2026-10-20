package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/ui/graph"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	originComparable = "comparable" // Comparison.OriginStatus: each finding's origin against the base branch is meaningful
	shortSHA         = 7

	noPolicy = "Neither the head nor the base branch holds the policy this report was computed from."
)

// apiPullGraph is GET /api/repos/{o}/{n}/pulls/{number}/graph. It draws the report the pull request page shows: the
// run its archfit/state check names, never the default branch's latest report.
func (s *server) apiPullGraph(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	ctx := r.Context()
	// The policy texts need only the pull request: they are read beside its report, and judged once it is known.
	var texts policyTexts
	pull, rep, err := s.readPull(ctx, sc, number, func(p api.Pull) { texts = s.policyTexts(ctx, sc, p) })
	if err != nil {
		return nil, err
	}
	head := headOf(sc, pull, rep)
	body := graphJSON{Pull: &head, Branch: sc.repo.DefaultBranch, Unavailable: pull.Unavailable}
	if rep == nil {
		return body, nil
	}
	text, ref, err := reportPolicy(pull.Pull, texts, *rep)
	if err != nil {
		return nil, err
	}
	view := graph.Build(*rep, text)
	if view.Stale {
		body.StaleReason = noPolicy
	}
	body.View = &view
	body.PolicyHref = "https://github.com/" + sc.repo.FullName + "/blob/" + escapeRef(ref) + "/" + policy.PolicyPath
	for _, l := range view.Links {
		if l.Origin == report.PairAdded {
			body.Added++
		}
	}
	return body, nil
}

// escapeRef escapes each segment of a ref for a URL path: a branch name may hold "#" or "?", and "/" separates segments
// on GitHub as it does in the name.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// policyTexts is the policy at a pull request's head and at its base, each with its read error.
type policyTexts struct {
	texts [2][]byte
	errs  [2]error
}

// policyTexts reads both texts side by side: the head's first, then the base's (reportPolicy judges them in order).
func (s *server) policyTexts(ctx context.Context, sc scope, pull api.Pull) (p policyTexts) {
	together(
		func() {
			p.texts[0], _, p.errs[0] = s.gh.files.File(ctx, sc.repo.RepoRef, pull.HeadSHA, policy.PolicyPath)
		},
		func() {
			p.texts[1], _, p.errs[1] = s.gh.files.File(ctx, sc.repo.RepoRef, pull.BaseRef, policy.PolicyPath)
		},
	)
	return p
}

// reportPolicy finds the policy text the report was computed from, for the module names, layers and lines. The
// Action reads the policy from the head when the pull request changes it, else from the base branch: the head first,
// then the base, the first whose digest is the report's config_hash. With neither, the head's text (or the base's,
// when the head has none) and graph.Build marks the view stale. Display only: the gate never reads this text.
// The base's text or failure counts only when the head's text is not the report's, as when they were read in turn.
func reportPolicy(pull api.Pull, p policyTexts, rep report.Report) (text []byte, ref string, err error) {
	refs := []string{pull.HeadSHA, pull.BaseRef}
	for i, r := range refs {
		t, err := p.texts[i], p.errs[i]
		if errors.Is(err, api.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if policy.Digest(t) == rep.Comparison.ConfigHash {
			return t, r, nil
		}
		if text == nil {
			text, ref = t, r
		}
	}
	if ref == "" {
		ref = pull.HeadSHA
	}
	return text, ref, nil
}
