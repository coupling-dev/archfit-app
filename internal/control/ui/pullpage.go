package ui

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	// external_id of a feedback check run: "v1:<run_id>:<attempt>:<report digest>", the digest "sha256:<hex>".
	externalIDPrefix = "v1:"

	noResult       = "No Archfit result for this commit yet. The workflow reports a few minutes after a push."
	noUpload       = "This result came without a report, so there is nothing to show here. The check on GitHub names the reason and the next step."
	actionRequired = "This result asks for action before its findings mean anything. The check on GitHub names the reason and the next step."
	reportExpired  = "GitHub no longer keeps the report of this run. The check on GitHub stays. Re-run the workflow to see the details here."
	reportMismatch = "The report of this run does not match its check, so it is not shown. Re-run the workflow."

	conclusionActionRequired = "action_required"
	findingsPerList          = report.ShownPerGroup // per group, the rest counted
)

// pullBody is what readPull found: the pull request, its archfit/state check, and why there is no report when there is
// none. The pull request page and its graph both start from it.
type pullBody struct {
	Pull        api.Pull
	Unavailable string
	CheckURL    string // the pull request's checks tab on GitHub

	Headline   string // the archfit/state title: feedback already states the effect on merge there
	Conclusion string // of archfit/state: neutral, success, action_required, …
	Required   bool   // the protected settings require archfit/gate
	RunID      int64  // report run selected for artifact lookup, not diagnostic context
	Check      *checkContextJSON
}

// pullNumber reads the {number} path value: a positive integer, or not a pull request.
func pullNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	return number, err == nil && number > 0
}

// readPull reads the pull request, the archfit/state check on its head, and the report of the run that check names.
// The pull request page and its graph both read through it, so they always show the same report. It never returns a
// report that does not match the check: the digest in the check's external_id must be the report's. The report is nil
// when Unavailable says why there is none. Each of also runs side by side with those reads, for a read that needs only
// the pull request's head and base.
//
// The last read left the pull request's head and base as hints. Settings, checks and policy texts can start beside
// the pull request read with those hints, and count only when the fresh pull request names the same head and base.
// Artifact reads always wait for the fresh check so a
// withdrawn result cannot use a previously admitted run as prefetch authorization.
func (s *server) readPull(ctx context.Context, sc scope, number int, also ...func(head, base string)) (pullBody, *report.Report, error) {
	key := hintKey{sc.repo.InstallationID, sc.repo.FullName, number}
	h, hinted := s.hints.get(key)
	repo := sc.repo.RepoRef
	gateRequired := func(base string) (bool, error) { return s.gateRequired(ctx, repo, base) }
	checkRuns := func(head string) ([]api.CheckRun, error) { return s.gh.checks.CheckRuns(ctx, repo, head, checkState) }
	var (
		pull api.Pull
		err  error
		gate guess[string, bool]
		runs guess[string, []api.CheckRun]
	)
	reads := []func(){func() { pull, err = s.gh.pulls.PullSummary(ctx, repo, number) }}
	if hinted {
		reads = append(reads, gate.ahead(h.base, gateRequired), runs.ahead(h.head, checkRuns))
		// An accepted hint can have been withdrawn since the last request. Artifact reads
		// must wait for the fresh check, even when the report bytes are cached.
		for _, fn := range also {
			reads = append(reads, func() { fn(h.head, h.base) })
		}
	}
	together(reads...)
	if errors.Is(err, api.ErrNotFound) {
		s.hints.drop(key)
	}
	if err != nil {
		return pullBody{}, nil, err
	}

	// The base's settings and the head's report do not depend on each other: they are read side by side. A failure
	// of either fails the page as it did when they were read in turn, the settings' first.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		required bool
		gateErr  error
		body     pullBody
		rep      *report.Report
	)
	reads = []func(){func() {
		if required, gateErr = gate.at(pull.BaseRef, gateRequired); gateErr != nil {
			cancel()
		}
	}, func() { body, rep, err = s.pullReport(ctx, sc, pull, &runs) }}
	if !hinted || pull.HeadSHA != h.head || pull.BaseRef != h.base {
		for _, fn := range also {
			reads = append(reads, func() { fn(pull.HeadSHA, pull.BaseRef) })
		}
	}
	together(reads...)
	if gateErr != nil {
		return pullBody{}, nil, gateErr
	}
	if err != nil {
		return pullBody{}, nil, err
	}
	s.hints.put(key, hint{base: pull.BaseRef, head: pull.HeadSHA})
	body.Required = required
	return body, rep, nil
}

