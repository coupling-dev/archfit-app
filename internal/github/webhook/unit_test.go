package webhook

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	secret := []byte(testSecret)
	payload := []byte(`{"action":"opened"}`)
	valid := sign(secret, payload)
	digest := strings.TrimPrefix(valid, "sha256=")

	flippedBody := append([]byte(nil), payload...)
	flippedBody[3] ^= 0x01
	flippedNibble := "f"
	if digest[0] == 'f' {
		flippedNibble = "0"
	}

	tests := []struct {
		name    string
		secret  []byte
		header  string
		body    []byte
		wantErr bool
	}{
		{"valid signature", secret, valid, payload, false},
		{"uppercase hex", secret, "sha256=" + strings.ToUpper(digest), payload, false},
		{"flipped body byte", secret, valid, flippedBody, true},
		{"flipped digest nibble", secret, "sha256=" + flippedNibble + digest[1:], payload, true},
		{"wrong secret", []byte("another-secret"), valid, payload, true},
		{"sha1 prefix", secret, "sha1=" + digest, payload, true},
		{"no prefix", secret, digest, payload, true},
		{"63 hex characters", secret, "sha256=" + digest[:63], payload, true},
		{"non-hex", secret, "sha256=" + strings.Repeat("z", 64), payload, true},
		{"empty header", secret, "", payload, true},
		{"empty secret", nil, sign(nil, payload), payload, true},
		{"empty body with a valid signature", secret, sign(secret, nil), nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyAny([][]byte{tt.secret}, tt.header, tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyAny() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyAnyAcceptsTheCurrentAndThePreviousSecret(t *testing.T) {
	current, previous := []byte("current-secret"), []byte("previous-secret")
	payload := []byte(`{"action":"opened"}`)
	tests := []struct {
		name    string
		secrets [][]byte
		header  string
		wantErr bool
	}{
		{"signed with the current secret", [][]byte{current, previous}, sign(current, payload), false},
		{"signed with the previous secret", [][]byte{current, previous}, sign(previous, payload), false},
		{"signed with neither", [][]byte{current, previous}, sign([]byte("another-secret"), payload), true},
		{"previous secret alone after the rotation", [][]byte{current}, sign(previous, payload), true},
		{"no secret configured", nil, sign(current, payload), true},
		{"an empty secret in the list fails closed", [][]byte{current, nil}, sign(current, payload), true},
		{"malformed header", [][]byte{current, previous}, "sha256=zz", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyAny(tt.secrets, tt.header, payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyAny() error = %v, wantErr %v", err, tt.wantErr)
			}
			for _, s := range [][]byte{current, previous} {
				if err != nil && strings.Contains(err.Error(), string(s)) {
					t.Errorf("error %q quotes a secret", err)
				}
			}
		})
	}
}

func TestVerifyUsesConstantTimeCompare(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "webhook.go", nil, 0)
	if err != nil {
		t.Fatalf("parse webhook.go: %v", err)
	}
	var verify *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "VerifyAny" && fn.Recv == nil {
			verify = fn
		}
	}
	if verify == nil {
		t.Fatal("VerifyAny not found in webhook.go")
	}

	usesHMACEqual := false
	ast.Inspect(verify.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			pkg, _ := n.X.(*ast.Ident)
			if pkg == nil {
				return true
			}
			switch name := pkg.Name + "." + n.Sel.Name; {
			case name == "hmac.Equal":
				usesHMACEqual = true
			case pkg.Name == "bytes" || pkg.Name == "subtle" || pkg.Name == "reflect" || pkg.Name == "slices":
				t.Errorf("VerifyAny uses %s; the digest comparison must be hmac.Equal", name)
			}
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && !isLengthOrNilCheck(n) {
				t.Errorf("VerifyAny compares with %s outside a length or nil check", n.Op)
			}
		}
		return true
	})
	if !usesHMACEqual {
		t.Error("VerifyAny does not call hmac.Equal")
	}
}

// isLengthOrNilCheck reports whether a comparison is `len(x) op y` or `x op nil`: the only ones that leak nothing.
func isLengthOrNilCheck(b *ast.BinaryExpr) bool {
	for _, side := range []ast.Expr{b.X, b.Y} {
		if id, ok := side.(*ast.Ident); ok && id.Name == "nil" {
			return true
		}
		if call, ok := side.(*ast.CallExpr); ok {
			if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "len" {
				return true
			}
		}
	}
	return false
}

