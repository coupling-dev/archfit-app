package ui

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
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

// editBranch is the user's own edit branch, or a requested branch under archfit/.
func editBranch(login, requested string) (string, error) {
	if requested == "" {
		return editBranchPrefix + strings.ToLower(login), nil
	}
	if !proposal.ValidBranch(requested) {
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
	ManagedOff   string // why managed mode is not offered; "" when it is, or when that cannot be told
	// DataSharingURL is the Data sharing page, for an administrator when the missing consent is the reason.
	DataSharingURL string
	RunState       string
	RunURL         string
	ReloadURL      string
	Diagnostics    []policy.Diagnostic // the problems of Content, listed under the editor
	Unsaved        bool                // Content is the submitted form, not the text on the branch
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
	body.OfferManaged, body.ManagedOff, body.DataSharingURL = s.managedMode(r.Context(), sc)
	if since := query.Get("since"); proposal.ValidHeadSHA(since) {
		body.RunState = s.assistRun(r.Context(), sc.repo, branch, draft, since)
		body.RunURL = "https://github.com/" + sc.repo.FullName + "/actions/workflows/" + path.Base(assist.AssistWorkflowPath)
		body.ReloadURL = editorURL(sc, body.BranchQuery, nil)
	}
	s.render(w, r, http.StatusOK, "editor", s.newPage(r, sc, "Policy", body))
}

// assistRun words the customer-mode run requested when the draft head was since. WorkflowRun has no
// timestamps, so a finished run counts as this request's only once the draft head has moved.
func (s *server) assistRun(ctx context.Context, repo api.Repo, branch string, draft proposal.Draft, since string) string {
	run, err := s.gh.actions.LatestRun(ctx, repo.RepoRef, assist.AssistWorkflowPath, api.RunFilter{Branch: branch})
	switch {
	case errors.Is(err, api.ErrNotFound):
		return runPending
	case err != nil:
		return stateUnknown
	case run.Status != "completed":
		return runPending
	case run.Conclusion != conclusionSuccess:
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
	if f.headSHA != "" && !proposal.ValidHeadSHA(f.headSHA) {
		return f, invalid("The form is not valid. Reload the page.")
	}
	return f, nil
}

// formBody is the editor page of a submitted form. It asks GitHub nothing, so it works while GitHub does not.
func formBody(sc scope, f editForm) editorBody {
	return editorBody{
		Branch:      f.branch,
		BranchQuery: f.branchQuery,
		HeadSHA:     f.headSHA,
		Content:     f.content,
		ReloadURL:   editorURL(sc, f.branchQuery, nil),
		Unsaved:     true,
	}
}

// failEditor answers a refused save, ready or assist: the documented status, the user's text kept in the editor,
// and the text's problems when the policy check refused it.
func (s *server) failEditor(w http.ResponseWriter, r *http.Request, sc scope, f editForm, err error) {
	status, outcome, sentence := explain(err)
	logOf(r).outcome = outcome
	if outcome == "rate_limited" {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	body := formBody(sc, f)
	var problems *policyProblems
	if errors.As(err, &problems) {
		body.Diagnostics = problems.list
	}
	p := s.newPage(r, sc, "Policy", body)
	p.Error = sentence
	s.render(w, r, status, "editor", p)
}

const policyProblemsSentence = "The policy has problems, listed below the editor. Nothing was saved."

// errPolicyProblems is the refusal of a text with problems; policyProblems carries the list to the page.
var errPolicyProblems = invalid(policyProblemsSentence)

type policyProblems struct {
	list []policy.Diagnostic
}

func (e *policyProblems) Error() string { return errPolicyProblems.Error() }
func (e *policyProblems) Unwrap() error { return errPolicyProblems }

// checkPolicy is the one check of an edited policy: Check runs it, and so does every save, assist's included.
func checkPolicy(content string) error {
	if len(content) > maxPolicyBytes {
		return invalid("The policy is larger than 256 KB.")
	}
	if !utf8.ValidString(content) {
		return invalid("The policy is not valid UTF-8 text.")
	}
	if list := policy.Diagnose([]byte(content)); len(list) > 0 {
		return &policyProblems{list: list}
	}
	return nil
}

// check runs checkPolicy on the submitted text and saves nothing. Both outcomes answer 200 with the editor.
func (s *server) check(w http.ResponseWriter, r *http.Request, sc scope) {
	f, err := readEditForm(r, sc)
	if err == nil {
		err = checkPolicy(f.content)
	}
	var problems *policyProblems
	if err != nil && !errors.As(err, &problems) {
		s.failEditor(w, r, sc, f, err)
		return
	}
	body := formBody(sc, f)
	p := s.newPage(r, sc, "Policy", body)
	if problems != nil {
		body.Diagnostics = problems.list
		p.Body = body
		p.Error = "The check found problems, listed below the editor."
	} else {
		p.Notice = "The check found no problems."
	}
	s.render(w, r, http.StatusOK, "editor", p)
}

// matchLineEndings undoes the CRLF a browser puts into every submitted textarea, unless the file on the
// branch already uses CRLF. Without it a no-JavaScript save would rewrite every line of the policy.
func matchLineEndings(content string, current []byte) string {
	if bytes.Contains(current, []byte("\r\n")) {
		return content
	}
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// isOnBranch reports whether the submitted text is the policy the editor would load for the form's branch.
func (s *server) isOnBranch(ctx context.Context, sc scope, f editForm) (bool, error) {
	_, onBranch, err := s.current(ctx, sc.repo, f.branch)
	if err != nil {
		return false, err
	}
	return matchLineEndings(f.content, onBranch) == string(onBranch), nil
}

// saveDraft applies the save rules in their documented order. changed is false for the no-op.
func (s *server) saveDraft(ctx context.Context, sc scope, f editForm) (draft proposal.Draft, changed bool, err error) {
	if err := checkPolicy(f.content); err != nil {
		return draft, false, err
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
	var problems *policyProblems
	if errors.As(err, &problems) {
		// Owner decision: asking AI to fix the draft that is already on the branch must work. Nothing is
		// committed here, and assist checks its answer with the same Diagnose.
		var onBranch bool
		if onBranch, err = s.isOnBranch(r.Context(), sc, f); err == nil && !onBranch {
			err = problems
		}
	}
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
