package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func queueNode(pos int, head string, pr int) map[string]any {
	return map[string]any{
		"position":    pos,
		"headCommit":  map[string]any{"oid": head},
		"baseCommit":  map[string]any{"oid": strings.Repeat("b", 40)},
		"pullRequest": map[string]any{"number": pr, "headRefOid": strings.Repeat("p", 40), "isCrossRepository": pr == 9},
	}
}

func TestMergeQueueReadsEveryPageInPositionOrder(t *testing.T) {
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	var branches []string
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var req struct {
				Variables map[string]any `json:"variables"`
			}
			_ = json.Unmarshal(raw, &req)
			branches = append(branches, req.Variables["branch"].(string))
			page := map[string]any{"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "c1"},
				"nodes": []any{queueNode(2, strings.Repeat("2", 40), 12)}}
			if req.Variables["after"] == "c1" {
				page = map[string]any{"pageInfo": map[string]any{"hasNextPage": false},
					"nodes": []any{queueNode(1, strings.Repeat("1", 40), 9)}}
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"mergeQueue": map[string]any{"entries": page}}}})
		})
	})
	got, err := env.c.MergeQueue(context.Background(), ref, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []QueueEntry{
		{Position: 1, HeadSHA: strings.Repeat("1", 40), BaseSHA: strings.Repeat("b", 40), PullRequest: 9, PullHeadSHA: strings.Repeat("p", 40), CrossRepo: true},
		{Position: 2, HeadSHA: strings.Repeat("2", 40), BaseSHA: strings.Repeat("b", 40), PullRequest: 12, PullHeadSHA: strings.Repeat("p", 40)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(branches, []string{"main", "main"}) {
		t.Fatalf("branches %v", branches)
	}
}

func TestMergeQueueNeverGuessesMembership(t *testing.T) {
	ref := RepoRef{InstallationID: 7, FullName: "acme/app"}
	tests := []struct {
		name    string
		answer  any
		wantErr error // nil: any error
		empty   bool  // no error, no entries
	}{
		{"no queue on the branch", map[string]any{"data": map[string]any{"repository": map[string]any{"mergeQueue": nil}}}, nil, true},
		{"permission missing", map[string]any{"data": map[string]any{"repository": map[string]any{"mergeQueue": nil}},
			"errors": []any{map[string]any{"type": "FORBIDDEN", "message": "Resource not accessible by integration"}}}, ErrQueueForbidden, false},
		{"other GraphQL error", map[string]any{"errors": []any{map[string]any{"type": "INTERNAL"}}}, nil, false},
		{"rate limited", map[string]any{"errors": []any{map[string]any{"type": "RATE_LIMITED"}}}, ErrRateLimited, false},
		{"repository gone", map[string]any{"data": map[string]any{"repository": nil}}, ErrNotFound, false},
		{"entry without a head commit", map[string]any{"data": map[string]any{"repository": map[string]any{"mergeQueue": map[string]any{"entries": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{map[string]any{"position": 1, "pullRequest": map[string]any{"number": 1}}}}}}}}, nil, false},
		{"endless queue", map[string]any{"data": map[string]any{"repository": map[string]any{"mergeQueue": map[string]any{"entries": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "x"}, "nodes": []any{}}}}}}, ErrQueueTooLong, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, tt.answer) })
			})
			got, err := env.c.MergeQueue(context.Background(), ref, "main")
			if tt.empty {
				if err != nil || len(got) != 0 {
					t.Fatalf("got %v, %v; want no entries and no error", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got %v and no error; want an error", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v; want %v", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "Resource not accessible") {
				t.Fatalf("error quotes GitHub's message: %v", err)
			}
		})
	}
}
