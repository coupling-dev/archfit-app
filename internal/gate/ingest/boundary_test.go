package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// endlessReader never ends and counts what was read from it.
type endlessReader struct{ n int64 }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = '#'
	}
	r.n += int64(len(p))
	return len(p), nil
}

func TestBodyIsCapped(t *testing.T) {
	t.Run("exactly MaxBytes", func(t *testing.T) {
		s := discoveryScenario(t)
		s.setBody(bytes.Repeat([]byte("#"), report.MaxBytes))
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if len(up.Payload) != report.MaxBytes {
			t.Errorf("payload length = %d, want %d", len(up.Payload), report.MaxBytes)
		}
	})

	t.Run("one byte more", func(t *testing.T) {
		s := discoveryScenario(t)
		s.setBody(bytes.Repeat([]byte("#"), report.MaxBytes+1))
		up, err := s.receive(t)
		wantRejection(t, up, err, 413, "payload_too_large")
	})

	t.Run("endless reader", func(t *testing.T) {
		s := discoveryScenario(t)
		endless := &endlessReader{}
		s.reader = endless
		up, err := s.receive(t)
		wantRejection(t, up, err, 413, "payload_too_large")
		if endless.n > report.MaxBytes+1 {
			t.Errorf("read %d bytes, want at most %d", endless.n, report.MaxBytes+1)
		}
		wantCalls(t, s)
	})
}

func TestClientSuppliedTenantIsIgnored(t *testing.T) {
	t.Run("tenant hints in headers and query", func(t *testing.T) {
		s := prScenario(t)
		s.target = "/v1/reports?installation_id=" + otherInstallID + "&repository=" + otherTenant
		s.headers = map[string]string{
			"X-Installation-Id":        otherInstallID,
			"X-GitHub-Installation-Id": otherInstallID,
			"X-Archfit-Installation":   otherInstallID,
			"X-Archfit-Repository":     otherTenant,
		}
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if up.Binding.Repo != s.repo {
			t.Errorf("repo = %+v, want the claim's tenant %+v", up.Binding.Repo, s.repo)
		}
		if len(s.resolved) != 1 || s.resolved[0] != repoName {
			t.Errorf("resolved = %v, want only %s", s.resolved, repoName)
		}
		want := api.RepoRef{InstallationID: installationID, FullName: repoName}
		if len(s.refs) == 0 {
			t.Fatal("no API call recorded")
		}
		for _, ref := range s.refs {
			if ref != want {
				t.Errorf("API call addressed %+v, want %+v", ref, want)
			}
		}
	})

	t.Run("envelope of another tenant", func(t *testing.T) {
		s := prScenario(t)
		s.env.Repository = otherTenant
		up, err := s.receive(t)
		wantRejection(t, up, err, 403, "repository_mismatch")
		if len(s.resolved) != 0 || len(s.refs) != 0 {
			t.Errorf("API calls = %v %v, want none", s.resolved, s.refs)
		}
	})
}

type leakage struct {
	name string
	seed string
	body []byte
}

// leakageFixtures seeds source-like content into the blocked fixture.
func leakageFixtures(t *testing.T) []leakage {
	t.Helper()
	finding := func(m map[string]any) map[string]any { return m["findings"].([]any)[0].(map[string]any) }
	seeds := []struct {
		name, seed string
		mutate     func(m map[string]any, seed string)
	}{
		{"snippet field", "SEEDED-LEAK-SNIPPET", func(m map[string]any, seed string) {
			finding(m)["snippet"] = "func " + seed + "() { return secret }"
		}},
		{"log blob", "SEEDED-LEAK-LOG", func(m map[string]any, seed string) {
			m["log"] = strings.Repeat(seed+" build step output\n", 200)
		}},
		{"raw model output", "SEEDED-LEAK-MODEL", func(m map[string]any, seed string) {
			m["raw_model_output"] = "As an assistant, " + seed
		}},
		{"multi-line message", "SEEDED-LEAK-MESSAGE", func(m map[string]any, seed string) {
			finding(m)["why"] = seed + " first line\nsecond line"
		}},
	}
	var out []leakage
	for _, s := range seeds {
		var m map[string]any
		if err := json.Unmarshal(readFixture(t, "blocked.json"), &m); err != nil {
			t.Fatal(err)
		}
		s.mutate(m, s.seed)
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, leakage{s.name, s.seed, body})
	}
	return out
}

