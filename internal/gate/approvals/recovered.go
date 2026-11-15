package approvals

import (
	"context"
	"errors"
	"slices"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Recovered recognizes a delayed real delivery of a merge already recovered by Reconcile. The caller invokes
// Pushed only for JudgedNone. It records the actual delivery key against immutable merge evidence, without changing
// repository authority, consuming again, or relying on an open proposal's index entry.
func (s *Service) Recovered(ctx context.Context, p Push) (Outcome, error) {
	none := Outcome{Judged: JudgedNone}
	if p.Before == "" || len(p.ChangedPaths) > 0 && !slices.Contains(p.ChangedPaths, PolicyPath) {
		return none, nil
	}
	key := records.PolicyPushKey(p.Repo.FullName, records.Push{Before: p.Before, After: p.After})
	var delivered records.PolicyPush
	err := records.Load(ctx, s.store, tenant(p.Repo), key, &delivered)
	if err == nil {
		return none, nil
	} // Pushed handles the recorded delivery and its handoff.
	if !errors.Is(err, records.ErrNoRecord) {
		return Outcome{}, err
	}
	c, err := s.gh.Compare(ctx, p.Repo.RepoRef, p.Before, p.After)
	if api.Permanent(err) {
		return none, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	chain := linearRange(c, p.Before, p.After)
	if len(chain) == 0 {
		return none, nil
	}
	raw, err := s.policy(ctx, p.Repo.RepoRef, p.After)
	if err != nil {
		return Outcome{}, err
	}
	digest := digestOf(raw)
	for _, commit := range chain {
		var proof records.PolicyPush
		err := records.Load(ctx, s.store, tenant(p.Repo), records.RecoveredPushKey(p.Repo.FullName, commit), &proof)
		if errors.Is(err, records.ErrNoRecord) {
			continue
		}
		if err != nil {
			return Outcome{}, err
		}
		if proof.Proposal == "" || proof.Digest != digest {
			continue
		}
		if err := records.RecoveredDelivery(ctx, s.store, tenant(p.Repo), p.Repo.FullName, commit,
			records.Push{Before: p.Before, After: p.After}, proof); err != nil {
			return Outcome{}, err
		}
		return s.consumed(ctx, p.Repo, proof.Proposal)
	}
	return none, nil
}

// linearRange returns the complete chain newest first. A cut or non-linear response is never proof.
func linearRange(c api.Comparison, before, tip string) []string {
	if c.Status != compareAhead || c.TotalCommits == 0 || len(c.Commits) != c.TotalCommits || c.Truncated {
		return nil
	}
	nodes := map[string]api.Commit{}
	for _, n := range c.Commits {
		if n.SHA == "" || len(n.Parents) != 1 || nodes[n.SHA].SHA != "" {
			return nil
		}
		nodes[n.SHA] = n
	}
	var reverse []string
	head := tip
	for range len(nodes) {
		n, ok := nodes[head]
		if !ok {
			return nil
		}
		reverse = append(reverse, head)
		head = n.Parents[0]
		if head == before {
			break
		}
	}
	if head != before || len(reverse) != len(nodes) {
		return nil
	}
	return reverse
}
