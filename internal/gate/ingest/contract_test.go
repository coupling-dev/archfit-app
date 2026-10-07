package ingest

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestReceivePullRequestReport(t *testing.T) {
	rows := []struct {
		name             string
		baseline, labels string
	}{
		{"no baseline and no labels", "", ""},
		{"baseline and labels claimed", strings.Repeat("1", 64), strings.Repeat("2", 64)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			s.env.BaselineDigest, s.env.LabelsDigest = row.baseline, row.labels
			up, err := s.receive(t)
			wantProblems(t, up, err)
			wantCalls(t, s, "verify", "resolve", "run", "pull")

			if want := healthyBinding(s); !reflect.DeepEqual(up.Binding, want) {
				t.Errorf("binding = %+v, want %+v", up.Binding, want)
			}
			wantIdentity := manifest.Identity{EngineVersion: "v2.3.1", ImageDigest: imageDigest, Platform: "linux/amd64"}
			if up.Identity != wantIdentity {
				t.Errorf("identity = %+v, want %+v", up.Identity, wantIdentity)
			}
			if up.BaselineDigest != row.baseline || up.LabelsDigest != row.labels {
				t.Errorf("claimed digests = %q, %q, want %q, %q", up.BaselineDigest, up.LabelsDigest, row.baseline, row.labels)
			}
			if up.Digest != envelope.PayloadDigest(s.body) {
				t.Errorf("digest = %q, want the digest of the body", up.Digest)
			}
			if !bytes.Equal(up.Payload, s.body) {
				t.Error("payload differs from the body")
			}
			want, err := report.Decode(s.body, s.env.EngineVersion)
			if err != nil {
				t.Fatal(err)
			}
			if up.Report == nil || !reflect.DeepEqual(*up.Report, want) {
				t.Errorf("report = %+v, want the decoded fixture", up.Report)
			}
		})
	}
}

func TestReportKeySetFollowsTheClaimedEngine(t *testing.T) {
	// A real v2.5.0 capture: its seams carry seams[].policy, a key v2.4.1 does not define.
	v250 := filepath.Join("..", "..", "wire", "testdata", "report", "real-seam-policy-v250.json")
	rows := []struct {
		name, version, body string // body "" keeps the scenario's v2.3.1 document
		problems            []Problem
	}{
		{"v2.3.1", "v2.3.1", "", nil},
		{"v2.4.0", "v2.4.0", "", nil},
		{"v2.4.1", "v2.4.1", "", nil},
		{"v2.5.0", "v2.5.0", "", nil},
		{"v2.5.0 capture", "v2.5.0", v250, nil},
		{"v2.5.0 capture claimed as v2.4.1", "v2.4.1", v250, []Problem{ProblemReportInvalid}},
		{"v9.9.9", "v9.9.9", "", []Problem{ProblemReportInvalid}}, // a release the decoder does not read
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			if row.body != "" {
				raw, err := os.ReadFile(row.body)
				if err != nil {
					t.Fatal(err)
				}
				s.setBody(raw)
			}
			s.env.EngineVersion = row.version
			up, err := s.receive(t)
			wantProblems(t, up, err, row.problems...)
			if (up.Report == nil) != (len(row.problems) > 0) {
				t.Errorf("report nil = %v, want %v", up.Report == nil, len(row.problems) > 0)
			}
			if up.Identity.EngineVersion != row.version {
				t.Errorf("identity engine version = %q, want the claimed %q", up.Identity.EngineVersion, row.version)
			}
		})
	}
}

func TestReceiveDefaultBranchPush(t *testing.T) {
	t.Run("default branch", func(t *testing.T) {
		s := pushScenario(t)
		up, err := s.receive(t)
		wantProblems(t, up, err)
		wantCalls(t, s, "verify", "resolve", "run")
		want := healthyBinding(s)
		want.Event, want.PullRequest, want.BaseRef, want.BaseSHA, want.MergeBaseSHA = "push", 0, defaultBranch, "", ""
		if !reflect.DeepEqual(up.Binding, want) {
			t.Errorf("binding = %+v, want %+v", up.Binding, want)
		}
	})

	t.Run("push to another branch", func(t *testing.T) {
		s := pushScenario(t)
		s.claims.Ref = "refs/heads/feature/x"
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemUnsupportedEvent)
		if up.Binding.BaseRef != "feature/x" {
			t.Errorf("BaseRef = %q, want feature/x", up.Binding.BaseRef)
		}
	})

	t.Run("unbound pull_request run", func(t *testing.T) {
		s := prScenario(t)
		s.run.PullRequests = []int{prNumber, 8}
		s.pulls[8] = openPull(8)
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemEnvelopeMismatch)
		if up.Binding.PullRequest != 0 || up.Binding.BaseRef != defaultBranch {
			t.Errorf("binding = %+v, want no pull request and the default branch", up.Binding)
		}
	})

	t.Run("tag push", func(t *testing.T) {
		s := pushScenario(t)
		s.claims.Ref = "refs/tags/v1.0.0"
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemUnsupportedEvent)
		if up.Binding.BaseRef != defaultBranch {
			t.Errorf("BaseRef = %q, want the default branch", up.Binding.BaseRef)
		}
	})
}

