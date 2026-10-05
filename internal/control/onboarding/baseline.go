package onboarding

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	baselineTitle   = "Update the Archfit baseline"
	baselineMessage = "Update the Archfit baseline captured by the approved workflow"

	// maxListedFindings caps each finding list of the PR body. GitHub refuses a body over 65 536 characters.
	// A listed line is at most 168 characters (a 128-character rule ID and a 32-hex fingerprint), so two full
	// lists stay under 51 000 and leave room for the rest of the body.
	maxListedFindings = 150
)

// Response codes of POST /v1/baselines that this module owns, beside the producer codes it shares with discovery.
const (
	codeStaleHead        = "stale_head"
	codeUnknownIdentity  = "unknown_engine_identity"
	codeBaselineTooLarge = "baseline_too_large"
	codeBaselineInvalid  = "baseline_invalid"
	codePolicyMissing    = "policy_missing"
	codePolicyMismatch   = "policy_mismatch"
	codeLabelsMismatch   = "labels_mismatch"
)

// protectedState is what the default-branch head holds at BaselinePath.
type protectedState int

const (
	protectedAbsent protectedState = iota
	protectedReadable
	protectedUnreadable // present, but not a readable archfit.baseline.v2 file
)

// baselineCheck is everything the PR body states, all read at one default-branch commit.
type baselineCheck struct {
	head      string
	capture   policy.Baseline
	state     protectedState
	current   policy.Baseline // set when state == protectedReadable
	unchanged bool            // the upload is byte-identical to the protected baseline
}

func (s *service) BaselineHandler() http.Handler {
	return http.HandlerFunc(s.serveBaseline)
}

// serveBaseline turns a baseline captured by the approved workflow into an owner-reviewed pull request.
// It never writes to the default branch: the only write is a commit on baselineBranch.
func (s *service) serveBaseline(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	up, ok := s.receive(w, req, envelope.KindBaseline)
	if !ok {
		return
	}

	b := up.Binding
	log := uploadLog(up)
	c, status, code := s.checkBaseline(ctx, up)
	if code != "" {
		log.WarnContext(ctx, "baseline upload refused", "outcome", code)
		writeError(w, status, code)
		return
	}
	if c.unchanged {
		log.InfoContext(ctx, "baseline upload unchanged", "outcome", "unchanged")
		writeJSON(w, http.StatusOK, map[string]bool{"unchanged": true})
		return
	}

	added, removed := diffAccepted(c)
	body := baselineBody(up, c, added, removed)
	files := []api.FileChange{{Path: policy.BaselinePath, Content: up.Payload}}
	draft, err := s.proposals.Propose(ctx, changeFor(b.Repo, baselineBranch, baselineTitle, body, baselineMessage, files), "")
	if err == nil {
		// An open pull request keeps its body on Propose; a re-upload must describe the bytes it committed.
		err = s.proposals.SetBody(ctx, b.Repo.RepoRef, draft.Number, body)
	}
	if err != nil {
		log.ErrorContext(ctx, "baseline upload not proposed", "outcome", "propose_failed", "error_class", errClass(err))
		writeUnavailable(w, err)
		return
	}
	log.InfoContext(ctx, "baseline upload proposed", "outcome", "proposed", "pull_request", draft.Number,
		"head_sha", c.head, "added", len(added), "removed", len(removed))
	writeJSON(w, http.StatusOK, map[string]any{"pull_request": draft.Number, "url": draft.URL})
}

