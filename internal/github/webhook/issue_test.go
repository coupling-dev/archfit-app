package webhook

import (
	"reflect"
	"strings"
	"testing"
)

func issuePayload(action string) obj {
	return obj{
		"action":       action,
		"issue":        obj{"number": 48, "body": "/archfit request why", "user": obj{"login": "worker-1", "type": "User"}},
		"repository":   repoObj("acme/shop", "main"),
		"installation": obj{"id": 5001},
	}
}

func TestIssuesOpenedFixture(t *testing.T) {
	got, err := Parse("issues", testDelivery, fixture(t, "issues_opened.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := Event{
		Kind: IssueOpened, DeliveryID: testDelivery, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: true,
		DefaultBranch: "main", Issue: 48, Author: "worker-1", AuthorType: "User",
		Body: "/archfit request tc needs the retry helper\n\n```text\npkg_tc -> pkg_util: graph: not an allowed dependency\n  balance: 9/10 (min 7)\n```\n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse() =\n %+v, want\n %+v", got, want)
	}
}

func TestIssuesMapping(t *testing.T) {
	tests := []struct {
		name string
		p    obj
		want EventKind
	}{
		{"opened", issuePayload("opened"), IssueOpened},
		{"edited", issuePayload("edited"), Ignored},
		{"reopened", issuePayload("reopened"), Ignored},
		{"closed", issuePayload("closed"), Ignored},
		{"a pull request", func() obj {
			p := issuePayload("opened")
			set(p, "issue.pull_request", obj{"url": "x"})
			return p
		}(), Ignored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustParse(t, "issues", tt.p).Kind; got != tt.want {
				t.Errorf("Kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIssuesRejectsIncompleteIdentity(t *testing.T) {
	breaks := map[string]func(obj){
		"missing installation": func(p obj) { delete(p, "installation") },
		"missing repository":   func(p obj) { delete(p, "repository") },
		"issue number 0":       func(p obj) { set(p, "issue.number", 0) },
		"no author":            func(p obj) { set(p, "issue.user.login", "") },
	}
	for name, breakIt := range breaks {
		t.Run(name, func(t *testing.T) {
			p := issuePayload("opened")
			breakIt(p)
			ev, err := Parse("issues", testDelivery, body(t, p))
			if err == nil {
				t.Fatalf("Parse accepted it: %+v", ev)
			}
			if strings.Contains(err.Error(), "worker-1") {
				t.Errorf("error echoes payload text: %v", err)
			}
		})
	}
}
