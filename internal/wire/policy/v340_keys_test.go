package policy

import "testing"

// Engine v3.4.0 added module_review.change_gate and the top-level target_branch; the closed key set still refuses
// anything the schema does not define.
func TestEngineV340KeysAreAccepted(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		invalid bool
	}{
		{name: "change_gate", policy: "version: 2\nmodule_review:\n  change_gate: fail\n"},
		{name: "change_gate beside the other module_review keys", policy: "version: 2\nmodule_review:\n  stale_after: 90d\n  gate: warn\n  change_gate: warn\n"},
		{name: "target_branch", policy: "version: 2\ntarget_branch: main\n"},
		{name: "both", policy: "version: 2\ntarget_branch: release/1\nmodule_review:\n  change_gate: off\n"},
		{name: "unknown top-level key", policy: "version: 2\ntarget_branches: main\n", invalid: true},
		{name: "unknown module_review key", policy: "version: 2\nmodule_review:\n  change_gates: fail\n", invalid: true},
		{name: "change_gate that is not a string", policy: "version: 2\nmodule_review:\n  change_gate: [fail]\n", invalid: true},
		{name: "target_branch that is not a string", policy: "version: 2\ntarget_branch: [main]\n", invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := Diagnose([]byte(tt.policy))
			if tt.invalid != (len(diags) > 0) {
				t.Errorf("Diagnose = %+v, invalid want %v", diags, tt.invalid)
			}
			if tt.name == "unknown top-level key" {
				if err := Validate([]byte(tt.policy)); err == nil {
					t.Error("Validate accepted a key the schema does not define")
				}
				return
			}
			if !tt.invalid {
				if err := Validate([]byte(tt.policy)); err != nil {
					t.Errorf("Validate = %v, want nil", err)
				}
			}
		})
	}
}
