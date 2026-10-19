package ui

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	locationsShown           = 3  // per finding, before the rest folds
	findingsPerList          = 50 // per group, the rest counted
)

// pullFinding is one finding as the page shows it. Engine strings reach the template as data, never as markup.
type pullFinding struct {
	report.Finding
	Shown, Folded []report.Location
	Introduced    bool         // the engine compared against the base and says this pull request added it
	Reverse       *pullFinding // the other direction of a module cycle, shown in the same item; nil otherwise
}

// unknownRow is one fact the engine could not measure, under "To measure more".
type unknownRow struct {
	Dimension, Fact, Reason string
}

// findingList is one group of the page. Its heading counts findings, as the PR comment does, not items.
type findingList []pullFinding

// Findings counts the findings in the list: an item holding a cycle holds two.
func (l findingList) Findings() int {
	n := len(l)
	for _, f := range l {
		if f.Reverse != nil {
			n++
		}
	}
	return n
}

type pullBody struct {
	Pull        api.Pull
	Unavailable string
	CheckURL    string // the pull request's checks tab on GitHub

	Headline   string // the archfit/state title: feedback already states the effect on merge there
	Conclusion string // of archfit/state: neutral, success, action_required, …
	Required   bool   // the protected settings require archfit/gate
	RunID      int64

	NoBaseline bool // the engine loaded no baseline: "new" means not accepted, not "added by this pull request"
	Compared   bool // the engine compared against the base branch, so Introduced is meaningful

	Violations, Advisories, Accepted findingList // a cycle's two directions are one item
	Omitted                          int         // findings past findingsPerList, all groups together
	Dimensions                       []report.Dimension
	Unknowns                         []unknownRow // every dimension's unknown facts, in the dimension order
	Tasks                            string       // the exact agent_tasks array, indented
	TaskCount                        int
}

func (s *server) pullPage(w http.ResponseWriter, r *http.Request, sc scope) {
	number, ok := pullNumber(r)
	if !ok {
		s.fail(w, r, sc, api.ErrNotFound)
		return
	}
	body, err := s.buildPull(r.Context(), sc, number)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	s.render(w, r, http.StatusOK, "pull", s.newPage(r, sc, "Pull request #"+strconv.Itoa(number), body))
}

// pullNumber reads the {number} path value: a positive integer, or not a pull request.
func pullNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	return number, err == nil && number > 0
}

func (s *server) buildPull(ctx context.Context, sc scope, number int) (pullBody, error) {
	body, rep, err := s.readPull(ctx, sc, number)
	if err != nil || rep == nil {
		return body, err
	}
	fillFindings(&body, *rep)
	return body, nil
}

// readPull reads the pull request, the archfit/state check on its head, and the report of the run that check names.
// The pull request page and its graph both read through it, so they always show the same report. It never returns a
// report that does not match the check: the digest in the check's external_id must be the report's. The report is nil
// when Unavailable says why there is none.
func (s *server) readPull(ctx context.Context, sc scope, number int) (pullBody, *report.Report, error) {
	pull, err := s.gh.pulls.Pull(ctx, sc.repo.RepoRef, number)
	if err != nil {
		return pullBody{}, nil, err
	}
	body := pullBody{Pull: pull, CheckURL: pull.URL + "/checks"}
	// The gate reads the settings on the pull request's base, as the published checks do; a base without them is
	// advisory.
	if raw, _, err := s.gh.files.File(ctx, sc.repo.RepoRef, pull.BaseRef, settings.Path); err == nil {
		if set, err := settings.Parse(raw); err == nil {
			body.Required = set.Gate == settings.GateRequired
		}
	} else if !errors.Is(err, api.ErrNotFound) {
		return pullBody{}, nil, err
	}

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

	payload, err := s.gh.actions.Artifact(ctx, sc.repo.RepoRef, runID, reportArtifact, report.MaxBytes)
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

func fillFindings(body *pullBody, rep report.Report) {
	body.NoBaseline = rep.GateReference == nil || !rep.GateReference.BaselinePresent
	body.Compared = rep.Comparison.OriginStatus == originComparable
	add := func(list *findingList, f report.Finding) {
		if len(*list) == findingsPerList {
			body.Omitted++
			return
		}
		pf := pullFinding{Finding: f, Introduced: body.Compared && f.Origin == "introduced"}
		pf.Shown = f.Locations[:min(len(f.Locations), locationsShown)]
		pf.Folded = f.Locations[len(pf.Shown):]
		*list = append(*list, pf)
	}
	for _, f := range rep.Findings {
		switch {
		case f.Status == "fixed":
		case f.Status == "baseline" || f.Status == "waived":
			add(&body.Accepted, f)
		case f.Kind == "advisory" && (f.Status == "new" || f.Status == "expired_waiver"):
			add(&body.Advisories, f)
		default: // active gate findings, and any status this version does not know: never hidden
			add(&body.Violations, f)
		}
	}
	body.Violations, body.Advisories, body.Accepted = pairCycles(body.Violations), pairCycles(body.Advisories), pairCycles(body.Accepted)
	for _, key := range []string{"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift"} {
		if d, ok := rep.Dimensions[key]; ok {
			d.Name = key
			body.Dimensions = append(body.Dimensions, d)
			for _, u := range d.Unknown {
				body.Unknowns = append(body.Unknowns, unknownRow{Dimension: key, Fact: u.Fact, Reason: u.Reason})
			}
		}
	}
	if len(rep.AgentTasks) > 0 {
		raw, _ := json.MarshalIndent(rep.AgentTasks, "", "  ")
		body.Tasks, body.TaskCount = string(raw), len(rep.AgentTasks)
	}
}

// pairCycles folds the two directions of a module cycle (report.Finding.Reverses) into one item. Display only: each
// finding keeps its ID and its locations.
func pairCycles(fs findingList) findingList {
	out := make(findingList, 0, len(fs))
	used := make([]bool, len(fs))
	for i := range fs {
		if used[i] {
			continue
		}
		f := fs[i]
		for j := i + 1; j < len(fs); j++ {
			g := fs[j]
			if !used[j] && f.Reverses(g.Finding) {
				used[j] = true
				f.Reverse = &g
				break
			}
		}
		out = append(out, f)
	}
	return out
}
