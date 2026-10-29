package webhook

import (
	"reflect"
	"strings"
	"testing"
)

func commentPayload(action string, onPull bool) obj {
	issue := obj{"number": 46}
	if onPull {
		issue["pull_request"] = obj{"url": "https://api.github.com/repos/acme/shop/pulls/46"}
	}
	return obj{
		"action":       action,
		"issue":        issue,
		"comment":      obj{"id": 9001, "body": "hello", "user": obj{"login": "olivia", "type": "User"}},
		"repository":   repoObj("acme/shop", "main"),
		"installation": obj{"id": 5001},
	}
}

func TestIssueCommentFixtures(t *testing.T) {
	tests := []struct {
		file string
		want Event
	}{
		{"issue_comment_created_pull.json", Event{
			Kind: PullCommented, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: true, PullRequest: 46,
			DefaultBranch: "main", CommentID: 9001, Author: "agent-bot[bot]", AuthorType: "Bot",
			Body: "<!-- archfit:decision-request -->\n```json\n{}\n```\n<!-- /archfit:decision-request -->",
		}},
		{"issue_comment_created_issue.json", Event{Kind: Ignored, InstallationID: 5001}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse("issue_comment", testDelivery, fixture(t, tt.file))
			if err != nil {
				t.Fatal(err)
			}
			tt.want.DeliveryID = testDelivery
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() =\n %+v, want\n %+v", got, tt.want)
			}
		})
	}
}

func TestIssueCommentMapping(t *testing.T) {
	tests := []struct {
		action string
		onPull bool
		want   EventKind
	}{
		{"created", true, PullCommented},
		{"created", false, Ignored},
		{"edited", true, Ignored},
		{"deleted", true, Ignored},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			if got := mustParse(t, "issue_comment", commentPayload(tt.action, tt.onPull)).Kind; got != tt.want {
				t.Errorf("Kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIssueCommentRejectsIncompleteIdentity(t *testing.T) {
	breaks := map[string]func(obj){
		"missing installation": func(p obj) { delete(p, "installation") },
		"missing repository":   func(p obj) { delete(p, "repository") },
		"issue number 0":       func(p obj) { set(p, "issue.number", 0) },
		"comment id 0":         func(p obj) { set(p, "comment.id", 0) },
		"no author":            func(p obj) { set(p, "comment.user.login", "") },
		"no comment":           func(p obj) { delete(p, "comment") },
	}
	for name, breakIt := range breaks {
		t.Run(name, func(t *testing.T) {
			p := commentPayload("created", true)
			breakIt(p)
			ev, err := Parse("issue_comment", testDelivery, body(t, p))
			if err == nil {
				t.Fatalf("Parse accepted it: %+v", ev)
			}
			if ev.Kind != "" {
				t.Errorf("Event with an error has kind %q", ev.Kind)
			}
			if strings.Contains(err.Error(), "olivia") {
				t.Errorf("error echoes payload text: %v", err)
			}
		})
	}
}
