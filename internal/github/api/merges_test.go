package api

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestMergesContract(t *testing.T) {
	ctx := context.Background()
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /repos/o/r/pulls/7/merge", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"sha": mergeSHA, "merged": true, "message": "Pull Request successfully merged"})
		})
		mux.HandleFunc("PUT /repos/o/r/pulls/7/update-branch", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusAccepted, map[string]any{"message": "Updating pull request branch.", "url": "https://github.com/o/r/pull/7"})
		})
	})

	got, err := env.c.MergePull(ctx, repoA, 7, testSHA1)
	if err != nil || got != mergeSHA {
		t.Fatalf("MergePull = %q, %v; want the merge commit SHA", got, err)
	}
	merge := findRequest(t, env.gh.requests(), "PUT", "/repos/o/r/pulls/7/merge")
	if want := map[string]any{"merge_method": "squash", "sha": testSHA1}; !reflect.DeepEqual(decodeBody(t, merge), want) {
		t.Errorf("merge body = %s", merge.Body)
	}
	if merge.Auth != inst1 {
		t.Errorf("merge Authorization = %q", merge.Auth)
	}

	if err := env.c.UpdateBranch(ctx, repoA, 7, testSHA1); err != nil {
		t.Fatalf("UpdateBranch (202): %v", err)
	}
	update := findRequest(t, env.gh.requests(), "PUT", "/repos/o/r/pulls/7/update-branch")
	if want := map[string]any{"expected_head_sha": testSHA1}; !reflect.DeepEqual(decodeBody(t, update), want) {
		t.Errorf("update-branch body = %s", update.Body)
	}
	if update.Auth != inst1 {
		t.Errorf("update-branch Authorization = %q", update.Auth)
	}

	env.gh.reset()
	for _, sha := range badSHAs {
		if _, err := env.c.MergePull(ctx, repoA, 7, sha); err == nil {
			t.Errorf("MergePull head %q: want error", sha)
		}
		if err := env.c.UpdateBranch(ctx, repoA, 7, sha); err == nil {
			t.Errorf("UpdateBranch head %q: want error", sha)
		}
	}
	for _, n := range []int{0, -1} {
		if _, err := env.c.MergePull(ctx, repoA, n, testSHA1); err == nil {
			t.Errorf("MergePull number %d: want error", n)
		}
		if err := env.c.UpdateBranch(ctx, repoA, n, testSHA1); err == nil {
			t.Errorf("UpdateBranch number %d: want error", n)
		}
	}
	if reqs := env.gh.requests(); len(reqs) != 0 {
		t.Errorf("guards sent %v", reqs)
	}
}

