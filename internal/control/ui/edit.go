package ui

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/control/answers"
	"github.com/coupling-dev/archfit-app/internal/control/passkey"
	"github.com/coupling-dev/archfit-app/internal/gate"
)

type editJSON struct {
	Answer string `json:"answer"`
	YAML   string `json:"yaml"`
	Base   string `json:"base_digest"`
	Target string `json:"target,omitempty"`
	Token  string `json:"token,omitempty"`
	Later  bool   `json:"merge_later,omitempty"`
}

type editPreviewJSON struct {
	Digest string   `json:"digest,omitempty"`
	Target string   `json:"target,omitempty"`
	Diff   string   `json:"diff"`
	Errors []string `json:"errors"`
}

func readEdit(r *http.Request) (answers.EditAnswer, error) {
	var in editJSON
	if err := decodeJSON(r, &in); err != nil {
		return answers.EditAnswer{}, err
	}
	if len(in.YAML) > gate.MaxEditBytes {
		return answers.EditAnswer{}, gate.ErrEditSize
	}
	if in.Answer != "edit" {
		return answers.EditAnswer{}, invalid("Choose edit for a YAML answer.")
	}
	return answers.EditAnswer{YAML: in.YAML, Base: in.Base, Target: in.Target, Token: in.Token, Later: in.Later}, nil
}

func (s *server) apiEditReload(r *http.Request, sc scope) (any, error) {
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return nil, err
	}
	base, err := s.answers.ReloadEditor(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login})
	if err != nil {
		return nil, err
	}
	return policyEditJSON{Available: true, File: string(base.File), Digest: base.Digest, Recovery: base.Recovery}, nil
}

func (s *server) apiEditPreview(r *http.Request, sc scope) (any, error) {
	in, err := readEdit(r)
	if err != nil {
		return nil, err
	}
	v, err := s.answers.PreviewEdit(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in)
	if err != nil {
		return nil, err
	}
	return editPreviewJSON{Digest: v.Digest, Target: v.Target, Diff: v.Diff, Errors: editProblems(v.Errors)}, nil
}

var (
	problemLine   = regexp.MustCompile(`^\.archfit\.yaml:(\d+): policy: (.*)$`)
	unknownKey    = regexp.MustCompile(`^field (.+) not found in type policy3\.\w+$`)
	missingGlob   = regexp.MustCompile(`^modules\.(\S+?)\.paths: required: list at least one glob$`)
	badVolatility = regexp.MustCompile(`^modules\.(\S+?)\.volatility: "([^"]*)" is not one of low, medium, high$`)
	wantList      = regexp.MustCompile(`^(modules\.\S+?): want a list$`)
	wrongType     = regexp.MustCompile(`^cannot unmarshal `)
)

// editProblems maps every problem and drops a repeat: two decoder messages for one line can read the same.
func editProblems(problems []string) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		if mapped := editProblem(p); !slices.Contains(out, mapped) {
			out = append(out, mapped)
		}
	}
	return out
}

// editProblem says one decoder problem in the words of the policy, with its line. The Go type names of the decoder
// do not reach the page; a problem no rule below names keeps its words and gets its line.
func editProblem(problem string) string {
	m := problemLine.FindStringSubmatch(problem)
	if m == nil {
		return problem
	}
	line, text := m[1], m[2]
	if k := unknownKey.FindStringSubmatch(text); k != nil {
		return fmt.Sprintf("Line %s: `%s` is not a policy key.", line, k[1])
	}
	if g := missingGlob.FindStringSubmatch(text); g != nil {
		return fmt.Sprintf("Line %s: module %s needs at least one path glob.", line, g[1])
	}
	if v := badVolatility.FindStringSubmatch(text); v != nil {
		return fmt.Sprintf("Line %s: volatility must be low, medium or high (got \"%s\").", line, v[2])
	}
	if w := wantList.FindStringSubmatch(text); w != nil {
		return fmt.Sprintf("Line %s: %s must be a list.", line, w[1])
	}
	if wrongType.MatchString(text) {
		return fmt.Sprintf("Line %s: this value has the wrong type.", line)
	}
	return fmt.Sprintf("Line %s: %s.", line, strings.TrimSuffix(text, "."))
}

func (s *server) apiEdit(r *http.Request, sc scope) (any, error) {
	in, err := readEdit(r)
	if err != nil {
		return nil, err
	}
	out, err := s.answers.Edit(r.Context(), sc.repo, passkey.User{ID: sc.user.UserID, Login: sc.user.Login}, in)
	if errors.Is(err, gate.ErrEditPolicy) {
		_, message, _ := strings.Cut(err.Error(), ":\n")
		return nil, invalid(message)
	}
	if err != nil {
		return nil, err
	}
	return answeredJSON{PullRequest: out.PullRequest, State: out.State, Merge: out.Merge, Notice: answeredNotice(out)}, nil
}
