package onboarding

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	reanchorTitle   = "Re-anchor the Archfit baseline"
	reanchorMessage = "Re-anchor the Archfit baseline captured by the approved workflow"
)

// Response codes of POST /v1/reanchors that this module owns. The App refuses a re-anchor it cannot match against the
// protected baseline, and one that would accept debt or seams the protected baseline does not accept.
const (
	codeReanchorBaselineMissing = "reanchor_baseline_missing"
	codeReanchorDigestMismatch  = "reanchor_digest_mismatch"
	codeReanchorNotSubset       = "reanchor_not_subset"
)

// reanchorCheck verifies a re-anchor without trusting the engine. The protected baseline must exist and read, and its
// bytes must hash to the digest the envelope names. Every accepted finding and every qualifying seam of the upload must
// already be in the protected baseline: a re-anchor keeps debt, it never accepts new debt. It returns the protected
// baseline as read, or a refusal.
func reanchorCheck(c baselineCheck, up gate.Upload) (policy.Baseline, int, string) {
	if c.protectedRaw == nil {
		return policy.Baseline{}, 403, codeReanchorBaselineMissing
	}
	protected, err := policy.ReadBaseline(c.protectedRaw)
	if err != nil {
		return policy.Baseline{}, 403, codeReanchorBaselineMissing
	}
	if policy.Digest(c.protectedRaw) != up.BaselineDigest {
		return policy.Baseline{}, 403, codeReanchorDigestMismatch
	}
	accepted := make(map[string]bool, len(protected.Accepted))
	for _, a := range protected.Accepted {
		accepted[acceptedKey(a)] = true
	}
	for _, a := range c.capture.Accepted {
		if !accepted[acceptedKey(a)] {
			return policy.Baseline{}, 403, codeReanchorNotSubset
		}
	}
	seams := make(map[string]bool, len(protected.QualifyingSeamIDs))
	for _, id := range protected.QualifyingSeamIDs {
		seams[id] = true
	}
	for _, id := range c.capture.QualifyingSeamIDs {
		if !seams[id] {
			return policy.Baseline{}, 403, codeReanchorNotSubset
		}
	}
	return protected, 0, ""
}

// acceptedKey identifies one accepted finding: its fingerprint and the rule that produced it.
func acceptedKey(a policy.Accepted) string { return a.Fingerprint + "\x00" + a.RuleID }

// reanchorBody states what the re-anchor keeps, drops and resets. Everything here comes from the two baseline files;
// the engine report stays in the Action run, which the body names.
func reanchorBody(up gate.Upload, c baselineCheck) string {
	b := up.Binding
	branch := mono(b.Repo.DefaultBranch)
	protected, capture := c.current, c.capture
	kept := len(capture.Accepted)

	keptKeys := make(map[string]bool, kept)
	for _, a := range capture.Accepted {
		keptKeys[acceptedKey(a)] = true
	}
	var dropped []policy.Accepted
	for _, a := range protected.Accepted {
		if !keptKeys[acceptedKey(a)] {
			dropped = append(dropped, a)
		}
	}
	dropped = sortAccepted(dropped)
	seamKept := make(map[string]bool, len(capture.QualifyingSeamIDs))
	for _, id := range capture.QualifyingSeamIDs {
		seamKept[id] = true
	}
	var seamsDropped []string
	for _, id := range protected.QualifyingSeamIDs {
		if !seamKept[id] {
			seamsDropped = append(seamsDropped, id)
		}
	}
	slices.Sort(seamsDropped)

	type metricChange struct {
		name          string
		version       string
		before, after float64
	}
	var metrics []metricChange
	for name, now := range capture.Metrics {
		before, ok := protected.Metrics[name]
		if ok && before.Version == now.Version && before.Value != now.Value {
			metrics = append(metrics, metricChange{name, now.Version, before.Value, now.Value})
		}
	}
	slices.SortFunc(metrics, func(x, y metricChange) int { return cmp.Compare(x.name, y.name) })

	var sb strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&sb, format+"\n", args...) }
	line("Archfit baseline re-anchored by the approved workflow on %s at %s.", branch, mono(c.head))
	line("")
	line("This pull request changes only %s. The Archfit App never commits a baseline to %s: "+
		"the baseline changes only when an architecture owner approves this pull request and it is merged.", mono(policy.BaselinePath), branch)
	line("")
	line("**Approving accepts no finding that the baseline on %s does not accept today.**", branch)
	line("")
	line("| Accepted findings | Count |")
	line("| --- | --- |")
	line("| Baseline on %s | %d |", branch, len(protected.Accepted))
	line("| Kept | %d |", kept)
	line("| Dropped | %d |", len(dropped))
	line("| Added | 0 |")
	line("| Qualifying seams dropped | %d |", len(seamsDropped))
	writeFindings(&sb, "Dropped", dropped)
	if len(seamsDropped) > 0 {
		fmt.Fprintf(&sb, "\n### Seams no longer qualifying (%d)\n\n", len(seamsDropped))
		for _, id := range seamsDropped {
			fmt.Fprintf(&sb, "- %s\n", mono(id))
		}
	}
	if len(metrics) > 0 {
		fmt.Fprintf(&sb, "\n### Metric values that change (%d)\n\n", len(metrics))
		line("Approving resets each value below to the current one, so a worse value is accepted with the re-anchor.")
		line("")
		line("| Metric | Version | Before | After |")
		line("| --- | --- | --- | --- |")
		for _, m := range metrics {
			line("| %s | %s | %s | %s |", mono(m.name), mono(m.version), number(m.before), number(m.after))
		}
	}
	line("")
	line("The engine's full re-anchor report, with the findings that stay new, the drift and the metrics it worsened, "+
		"is in the job summary and the `archfit-baseline` artifact of run %s.", mono(strconv.FormatInt(b.RunID, 10)))
	writeCaptureAndReview(&sb, up, c, branch)
	return sb.String()
}

func number(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
