package ui

import (
	"bytes"
	"context"
	"errors"
	"net/http"
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

type checkJSON struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Tone   string `json:"tone"`
	Reason string `json:"reason"`
}

type diagnosticJSON struct {
	Line    int    `json:"line,omitempty"`   // 1-based; absent when the problem has no single place
	Column  int    `json:"column,omitempty"` // 1-based; absent when unknown
	Message string `json:"message"`
}

func diagnosticsJSON(list []policy.Diagnostic) []diagnosticJSON {
	out := make([]diagnosticJSON, 0, len(list))
	for _, d := range list {
		out = append(out, diagnosticJSON{Line: d.Line, Column: d.Column, Message: d.Message})
	}
	return out
}

// managedJSON is whether the editor offers managed AI and, when it does not, why in the page's words.
type managedJSON struct {
	Offer bool   `json:"offer"`
	Off   string `json:"off,omitempty"` // "" when offered, or when the reason cannot be told
	// DataSharing is the Data sharing page, for an administrator when the missing consent is the reason.
	DataSharing string `json:"data_sharing,omitempty"`
}

// runJSON is the customer-mode run requested when the draft head was since.
type runJSON struct {
	State string `json:"state"` // pending, failed, completed or unknown
	URL   string `json:"url"`
}

// policyJSON is the policy page: the text the editor shows for the branch and what the user may do with it.
type policyJSON struct {
	Branch string `json:"branch"`
	// HeadSHA is the open draft's head, "" without one. Every write sends it back, so a draft that moved on GitHub
	// since this answer refuses the write (409) instead of overwriting the move.
	HeadSHA     string           `json:"head_sha"`
	Content     string           `json:"content"`
	Draft       string           `json:"draft"`       // none, draft, awaiting_review, published or closed
	DraftState  string           `json:"draft_state"` // the draft's state in the page's words, "" for none
	DraftURL    string           `json:"draft_url"`
	Checks      []checkJSON      `json:"checks"`      // the Archfit checks on the draft head, while the draft is open
	Diagnostics []diagnosticJSON `json:"diagnostics"` // the problems of Content
	CanWrite    bool             `json:"can_write"`
	CanReady    bool             `json:"can_ready"`
	Managed     managedJSON      `json:"managed"`
	Run         *runJSON         `json:"run,omitempty"`
	Since       string           `json:"since,omitempty"` // the head the run's proposal follows
	Notice      string           `json:"notice,omitempty"`
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

// apiPolicy is the policy page of ?branch= (the user's own edit branch without one) and, with ?since=, the
// customer-mode run requested at that head.
func (s *server) apiPolicy(r *http.Request, sc scope) (any, error) {
	query := r.URL.Query()
	branch, err := editBranch(sc.user.Login, query.Get("branch"))
	if err != nil {
		return nil, err
	}
	return s.policyPage(r.Context(), sc, branch, query.Get("since"))
}

func (s *server) policyPage(ctx context.Context, sc scope, branch, since string) (policyJSON, error) {
	draft, text, err := s.current(ctx, sc.repo, branch)
	if err != nil {
		return policyJSON{}, err
	}
	body := policyJSON{
		Branch:      branch,
		Content:     string(text),
		Draft:       string(draft.State),
		DraftState:  draftWords[draft.State],
		DraftURL:    draft.URL,
		Checks:      []checkJSON{},
		Diagnostics: []diagnosticJSON{},
	}
	var problems *policyProblems
	if errors.As(checkPolicy(body.Content), &problems) {
		body.Diagnostics = diagnosticsJSON(problems.list)
	}
	if isOpen(draft) {
		body.HeadSHA = draft.HeadSHA
		for _, name := range []string{checkState, checkGate} {
			state, reason := s.checkWord(ctx, sc.repo.RepoRef, draft.HeadSHA, name)
			body.Checks = append(body.Checks, checkJSON{Name: name, State: state, Tone: toneOf(state), Reason: reason})
		}
	}
	// The route needs read; the buttons that write need write. The server checks write again on every write.
	if _, err := s.sessions.Authorize(ctx, sc.user, sc.repo.FullName, permWrite); err == nil {
		body.CanWrite = true
		body.CanReady = draft.State == proposal.DraftOpen
		body.Managed.Offer, body.Managed.Off, body.Managed.DataSharing = s.managedMode(ctx, sc)
	}
	if proposal.ValidHeadSHA(since) {
		body.Since = since
		body.Run = &runJSON{
			State: s.assistRun(ctx, sc.repo, branch, draft, since),
			URL:   "https://github.com/" + sc.repo.FullName + "/actions/workflows/" + path.Base(assist.AssistWorkflowPath),
		}
	}
	return body, nil
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

// editJSON is the part of a write that save, ready and assist share. Branch is the branch the page shows; it is
// checked again here, never trusted.
type editJSON struct {
	Branch  string `json:"branch"`
	HeadSHA string `json:"head_sha"`
	Content string `json:"content"`
}

// edit is an editJSON whose branch and head have been checked.
type edit struct {
	branch  string
	headSHA string
	content string
}

func readEdit(sc scope, in editJSON) (edit, error) {
	requested := in.Branch
	if requested == editBranchPrefix+strings.ToLower(sc.user.Login) {
		requested = ""
	}
	branch, err := editBranch(sc.user.Login, requested)
	if err != nil {
		return edit{}, err
	}
	if in.HeadSHA != "" && !proposal.ValidHeadSHA(in.HeadSHA) {
		return edit{}, invalid("The request is not valid. Reload the page.")
	}
	return edit{branch: branch, headSHA: in.HeadSHA, content: in.Content}, nil
}

const policyProblemsSentence = "The policy has problems, listed above the editor. Nothing was saved."

// errPolicyProblems is the refusal of a text with problems; policyProblems carries the list to the answer.
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

type checkPolicyJSON struct {
	Content string `json:"content"`
}

type checkedJSON struct {
	Diagnostics []diagnosticJSON `json:"diagnostics"`
	Notice      string           `json:"notice"`
}

// apiCheckPolicy runs checkPolicy on the text and saves nothing. A text with problems answers 200 with the list.
func (s *server) apiCheckPolicy(r *http.Request, _ scope) (any, error) {
	var in checkPolicyJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	var problems *policyProblems
	switch err := checkPolicy(in.Content); {
	case errors.As(err, &problems):
		return checkedJSON{Diagnostics: diagnosticsJSON(problems.list), Notice: "The check found problems, listed above the editor."}, nil
	case err != nil:
		return nil, err
	}
	return checkedJSON{Diagnostics: []diagnosticJSON{}, Notice: "The check found no problems."}, nil
}

// matchLineEndings undoes CRLF in a text sent for a file on the branch that has none, so a client that sends
// CRLF cannot rewrite every line of the policy.
func matchLineEndings(content string, current []byte) string {
	if bytes.Contains(current, []byte("\r\n")) {
		return content
	}
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// isOnBranch reports whether the sent text is the policy the editor would load for the edit's branch.
func (s *server) isOnBranch(ctx context.Context, sc scope, e edit) (bool, error) {
	_, onBranch, err := s.current(ctx, sc.repo, e.branch)
	if err != nil {
		return false, err
	}
	return matchLineEndings(e.content, onBranch) == string(onBranch), nil
}

// saveDraft applies the save rules in their documented order. changed is false for the no-op.
func (s *server) saveDraft(ctx context.Context, sc scope, e edit) (draft proposal.Draft, changed bool, err error) {
	if err := checkPolicy(e.content); err != nil {
		return draft, false, err
	}
	draft, onBranch, err := s.current(ctx, sc.repo, e.branch)
	if err != nil {
		return draft, false, err
	}
	content := matchLineEndings(e.content, onBranch)
	if content == string(onBranch) {
		return draft, false, nil
	}
	if isOpen(draft) && draft.HeadSHA != e.headSHA {
		return draft, false, proposal.ErrStale
	}
	draft, err = s.proposals.Propose(ctx, proposal.Change{
		Repo:    sc.repo.RepoRef,
		Branch:  e.branch,
		Base:    sc.repo.DefaultBranch,
		Title:   "Archfit policy edit by @" + sc.user.Login,
		Body:    editBody,
		Message: "archfit: edit policy (@" + sc.user.Login + ")",
		Files:   []api.FileChange{{Path: policy.PolicyPath, Content: []byte(content)}},
	}, e.headSHA)
	return draft, err == nil, err
}

// written answers a write that GitHub took: the page read again, with the notice. head is the draft head the write
// left; a page read at another head means the draft moved again in between, and the page must not take that head as
// the base of the text it keeps. A page that cannot be read back says the change was saved, never that nothing was.
func (s *server) written(ctx context.Context, sc scope, branch, head, since, notice string) (any, error) {
	body, err := s.policyPage(ctx, sc, branch, since)
	if err != nil {
		return nil, errSavedUnread
	}
	if body.HeadSHA != head {
		return nil, errSavedMoved
	}
	body.Notice = notice
	return body, nil
}

// errSavedMoved is a 409 like a stale save, so the page offers to discard the edits and load the draft.
var errSavedMoved = &userError{http.StatusConflict, "stale",
	"The change was saved, but the draft changed again on GitHub since. Discard your edits and load the draft."}

func (s *server) apiSavePolicy(r *http.Request, sc scope) (any, error) {
	var in editJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	e, err := readEdit(sc, in)
	if err != nil {
		return nil, err
	}
	draft, changed, err := s.saveDraft(r.Context(), sc, e)
	if err != nil {
		return nil, err
	}
	if !changed {
		body, err := s.policyPage(r.Context(), sc, e.branch, "")
		if err == nil && body.HeadSHA != draft.HeadSHA {
			err = proposal.ErrStale
		}
		body.Notice = notices["unchanged"]
		return body, err
	}
	return s.written(r.Context(), sc, e.branch, draft.HeadSHA, "", notices["saved"])
}

type readyJSON struct {
	Branch  string `json:"branch"`
	HeadSHA string `json:"head_sha"`
}

// apiReady asks for review. ui never merges: publication is the merge on GitHub.
func (s *server) apiReady(r *http.Request, sc scope) (any, error) {
	var in readyJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	e, err := readEdit(sc, editJSON{Branch: in.Branch, HeadSHA: in.HeadSHA})
	if err != nil {
		return nil, err
	}
	draft, err := s.proposals.Get(r.Context(), sc.repo.RepoRef, e.branch)
	switch {
	case err != nil:
	case draft.State != proposal.DraftOpen:
		err = conflict("There is no draft pull request to mark ready. Reload the page.")
	case draft.HeadSHA != e.headSHA:
		err = proposal.ErrStale
	default:
		err = s.proposals.Ready(r.Context(), sc.repo.RepoRef, draft.Number)
	}
	if err != nil {
		return nil, err
	}
	return s.written(r.Context(), sc, e.branch, e.headSHA, "", notices["ready"])
}

type assistJSON struct {
	editJSON
	Instruction string `json:"instruction"`
	Mode        string `json:"mode"` // managed or customer
}

// apiAssist is the explicit AI request: save what the user sees, then ask assist for a proposal bound to that head.
// A customer-mode answer carries since: the page follows the run in the customer's CI from it.
func (s *server) apiAssist(r *http.Request, sc scope) (any, error) {
	var in assistJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	e, err := readEdit(sc, in.editJSON)
	if err != nil {
		return nil, err
	}
	mode := assist.Mode(in.Mode)
	if mode != assist.ModeManaged && mode != assist.ModeCustomer {
		return nil, invalid("Choose managed or customer mode.")
	}
	draft, changed, err := s.saveDraft(r.Context(), sc, e)
	var problems *policyProblems
	if errors.As(err, &problems) {
		// Owner decision: asking AI to fix the draft that is already on the branch must work. Nothing is
		// committed here, and assist checks its answer with the same Diagnose.
		var onBranch bool
		if onBranch, err = s.isOnBranch(r.Context(), sc, e); err == nil && !onBranch {
			err = problems
		}
	}
	if err != nil {
		return nil, err
	}
	if changed {
		e.headSHA = draft.HeadSHA
	}
	res, err := s.assist.Propose(r.Context(), mode, assist.Request{
		Repo:        sc.repo,
		Branch:      e.branch,
		HeadSHA:     e.headSHA,
		Instruction: in.Instruction,
		By:          sc.user.Login,
	})
	if err != nil {
		return nil, err
	}
	if res.Mode == assist.ModeCustomer {
		return s.written(r.Context(), sc, e.branch, e.headSHA, e.headSHA, notices["requested"])
	}
	return s.written(r.Context(), sc, e.branch, res.Draft.HeadSHA, "", notices["proposed"])
}
