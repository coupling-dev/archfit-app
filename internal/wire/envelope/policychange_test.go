package envelope_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const policyChangeDir = "../testdata/policy-change"

func policyChangeFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(policyChangeDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func withPolicyChange(base map[string]string, value string) []byte {
	base["policy_change"] = value
	return render(base)
}

// The Action sends the engine's policy_change on a pull request report; the envelope accepts exactly that.
func TestDecodeAcceptsThePoliciesTheActionSends(t *testing.T) {
	for _, name := range []string{"widen", "addrule", "exclude"} {
		t.Run(name, func(t *testing.T) {
			value := policyChangeFixture(t, name)
			raw := withPolicyChange(prFields(), value)
			env := wantDecoded(t, raw)
			if string(env.PolicyChange) != value {
				t.Errorf("policy_change = %s, want the bytes sent", env.PolicyChange)
			}
			wantSchemaAgrees(t, raw, true)
		})
	}
	t.Run("absent", func(t *testing.T) {
		env := wantDecoded(t, render(prFields()))
		if env.PolicyChange != nil {
			t.Errorf("policy_change = %s, want none", env.PolicyChange)
		}
	})
}

func TestPolicyChangeRules(t *testing.T) {
	widen := policyChangeFixture(t, "widen")
	replaced := func(old, repl string) string {
		if !strings.Contains(widen, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(widen, old, repl, 1)
	}
	tests := []struct {
		name   string
		raw    []byte
		schema bool // false: the rule is one JSON Schema cannot state (a duplicate key), so only Decode is asked
	}{
		{"on a push", withPolicyChange(pushFields("push"), widen), true},
		{"on a workflow_dispatch report", withPolicyChange(pushFields("workflow_dispatch"), widen), true},
		{"on a discovery upload", withPolicyChange(discoveryFields(), widen), true},
		{"on a baseline upload", withPolicyChange(baselineFields(), widen), true},
		{"on a reanchor upload", withPolicyChange(reanchorFields(), widen), true},
		{"on a discovery upload of a pull request", withPolicyChange(func() map[string]string {
			f := prFields()
			f["kind"] = quoted("discovery")
			return f
		}(), widen), true},
		{"null", withPolicyChange(prFields(), `null`), true},
		{"a string", withPolicyChange(prFields(), `"loosening"`), true},
		{"an array", withPolicyChange(prFields(), `[]`), true},
		{"an empty object", withPolicyChange(prFields(), `{}`), true},
		{"a missing list", withPolicyChange(prFields(), replaced(`"coverage_lost":[],`, ``)), true},
		{"a missing direction", withPolicyChange(prFields(), replaced(`"direction":"loosening",`, ``)), true},
		{"an unknown key", withPolicyChange(prFields(), replaced(`{"direction"`, `{"approved":true,"direction"`)), true},
		{"a duplicate list", withPolicyChange(prFields(), strings.TrimSuffix(widen, "}")+`,"coverage_lost":[]}`), false},
		{"a duplicate direction", withPolicyChange(prFields(), strings.TrimSuffix(widen, "}")+`,"direction":"loosening"}`), false},
		{"direction none", withPolicyChange(prFields(), replaced(`"loosening"`, `"none"`)), true},
		{"direction other", withPolicyChange(prFields(), replaced(`"loosening"`, `"neutral"`)), true},
		{"direction null", withPolicyChange(prFields(), replaced(`"loosening"`, `null`)), true},
		{"direction not a string", withPolicyChange(prFields(), replaced(`"loosening"`, `1`)), true},
		{"a list that is an object", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":{}`)), true},
		{"a list that is a string", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":"x"`)), true},
		{"a list that is null", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":null`)), true},
		{"a list item that is a number", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":[1]`)), true},
		{"a list item that is null", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":[null]`)), true},
		{"a list item that is an array", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":[[]]`)), true},
		{"a later list item that is a string", withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":[{},"x"]`)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := wantRejected(t, tt.raw, "policy_change")
			if strings.Contains(msg, "neutral") || strings.Contains(msg, "approved") {
				t.Errorf("error echoes the offending value: %q", msg)
			}
			if tt.schema {
				wantSchemaAgrees(t, tt.raw, false)
			}
		})
	}

	// What an item holds is DecodePolicyChange's business, not the envelope's.
	t.Run("item contents are not checked", func(t *testing.T) {
		raw := withPolicyChange(prFields(), replaced(`"rules_removed":[]`, `"rules_removed":[{"anything":1}]`))
		wantDecoded(t, raw)
		wantSchemaAgrees(t, raw, true)
	})
	t.Run("the envelope stays under its size cap", func(t *testing.T) {
		raw := withPolicyChange(prFields(), widen)
		if len(raw) > 8192 {
			t.Fatalf("fixture envelope is %d bytes", len(raw))
		}
		big := replaced(`"rules_removed":[]`, `"rules_removed":[{"x":"`+strings.Repeat("a", 8192)+`"}]`)
		wantRejected(t, withPolicyChange(prFields(), big), "")
	})
	t.Run("spacing in the value is kept", func(t *testing.T) {
		spaced := bytes.ReplaceAll([]byte(widen), []byte(","), []byte(", "))
		env := wantDecoded(t, withPolicyChange(prFields(), string(spaced)))
		if !bytes.Equal(env.PolicyChange, spaced) {
			t.Errorf("policy_change = %s", env.PolicyChange)
		}
	})
}
