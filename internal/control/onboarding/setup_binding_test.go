package onboarding

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
)

type setupTokens struct{ auth.TokenSource }

func (setupTokens) InstallationToken(context.Context, int64) (string, error) {
	return "installation-token", nil
}

type setupTransport func(*http.Request) (*http.Response, error)

func (f setupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSetupFirstApprovalAndGateBindingAgree(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		policyAtTip, workflowAtTip bool
		first, missing             int
	}{
		{"policy only on onboarding head", false, false, 12, 2},
		{"default policy without workflow", true, false, 0, 1},
		{"already converted default", true, true, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newSetupHub()
			h.files["tip"] = map[string]string{}
			if tt.policyAtTip {
				h.files["tip"][".archfit.yaml"] = setupPolicy
			}
			if tt.workflowAtTip {
				h.files["tip"][workflowPath] = setupWorkflow(setupCommit)
			}
			h.files["onboarding-head"] = map[string]string{".archfit.yaml": setupPolicy, workflowPath: setupWorkflow(setupCommit)}
			h.pulls = []api.Pull{{Number: 12, State: "open", BaseRef: "main", HeadRepo: setupRepo.FullName, HeadSHA: "onboarding-head"}}
			hc := &http.Client{Transport: setupTransport(func(r *http.Request) (*http.Response, error) {
				status := http.StatusOK
				var body any
				switch {
				case r.URL.Path == "/repos/acme/app/git/ref/heads/main":
					body = map[string]any{"object": map[string]any{"sha": "tip"}}
				case r.URL.Path == "/repos/acme/app/pulls/12":
					body = map[string]any{"number": 12, "state": "open", "head": map[string]any{"sha": "onboarding-head", "repo": map[string]any{"full_name": setupRepo.FullName}},
						"base": map[string]any{"ref": "main", "sha": "tip"}}
				case strings.HasPrefix(r.URL.Path, "/repos/acme/app/compare/"):
					body = map[string]any{"merge_base_commit": map[string]any{"sha": "tip"}}
				case strings.HasPrefix(r.URL.Path, "/repos/acme/app/contents/"):
					path := strings.TrimPrefix(r.URL.Path, "/repos/acme/app/contents/")
					raw, ok := h.files[r.URL.Query().Get("ref")][path]
					if !ok {
						status = http.StatusNotFound
						body = map[string]any{"message": "Not Found"}
						break
					}
					body = map[string]any{"type": "file", "encoding": "base64", "size": len(raw), "content": base64.StdEncoding.EncodeToString([]byte(raw)), "sha": setupCommit}
				default:
					t.Errorf("unexpected API call: %s", r.URL)
					status = http.StatusNotFound
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			mem := store.NewMemory()
			g := gate.New(gate.Deps{GitHub: api.New(setupTokens{}, hc), Store: mem, Admissions: gate.NewAdmissions(mem, []string{"acme"})})
			s := &setups{gh: h, gate: g, actionApproved: func(ref string) bool { return ref == setupCommit }}
			out, err := s.Setup(context.Background(), setupRepo)
			if err != nil || out.State != "first_approval" || out.FirstPull != tt.first || len(out.Missing) != tt.missing || out.Admitted != (tt.policyAtTip && tt.workflowAtTip) {
				t.Fatalf("Setup = %+v, %v", out, err)
			}
			digest, err := g.OwnerDigest(context.Background(), gate.Target{Repo: setupRepo}, gate.OwnerAction{Kind: gate.OwnerFirst, Pull: out.FirstPull})
			if err != nil || digest == "" {
				t.Fatalf("Setup's selected approval cannot bind: %q, %v", digest, err)
			}
			if tt.policyAtTip {
				if _, err := g.OwnerDigest(context.Background(), gate.Target{Repo: setupRepo}, gate.OwnerAction{Kind: gate.OwnerFirst, Pull: 12}); !errors.Is(err, gate.ErrOwnerNotPossible) {
					t.Fatalf("gate must refuse onboarding PR when default policy exists: %v", err)
				}
			}
		})
	}
}
