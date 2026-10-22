package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testSecret   = "test-webhook-secret"
	testDelivery = "72d3162e-cc78-11e3-81ab-4c9367dc0958"

	shaA = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	shaB = "2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e"
)

// fixtureCase is one real delivery under testdata/ and the Event it must yield (DeliveryID aside).
type fixtureCase struct {
	file      string
	eventType string
	want      Event
}

var fixtureCases = []fixtureCase{
	{"pull_request_opened.json", "pull_request", Event{
		Kind: PullOpened, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: true, PullRequest: 42,
		HeadSHA: shaA, HeadRepo: "acme/shop", BaseRef: "main",
	}},
	{"pull_request_synchronize.json", "pull_request", Event{
		Kind: PullSynchronized, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: true, PullRequest: 43,
		HeadSHA: shaB, HeadRepo: "octocat/shop", BaseRef: "main",
	}},
	{"pull_request_closed.json", "pull_request", Event{
		Kind: PullClosed, InstallationID: 5001, Repo: "acme/shop", RepoPrivate: true, PullRequest: 44,
		HeadSHA: "3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f", HeadRepo: "", BaseRef: "release/1.x",
	}},
	{"pull_request_review_submitted.json", "pull_request_review", Event{
		Kind: ReviewChanged, InstallationID: 5002, Repo: "acme/lib", PullRequest: 45,
		HeadSHA: "4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70", HeadRepo: "acme/lib", BaseRef: "trunk",
	}},
	{"push_default_branch.json", "push", Event{
		Kind: DefaultBranchPush, InstallationID: 5002, Repo: "acme/lib",
		HeadSHA: "6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192", HeadRepo: "acme/lib", BaseRef: "trunk",
		Branch: "trunk", Pusher: "octocat",
		ChangedPaths: []string{".archfit.yaml", "go.mod", "internal/api/handler.go", "legacy/old.go"},
	}},
	{"installation_created.json", "installation", Event{Kind: InstallationAdded, InstallationID: 6001, Account: "acme"}},
	{"installation_deleted.json", "installation", Event{Kind: InstallationRemoved, InstallationID: 6001, Account: "acme"}},
	{"installation_repositories_added.json", "installation_repositories", Event{
		Kind: InstallationAdded, InstallationID: 6002, Account: "acme",
	}},
	{"installation_repositories_removed.json", "installation_repositories", Event{
		Kind: ReposRemoved, InstallationID: 6002, Account: "acme",
	}},
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type obj = map[string]any

func body(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// set writes v at a dotted path of nested objects, creating them as needed.
func set(m obj, path string, v any) obj {
	keys := strings.Split(path, ".")
	cur := m
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(obj)
		if !ok {
			next = obj{}
			cur[k] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = v
	return m
}

func repoObj(fullName, defaultBranch string) obj {
	return obj{"full_name": fullName, "private": false, "default_branch": defaultBranch}
}

func prPayload(action, headSHA string) obj {
	return obj{
		"action": action,
		"pull_request": obj{
			"number": 7,
			"title":  "A title",
			"head":   obj{"ref": "feature", "sha": headSHA, "repo": obj{"full_name": "acme/shop"}},
			"base":   obj{"ref": "main", "repo": obj{"full_name": "acme/shop"}},
		},
		"repository":   repoObj("acme/shop", "main"),
		"installation": obj{"id": 5001},
	}
}

func reviewPayload(action, headSHA string) obj {
	p := prPayload(action, headSHA)
	p["review"] = obj{"id": 80, "state": "approved"}
	return p
}

func pushPayload(ref, defaultBranch string) obj {
	return obj{
		"ref":     ref,
		"after":   shaA,
		"deleted": false,
		"forced":  false,
		"commits": []any{
			obj{"added": []string{"a.go"}, "modified": []string{}, "removed": []string{}},
		},
		"repository":   repoObj("acme/shop", defaultBranch),
		"installation": obj{"id": 5001},
	}
}

func installationPayload(action string, id int64) obj {
	return obj{
		"action":       action,
		"installation": obj{"id": id, "account": obj{"login": "acme"}},
		"repositories": []any{obj{"full_name": "acme/shop"}},
	}
}

func installationReposPayload(action string, id int64) obj {
	return obj{
		"action":               action,
		"installation":         obj{"id": id},
		"repositories_added":   []any{obj{"full_name": "acme/lib"}},
		"repositories_removed": []any{obj{"full_name": "acme/old"}},
	}
}

func mustParse(t *testing.T, eventType string, payload any) Event {
	t.Helper()
	ev, err := Parse(eventType, testDelivery, body(t, payload))
	if err != nil {
		t.Fatalf("Parse(%q): unexpected error: %v", eventType, err)
	}
	return ev
}
