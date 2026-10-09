package policy

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// BaselineSchemaV3 is the only engine baseline schema the App reads (engine v3.0.0 writes it).
const BaselineSchemaV3 = "archfit.baseline.v3"

// MaxBaselineBytes caps a baseline file: 1 MiB, the size github/api reads back from the protected ref.
const MaxBaselineBytes = 1 << 20

const (
	fingerprintHexLen = 32 // hex(sha256(...)[:16]), the engine's finding ID
	maxRuleIDLen      = 128
	maxProfileLen     = 64
)

// Accepted is one accepted finding of a baseline: its engine finding ID and the rule that produced it.
type Accepted struct {
	Fingerprint string // 32 lowercase hex
	RuleID      string
}

// MetricValue is one stored metric of a baseline: its value and the version of its definition.
type MetricValue struct {
	Value   float64
	Version string
}

// Baseline is the App's view of an engine baseline file: what it accepts and what it was captured under.
type Baseline struct {
	Accepted          []Accepted // file order
	QualifyingSeamIDs []string   // state.qualifying_seam_ids: the distributed-monolith seams the capture accepted
	Metrics           map[string]MetricValue
	ConfigHash        string // state.config_hash: Digest of the policy the capture ran under
	ProfileVersion    string // state.measurement_profile.version
}

// baselineFile is the transport shape. Keys the App does not read are ignored: the engine adds baseline keys
// over time, and the App compares only fingerprints, the config hash and the profile version.
type baselineFile struct {
	SchemaVersion *string `json:"schema_version"`
	Accepted      *[]struct {
		Fingerprint *string `json:"fingerprint"`
		RuleID      *string `json:"rule_id"`
	} `json:"accepted"`
	Metrics *map[string]*struct {
		Value   *float64 `json:"value"`
		Version *string  `json:"version"`
	} `json:"metrics"`
	State *struct {
		ConfigHash         *string   `json:"config_hash"`
		QualifyingSeamIDs  *[]string `json:"qualifying_seam_ids"`
		MeasurementProfile *struct {
			Version *string `json:"version"`
		} `json:"measurement_profile"`
	} `json:"state"`
}

// ReadBaseline decodes an engine baseline file. It is a structural check, not engine validation: one JSON
// object of schema BaselineSchemaV3, at most MaxBaselineBytes, with a state block naming a 64-hex config hash
// and a measurement profile version, and accepted findings with distinct 32-hex fingerprints and token rule IDs.
// Errors never echo keys or values.
func ReadBaseline(raw []byte) (Baseline, error) {
	switch {
	case len(raw) > MaxBaselineBytes:
		return Baseline{}, errors.New("policy: baseline is larger than 1 MiB")
	case !utf8.Valid(raw):
		return Baseline{}, errors.New("policy: baseline is not valid UTF-8")
	}
	var f baselineFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Baseline{}, errors.New("policy: baseline is not one JSON object of the baseline shape")
	}
	switch {
	case f.SchemaVersion == nil || *f.SchemaVersion != BaselineSchemaV3:
		return Baseline{}, errors.New("policy: baseline schema_version must be " + BaselineSchemaV3)
	case f.Accepted == nil:
		return Baseline{}, errors.New("policy: baseline accepted is required")
	case f.State == nil:
		return Baseline{}, errors.New("policy: baseline state is required")
	case f.State.ConfigHash == nil || !isLowerHex(*f.State.ConfigHash, 2*32):
		return Baseline{}, errors.New("policy: baseline state.config_hash must be 64 lowercase hex characters")
	case f.State.MeasurementProfile == nil || f.State.MeasurementProfile.Version == nil ||
		!isToken(*f.State.MeasurementProfile.Version, maxProfileLen):
		return Baseline{}, errors.New("policy: baseline state.measurement_profile.version is required")
	case f.State.QualifyingSeamIDs == nil:
		return Baseline{}, errors.New("policy: baseline state.qualifying_seam_ids is required")
	case f.Metrics == nil:
		return Baseline{}, errors.New("policy: baseline metrics is required")
	}

	b := Baseline{
		Accepted:          make([]Accepted, 0, len(*f.Accepted)),
		QualifyingSeamIDs: make([]string, 0, len(*f.State.QualifyingSeamIDs)),
		Metrics:           make(map[string]MetricValue, len(*f.Metrics)),
		ConfigHash:        *f.State.ConfigHash,
		ProfileVersion:    *f.State.MeasurementProfile.Version,
	}
	for name, m := range *f.Metrics {
		switch {
		case !isToken(name, maxRuleIDLen) || m == nil || m.Value == nil || m.Version == nil || !isToken(*m.Version, maxProfileLen):
			return Baseline{}, errors.New("policy: baseline metric must name a token with a value and a version token")
		}
		b.Metrics[name] = MetricValue{Value: *m.Value, Version: *m.Version}
	}
	seenSeam := make(map[string]bool, len(*f.State.QualifyingSeamIDs))
	for _, id := range *f.State.QualifyingSeamIDs {
		switch {
		case !isToken(id, maxRuleIDLen) || seenSeam[id]:
			return Baseline{}, errors.New("policy: baseline qualifying seam id must be a distinct token")
		}
		seenSeam[id] = true
		b.QualifyingSeamIDs = append(b.QualifyingSeamIDs, id)
	}
	seen := make(map[string]bool, len(*f.Accepted))
	for _, a := range *f.Accepted {
		switch {
		case a.Fingerprint == nil || !isLowerHex(*a.Fingerprint, fingerprintHexLen):
			return Baseline{}, errors.New("policy: baseline accepted fingerprint must be 32 lowercase hex characters")
		case a.RuleID == nil || !isToken(*a.RuleID, maxRuleIDLen):
			return Baseline{}, errors.New("policy: baseline accepted rule_id must be 1 to 128 characters of A-Z a-z 0-9 _ . : / + -")
		case seen[*a.Fingerprint]:
			return Baseline{}, errors.New("policy: baseline accepted fingerprints must be distinct")
		}
		seen[*a.Fingerprint] = true
		b.Accepted = append(b.Accepted, Accepted{Fingerprint: *a.Fingerprint, RuleID: *a.RuleID})
	}
	return b, nil
}

func isLowerHex(s string, n int) bool {
	return len(s) == n && !strings.ContainsFunc(s, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}

// isToken accepts 1 to limit characters that are safe to render verbatim in Markdown code spans.
func isToken(s string, limit int) bool {
	return s != "" && len(s) <= limit && !strings.ContainsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_.:/+-", r)
	})
}
