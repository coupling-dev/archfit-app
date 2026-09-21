package gate

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// negativeRow breaks one part of a consistent upload so that no tenant is authenticated or the upload is stale.
type negativeRow struct {
	name   string
	status int
	code   string // asserted only where gate/module.md names the code
	damage func(w *world, p *pull, s *uploadSpec)
}

func negativeRows() []negativeRow {
	return []negativeRow{
		{"bad signature", 401, "", func(_ *world, _ *pull, s *uploadSpec) {
			s.token, s.claims = "forged", oidc.Claims{}
		}},
		{"wrong audience", 401, "", func(w *world, _ *pull, s *uploadSpec) {
			w.oidc.tokens[s.token] = fakeToken{claims: s.claims, audience: "https://elsewhere.test"}
			s.claims = oidc.Claims{}
		}},
		{"expired token", 401, "", func(w *world, _ *pull, s *uploadSpec) {
			w.oidc.tokens[s.token] = fakeToken{err: errBadToken}
			s.claims = oidc.Claims{}
		}},
		{"wrong tenant", 403, "", func(_ *world, _ *pull, s *uploadSpec) {
			s.claims.Repository, s.claims.RepositoryID = "mallory/other", 666
		}},
		{"unknown envelope field", 400, "", func(w *world, _ *pull, s *uploadSpec) {
			raw, err := json.Marshal(s.env)
			if err != nil {
				w.t.Fatal(err)
			}
			s.rawEnvelope = append([]byte(`{"installation_id":99,`), raw[1:]...)
		}},
		{"oversized body", 413, "", func(_ *world, _ *pull, s *uploadSpec) {
			s.body = bytes.Repeat([]byte(" "), report.MaxBytes+1)
		}},
		{"modified report bytes", 400, "", func(_ *world, _ *pull, s *uploadSpec) {
			s.body = append(bytes.Clone(s.body), ' ')
		}},
		{"stale head", 409, "", func(w *world, p *pull, _ *uploadSpec) {
			w.editPull(p, func(pl *api.Pull) { pl.HeadSHA = w.sha() })
		}},
		{"older attempt", 409, "", func(w *world, p *pull, _ *uploadSpec) {
			w.editRun(p, func(r *api.WorkflowRun) { r.Attempt++ })
		}},
		{"signing keys unavailable", 503, "oidc_unavailable", func(w *world, _ *pull, s *uploadSpec) {
			w.oidc.tokens[s.token] = fakeToken{err: oidc.ErrUnavailable}
			s.claims = oidc.Claims{}
		}},
	}
}

// problemRow is an authenticated upload with an eligibility defect: it is published, never rejected.
type problemRow struct {
	name   string
	reason string
	spec   func(w *world, p *pull) uploadSpec
}

func problemRows() []problemRow {
	healthy := func(w *world, p *pull) uploadSpec { return w.spec(p, w.report(p.repo, fixtureHealthy)) }
	return []problemRow{
		{"workflow_run upload", "unsupported_event", func(w *world, p *pull) uploadSpec {
			w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests = "workflow_run", nil })
			s := healthy(w, p)
			s.claims.EventName, s.claims.Ref = "workflow_run", "refs/heads/"+testBranch
			return s
		}},
		{"fork PR upload", "fork_unsupported", func(w *world, p *pull) uploadSpec {
			w.editPull(p, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" })
			w.editRun(p, func(r *api.WorkflowRun) { r.HeadRepo = "mallory/shop" })
			return healthy(w, p)
		}},
		{"invalid report with correct digest", "report_invalid", func(w *world, p *pull) uploadSpec {
			return w.spec(p, []byte(`{"schema_version":"not-a-report"}`))
		}},
		{"envelope naming another PR", "envelope_mismatch", func(w *world, p *pull) uploadSpec {
			s := healthy(w, p)
			s.env.PullRequest++
			return s
		}},
		{"modified workflow path", "workflow_not_approved", func(w *world, p *pull) uploadSpec {
			s := healthy(w, p)
			s.claims.JobWorkflowRef = strings.Replace(s.claims.JobWorkflowRef, testWorkflow, ".github/workflows/evil.yaml", 1)
			return s
		}},
		{"wrong image digest", "unknown_engine_identity", func(w *world, p *pull) uploadSpec {
			s := healthy(w, p)
			s.env.ImageDigest = "sha256:" + strings.Repeat("c", 64)
			return s
		}},
		{"missing profile", "measurement_profile_missing", func(w *world, p *pull) uploadSpec {
			return w.spec(p, w.report(p.repo, fixtureNoProfile))
		}},
	}
}
