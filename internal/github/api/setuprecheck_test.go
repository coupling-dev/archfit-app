package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSetupRecheckAdapter(t *testing.T) {
	for _, tt := range []struct {
		name   string
		suites string
		status int
		marker bool
	}{
		{"accepted", `{"check_runs":[{"id":2,"name":"archfit","app":{"id":15368},"check_suite":{"id":12}}]}`, 201, true},
		{"refused", `{"check_runs":[{"id":2,"name":"archfit","app":{"id":15368},"check_suite":{"id":12}}]}`, 403, false},
		{"no suite", `{"check_runs":[]}`, 201, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := muxEnv(t, func(mux *http.ServeMux) {
				mux.HandleFunc("GET /repos/o/r/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("app_id") != "15368" || r.URL.Query().Get("check_name") != "archfit" {
						t.Error("wrong app_id")
					}
					_, _ = fmt.Fprint(w, tt.suites)
				})
				mux.HandleFunc("POST /repos/o/r/check-suites/12/rerequest", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })
				mux.HandleFunc("GET /repos/o/r/check-suites/12/check-runs", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprint(w, `{"check_runs":[{"name":"ci","html_url":"https://github.com/o/r/actions/runs/1/job/1"},{"name":"archfit","app":{"id":15368},"html_url":"https://github.com/o/r/actions/runs/1/job/2"}]}`)
				})
				mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"id":10,"slug":"archfit"}`) })
				mux.HandleFunc("GET /repos/o/r/issues/7/comments", func(w http.ResponseWriter, _ *http.Request) {
					body := "other"
					if tt.marker {
						body = "<!-- archfit:setup-recheck -->"
					}
					_, _ = fmt.Fprintf(w, `[{"id":1,"body":%q,"user":{"login":"archfit[bot]"}}]`, body)
				})
			})
			repo := Repo{RepoRef: RepoRef{InstallationID: 1, FullName: "o/r"}, ID: 1}
			ctx := context.Background()
			id, found, err := env.c.ActionsSuite(ctx, repo, strings.Repeat("a", 40))
			if err != nil || found != (id == 12) {
				t.Fatalf("suite %d %v %v", id, found, err)
			}
			if strings.Contains(tt.suites, "id") {
				if !found {
					t.Fatal("missing suite")
				}
				status, _ := env.c.RerequestSuite(ctx, repo, id)
				if status != tt.status {
					t.Fatalf("status %d", status)
				}
				link, err := env.c.RunURL(ctx, repo, id)
				if err != nil || !strings.HasSuffix(link, "/job/2") {
					t.Fatalf("link %q: %v", link, err)
				}
			} else if found {
				t.Fatal("unexpected suite")
			}
			marker, err := env.c.HasMarkerComment(ctx, repo, 7, "<!-- archfit:setup-recheck -->")
			if err != nil || marker != tt.marker {
				t.Fatalf("marker %v: %v", marker, err)
			}
		})
	}
}

func TestSearchSetupPulls(t *testing.T) {
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /search/issues", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("q") != "is:pr is:open head:archfit/setup user:acme" {
				t.Errorf("query %q", r.URL.Query().Get("q"))
			}
			_, _ = fmt.Fprint(w, `{"total_count":2,"incomplete_results":false,"items":[{"number":2,"repository_url":"https://api.github.com/repos/acme/a"},{"number":3,"repository_url":"https://api.github.com/repos/acme/b"}]}`)
		})
	})
	got, err := env.c.SearchSetupPulls(context.Background(), 1, "acme")
	if err != nil || len(got) != 2 || got[0].Repo != "acme/a" || got[0].Number != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
