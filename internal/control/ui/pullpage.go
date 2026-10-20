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
	"sync"

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
	RunID      int64
}

// pullNumber reads the {number} path value: a positive integer, or not a pull request.
func pullNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	return number, err == nil && number > 0
}

// readPull reads the pull request, the archfit/state check on its head, and the report of the run that check names.
// The pull request page and its graph both read through it, so they always show the same report. It never returns a
// report that does not match the check: the digest in the check's external_id must be the report's. The report is nil
// when Unavailable says why there is none.
func (s *server) readPull(ctx context.Context, sc scope, number int) (pullBody, *report.Report, error) {
	pull, err := s.gh.pulls.PullSummary(ctx, sc.repo.RepoRef, number)
	if err != nil {
		return pullBody{}, nil, err
	}
	// The base's settings and the head's report do not depend on each other: they are read side by side. A failure
	// of either fails the page as it did when they were read in turn, the settings' first.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		required    bool
		settingsErr error
		wg          sync.WaitGroup
	)
	wg.Go(func() {
		// The gate reads the settings on the pull request's base, as the published checks do; a base without them is
		// advisory.
		raw, _, err := s.gh.files.File(ctx, sc.repo.RepoRef, pull.BaseRef, settings.Path)
		switch {
		case err == nil:
			if set, err := settings.Parse(raw); err == nil {
				required = set.Gate == settings.GateRequired
			}
		case !errors.Is(err, api.ErrNotFound):
			settingsErr = err
			cancel()
		}
	})
	body, rep, err := s.pullReport(ctx, sc, pull)
	wg.Wait()
	if settingsErr != nil {
		return pullBody{}, nil, settingsErr
	}
	if err != nil {
		return pullBody{}, nil, err
	}
	body.Required = required
	return body, rep, nil
}

// pullReport reads the archfit/state check on the pull request's head and the report of the run it names.
func (s *server) pullReport(ctx context.Context, sc scope, pull api.Pull) (pullBody, *report.Report, error) {
	body := pullBody{Pull: pull, CheckURL: pull.URL + "/checks"}
	runs, err := s.gh.checks.CheckRuns(ctx, sc.repo.RepoRef, pull.HeadSHA, checkState)
	if err != nil {
		return pullBody{}, nil, err
	}
	if len(runs) == 0 {
		body.Unavailable = noResult
		return body, nil, nil
	}
	run := slices.MaxFunc(runs, func(a, b api.CheckRun) int { return cmp.Compare(a.ID, b.ID) })
	body.Headline, body.Conclusion = run.Title, run.Conclusion
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

	payload, err := s.reportPayload(ctx, sc.repo.RepoRef, runID)
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
	return body, &rep, nil
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