// checkBaseline returns a non-empty code when the upload must not become a pull request. Every protected input is
// read at the default-branch head the run captured, never at the run's own tree, and never at a branch name that
// may move between two reads.
func (s *service) checkBaseline(ctx context.Context, up gate.Upload) (c baselineCheck, status int, code string) {
	refuse := func(status int, code string) (baselineCheck, int, string) { return baselineCheck{}, status, code }

	head, status, code := s.checkProducer(ctx, up, true)
	if code != "" {
		return refuse(status, code)
	}
	entry, approved := manifest.Lookup(up.Identity)
	if !approved {
		return refuse(http.StatusForbidden, codeUnknownIdentity)
	}
	if len(up.Payload) > policy.MaxBaselineBytes {
		return refuse(http.StatusRequestEntityTooLarge, codeBaselineTooLarge)
	}
	capture, err := policy.ReadBaseline(up.Payload) // the error text is dropped: never echoed, never logged
	if err != nil {
		return refuse(http.StatusBadRequest, codeBaselineInvalid)
	}
	// Same predicate as the gate's identity check: an approved row and that row's measurement profile.
	if capture.ProfileVersion != entry.ProfileVersion {
		return refuse(http.StatusForbidden, codeUnknownIdentity)
	}

	r := up.Binding.Repo
	policyText, found, err := s.fileAt(ctx, r, head, policy.PolicyPath)
	switch {
	case err != nil:
		return refuse(unavailable(err))
	case !found:
		return refuse(http.StatusForbidden, codePolicyMissing)
	case policy.Digest(policyText) != capture.ConfigHash:
		return refuse(http.StatusForbidden, codePolicyMismatch)
	}
	labels, found, err := s.fileAt(ctx, r, head, policy.LabelsPath)
	if err != nil {
		return refuse(unavailable(err))
	}
	protectedLabels := ""
	if found {
		protectedLabels = policy.Digest(labels)
	}
	if up.LabelsDigest != protectedLabels {
		return refuse(http.StatusForbidden, codeLabelsMismatch)
	}

	c = baselineCheck{head: head, capture: capture}
	current, found, err := s.fileAt(ctx, r, head, policy.BaselinePath)
	switch {
	case err != nil:
		return refuse(unavailable(err))
	case !found:
		c.state = protectedAbsent
	case bytes.Equal(current, up.Payload):
		c.unchanged = true
	default:
		if c.current, err = policy.ReadBaseline(current); err == nil {
			c.state = protectedReadable
		} else {
			c.state = protectedUnreadable
		}
	}
	return c, 0, ""
}

// diffAccepted compares the accepted fingerprints as sets. Without a readable protected baseline every captured
// finding counts as added: overstating what approval accepts is the safe direction.
func diffAccepted(c baselineCheck) (added, removed []policy.Accepted) {
	inCurrent := make(map[string]bool, len(c.current.Accepted))
	for _, a := range c.current.Accepted {
		inCurrent[a.Fingerprint] = true
	}
	inCapture := make(map[string]bool, len(c.capture.Accepted))
	for _, a := range c.capture.Accepted {
		inCapture[a.Fingerprint] = true
		if !inCurrent[a.Fingerprint] {
			added = append(added, a)
		}
	}
	for _, a := range c.current.Accepted {
		if !inCapture[a.Fingerprint] {
			removed = append(removed, a)
		}
	}
	return sortAccepted(added), sortAccepted(removed)
}

func sortAccepted(list []policy.Accepted) []policy.Accepted {
	slices.SortFunc(list, func(a, b policy.Accepted) int {
		return cmp.Or(strings.Compare(a.RuleID, b.RuleID), strings.Compare(a.Fingerprint, b.Fingerprint))
	})
	return list
}