func TestMergeRefusalsAreTold(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error // nil: a plain error
	}{
		// The 405 answers measured on the test repository (design D.2).
		{"required check expected", http.StatusMethodNotAllowed, `{"message":"Required status check \"archfit/gate\" is expected."}`, ErrNotMergeable},
		{"required check in progress", http.StatusMethodNotAllowed, `{"message":"Required status check \"archfit/gate\" is in progress."}`, ErrNotMergeable},
		{"required check from another app", http.StatusMethodNotAllowed, `{"message":"Required status check \"archfit/gate\" was not set by the expected GitHub app."}`, ErrNotMergeable},
		{"head moved", http.StatusConflict, `{"message":"Head branch was modified. Review and try the merge again."}`, ErrHeadChanged},
		{"validation failed", http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`, ErrUnprocessable},
		{"gone", http.StatusNotFound, `{"message":"Not Found"}`, ErrNotFound},
		{"no permission", http.StatusForbidden, `{"message":"Resource not accessible by integration"}`, nil},
		{"server error", http.StatusBadGateway, `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(reply{status: tt.status, body: tt.body}))
			_, err := env.c.MergePull(context.Background(), repoA, 7, testSHA1)
			if tt.want != nil {
				wantErrIs(t, "MergePull", err, tt.want)
			} else {
				wantPlainErr(t, "MergePull", err)
			}
			if tt.status != http.StatusNotFound && Status(err) != tt.status {
				t.Errorf("Status = %d, want %d", Status(err), tt.status)
			}
			if n := len(env.gh.requests()); n != 1 {
				t.Errorf("requests = %d, want 1: a merge is never repeated", n)
			}
		})
	}

	for name, answer := range map[string]string{
		"not merged":      `{"sha":"` + mergeSHA + `","merged":false}`,
		"no merge commit": `{"merged":true}`,
		"not a SHA":       `{"sha":"main","merged":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, script(reply{status: http.StatusOK, body: answer}))
			if sha, err := env.c.MergePull(context.Background(), repoA, 7, testSHA1); err == nil || sha != "" {
				t.Errorf("MergePull = %q, %v; want an error", sha, err)
			}
		})
	}
}

func TestUpdateBranchRefusalsAreTold(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error // nil: a plain error
	}{
		{"head is not the expected SHA", http.StatusUnprocessableEntity, ErrUnprocessable},
		{"gone", http.StatusNotFound, ErrNotFound},
		{"no permission", http.StatusForbidden, nil},
		{"server error", http.StatusBadGateway, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(reply{status: tt.status, body: `{"message":"no"}`}))
			err := env.c.UpdateBranch(context.Background(), repoA, 7, testSHA1)
			if tt.want != nil {
				wantErrIs(t, "UpdateBranch", err, tt.want)
			} else {
				wantPlainErr(t, "UpdateBranch", err)
			}
			if tt.status != http.StatusNotFound && Status(err) != tt.status {
				t.Errorf("Status = %d, want %d", Status(err), tt.status)
			}
			if n := len(env.gh.requests()); n != 1 {
				t.Errorf("requests = %d, want 1: a branch update is never repeated", n)
			}
		})
	}
}

func TestCompareContract(t *testing.T) {
	ctx := context.Background()
	files := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"filename": fmt.Sprintf("f%d.go", i), "status": "modified"}
		}
		return out
	}
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/compare/{basehead}", func(w http.ResponseWriter, r *http.Request) {
			switch r.PathValue("basehead") {
			case testSHA1 + "..." + testSHA2:
				writeJSON(w, http.StatusOK, map[string]any{"status": "ahead", "files": []map[string]any{
					{"filename": ".archfit.yaml", "status": "modified"},
					{"filename": "policy.yaml", "previous_filename": ".archfit.yaml", "status": "renamed"},
					{"filename": "a.go", "status": "added"},
				}})
			case testSHA1 + "..." + testSHA3:
				writeJSON(w, http.StatusOK, map[string]any{"status": "diverged", "files": files(maxCompareFiles)})
			case testSHA1 + "..." + testSHA1:
				writeJSON(w, http.StatusOK, map[string]any{"status": "identical", "files": []any{}})
			case testSHA1 + "..." + testSHA4:
				writeJSON(w, http.StatusOK, map[string]any{"status": "ahead"})
			case testSHA2 + "..." + testSHA4:
				writeJSON(w, http.StatusOK, map[string]any{"status": "ahead-ish", "files": []any{}})
			default:
				writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			}
		})
	})

	got, err := env.c.Compare(ctx, repoA, testSHA1, testSHA2)
	want := Comparison{Status: "ahead", Paths: []string{".archfit.yaml", "policy.yaml", "a.go"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Compare = %+v, %v\nwant      %+v", got, err, want)
	}
	req := findRequest(t, env.gh.requests(), "GET", "/repos/o/r/compare/"+testSHA1+"..."+testSHA2)
	if req.Query.Get("per_page") != "1" || req.Auth != inst1 {
		t.Errorf("compare query %v, Authorization %q", req.Query, req.Auth)
	}

	got, err = env.c.Compare(ctx, repoA, testSHA1, testSHA3)
	if err != nil || !got.Truncated || got.Status != "diverged" || len(got.Paths) != maxCompareFiles {
		t.Errorf("Compare at GitHub's 300-file cap = %v paths, truncated %v, %v; want truncated", len(got.Paths), got.Truncated, err)
	}

	got, err = env.c.Compare(ctx, repoA, testSHA1, testSHA1)
	if want := (Comparison{Status: "identical", Paths: []string{}}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("identical Compare = %+v, %v", got, err)
	}

	_, err = env.c.Compare(ctx, repoA, testSHA1, testSHA4)
	wantPlainErr(t, "a compare without a file list", err)
	_, err = env.c.Compare(ctx, repoA, testSHA2, testSHA4)
	wantPlainErr(t, "a status GitHub does not document", err)
	_, err = env.c.Compare(ctx, repoA, testSHA1, testSHA5)
	wantErrIs(t, "unknown commit", err, ErrNotFound)

	env.gh.reset()
	for _, sha := range badSHAs {
		if _, err := env.c.Compare(ctx, repoA, sha, testSHA2); err == nil {
			t.Errorf("Compare base %q: want error", sha)
		}
		if _, err := env.c.Compare(ctx, repoA, testSHA1, sha); err == nil {
			t.Errorf("Compare head %q: want error", sha)
		}
	}
	if reqs := env.gh.requests(); len(reqs) != 0 {
		t.Errorf("guards sent %v", reqs)
	}
}
