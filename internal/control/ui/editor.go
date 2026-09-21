package ui

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	maxPolicyBytes   = 256 << 10
	editBranchPrefix = "archfit/edit-"

	editBody = "Policy edit made in the Archfit editor.\n\n" +
		"The Archfit check on this pull request is the authoritative validation. " +
		"Merging it under the repository's rules publishes the policy."

	runPending   = "pending"
	runFailed    = "failed"
	runCompleted = "completed"
)

var (
	branchPattern = regexp.MustCompile(`^archfit/[A-Za-z0-9._/-]+$`)
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// editBranch is the user's own edit branch, or a requested branch under archfit/.
func editBranch(login, requested string) (string, error) {
	if requested == "" {
		return editBranchPrefix + strings.ToLower(login), nil
	}
	if !branchPattern.MatchString(requested) || strings.Contains(requested, "..") ||
		strings.Contains(requested, "//") || strings.HasSuffix(requested, "/") {
		return "", invalid("The branch must be a branch under archfit/.")
	}
	return requested, nil
}

func isOpen(d proposal.Draft) bool {
	return d.State == proposal.DraftOpen || d.State == proposal.DraftInReview
}

type checkLine struct {
	Name, State, Reason string
}

type editorBody struct {
	Branch       string
	BranchQuery  string // "" for the user's own edit branch
	HeadSHA      string
	Content      string
	DraftState   string
	DraftURL     string
	CanReady     bool
	Checks       []checkLine
	OfferManaged bool
	RunState     string
	RunURL       string
	ReloadURL    string
}

// current returns the draft of branch and the policy text the editor shows for it: the file at the draft head
// while the draft is open, else the file on the default branch. A missing file is an empty text.
func (s *server) current(ctx context.Context, repo api.Repo, branch string) (proposal.Draft, []byte, error) {
	draft, err := s.proposals.Get(ctx, repo.RepoRef, branch)
	if errors.Is(err, api.ErrNotFound) {
		draft, err = proposal.Draft{State: proposal.DraftNone}, nil
	}
	if err != nil {
		return proposal.Draft{}, nil, err
	}
	ref := repo.DefaultBranch
	if isOpen(draft) {
		ref = draft.HeadSHA
	}
	text, _, err := s.gh.files.File(ctx, repo.RepoRef, ref, policy.PolicyPath)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return proposal.Draft{}, nil, err
	}
	return draft, text, nil
}

func (s *server) editor(w http.ResponseWriter, r *http.Request, sc scope) {
	query := r.URL.Query()
	branch, err := editBranch(sc.user.Login, query.Get("branch"))
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	draft, text, err := s.current(r.Context(), sc.repo, branch)
	if err != nil {
		s.fail(w, r, sc, err)
		return
	}
	body := editorBody{
		Branch:      branch,
		BranchQuery: query.Get("branch"),
		Content:     string(text),
		DraftState:  draftWords[draft.State],
		DraftURL:    draft.URL,
		CanReady:    draft.State == proposal.DraftOpen,
	}
	if isOpen(draft) {
		body.HeadSHA = draft.HeadSHA
		for _, name := range []string{checkState, checkGate} {
			state, reason := s.checkWord(r.Context(), sc.repo.RepoRef, draft.HeadSHA, name)
			body.Checks = append(body.Checks, checkLine{Name: name, State: state, Reason: reason})
		}
	}
	if set, err := s.settings(r.Context(), sc.repo); err == nil && set.DataMode == settings.DataManaged {
		body.OfferManaged = s.consents.Has(r.Context(), sc.repo.InstallationID, account.ConsentManagedLLM)
	}
	if since := query.Get("since"); shaPattern.MatchString(since) {
		body.RunState = s.assistRun(r.Context(), sc.repo, branch, draft, since)
		body.RunURL = "https://github.com/" + sc.repo.FullName + "/actions/workflows/" + path.Base(assist.AssistWorkflowPath)
		body.ReloadURL = editorURL(sc, body.BranchQuery, nil)
	}
	s.render(w, r, http.StatusOK, "editor", s.newPage(r, sc, "Policy", body))
}

// assistRun words the customer-mode run requested when the draft head was since. WorkflowRun has no
// timestamps, so a finished run counts as this request's only once the draft head has moved.
func (s *server) assistRun(ctx context.Context, repo api.Repo, branch string, draft proposal.Draft, since string) string {
	run, err := s.gh.actions.LatestRun(ctx, repo.RepoRef, assist.AssistWorkflowPath, branch)
	switch {
	case errors.Is(err, api.ErrNotFound):
		return runPending
	case err != nil:
		return stateUnknown
	case run.Status != "completed":
		return runPending
	case run.Conclusion != "success":
		return runFailed
	case draft.HeadSHA != since:
		return runCompleted
	}
	return runPending
}

