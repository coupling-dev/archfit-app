package webhook

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestParseRejectsIncompleteIdentity(t *testing.T) {
	kinds := []struct {
		name      string
		eventType string
		payload   func() obj
		repo      bool // identity includes the repository
		pull      bool // identity includes the pull request
		shaPath   string
	}{
		{"PullOpened", "pull_request", func() obj { return prPayload("opened", shaA) }, true, true, "pull_request.head.sha"},
		{"PullSynchronized", "pull_request", func() obj { return prPayload("synchronize", shaA) }, true, true, "pull_request.head.sha"},
		{"PullClosed", "pull_request", func() obj { return prPayload("closed", shaA) }, true, true, "pull_request.head.sha"},
		{"ReviewChanged", "pull_request_review", func() obj { return reviewPayload("submitted", shaA) }, true, true, "pull_request.head.sha"},
		{"DefaultBranchPush", "push", func() obj { return pushPayload("refs/heads/main", "main") }, true, false, "after"},
		{"InstallationAdded", "installation", func() obj { return installationPayload("created", 6001) }, false, false, ""},
		{"InstallationAdded by repositories", "installation_repositories", func() obj { return installationReposPayload("added", 6001) }, false, false, ""},
		{"InstallationRemoved", "installation", func() obj { return installationPayload("deleted", 6001) }, false, false, ""},
		{"ReposRemoved", "installation_repositories", func() obj { return installationReposPayload("removed", 6001) }, false, false, ""},
	}

	for _, k := range kinds {
		breaks := map[string]func(obj){
			"missing installation": func(p obj) { delete(p, "installation") },
			"installation.id 0":    func(p obj) { set(p, "installation.id", 0) },
			"negative installation.id": func(p obj) {
				set(p, "installation.id", -5)
			},
		}
		if k.repo {
			breaks["missing repository"] = func(p obj) { delete(p, "repository") }
			breaks["full_name without a slash"] = func(p obj) { set(p, "repository.full_name", "shop") }
			breaks["empty full_name"] = func(p obj) { set(p, "repository.full_name", "") }
			breaks["head SHA of 39 characters"] = func(p obj) { set(p, k.shaPath, shaA[:39]) }
			breaks["uppercase head SHA"] = func(p obj) { set(p, k.shaPath, strings.ToUpper(shaA)) }
			breaks["empty head SHA"] = func(p obj) { set(p, k.shaPath, "") }
			breaks["non-hex head SHA"] = func(p obj) { set(p, k.shaPath, strings.Repeat("g", 40)) }
		}
		if k.pull {
			breaks["PR number 0"] = func(p obj) { set(p, "pull_request.number", 0) }
			breaks["missing pull_request"] = func(p obj) { delete(p, "pull_request") }
		}

		for name, mutate := range breaks {
			t.Run(k.name+"/"+name, func(t *testing.T) {
				payload := k.payload()
				mutate(payload)
				got, err := Parse(k.eventType, testDelivery, body(t, payload))
				if err == nil {
					t.Errorf("Parse() = %+v, want an error", got)
				}
				if !reflect.DeepEqual(got, Event{}) {
					t.Errorf("Parse() returned %+v with an error, want the zero Event", got)
				}
			})
		}

		t.Run(k.name+"/empty deliveryID", func(t *testing.T) {
			got, err := Parse(k.eventType, "", body(t, k.payload()))
			if err == nil || !reflect.DeepEqual(got, Event{}) {
				t.Errorf("Parse() = %+v, %v; want the zero Event and an error", got, err)
			}
		})
	}
}

func TestParseMalformedJSON(t *testing.T) {
	valid := string(body(t, prPayload("opened", shaA)))
	bodies := map[string]string{
		"truncated JSON":  valid[:len(valid)/2],
		"JSON array":      `[{"action":"opened"}]`,
		"null":            `null`,
		"empty body":      ``,
		"wrong type":      `{"action":"opened","installation":{"id":"5001"}}`,
		"trailing second": valid + valid,
	}
	for _, eventType := range []string{
		"pull_request", "pull_request_review", "push", "installation", "installation_repositories",
	} {
		for name, payload := range bodies {
			t.Run(eventType+"/"+name, func(t *testing.T) {
				got, err := Parse(eventType, testDelivery, []byte(payload))
				if err == nil {
					t.Errorf("Parse() = %+v, want an error", got)
				}
			})
		}
	}
}

