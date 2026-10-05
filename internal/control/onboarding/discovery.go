package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	maxDraftBytes = 1 << 20
	maxLineBytes  = 500

	policyTitle   = "Add the Archfit policy proposed by discovery"
	policyMessage = "Add the Archfit policy proposed by discovery"
)

// Response codes of POST /v1/discoveries that this module owns.
const (
	codeNotDefaultBranch    = "not_default_branch"
	codeSettingsMissing     = "settings_missing"
	codeWorkflowNotApproved = "workflow_not_approved"
	codeTooLarge            = "discovery_too_large"
	codeInvalid             = "discovery_invalid"
	codeRateLimited         = "rate_limited"
	codeInternal            = "internal"
)

func (s *service) DiscoveryHandler() http.Handler {
	return http.HandlerFunc(s.serveDiscovery)
}

func (s *service) serveDiscovery(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	up, ok := s.receive(w, req, envelope.KindDiscovery)
	if !ok {
		return
	}

	b := up.Binding
	log := uploadLog(up)
	status, code := s.checkUpload(ctx, up)
	if code != "" {
		log.WarnContext(ctx, "discovery upload refused", "outcome", code)
		writeError(w, status, code)
		return
	}

	body := fmt.Sprintf("Engine discovery draft from run `%d`, payload digest `%s`.\n\n"+
		"This is a proposal to review, not measurement evidence. Edit `%s` on this branch as needed, then merge.\n",
		b.RunID, up.Digest, policy.PolicyPath)
	files := []api.FileChange{{Path: policy.PolicyPath, Content: up.Payload}}
	draft, err := s.proposals.Propose(ctx, changeFor(b.Repo, policyBranch, policyTitle, body, policyMessage, files), "")
	if err != nil {
		log.ErrorContext(ctx, "discovery upload not proposed", "outcome", "propose_failed", "error_class", errClass(err))
		writeUnavailable(w, err)
		return
	}
	log.InfoContext(ctx, "discovery upload proposed", "outcome", "proposed", "pull_request", draft.Number)
	writeJSON(w, http.StatusOK, map[string]any{"pull_request": draft.Number, "url": draft.URL})
}

// receive runs the gate's provenance checks for one upload kind. On any failure it has answered the request.
func (s *service) receive(w http.ResponseWriter, req *http.Request, kind envelope.Kind) (gate.Upload, bool) {
	up, err := s.receiver.Receive(req.Context(), req, kind)
	var rejection *gate.Rejection
	switch {
	case errors.As(err, &rejection):
		writeError(w, rejection.Status, rejection.Code)
		return gate.Upload{}, false
	case err != nil:
		slog.Default().ErrorContext(req.Context(), "upload not received", "kind", string(kind), "error_class", errClass(err))
		writeUnavailable(w, err)
		return gate.Upload{}, false
	}
	return up, true
}

// uploadLog carries the identifiers every upload log line names; never payload bytes.
func uploadLog(up gate.Upload) *slog.Logger {
	b := up.Binding
	return slog.Default().With("installation_id", b.Repo.InstallationID, "repository_id", b.Repo.ID, "run_id", b.RunID, "digest", up.Digest)
}

// checkUpload returns a non-empty code when the upload must not become a pull request.
func (s *service) checkUpload(ctx context.Context, up gate.Upload) (status int, code string) {
	if _, status, code = s.checkProducer(ctx, up, false); code != "" {
		return status, code
	}
	switch code = checkDraft(up.Payload); code {
	case "":
		return 0, ""
	case codeTooLarge:
		return http.StatusRequestEntityTooLarge, code
	}
	return http.StatusBadRequest, code
}

// checkProducer is the producer check both upload routes share: no problem, a run on the default branch, and the
// workflow that the default-branch settings approve. The head tree of the run is never trusted. With pinHead the
// run must also be at the current default-branch head, and ref is that commit, so that every later read of the
// caller sees the same tree; otherwise ref is the default branch name.
func (s *service) checkProducer(ctx context.Context, up gate.Upload, pinHead bool) (ref string, status int, code string) {
	b := up.Binding
	if len(up.Problems) > 0 {
		return "", http.StatusBadRequest, string(up.Problems[0])
	}
	if b.BaseRef != b.Repo.DefaultBranch {
		return "", http.StatusForbidden, codeNotDefaultBranch
	}
	ref = b.Repo.DefaultBranch
	if pinHead {
		head, err := s.gh.repos.BranchHead(ctx, b.Repo.RepoRef, b.Repo.DefaultBranch)
		if err != nil {
			status, code = unavailable(err)
			return "", status, code
		}
		if head != b.HeadSHA {
			return "", http.StatusConflict, codeStaleHead
		}
		ref = head
	}
	raw, found, err := s.fileAt(ctx, b.Repo, ref, settings.Path)
	if err != nil {
		status, code = unavailable(err)
		return "", status, code
	}
	set, err := settings.Parse(raw)
	if !found || err != nil {
		return "", http.StatusForbidden, codeSettingsMissing
	}
	if b.WorkflowPath != set.Workflow {
		return "", http.StatusForbidden, codeWorkflowNotApproved
	}
	return ref, 0, ""
}

// checkDraft returns "" for a draft that may become a pull request. The size and line checks are shape
// checks, not a classifier: policy.Validate is what keeps a foreign document (an engine report, prose, JSON)
// out. Its error text is dropped, never echoed or logged.
func checkDraft(draft []byte) string {
	switch {
	case len(draft) > maxDraftBytes:
		return codeTooLarge
	case len(draft) == 0:
		return codeInvalid
	}
	for line := range bytes.Lines(draft) {
		if !cleanLine(strings.TrimSuffix(string(line), "\n")) {
			return codeInvalid
		}
	}
	if policy.Validate(draft) != nil {
		return codeInvalid
	}
	if waivers, err := policy.Waivers(draft); err != nil || len(waivers) > 0 {
		return codeInvalid // discovery never proposes exceptions
	}
	return ""
}

// cleanLine accepts one line of valid UTF-8 without control characters, at most maxLineBytes long.
func cleanLine(line string) bool {
	return len(line) <= maxLineBytes && utf8.ValidString(line) && !strings.ContainsFunc(line, unicode.IsControl)
}

func unavailable(err error) (int, string) {
	if errors.Is(err, api.ErrRateLimited) {
		return http.StatusServiceUnavailable, codeRateLimited
	}
	return http.StatusInternalServerError, codeInternal
}

func writeUnavailable(w http.ResponseWriter, err error) {
	status, code := unavailable(err)
	writeError(w, status, code)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("discovery response not written", "error_class", errClass(err))
	}
}
