package report

import (
	"strings"
	"testing"
)

var engineCases = []string{"new_violation", "existing_violation", "unmeasured_ratchet_from_profile_drift", "dead_selector", "missing_evidence"}

// The engine's own state-report fixtures of the five E1 cases decode, and carry the identity the engine states.
func TestEngineStateFixturesDecodeWithTheirIdentity(t *testing.T) {
	for _, name := range engineCases {
		t.Run(name, func(t *testing.T) {
			r := mustDecodeAs(t, loadFixture(t, "engine-next/state-"+name+".json"), "v3.4.0")
			id := r.Identity
			if id == nil {
				t.Fatal("Identity = nil, want the block the engine states")
			}
			if id.SchemaVersion != ResultIdentitySchemaV1 || id.EngineVersion != "v0.0.0-test" || id.PolicyDigest == "" ||
				id.HeadTreeDigest == "" || id.MeasurementProfileHash == "" || id.BaseSHA == "" {
				t.Errorf("Identity = %+v", *id)
			}
		})
	}
}

// From engine v3.4.0 the state schema requires identity: a report without it is refused. The releases before it carry
// none and still decode, with no identity, so that their uploads end unknown_engine_identity at the manifest.
func TestIdentityIsRequiredFromThePin(t *testing.T) {
	for _, tt := range []struct {
		release  string
		required bool
	}{
		{"v3.0.0", false}, {"v3.0.1", false}, {"v3.2.0", false}, {"v3.2.2", false}, {"v3.2.3", false},
		{"v3.4.0", true},
	} {
		t.Run(tt.release, func(t *testing.T) {
			doc := baseDoc(t)
			delete(doc, "identity")
			r, err := Decode(encode(t, doc), tt.release)
			if tt.required {
				wantError(t, err, "identity")
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if r.Identity != nil {
				t.Errorf("Identity = %+v for a report without one", *r.Identity)
			}
		})
	}
}

func TestIdentityRules(t *testing.T) {
	identity := func(t *testing.T, doc map[string]any, edit func(id map[string]any)) {
		id := parseDoc(t, loadFixture(t, "engine-next/state-new_violation.json"))["identity"].(map[string]any)
		if edit != nil {
			edit(id)
		}
		doc["identity"] = id
	}
	withPolicyRef := func(id map[string]any) {
		id["policy_ref"] = map[string]any{"ref": "refs/heads/main", "sha": strings.Repeat("a", 40)}
	}
	rows := []mutation{
		{name: "the engine's block", edit: func(t *testing.T, d map[string]any) { identity(t, d, nil) }},
		{name: "a block with a policy ref", edit: func(t *testing.T, d map[string]any) { identity(t, d, withPolicyRef) }},
		{name: "only the required keys", edit: func(t *testing.T, d map[string]any) {
			d["identity"] = map[string]any{"schema_version": ResultIdentitySchemaV1, "engine_version": "v3.3.0"}
		}},
		{name: "a digest that is empty", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["baseline_digest"] = "" })
		}},

		{name: "wrong identity version", wantLoc: "identity.schema_version", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["schema_version"] = "archfit.result-identity.v2" })
		}},
		{name: "no engine version", wantLoc: "identity.engine_version", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { delete(id, "engine_version") })
		}},
		{name: "no identity version", wantLoc: "identity.schema_version", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { delete(id, "schema_version") })
		}},
		{name: "a key the engine does not define", wantLoc: "identity", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["trusted"] = true })
		}},
		{name: "a digest that is not a string", wantLoc: "identity.policy_digest", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["policy_digest"] = 7 })
		}},
		{name: "a digest with a line break", wantLoc: "identity.head_tree_digest", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["head_tree_digest"] = "abc\ndef" })
		}},
		{name: "a digest with characters outside the token set", wantLoc: "identity.policy_digest", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["policy_digest"] = "<script>" })
		}},
		{name: "a digest that is too long", wantLoc: "identity.measurement_profile_hash", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["measurement_profile_hash"] = strings.Repeat("a", 5000) })
		}},
		{name: "an engine version with a space-separated command", wantLoc: "identity.engine_version", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["engine_version"] = "v3.3.0; rm -rf" })
		}},
		{name: "a policy ref without a commit", wantLoc: "identity.policy_ref.sha", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) { id["policy_ref"] = map[string]any{"ref": "refs/heads/main"} })
		}},
		{name: "a policy ref with an unknown key", wantLoc: "identity.policy_ref", edit: func(t *testing.T, d map[string]any) {
			identity(t, d, func(id map[string]any) {
				id["policy_ref"] = map[string]any{"ref": "refs/heads/main", "sha": "abc", "extra": 1}
			})
		}},
		{name: "a null identity", wantLoc: "identity", edit: func(t *testing.T, d map[string]any) { d["identity"] = nil }},
		{name: "an identity that is a list", wantLoc: "identity", edit: func(t *testing.T, d map[string]any) { d["identity"] = []any{} }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := baseDoc(t)
			row.edit(t, doc)
			r, err := Decode(encode(t, doc), "v3.4.0")
			if row.wantLoc != "" {
				wantError(t, err, row.wantLoc)
				for _, leak := range []string{"<script>", "rm -rf", "abc\\ndef", "archfit.result-identity.v2"} {
					if err != nil && strings.Contains(err.Error(), leak) {
						t.Errorf("error echoes the value %q: %v", leak, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if r.Identity == nil {
				t.Error("Identity = nil")
			}
		})
	}
}

// coverage.tools[].details (engine v3.3.0) is an optional list of one-line strings: validated, not kept.
func TestToolCoverageDetailsRules(t *testing.T) {
	projects := func(n int) []any {
		list := make([]any, n)
		for i := range list {
			list[i] = "project"
		}
		return list
	}
	rows := []mutation{
		{name: "absent"},
		{name: "empty list", edit: func(t *testing.T, d map[string]any) { set(t, d, []any{}, "coverage", "tools", 0, "details") }},
		{name: "project lines", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{"App.csproj: ok, net8.0, 12 files", "Lib.csproj: partial, 3 unresolved"}, "coverage", "tools", 0, "details")
		}},
		{name: "not a list", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, "App.csproj", "coverage", "tools", 0, "details")
		}},
		{name: "an item that is not a string", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{map[string]any{"status": "ok"}}, "coverage", "tools", 0, "details")
		}},
		{name: "a line break in an item", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{"a\nb"}, "coverage", "tools", 0, "details")
		}},
		{name: "more projects than the small lists hold", edit: func(t *testing.T, d map[string]any) {
			set(t, d, projects(maxSmallList+1), "coverage", "tools", 0, "details")
		}},
		{name: "a line over the ordinary text length", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{strings.Repeat("x", maxTextLen+1)}, "coverage", "tools", 0, "details")
		}},
		{name: "a line of the longest length", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{strings.Repeat("x", maxDetailLen)}, "coverage", "tools", 0, "details")
		}},
		{name: "a line over the longest length", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, []any{strings.Repeat("x", maxDetailLen+1)}, "coverage", "tools", 0, "details")
		}},
		{name: "as many items as the bound", edit: func(t *testing.T, d map[string]any) {
			set(t, d, projects(maxToolDetails), "coverage", "tools", 0, "details")
		}},
		{name: "one item over the bound", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, projects(maxToolDetails+1), "coverage", "tools", 0, "details")
		}},
		{name: "null", wantLoc: "coverage.tools[0].details", edit: func(t *testing.T, d map[string]any) {
			set(t, d, nil, "coverage", "tools", 0, "details")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := baseDoc(t)
			if row.edit != nil {
				row.edit(t, doc)
			}
			_, err := Decode(encode(t, doc), "v3.4.0")
			if row.wantLoc != "" {
				wantError(t, err, row.wantLoc)
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
		})
	}
}