func editorURL(sc scope, branchQuery string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	if branchQuery != "" {
		query.Set("branch", branchQuery)
	}
	u := repoPath(sc, "/policy")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// editForm is the part of the editor form that save, ready and assist share.
type editForm struct {
	branch      string
	branchQuery string
	headSHA     string
	content     string
}

func readEditForm(r *http.Request, sc scope) (editForm, error) {
	f := editForm{
		branchQuery: r.PostForm.Get("branch"),
		headSHA:     r.PostForm.Get("head_sha"),
		content:     r.PostForm.Get("content"),
	}
	if f.branchQuery == editBranchPrefix+strings.ToLower(sc.user.Login) {
		f.branchQuery = ""
	}
	var err error
	if f.branch, err = editBranch(sc.user.Login, f.branchQuery); err != nil {
		return f, err
	}
	if f.headSHA != "" && !shaPattern.MatchString(f.headSHA) {
		return f, invalid("The form is not valid. Reload the page.")
	}
	return f, nil
}

// failEditor answers a refused save, ready or assist: the documented status, the user's text kept in the editor.
// It asks GitHub nothing, so it works while GitHub does not.
func (s *server) failEditor(w http.ResponseWriter, r *http.Request, sc scope, f editForm, err error) {
	status, outcome, sentence := explain(err)
	logOf(r).outcome = outcome
	if outcome == "rate_limited" {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	body := editorBody{Branch: f.branch, BranchQuery: f.branchQuery, HeadSHA: f.headSHA, Content: f.content}
	body.ReloadURL = editorURL(sc, f.branchQuery, nil)
	p := s.newPage(r, sc, "Policy", body)
	p.Error = sentence
	s.render(w, r, status, "editor", p)
}

// matchLineEndings undoes the CRLF a browser puts into every submitted textarea, unless the file on the
// branch already uses CRLF. Without it a no-JavaScript save would rewrite every line of the policy.
func matchLineEndings(content string, current []byte) string {
	if bytes.Contains(current, []byte("\r\n")) {
		return content
	}
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// saveDraft applies the save rules in their documented order. changed is false for the no-op.
func (s *server) saveDraft(ctx context.Context, sc scope, f editForm) (draft proposal.Draft, changed bool, err error) {
	if len(f.content) > maxPolicyBytes {
		return draft, false, invalid("The policy is larger than 256 KB.")
	}
	if !utf8.ValidString(f.content) {
		return draft, false, invalid("The policy is not valid UTF-8 text.")
	}
	if _, err := policy.Waivers([]byte(f.content)); err != nil {
		return draft, false, invalid("The policy is not valid YAML. Nothing was saved.")
	}
	if _, err := policy.Modules([]byte(f.content)); err != nil {
		return draft, false, invalid("The policy is not valid YAML. Nothing was saved.")
	}
	// Waivers and Modules ignore unknown keys. The wording is fixed: Validate's error text is never shown.
	if err := policy.Validate([]byte(f.content)); err != nil {
		return draft, false, invalid("The text is not an Archfit policy: it has an unknown top-level key, " +
			"has no version key, or is not a mapping. Nothing was saved.")
	}
	draft, onBranch, err := s.current(ctx, sc.repo, f.branch)
	if err != nil {
		return draft, false, err
	}
	content := matchLineEndings(f.content, onBranch)
	if content == string(onBranch) {
		return draft, false, nil
	}
	if isOpen(draft) && draft.HeadSHA != f.headSHA {
		return draft, false, proposal.ErrStale
	}
	draft, err = s.proposals.Propose(ctx, proposal.Change{
		Repo:    sc.repo.RepoRef,
		Branch:  f.branch,
		Base:    sc.repo.DefaultBranch,
		Title:   "Archfit policy edit by @" + sc.user.Login,
		Body:    editBody,
		Message: "archfit: edit policy (@" + sc.user.Login + ")",
		Files:   []api.FileChange{{Path: policy.PolicyPath, Content: []byte(content)}},
	}, f.headSHA)
	return draft, err == nil, err
}

func (s *server) save(w http.ResponseWriter, r *http.Request, sc scope) {
	f, err := readEditForm(r, sc)
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	_, changed, err := s.saveDraft(r.Context(), sc, f)
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	notice := "saved"
	if !changed {
		notice = "unchanged"
	}
	seeOther(w, r, editorURL(sc, f.branchQuery, url.Values{"notice": {notice}}), nil)
}

// ready asks for review. ui never merges: publication is the merge on GitHub.
func (s *server) ready(w http.ResponseWriter, r *http.Request, sc scope) {
	f, err := readEditForm(r, sc)
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	draft, err := s.proposals.Get(r.Context(), sc.repo.RepoRef, f.branch)
	switch {
	case err != nil:
	case draft.State != proposal.DraftOpen:
		err = conflict("There is no draft pull request to mark ready. Reload the page.")
	case draft.HeadSHA != f.headSHA:
		err = proposal.ErrStale
	default:
		err = s.proposals.Ready(r.Context(), sc.repo.RepoRef, draft.Number)
	}
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	seeOther(w, r, editorURL(sc, f.branchQuery, url.Values{"notice": {"ready"}}), nil)
}

// propose is the explicit AI request: save what the user sees, then ask assist for a proposal bound to that head.
func (s *server) propose(w http.ResponseWriter, r *http.Request, sc scope) {
	f, err := readEditForm(r, sc)
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	mode := assist.Mode(r.PostForm.Get("mode"))
	if mode != assist.ModeManaged && mode != assist.ModeCustomer {
		s.failEditor(w, r, sc, f, invalid("Choose managed or customer mode."))
		return
	}
	draft, changed, err := s.saveDraft(r.Context(), sc, f)
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	if changed {
		f.headSHA = draft.HeadSHA
	}
	res, err := s.assist.Propose(r.Context(), mode, assist.Request{
		Repo:        sc.repo,
		Branch:      f.branch,
		HeadSHA:     f.headSHA,
		Instruction: r.PostForm.Get("instruction"),
		By:          sc.user.Login,
	})
	if err != nil {
		s.failEditor(w, r, sc, f, err)
		return
	}
	query := url.Values{"notice": {"proposed"}}
	if res.Mode == assist.ModeCustomer {
		query = url.Values{"since": {f.headSHA}}
	}
	seeOther(w, r, editorURL(sc, f.branchQuery, query), nil)
}