func TestEventTypeHeaderCannotForgeIdentity(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		presentedAs string
		// identity the signed body contains
		installationID int64
	}{
		{"push body as pull_request", "push_default_branch.json", "pull_request", 5002},
		{"pull_request body as installation", "pull_request_opened.json", "installation", 5001},
		{"pull_request body as push", "pull_request_opened.json", "push", 5001},
		{"pull_request body as installation_repositories", "pull_request_opened.json", "installation_repositories", 5001},
		{"installation body as pull_request", "installation_created.json", "pull_request", 6001},
		{"installation body as installation_repositories", "installation_created.json", "installation_repositories", 6001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.presentedAs, testDelivery, fixture(t, tt.file))
			if err != nil {
				return
			}
			if got.Kind != Ignored {
				t.Fatalf("Kind = %q, want an error or %q", got.Kind, Ignored)
			}
			if got.Repo != "" || got.HeadRepo != "" {
				t.Errorf("ignored event names a repository: %+v", got)
			}
			if got.InstallationID != 0 && got.InstallationID != tt.installationID {
				t.Errorf("InstallationID = %d, absent from the signed body", got.InstallationID)
			}
		})
	}

	// A body whose action is valid under another type still yields only the identity it contains.
	t.Run("pull_request edited body as pull_request_review", func(t *testing.T) {
		got := mustParse(t, "pull_request_review", prPayload("edited", shaA))
		if got.InstallationID != 5001 || got.Repo != "acme/shop" {
			t.Errorf("identity %d %q does not come from the body", got.InstallationID, got.Repo)
		}
	})
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	for _, tt := range fixtureCases {
		t.Run(tt.file, func(t *testing.T) {
			plain := fixture(t, tt.file)
			var doc any
			if err := json.Unmarshal(plain, &doc); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			addUnknownFields(doc)

			want, err := Parse(tt.eventType, testDelivery, plain)
			if err != nil {
				t.Fatalf("Parse(plain) error = %v", err)
			}
			got, err := Parse(tt.eventType, testDelivery, body(t, doc))
			if err != nil {
				t.Fatalf("Parse(extended) error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse(extended) =\n %+v, want\n %+v", got, want)
			}
		})
	}
}

// addUnknownFields adds fields GitHub does not send today to every object, top-level and nested.
func addUnknownFields(v any) {
	switch v := v.(type) {
	case map[string]any:
		for _, child := range v {
			addUnknownFields(child)
		}
		v["x_future_scalar"] = "value"
		v["x_future_object"] = map[string]any{"id": 999, "full_name": "evil/other", "list": []any{1, "two"}}
	case []any:
		for _, child := range v {
			addUnknownFields(child)
		}
	}
}

func TestErrorsCarryNoPayload(t *testing.T) {
	const marker = "MARKER-c0ffee"
	secret := []byte("secret-" + marker)

	marked := func(base obj) obj {
		set(base, "pull_request.title", "title "+marker)
		set(base, "pull_request.head.ref", "branch-"+marker)
		set(base, "pull_request.base.ref", "base-"+marker)
		set(base, "ref", "refs/heads/"+marker)
		set(base, "repository.default_branch", marker)
		base["commits"] = []any{obj{"added": []string{"dir-" + marker + "/file.go"}}}
		return base
	}
	pr := func(mutate func(obj)) []byte {
		p := marked(prPayload("opened", shaA))
		mutate(p)
		return body(t, p)
	}
	push := func(mutate func(obj)) []byte {
		p := marked(pushPayload("", ""))
		mutate(p)
		return body(t, p)
	}
	markedBody := pr(func(obj) {})
	validSig := sign(secret, markedBody)

	var errs []error
	verify := func(secret []byte, header string, body []byte) {
		errs = append(errs, Verify(secret, header, body))
	}
	parse := func(eventType, deliveryID string, body []byte) {
		_, err := Parse(eventType, deliveryID, body)
		errs = append(errs, err)
	}

	verify(nil, validSig, markedBody)
	verify(secret, "", markedBody)
	verify(secret, marker, markedBody)
	verify(secret, "sha256="+marker, markedBody)
	verify(secret, "sha256="+strings.Repeat("z", 64), markedBody)
	verify(secret, sign([]byte("other"), markedBody), markedBody)
	verify(secret, validSig, append([]byte(marker), markedBody...))

	parse("pull_request", "", markedBody)
	parse("pull_request", testDelivery, markedBody[:len(markedBody)-10])
	parse("pull_request", testDelivery, []byte(`{"action":"`+marker+`","installation":{"id":"`+marker+`"}}`))
	parse("pull_request", testDelivery, []byte(marker))
	parse("pull_request", testDelivery, pr(func(p obj) { delete(p, "installation") }))
	parse("pull_request", testDelivery, pr(func(p obj) { set(p, "repository.full_name", marker) }))
	parse("pull_request", testDelivery, pr(func(p obj) { set(p, "pull_request.number", 0) }))
	parse("pull_request", testDelivery, pr(func(p obj) { set(p, "pull_request.head.sha", marker) }))
	parse("push", testDelivery, push(func(p obj) { set(p, "after", marker) }))
	parse("push", testDelivery, push(func(p obj) { set(p, "repository.full_name", marker) }))

	for i, err := range errs {
		if err == nil {
			t.Errorf("case %d: expected an error", i)
			continue
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("case %d: error text leaks payload or secret: %q", i, err)
		}
	}
}

func TestWebhookImportsOnlyStdlib(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const self = "github.com/alexei-led/archfit-app/internal/github/webhook"
	for _, pkg := range strings.Fields(string(out)) {
		if pkg != self {
			t.Errorf("non-standard dependency: %s", pkg)
		}
	}
}
