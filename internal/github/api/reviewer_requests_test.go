package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestReviewerRequestsUseCurrentRequestsAndOnlyMissingPrincipalInputs(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET api.github.com/repos/o/r/pulls/3/requested_reviewers", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"users": []any{map[string]any{"login": "existing-user"}},
				"teams": []any{map[string]any{"slug": "architects", "organization": map[string]any{"login": "o"}}},
			})
		})
		mux.HandleFunc("POST api.github.com/repos/o/r/pulls/3/requested_reviewers", func(w http.ResponseWriter, r *http.Request) {
			var body map[string][]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			if !reflect.DeepEqual(body["reviewers"], []string{"new-owner"}) || !reflect.DeepEqual(body["team_reviewers"], []string{"architects"}) {
				t.Errorf("request body = %+v, want only missing owner and team", body)
			}
			writeJSON(w, http.StatusCreated, map[string]any{})
		})
	})
	ctx := context.Background()
	users, teams, err := env.c.RequestedReviewers(ctx, repoA, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(users, []string{"existing-user"}) || !reflect.DeepEqual(teams, []string{"o/architects"}) {
		t.Fatalf("requested reviewers = %v / %v", users, teams)
	}
	if err := env.c.RequestReviewers(ctx, repoA, 3, []string{"new-owner"}, []string{"o/architects"}); err != nil {
		t.Fatal(err)
	}
	requests := env.gh.requests()
	if len(requests) != 2 || requests[1].Method != http.MethodPost {
		t.Fatalf("requests = %v, want GET then POST", requests)
	}
	if err := env.c.RequestReviewers(ctx, repoA, 3, nil, nil); err != nil {
		t.Fatalf("empty reviewer request: %v", err)
	}
	if len(env.gh.requests()) != 2 {
		t.Fatal("empty request issued a GitHub call")
	}
}

func TestRequestReviewersRejectsInvalidPrincipalsBeforeWriting(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {})
	for _, test := range []struct {
		users []string
		teams []string
	}{
		{users: []string{"o/owner"}},
		{teams: []string{"other-org/architects"}},
		{teams: []string{"o/../../settings"}},
	} {
		if err := env.c.RequestReviewers(context.Background(), repoA, 3, test.users, test.teams); err == nil {
			t.Errorf("RequestReviewers(%v, %v) = nil, want validation error", test.users, test.teams)
		}
	}
	if len(env.gh.requests()) != 0 {
		t.Fatalf("invalid reviewer request reached GitHub: %v", env.gh.requests())
	}
}
