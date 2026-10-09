package onboarding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	seamKept         = "1111111111111111111111111111111111111111111111111111111111111111"
	seamDropped      = "2222222222222222222222222222222222222222222222222222222222222222"
	seamNew          = "3333333333333333333333333333333333333333333333333333333333333333"
	newFingerprint   = "44444444444444444444444444444444"
	protectedMetrics = `{"coverage":{"value":2,"version":"coverage.v1"}}`
)

// protectedFixture is the protected baseline a re-anchor is checked against: accepts A and B, qualifies two seams.
func protectedFixture(t *testing.T, policyText []byte) []byte {
	t.Helper()
	return syntheticBaselineWith(policyText, "archfit.measurement.v2", []string{seamKept, seamDropped}, protectedMetrics,
		[2]string{fingerprintA, "no_direct_b_dependency"}, [2]string{fingerprintB, "bc/imbalanced_coupling"})
}

// reanchorUpload is the upload the Action sends for a re-anchor: the new baseline bytes and the protected digest.
func reanchorUpload(r api.Repo, payload []byte, protectedDigest string) gate.Upload {
	up := baselineUpload(r, payload, "")
	up.BaselineDigest = protectedDigest
	return up
}

func (f *fixture) postReanchor(t *testing.T) uploadResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/reanchors", strings.NewReader("ignored: the fake Receiver supplies the upload"))
	rec := httptest.NewRecorder()
	f.svc.ReanchorHandler().ServeHTTP(rec, req)
	out := uploadResponse{status: rec.Code, raw: rec.Body.String()}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q", out.raw)
	}
	return out
}

func TestReanchorOpensPRThatKeepsTheProtectedDebt(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	protected := protectedFixture(t, policyText)
	// The re-anchor keeps A, drops B, keeps one seam, drops the other, and resets the coverage value 2 to 1.
	reanchored := syntheticBaselineWith(policyText, "archfit.measurement.v2", []string{seamKept},
		`{"coverage":{"value":1,"version":"coverage.v1"}}`, [2]string{fingerprintA, "no_direct_b_dependency"})

	f := newFixture(t)
	f.protect(policyText, nil, protected)
	before := f.protectedSnapshot()
	f.receiver.upload = reanchorUpload(f.repo, reanchored, policy.Digest(protected))

	res := f.postReanchor(t)
	if res.status != 200 || res.Number != 1 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	if !reflect.DeepEqual(f.receiver.kinds, []envelope.Kind{envelope.KindReanchor}) {
		t.Errorf("Receive kinds = %v, want the reanchor kind", f.receiver.kinds)
	}
	if got, _ := f.hub.file(f.repo, baselineBranch, policy.BaselinePath); string(got) != string(reanchored) {
		t.Error("the committed baseline differs from the uploaded bytes")
	}
	f.wantProtectedUntouched(t, before)

	body := f.hub.body(f.repo, baselineBranch)
	for _, want := range []string{
		"Approving accepts no finding", "| Kept | 1 |", "| Dropped | 1 |", "| Added | 0 |", "| Qualifying seams dropped | 1 |",
		fingerprintB, "`bc/imbalanced_coupling`", seamDropped, "| `coverage` | `coverage.v1` | 2 | 1 |",
		"archfit-baseline", "9002", "customer-attested",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q:\n%s", want, body)
		}
	}
	if strings.Count(body, seamKept) != 0 {
		t.Errorf("the kept seam is named in the body:\n%s", body)
	}
}

func TestReanchorRefusals(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	protected := protectedFixture(t, policyText)
	tests := []struct {
		name      string
		protect   []byte // the protected baseline on the default branch; nil = none
		payload   []byte
		digest    string
		wantCode  string
		wantError bool
	}{
		{"no protected baseline", nil, syntheticBaselineWith(policyText, "archfit.measurement.v2", nil, "{}"), "", codeReanchorBaselineMissing, true},
		{"protected baseline unreadable", []byte("not a baseline\n"), protected, "", codeReanchorBaselineMissing, true},
		{"digest names another file", protected, protected, strings.Repeat("ab", 32), codeReanchorDigestMismatch, true},
		{"new accepted finding", protected,
			syntheticBaselineWith(policyText, "archfit.measurement.v2", []string{seamKept}, "{}", [2]string{newFingerprint, "no_direct_b_dependency"}),
			"", codeReanchorNotSubset, true},
		{"same fingerprint under another rule", protected,
			syntheticBaselineWith(policyText, "archfit.measurement.v2", []string{seamKept}, "{}", [2]string{fingerprintA, "bc/imbalanced_coupling"}),
			"", codeReanchorNotSubset, true},
		{"new qualifying seam", protected,
			syntheticBaselineWith(policyText, "archfit.measurement.v2", []string{seamKept, seamNew}, "{}", [2]string{fingerprintA, "no_direct_b_dependency"}),
			"", codeReanchorNotSubset, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.protect(policyText, nil, tt.protect)
			digest := tt.digest
			if digest == "" && tt.protect != nil {
				digest = policy.Digest(tt.protect)
			}
			f.receiver.upload = reanchorUpload(f.repo, tt.payload, digest)

			res := f.postReanchor(t)
			if res.status != http.StatusForbidden || res.Error != tt.wantCode {
				t.Fatalf("response = %d %s, want 403 %s", res.status, res.raw, tt.wantCode)
			}
			if n := f.hub.count("Propose"); n != 0 {
				t.Errorf("%d Propose calls for a refused re-anchor", n)
			}
			if n := f.hub.prCount(f.repo, ""); n != 0 {
				t.Errorf("%d pull requests opened", n)
			}
		})
	}
}

func TestReanchorUnchangedOpensNothing(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	protected := protectedFixture(t, policyText)

	f := newFixture(t)
	f.protect(policyText, nil, protected)
	f.receiver.upload = reanchorUpload(f.repo, protected, policy.Digest(protected))

	res := f.postReanchor(t)
	if res.status != 200 || !res.Unchanged {
		t.Fatalf("response = %d %s, want 200 unchanged", res.status, res.raw)
	}
	if n := f.hub.count("Propose") + f.hub.count("SetBody"); n != 0 {
		t.Errorf("%d write calls for an unchanged re-anchor", n)
	}
}

// TestReanchorBodyStaysUnderGitHubLimit: a protected baseline with more debt than a body lists keeps the body under
// GitHub's limit and counts the rest.
func TestReanchorBodyStaysUnderGitHubLimit(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	var accepted [][2]string
	var seams []string
	for i := range 200 {
		accepted = append(accepted, [2]string{fmt.Sprintf("%032x", i+1), "no_direct_b_dependency"})
		seams = append(seams, fmt.Sprintf("seam-%03d", i))
	}
	protected := syntheticBaselineWith(policyText, "archfit.measurement.v2", seams, "{}", accepted...)
	reanchored := syntheticBaselineWith(policyText, "archfit.measurement.v2", nil, "{}")

	f := newFixture(t)
	f.protect(policyText, nil, protected)
	f.receiver.upload = reanchorUpload(f.repo, reanchored, policy.Digest(protected))

	if res := f.postReanchor(t); res.status != 200 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	body := f.hub.body(f.repo, baselineBranch)
	if len(body) > 65536 {
		t.Errorf("body is %d bytes; GitHub refuses more than 65536 characters", len(body))
	}
	for _, want := range []string{"and 50 more", "and 100 more", "| Dropped | 200 |"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not mention %q", want)
		}
	}
}