// baselineBody is deterministic, so a retried upload rewrites the same text. It carries IDs, digests and counts
// only: fingerprints and rule IDs are token classes that wire/policy checked, and nothing else of the file appears.
// The default branch is the one GitHub-supplied name in it; mono sanitizes it like every other code span.
func baselineBody(up gate.Upload, c baselineCheck, added, removed []policy.Accepted) string {
	b := up.Binding
	branch := mono(b.Repo.DefaultBranch)
	// A "|" ends a table cell even inside a code span; GitHub's table syntax takes "\|" as a literal pipe there.
	branchCell := strings.ReplaceAll(branch, "|", `\|`)
	var sb strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&sb, format+"\n", args...) }

	line("Archfit baseline captured by the approved workflow on %s at %s.", branch, mono(c.head))
	line("")
	line("This pull request changes only %s. The Archfit App never commits a baseline to %s: "+
		"the baseline changes only when an architecture owner approves this pull request and it is merged.", mono(policy.BaselinePath), branch)
	line("")
	switch len(added) {
	case 0:
		line("**Approving accepts no finding that the baseline on %s does not accept today.**", branch)
	case 1:
		line("**Approving accepts 1 finding that the baseline on %s does not accept today.**", branch)
	default:
		line("**Approving accepts %d findings that the baseline on %s does not accept today.**", len(added), branch)
	}
	line("")

	before, gone := strconv.Itoa(len(c.current.Accepted)), strconv.Itoa(len(removed))
	switch c.state {
	case protectedAbsent:
		before = "none"
		line("There is no baseline on %s yet: this is its first capture.", branch)
		line("")
	case protectedUnreadable:
		before, gone = "unreadable", "unknown"
		line("The %s on %s is not a readable `%s` file, so every accepted finding of this capture counts as added "+
			"and removals are unknown.", mono(policy.BaselinePath), branch, policy.BaselineSchemaV2)
		line("")
	}
	line("| Accepted findings | Count |")
	line("| --- | --- |")
	line("| Baseline on %s | %s |", branchCell, before)
	line("| This pull request | %d |", len(c.capture.Accepted))
	line("| Added | %d |", len(added))
	line("| Removed | %s |", gone)
	writeFindings(&sb, "Added", added)
	writeFindings(&sb, "Removed", removed)

	line("")
	line("### Capture")
	line("")
	line("- Run %s, attempt %d.", mono(strconv.FormatInt(b.RunID, 10)), b.RunAttempt)
	line("- Engine %s on %s, image %s, measurement profile %s.",
		mono(up.Identity.EngineVersion), mono(up.Identity.Platform), mono(up.Identity.ImageDigest), mono(c.capture.ProfileVersion))
	line("- Policy digest %s, equal to %s on %s.", mono(c.capture.ConfigHash), mono(policy.PolicyPath), branch)
	if up.LabelsDigest == "" {
		line("- No labels file.")
	} else {
		line("- Labels digest %s, equal to %s on %s.", mono(up.LabelsDigest), mono(policy.LabelsPath), branch)
	}
	line("- Payload digest %s.", mono(up.Digest))
	line("")
	line("The App checked the run, the workflow, the default-branch head, the engine identity and the policy and labels " +
		"digests. The capture itself is customer-attested: the App did not run the engine.")
	line("")
	line("### Review")
	line("")
	line("An architecture owner named in %s on %s must approve the exact head commit of this pull request. "+
		"Mark it ready for review, approve it, then merge it. The Archfit gate on this pull request checks that approval; "+
		"a new commit on this branch needs a new approval.", mono(settings.Path), branch)
	return sb.String()
}

func writeFindings(sb *strings.Builder, title string, list []policy.Accepted) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(sb, "\n### %s (%d)\n\n", title, len(list))
	for i, a := range list {
		if i == maxListedFindings {
			fmt.Fprintf(sb, "- and %d more\n", len(list)-maxListedFindings)
			break
		}
		fmt.Fprintf(sb, "- %s %s\n", mono(a.RuleID), mono(a.Fingerprint))
	}
}

// mono renders a value as a Markdown code span. It sanitizes first, so a value outside a token class, such as a
// default branch named with a backtick or "<!--", can neither leave the span nor open an HTML comment.
func mono(s string) string { return "`" + sanitize(s) + "`" }

// Strings the App does not control are sanitized before they enter a PR body, by the rule gate/feedback applies to
// engine-authored text: remove the HTML comment markers, backticks and line breaks, then escape the angle brackets,
// so that no reassembled "<!--" survives and no such string can forge the managed marker.
var (
	markdownStripper = strings.NewReplacer("<!--", "", "-->", "", "`", "", "\r", "", "\n", "")
	markdownEscaper  = strings.NewReplacer("<", "&lt;", ">", "&gt;")
)

func sanitize(s string) string {
	return markdownEscaper.Replace(markdownStripper.Replace(s))
}