func TestLeakageFixturesBecomeProblems(t *testing.T) {
	for _, leak := range leakageFixtures(t) {
		t.Run(leak.name, func(t *testing.T) {
			before := len(logs.String())
			s := prScenario(t)
			s.setBody(leak.body)
			up, err := s.receive(t)
			wantProblems(t, up, err, ProblemReportInvalid)
			if up.Report != nil {
				t.Error("report is set for an undecodable payload")
			}
			logged := logs.String()[before:]
			if logged == "" {
				t.Error("nothing was logged")
			}
			if strings.Contains(logged, leak.seed) {
				t.Errorf("seed %s was logged", leak.seed)
			}
		})
	}
}

// replayAll runs every table scenario of the package so that the log capture covers them.
func replayAll(t *testing.T) {
	t.Helper()
	var all []*scenario
	for _, row := range rejectionRows() {
		all = append(all, row.scenario(t))
	}
	for _, row := range resolutionRows() {
		s := prScenario(t)
		row.mutate(s)
		all = append(all, s)
	}
	for _, leak := range leakageFixtures(t) {
		s := prScenario(t)
		s.setBody(leak.body)
		all = append(all, s)
	}
	blocked := prScenario(t)
	blocked.setBody(readFixture(t, "blocked.json"))
	feature := pushScenario(t)
	feature.claims.Ref = "refs/heads/feature/x"
	failing := prScenario(t)
	failing.runErr = errSensitive
	all = append(all, prScenario(t), blocked, pushScenario(t), feature, discoveryScenario(t), failing)
	for _, s := range all {
		_, _ = s.receive(t)
	}
}

// errSensitive stands for an API client error that quotes a request path.
var errSensitive = &pathError{"GET /repos/" + repoName + "/actions/runs/1001: connection reset"}

type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }

func TestNothingSensitiveIsLogged(t *testing.T) {
	replayAll(t)
	captured := logs.String()

	forbidden := []string{
		testToken, "Bearer",
		"schema_version", "archfit.report-envelope.v1", "archfit.architecture-state.v1", "payload_digest", // envelope and payload JSON
		"LEAK-DISCOVERY-DRAFT", "SEEDED-LEAK",
		"pkg/a", "pkg/shop", workflowPath, "feature/x", "refs/", // paths and refs
		"explicitly forbidden", "Remove the dependency", // report messages
		repoName, otherTenant, "connection reset",
	}
	for _, s := range forbidden {
		if strings.Contains(captured, s) {
			t.Errorf("log output contains %q", s)
		}
	}

	allowed := map[string]bool{
		"time": true, "level": true, "msg": true,
		"kind": true, "outcome": true, "status": true, "code": true,
		"repository_id": true, "installation_id": true, "run_id": true, "run_attempt": true,
		"digest": true, "problems": true, "runner_environment": true, "duration_ms": true,
	}
	var lines, accepted int
	sc := bufio.NewScanner(strings.NewReader(captured))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		lines++
		if rec["msg"] != "ingest upload" {
			t.Errorf("log message = %q, want the one fixed text", rec["msg"])
		}
		for key := range rec {
			if !allowed[key] {
				t.Errorf("log line carries the key %q", key)
			}
		}
		if rec["outcome"] == "accepted" {
			accepted++
			for _, key := range []string{"repository_id", "installation_id", "run_id", "run_attempt", "digest", "duration_ms"} {
				if _, ok := rec[key]; !ok {
					t.Errorf("accepted log line lacks %q", key)
				}
			}
		}
	}
	if lines == 0 || accepted == 0 {
		t.Errorf("log lines = %d, accepted = %d, want both > 0", lines, accepted)
	}
}