func TestReceiveDiscovery(t *testing.T) {
	t.Run("workflow_dispatch", func(t *testing.T) {
		s := discoveryScenario(t)
		up, err := s.receive(t)
		// A YAML payload cannot pass report.Decode: no problem proves it was not called.
		wantProblems(t, up, err)
		if up.Report != nil {
			t.Error("report is set for a discovery upload")
		}
		if string(up.Payload) != discoveryYAML {
			t.Error("payload differs from the body")
		}
		if up.Binding.Event != "workflow_dispatch" || up.Binding.BaseRef != defaultBranch {
			t.Errorf("binding = %+v, want workflow_dispatch on the default branch", up.Binding)
		}
	})

	t.Run("default branch push", func(t *testing.T) {
		s := pushScenario(t)
		s.toDiscovery()
		up, err := s.receive(t)
		wantProblems(t, up, err)
	})

	t.Run("pull_request run", func(t *testing.T) {
		s := prScenario(t)
		s.toDiscovery()
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemUnsupportedEvent)
		if up.Report != nil || string(up.Payload) != discoveryYAML {
			t.Error("discovery upload must carry the payload only")
		}
		if up.Binding.PullRequest != prNumber {
			t.Errorf("PullRequest = %d, want the same provenance checks as a report", up.Binding.PullRequest)
		}
	})

	t.Run("workflow_dispatch report", func(t *testing.T) {
		s := discoveryScenario(t)
		s.kind, s.env.Kind = envelope.KindReport, envelope.KindReport
		s.setBody(readFixture(t, "healthy.json"))
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemUnsupportedEvent)
	})
}

func TestReceiveBaseline(t *testing.T) {
	t.Run("workflow_dispatch", func(t *testing.T) {
		s := discoveryScenario(t)
		s.toBaseline()
		up, err := s.receive(t)
		// The JSON payload is not a report: no problem proves report.Decode was not called.
		wantProblems(t, up, err)
		if up.Report != nil || string(up.Payload) != baselineJSON {
			t.Error("baseline upload must carry the payload only")
		}
		if up.Binding.Event != "workflow_dispatch" || up.Binding.BaseRef != defaultBranch {
			t.Errorf("binding = %+v, want workflow_dispatch on the default branch", up.Binding)
		}
	})

	t.Run("default branch push", func(t *testing.T) {
		s := pushScenario(t)
		s.toBaseline()
		up, err := s.receive(t)
		wantProblems(t, up, err)
	})

	t.Run("pull_request run", func(t *testing.T) {
		s := prScenario(t)
		s.toBaseline()
		up, err := s.receive(t)
		wantProblems(t, up, err, ProblemUnsupportedEvent)
	})

	t.Run("dispatch on another branch", func(t *testing.T) {
		s := discoveryScenario(t)
		s.toBaseline()
		s.claims.Ref = "refs/heads/feature"
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if up.Binding.BaseRef != "feature" {
			t.Errorf("BaseRef = %q, want the branch the run executed on, for the route to refuse", up.Binding.BaseRef)
		}
	})

	t.Run("baseline envelope on the discovery route", func(t *testing.T) {
		s := discoveryScenario(t)
		s.env.Kind = envelope.KindBaseline
		up, err := s.receive(t)
		wantRejection(t, up, err, 400, "kind_mismatch")
	})
}

func TestProblemsNeverReject(t *testing.T) {
	workflowRun := func(s *scenario) { s.toEvent("workflow_run", "refs/heads/"+defaultBranch) }
	forkPR := func(s *scenario) {
		p := s.pulls[prNumber]
		p.HeadRepo = "fork/shop"
		s.pulls[prNumber] = p
		s.run.HeadRepo = "fork/shop"
		s.run.PullRequests = nil // GitHub leaves it empty for fork runs
	}
	invalid := func(s *scenario) { s.setBody([]byte(`{"schema_version":"nope"}`)) }

	rows := []struct {
		name     string
		mutate   func(*scenario)
		problems []Problem
		fork     bool
	}{
		{"workflow_run upload", workflowRun, []Problem{ProblemUnsupportedEvent}, false},
		{"fork PR", forkPR, []Problem{ProblemFork}, true},
		{"fork PR from the pull alone", func(s *scenario) {
			p := s.pulls[prNumber]
			p.HeadRepo = "fork/shop"
			s.pulls[prNumber] = p
		}, []Problem{ProblemFork}, true},
		{"invalid report bytes", invalid, []Problem{ProblemReportInvalid}, false},
		{"all three at once", func(s *scenario) {
			workflowRun(s)
			s.run.HeadRepo = "fork/shop" // a workflow_run run carries the fork as its head repository
			invalid(s)
		}, []Problem{ProblemUnsupportedEvent, ProblemFork, ProblemReportInvalid}, true},
		{"every problem, mismatches repeated", func(s *scenario) {
			forkPR(s)
			p := s.pulls[prNumber]
			p.State = "closed"
			s.pulls[prNumber] = p
			s.env.RunAttempt = 3
			s.env.RunID = runID + 1
			invalid(s)
		}, []Problem{ProblemEnvelopeMismatch, ProblemFork, ProblemReportInvalid}, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			row.mutate(s)
			up, err := s.receive(t)
			wantProblems(t, up, err, row.problems...)
			if up.Binding.Fork != row.fork {
				t.Errorf("Fork = %v, want %v", up.Binding.Fork, row.fork)
			}
			invalidReport := row.problems[len(row.problems)-1] == ProblemReportInvalid
			if (up.Report == nil) != invalidReport {
				t.Errorf("report nil = %v, want %v", up.Report == nil, invalidReport)
			}
			if !bytes.Equal(up.Payload, s.body) {
				t.Error("payload differs from the body")
			}
		})
	}
}