// gateRequired reads the settings on a pull request's base, as the published checks do: the gate is required only when
// they say so, and a base without them is advisory.
func (s *server) gateRequired(ctx context.Context, repo api.RepoRef, base string) (bool, error) {
	raw, _, err := s.gh.files.File(ctx, repo, base, settings.Path)
	if errors.Is(err, api.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	set, err := settings.Parse(raw)
	return err == nil && set.Gate == settings.GateRequired, nil
}

// pullReport reads the head's archfit/state check and the report it names. Only the check read is hinted;
// the artifact listing starts after a fresh check permits it.
func (s *server) pullReport(ctx context.Context, sc scope, pull api.Pull, runs *guess[string, []api.CheckRun]) (pullBody, *report.Report, error) {
	repo := sc.repo.RepoRef
	body := pullBody{Pull: pull, CheckURL: pull.URL + "/checks"}
	checks, err := runs.at(pull.HeadSHA, func(head string) ([]api.CheckRun, error) {
		return s.gh.checks.CheckRuns(ctx, repo, head, checkState)
	})
	if err != nil {
		return pullBody{}, nil, err
	}
	if len(checks) == 0 {
		body.Unavailable = noResult
		return body, nil, nil
	}
	run := slices.MaxFunc(checks, func(a, b api.CheckRun) int { return cmp.Compare(a.ID, b.ID) })
	body.Headline, body.Conclusion = run.Title, run.Conclusion
	body.Check = actionContext(run)
	// action_required withdraws a result (stale inputs, an expired waiver, a suspension, a missing approval) or says
	// the evidence is unusable. The sweep keeps the old run in external_id when it withdraws, so the old report would
	// still match the digest: findings are never shown under a result that asks for action.
	if run.Conclusion == conclusionActionRequired {
		body.Unavailable = actionRequired
		return body, nil, nil
	}
	runID, digest, ok := parseExternalID(run.ExternalID)
	if !ok || runID == 0 {
		body.Unavailable = noUpload
		return body, nil, nil
	}
	body.RunID = runID

	payload, err := s.runReport(ctx, repo, runID)
	if errors.Is(err, api.ErrNotFound) {
		body.Unavailable = reportExpired
		return body, nil, nil
	}
	if err != nil {
		return pullBody{}, nil, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != digest {
		body.Unavailable = reportMismatch
		return body, nil, nil
	}
	releases := report.EngineVersions()
	rep, err := report.Decode(payload, releases[len(releases)-1])
	if err != nil {
		body.Unavailable = reportUnavailable
		return body, nil, nil
	}
	body.Check.Schema = rep.SchemaVersion
	return body, &rep, nil
}

// runReport lists the artifact fresh; the report cache can replace only the download.
func (s *server) runReport(ctx context.Context, repo api.RepoRef, runID int64) ([]byte, error) {
	id, err := s.artifactIDs(ctx, repo)(runID)
	if err != nil {
		return nil, err
	}
	return s.reportByID(ctx, repo, id)
}

// parseExternalID reads "v1:<run_id>:<attempt>:<digest>" and returns the run and the bare hex digest.
func parseExternalID(id string) (runID int64, digest string, ok bool) {
	rest, found := strings.CutPrefix(id, externalIDPrefix)
	if !found {
		return 0, "", false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return 0, "", false
	}
	runID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return runID, strings.TrimPrefix(parts[2], "sha256:"), true
}