func TestParseMapping(t *testing.T) {
	tests := []struct {
		eventType string
		action    string
		want      EventKind
	}{
		{"pull_request", "opened", PullOpened},
		{"pull_request", "reopened", PullOpened},
		{"pull_request", "ready_for_review", PullOpened},
		{"pull_request", "synchronize", PullSynchronized},
		{"pull_request", "closed", PullClosed},
		{"pull_request", "labeled", Ignored},
		{"pull_request", "edited", Ignored},
		{"pull_request_review", "submitted", ReviewChanged},
		{"pull_request_review", "edited", ReviewChanged},
		{"pull_request_review", "dismissed", ReviewChanged},
		{"pull_request_review", "labeled", Ignored},
		{"installation", "created", InstallationAdded},
		{"installation", "deleted", InstallationRemoved},
		{"installation", "suspend", Ignored},
		{"installation", "unsuspend", Ignored},
		{"installation", "new_permissions_accepted", Ignored},
		{"installation_repositories", "added", InstallationAdded},
		{"installation_repositories", "removed", ReposRemoved},
		{"ping", "", Ignored},
		{"issues", "opened", Ignored},
		{"", "opened", Ignored},
	}
	for _, tt := range tests {
		t.Run(tt.eventType+"/"+tt.action, func(t *testing.T) {
			var payload obj
			switch tt.eventType {
			case "installation":
				payload = installationPayload(tt.action, 6001)
			case "installation_repositories":
				payload = installationReposPayload(tt.action, 6001)
			default:
				payload = reviewPayload(tt.action, shaA)
			}
			if got := mustParse(t, tt.eventType, payload).Kind; got != tt.want {
				t.Errorf("Kind = %q, want %q", got, tt.want)
			}
		})
	}

	pushes := []struct {
		name    string
		payload obj
		want    EventKind
	}{
		{"default branch", pushPayload("refs/heads/main", "main"), DefaultBranchPush},
		{"other branch", pushPayload("refs/heads/feature", "main"), BranchPush},
		{"tag", pushPayload("refs/tags/main", "main"), Ignored},
		{"deletion", set(pushPayload("refs/heads/main", "main"), "deleted", true), Ignored},
	}
	for _, tt := range pushes {
		t.Run("push/"+tt.name, func(t *testing.T) {
			if got := mustParse(t, "push", tt.payload).Kind; got != tt.want {
				t.Errorf("Kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseFields(t *testing.T) {
	seen := map[EventKind]bool{}
	for _, tt := range fixtureCases {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse(tt.eventType, testDelivery, fixture(t, tt.file))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			want := tt.want
			want.DeliveryID = testDelivery
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse() =\n %+v, want\n %+v", got, want)
			}
			seen[got.Kind] = true
		})
	}
	for _, kind := range []EventKind{
		PullOpened, PullSynchronized, PullClosed, ReviewChanged,
		DefaultBranchPush, InstallationAdded, InstallationRemoved, ReposRemoved,
	} {
		if !seen[kind] {
			t.Errorf("no fixture covers kind %q", kind)
		}
	}

	t.Run("ignored carries only the installation ID", func(t *testing.T) {
		got := mustParse(t, "pull_request", prPayload("labeled", shaA))
		want := Event{Kind: Ignored, DeliveryID: testDelivery, InstallationID: 5001}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse() = %+v, want %+v", got, want)
		}
	})
}

func TestChangedPaths(t *testing.T) {
	commit := func(added, modified, removed []string) obj {
		return obj{"added": added, "modified": modified, "removed": removed}
	}
	distinctPaths := func(n int) []string {
		paths := make([]string, n)
		for i := range paths {
			paths[i] = fmt.Sprintf("dir/file%04d.go", i)
		}
		return paths
	}
	manyCommits := make([]any, 2048)
	for i := range manyCommits {
		manyCommits[i] = commit(nil, []string{"a.go"}, nil)
	}
	overlapping := []any{
		commit([]string{"b.go"}, []string{"a.go"}, nil),
		commit(nil, []string{"a.go", "c/d.go"}, nil),
		commit([]string{"b.go"}, []string{"c/d.go"}, nil),
	}

	tests := []struct {
		name   string
		mutate func(obj)
		want   []string
	}{
		{"three commits with overlapping files", func(p obj) { p["commits"] = overlapping },
			[]string{"a.go", "b.go", "c/d.go"}},
		{"file added then removed", func(p obj) {
			p["commits"] = []any{commit([]string{"tmp.go"}, []string{"a.go"}, nil), commit(nil, nil, []string{"tmp.go"})}
		}, []string{"a.go", "tmp.go"}},
		{"forced", func(p obj) { p["commits"] = overlapping; p["forced"] = true }, nil},
		{"2048 commits", func(p obj) { p["commits"] = manyCommits }, nil},
		{"3001 distinct paths", func(p obj) { p["commits"] = []any{commit(distinctPaths(3001), nil, nil)} }, nil},
		{"exactly 3000 paths", func(p obj) { p["commits"] = []any{commit(distinctPaths(3000), nil, nil)} },
			distinctPaths(3000)},
		{"no commits key", func(p obj) { delete(p, "commits") }, nil},
		{"empty commits list", func(p obj) { p["commits"] = []any{} }, nil},
		{"null commits", func(p obj) { p["commits"] = nil }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := pushPayload("refs/heads/main", "main")
			tt.mutate(payload)
			ev := mustParse(t, "push", payload)
			if ev.Kind != DefaultBranchPush {
				t.Fatalf("Kind = %q, want %q", ev.Kind, DefaultBranchPush)
			}
			if len(ev.ChangedPaths) != len(tt.want) || (len(tt.want) > 0 && !reflect.DeepEqual(ev.ChangedPaths, tt.want)) {
				t.Errorf("ChangedPaths has %d entries, want %d: %v", len(ev.ChangedPaths), len(tt.want), ev.ChangedPaths)
			}
			if tt.want == nil && ev.ChangedPaths != nil {
				t.Errorf("ChangedPaths = %#v, want nil for unknown", ev.ChangedPaths)
			}
		})
	}
}
