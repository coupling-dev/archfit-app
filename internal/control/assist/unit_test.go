package assist

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

func TestValidateRequest(t *testing.T) {
	t.Parallel()
	edit := func(f func(*Request)) Request {
		r := validRequest()
		f(&r)
		return r
	}
	tests := []struct {
		name    string
		mode    Mode
		req     Request
		wantErr bool
	}{
		{"empty instruction", ModeManaged, edit(func(r *Request) { r.Instruction = "" }), true},
		{"2001-byte instruction", ModeManaged, edit(func(r *Request) { r.Instruction = strings.Repeat("x", 2001) }), true},
		{"branch main", ModeManaged, edit(func(r *Request) { r.Branch = "main" }), true},
		{"branch equal to the default branch", ModeManaged, edit(func(r *Request) {
			r.Repo.DefaultBranch = "archfit/trunk"
			r.Branch = "archfit/trunk"
		}), true},
		{"branch with dot-dot", ModeManaged, edit(func(r *Request) { r.Branch = "archfit/../x" }), true},
		{"branch with a space", ModeManaged, edit(func(r *Request) { r.Branch = "archfit/my draft" }), true},
		{"39-character SHA", ModeManaged, edit(func(r *Request) { r.HeadSHA = strings.Repeat("a", 39) }), true},
		{"upper-case SHA", ModeManaged, edit(func(r *Request) { r.HeadSHA = strings.Repeat("A", 40) }), true},
		{"empty By", ModeManaged, edit(func(r *Request) { r.By = "" }), true},
		{"unknown mode", Mode("auto"), validRequest(), true},
		{"invalid customer request", ModeCustomer, edit(func(r *Request) { r.Instruction = "" }), true},
		{"2000-byte instruction", ModeManaged, edit(func(r *Request) { r.Instruction = strings.Repeat("x", 2000) }), false},
		{"valid request", ModeManaged, validRequest(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixture()
			_, err := fx.assist().Propose(context.Background(), tt.mode, tt.req)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Propose() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Propose() error = %v, want ErrInvalidRequest", err)
			}
			if fx.consents.calls != 0 || len(fx.repos.reads) != 0 || fx.proposals.attemptCount() != 0 ||
				fx.budget.reserveCount() != 0 || fx.provider.callCount() != 0 || len(fx.actions.calls) != 0 {
				t.Errorf("a dependency was called for an invalid request")
			}
		})
	}
}

func TestPromptContainsOnlyPermittedContext(t *testing.T) {
	t.Parallel()
	const (
		policyMarker      = "POLICY-MARKER-5f1c"
		instructionMarker = "INSTRUCTION-MARKER-88d0"
	)
	forbidden := map[string]string{
		"archfit-report.json":  `{"findings":[{"id":"FINDING-MARKER-31aa"}]}`,
		"main.go":              "package main // SOURCE-MARKER-9c2e",
		"findings.sarif":       "SARIF-MARKER-4b7d",
		".archfit-labels.yaml": "labels: [LABELS-MARKER-0e6f]\n",
	}
	fx := newFixture()
	draft := draftYAML + "# " + policyMarker + "\n"
	fx.repos.files[fileKey{headA, policy.PolicyPath}] = []byte(draft)
	for path, content := range forbidden {
		fx.repos.files[fileKey{headA, path}] = []byte(content)
		fx.repos.files[fileKey{mainBranch, path}] = []byte(content)
	}
	req := validRequest()
	req.Instruction = "tighten the limits " + instructionMarker

	if _, err := fx.assist().Propose(context.Background(), ModeManaged, req); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if len(fx.provider.prompts) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(fx.provider.prompts))
	}
	p := fx.provider.prompts[0]
	all := p.System + p.User
	for name, want := range map[string]string{"policy": draft, "instruction": req.Instruction, "schema": string(policy.Schema())} {
		if !strings.Contains(all, want) {
			t.Errorf("prompt does not contain the %s", name)
		}
	}
	if !strings.Contains(p.System, string(policy.Schema())) || !strings.Contains(p.User, draft) {
		t.Errorf("schema must be in System and the policy in User")
	}
	for path, content := range forbidden {
		if strings.Contains(all, content) || strings.Contains(all, path) {
			t.Errorf("prompt leaks %s", path)
		}
	}
	for _, marker := range []string{"FINDING-MARKER", "SOURCE-MARKER", "SARIF-MARKER", "LABELS-MARKER", testRepo.FullName, req.By} {
		if strings.Contains(all, marker) {
			t.Errorf("prompt contains %q", marker)
		}
	}
	if p.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want 8192", p.MaxTokens)
	}
	for _, read := range fx.repos.reads {
		if _, leaked := forbidden[read.path]; leaked {
			t.Errorf("assist read %s", read.path)
		}
	}
}

func TestNormalizeAndValidateAnswer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{"plain YAML", editedYAML, editedYAML},
		{"plain YAML with surrounding blank lines", "\n\n" + editedYAML + "\n\n", editedYAML},
		{"YAML inside one code fence", "```yaml\n" + editedYAML + "```", editedYAML},
		{"YAML inside a bare code fence", "```\n" + editedYAML + "```\n", editedYAML},
		{"prose before a fence", "Here is the file:\n```yaml\n" + editedYAML + "```", ""},
		{"prose after a fence", "```yaml\n" + editedYAML + "```\nI lowered the limit.", ""},
		{"fence never closed", "```yaml\n" + editedYAML, ""},
		{"prose only", "I cannot change that file.", ""},
		{"empty", "", ""},
		{"blank", " \n\t\n", ""},
		{"empty fence", "```yaml\n```", ""},
		{"identical to input", draftYAML, ""},
		{"identical to input inside a fence", "```yaml\n" + draftYAML + "```", ""},
		{"65 KB", "version: 1\n# " + strings.Repeat("x", 65<<10) + "\n", ""},
		{"invalid UTF-8", "version: 1\nname: \xff\xfe\n", ""},
		{"NUL byte", "version: 1\nname: a\x00b\n", ""},
		{"tab-indented mapping", "version: 1\nrules:\n\tmax_fan_out: 7\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeAnswer(tt.text, []byte(draftYAML))
			if tt.want == "" {
				if !errors.Is(err, ErrInvalidProposal) {
					t.Fatalf("normalizeAnswer() = %q, %v; want ErrInvalidProposal", got, err)
				}
				if got != nil {
					t.Errorf("normalizeAnswer() kept %d bytes of a rejected answer", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeAnswer() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("normalizeAnswer() = %q, want %q", got, tt.want)
			}
		})
	}
}
