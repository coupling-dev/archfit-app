package wire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const prefixedDigest = "sha256:"

// requiredGateOutcomes are the `conclusion reasons[0]` pairs the report fixtures must cover. A success splits by
// the report's verdict: the gate gives warning-only attention its own reason, `attention_only`.
var requiredGateOutcomes = []string{
	"success healthy healthy",
	"success attention_only needs_attention",
	"failure blocking_findings",
	"action_required missing_required_coverage",
	"action_required baseline_not_comparable",
	"action_required measurement_profile_missing",
	"action_required unknown_engine_identity",
	"action_required report_invalid",
}

// upload is a report fixture that ships the envelope the producer would send with it.
type upload struct {
	fixture
	report   []byte
	envelope envelope.Envelope
}

func uploads(t *testing.T) []upload {
	t.Helper()
	var out []upload
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		raw, ok := f.sibling(t, suffixEnvelope)
		if !ok {
			continue
		}
		env, err := envelope.Decode(raw)
		if err != nil {
			t.Fatalf("%s: envelope sibling: %v", f.id(), err)
		}
		out = append(out, upload{fixture: f, report: f.bytes(t), envelope: env})
	}
	if len(out) == 0 {
		t.Fatal("no report fixture ships an envelope")
	}
	return out
}

func TestUploadPairIsConsistent(t *testing.T) {
	hits, misses := 0, 0
	for _, u := range uploads(t) {
		if got := envelope.PayloadDigest(u.report); got != u.envelope.PayloadDigest {
			t.Errorf("%s: PayloadDigest = %s, envelope states %s", u.id(), got, u.envelope.PayloadDigest)
		}
		_, approved := manifest.Lookup(manifest.Identity{
			EngineVersion: u.envelope.EngineVersion, ImageDigest: u.envelope.ImageDigest, Platform: u.envelope.Platform,
		})
		if want := u.Expect.identityApproved(t); approved != want {
			t.Errorf("%s: manifest.Lookup = %v, expectation says identity approved = %v", u.id(), approved, want)
		}
		if approved {
			hits++
		} else {
			misses++
		}
	}
	if hits == 0 || misses == 0 {
		t.Errorf("approved uploads = %d, unlisted uploads = %d, want both kinds", hits, misses)
	}

	for _, f := range inFolder(loadCorpus(t), folderEnvelope) {
		payload, ok := f.sibling(t, suffixPayload)
		if !ok {
			continue
		}
		env, err := envelope.Decode(f.bytes(t))
		if err != nil {
			t.Fatalf("%s: %v", f.id(), err)
		}
		if got := envelope.PayloadDigest(payload); got != env.PayloadDigest {
			t.Errorf("%s: PayloadDigest of the payload sibling = %s, envelope states %s", f.id(), got, env.PayloadDigest)
		}
	}
}

func TestPolicyDigestBindsReportToPolicy(t *testing.T) {
	bound := 0
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		policyBytes, ok := f.sibling(t, suffixPolicy)
		if !ok {
			continue
		}
		r, err := report.Decode(f.bytes(t))
		if err != nil {
			t.Fatalf("%s: a report with a policy sibling must decode: %v", f.id(), err)
		}
		bound++
		if got := policy.Digest(policyBytes); got != r.Comparison.ConfigHash {
			t.Errorf("%s: policy.Digest = %s, config_hash = %s", f.id(), got, r.Comparison.ConfigHash)
		}
		if strings.HasPrefix(r.Comparison.ConfigHash, prefixedDigest) {
			t.Errorf("%s: config_hash carries a prefix", f.id())
		}
		edited := append([]byte(nil), policyBytes...)
		edited[len(edited)-1] ^= 1
		if policy.Digest(edited) == r.Comparison.ConfigHash {
			t.Errorf("%s: a one-byte policy edit keeps the digest", f.id())
		}
	}
	if bound == 0 {
		t.Error("no report fixture ships a policy file")
	}
}

func TestInputDigestsBindUploadToProtectedFiles(t *testing.T) {
	// override returns the facts value a tampering fixture declares for key.
	override := func(u upload, key string) (string, bool) {
		raw, ok := u.Expect.Facts[key]
		if !ok {
			return "", false
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("%s: facts.%s: %v", u.id(), key, err)
		}
		return value, true
	}

	withBaseline, withLabels, without := 0, 0, 0
	for _, u := range uploads(t) {
		cases := []struct {
			key, suffix, envelopeValue string
			count                      *int
		}{
			{"baseline_digest", suffixBaseline, u.envelope.BaselineDigest, &withBaseline},
			{"labels_digest", suffixLabels, u.envelope.LabelsDigest, &withLabels},
		}
		for _, c := range cases {
			if strings.HasPrefix(c.envelopeValue, prefixedDigest) {
				t.Errorf("%s: %s carries a prefix", u.id(), c.key)
			}
			got := c.envelopeValue
			if value, ok := override(u, c.key); ok {
				// A tampering fixture: the envelope states the override, which the sibling file must not produce.
				file, _ := u.sibling(t, c.suffix)
				if got != value || got == policy.Digest(file) {
					t.Errorf("%s: facts.%s must equal the envelope value and differ from the sibling's digest", u.id(), c.key)
				}
				continue
			}
			file, ok := u.sibling(t, c.suffix)
			if !ok {
				without++
				if got != "" {
					t.Errorf("%s: %s = %q without a sibling file, want \"\"", u.id(), c.key, got)
				}
				continue
			}
			*c.count++
			if want := policy.Digest(file); got != want {
				t.Errorf("%s: %s = %q, policy.Digest of the sibling = %q", u.id(), c.key, got, want)
			}
		}
	}
	if withBaseline == 0 || withLabels == 0 || without == 0 {
		t.Errorf("uploads with baseline = %d, with labels = %d, without a file = %d, want all three", withBaseline, withLabels, without)
	}
}

func TestFixturesCoverTheGateMapping(t *testing.T) {
	reports := inFolder(loadCorpus(t), folderReport)
	covered := map[string]int{}
	ordered := 0
	for _, f := range reports {
		gate := f.Expect.Gate
		if gate == nil || len(gate.Reasons) == 0 {
			t.Errorf("%s: no gate expectation", f.id())
			continue
		}
		if gate.Conclusion == "neutral" || gate.Conclusion == "skipped" {
			t.Errorf("%s: conclusion %q is never expected", f.id(), gate.Conclusion)
		}
		outcome := gate.Conclusion + " " + gate.Reasons[0]
		if gate.Conclusion == "success" {
			var fields reportFields
			strictFields(t, f, &fields)
			outcome += " " + fields.Verdict
		}
		covered[outcome]++
		if len(gate.Reasons) >= 2 {
			ordered++
		}
	}
	for _, want := range requiredGateOutcomes {
		if covered[want] == 0 {
			t.Errorf("no report fixture expects %q", want)
		}
	}
	if ordered == 0 {
		t.Error("no report fixture expects two or more ordered reasons")
	}

	for _, name := range []string{"unknown-identity", "invalid-input"} {
		for _, f := range reports {
			if f.Name == name && len(f.Expect.Facts) == 0 {
				t.Errorf("%s: the fixture must declare its facts override", f.id())
			}
		}
	}
}
