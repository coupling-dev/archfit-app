package api

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

func TestCheckRunCallerContract(t *testing.T) {
	annotations := func(n int) []Annotation {
		return make([]Annotation, n)
	}
	base := CheckRun{ID: 5, Name: "archfit", HeadSHA: "abc", Status: "in_progress"}
	with := func(edit func(*CheckRun)) CheckRun {
		c := base
		edit(&c)
		return c
	}
	tests := []struct {
		name       string
		run        CheckRun
		updateOnly bool
		wantErr    bool
	}{
		{name: "summary of 65535 bytes", run: with(func(c *CheckRun) { c.Summary = strings.Repeat("s", 65535) })},
		{name: "summary of 65536 bytes", run: with(func(c *CheckRun) { c.Summary = strings.Repeat("s", 65536) }), wantErr: true},
		{name: "multi-byte summary over 65535 bytes", run: with(func(c *CheckRun) { c.Summary = strings.Repeat("é", 40000) }), wantErr: true},
		{name: "50 annotations", run: with(func(c *CheckRun) { c.Annotations = annotations(50) })},
		{name: "51 annotations", run: with(func(c *CheckRun) { c.Annotations = annotations(51) }), wantErr: true},
		{name: "completed without conclusion", run: with(func(c *CheckRun) { c.Status = "completed" }), wantErr: true},
		{name: "completed with conclusion", run: with(func(c *CheckRun) { c.Status, c.Conclusion = "completed", "success" })},
		{name: "in_progress with conclusion", run: with(func(c *CheckRun) { c.Conclusion = "success" }), wantErr: true},
		{name: "update with ID 0", run: with(func(c *CheckRun) { c.ID = 0 }), updateOnly: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusCreated, map[string]any{"id": 5})
			}))
			calls := 1
			err := env.c.UpdateCheckRun(context.Background(), repoA, tt.run)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateCheckRun: err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.updateOnly {
				calls = 2
				_, err = env.c.CreateCheckRun(context.Background(), repoA, tt.run)
				if (err != nil) != tt.wantErr {
					t.Errorf("CreateCheckRun: err = %v, wantErr %v", err, tt.wantErr)
				}
			}
			reqs := env.gh.requests()
			if tt.wantErr && len(reqs) != 0 {
				t.Fatalf("an invalid check run sent %v", reqs)
			}
			if !tt.wantErr {
				if len(reqs) != calls {
					t.Fatalf("requests = %d, want %d", len(reqs), calls)
				}
				sent := decodeBody(t, reqs[0])
				if tt.run.Summary != "" && sent["output"].(map[string]any)["summary"] != tt.run.Summary {
					t.Error("summary was changed on the way out")
				}
				if n := len(tt.run.Annotations); n > 0 && len(sent["output"].(map[string]any)["annotations"].([]any)) != n {
					t.Error("annotations were dropped on the way out")
				}
			}
		})
	}
}

func TestRevokedAccessAndRemovedRepository(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	removed := false
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if removed {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		etagJSON(w, r, `"policy-v1"`, map[string]any{"type": "file", "encoding": "base64", "content": "c2VjcmV0", "sha": "blob"})
	}))

	for range 2 {
		content, _, err := env.c.File(ctx, repoA, "main", "policy.yaml")
		if err != nil || string(content) != "secret" {
			t.Fatalf("File = %q, %v", content, err)
		}
	}
	if env.gh.requests()[1].IfNoneMatch == "" {
		t.Fatal("the file was not cached, so the test proves nothing")
	}

	mu.Lock()
	removed = true
	mu.Unlock()
	content, sha, err := env.c.File(ctx, repoA, "main", "policy.yaml")
	if !errors.Is(err, ErrNotFound) || content != nil || sha != "" {
		t.Errorf("removed repository: %q, %q, %v; want ErrNotFound and no data", content, sha, err)
	}

	mu.Lock()
	removed = false
	mu.Unlock()
	errMint := errors.New("installation is gone")
	env.ts.instErr = errMint
	before := len(env.gh.requests())
	content, sha, err = env.c.File(ctx, repoA, "main", "policy.yaml")
	if !errors.Is(err, errMint) || content != nil || sha != "" {
		t.Errorf("uninstalled: %q, %q, %v; want the wrapped mint error and no data", content, sha, err)
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("mint error became a sentinel: %v", err)
	}
	if len(env.gh.requests()) != before {
		t.Error("a request was sent without a token")
	}
}

// repoRefCalls is every RepoRef method of every role interface, each reporting whether it returned data.
func repoRefCalls(c *Client, r RepoRef) map[string]func(context.Context) (bool, error) {
	run := CheckRun{ID: 5, Name: "archfit", HeadSHA: "abc", Status: "in_progress"}
	return map[string]func(context.Context) (bool, error){
		"Repos.File": func(ctx context.Context) (bool, error) {
			content, sha, err := c.File(ctx, r, "main", "a.yaml")
			return content != nil || sha != "", err
		},
		"Repos.BranchHead": func(ctx context.Context) (bool, error) {
			sha, err := c.BranchHead(ctx, r, "main")
			return sha != "", err
		},
		"Pulls.Pull": func(ctx context.Context) (bool, error) {
			p, err := c.Pull(ctx, r, 1)
			return p != Pull{}, err
		},
		"Pulls.OpenPulls": func(ctx context.Context) (bool, error) {
			p, err := c.OpenPulls(ctx, r)
			return p != nil, err
		},
		"Pulls.PullByHead": func(ctx context.Context) (bool, error) {
			p, err := c.PullByHead(ctx, r, "feature")
			return p != Pull{}, err
		},
		"Pulls.ChangedFiles": func(ctx context.Context) (bool, error) {
			paths, truncated, err := c.ChangedFiles(ctx, r, 1)
			return paths != nil || truncated, err
		},
		"Pulls.Reviews": func(ctx context.Context) (bool, error) {
			rv, err := c.Reviews(ctx, r, 1)
			return rv != nil, err
		},
		"Checks.CheckRuns": func(ctx context.Context) (bool, error) {
			runs, err := c.CheckRuns(ctx, r, "abc", "archfit")
			return runs != nil, err
		},
		"Checks.CreateCheckRun": func(ctx context.Context) (bool, error) {
			created, err := c.CreateCheckRun(ctx, r, run)
			return created.ID != 0 && err != nil, err
		},
		"Checks.UpdateCheckRun": func(ctx context.Context) (bool, error) {
			return false, c.UpdateCheckRun(ctx, r, run)
		},
		"Comments.AppComments": func(ctx context.Context) (bool, error) {
			cs, err := c.AppComments(ctx, r, 1)
			return cs != nil, err
		},
		"Comments.CreateComment": func(ctx context.Context) (bool, error) {
			cm, err := c.CreateComment(ctx, r, 1, "hello")
			return cm != Comment{}, err
		},
		"Comments.UpdateComment": func(ctx context.Context) (bool, error) {
			return false, c.UpdateComment(ctx, r, 9, "hello")
		},
		"Issues.AppIssues": func(ctx context.Context) (bool, error) {
			is, err := c.AppIssues(ctx, r)
			return is != nil, err
		},
		"Issues.CreateIssue": func(ctx context.Context) (bool, error) {
			i, err := c.CreateIssue(ctx, r, NewIssue{Title: "t", Body: "b", Assignee: "alice"})
			return i != Issue{}, err
		},
		"Issues.UpdateIssueBody": func(ctx context.Context) (bool, error) {
			return false, c.UpdateIssueBody(ctx, r, 9, "b")
		},
		"Issues.CloseIssue": func(ctx context.Context) (bool, error) {
			return false, c.CloseIssue(ctx, r, 9)
		},
		"Actions.Run": func(ctx context.Context) (bool, error) {
			wr, err := c.Run(ctx, r, 55)
			return wr.ID != 0, err
		},
		"Actions.Rerun": func(ctx context.Context) (bool, error) {
			return false, c.Rerun(ctx, r, 55)
		},
		"Actions.Dispatch": func(ctx context.Context) (bool, error) {
			return false, c.Dispatch(ctx, r, ".github/workflows/archfit.yaml", "main", nil)
		},
		"Actions.LatestRun": func(ctx context.Context) (bool, error) {
			wr, err := c.LatestRun(ctx, r, ".github/workflows/archfit.yaml", RunFilter{Branch: "main"})
			return wr.ID != 0, err
		},
		"Actions.Artifact": func(ctx context.Context) (bool, error) {
			data, err := c.Artifact(ctx, r, 55, "report", 1<<20)
			return data != nil, err
		},
		"Changes.EnsureBranch": func(ctx context.Context) (bool, error) {
			return false, c.EnsureBranch(ctx, r, "archfit/setup", "0123456789012345678901234567890123456789")
		},
		"Changes.Commit": func(ctx context.Context) (bool, error) {
			sha, err := c.Commit(ctx, r, "main", "msg", []FileChange{{Path: "a", Content: []byte("b")}})
			return sha != "", err
		},
		"Changes.EnsurePull": func(ctx context.Context) (bool, error) {
			p, err := c.EnsurePull(ctx, r, NewPull{Title: "t", Head: "archfit/setup", Base: "main"})
			return p != Pull{}, err
		},
		"Changes.UpdatePullBody": func(ctx context.Context) (bool, error) {
			return false, c.UpdatePullBody(ctx, r, 1, "body")
		},
		"Changes.MarkReady": func(ctx context.Context) (bool, error) {
			return false, c.MarkReady(ctx, r, 1)
		},
	}
}

func TestCrossInstallationRepoRef(t *testing.T) {
	const tokenA = "Bearer inst-1-v0"
	// Installation 1 (A) has no access to b-org/secret, which installation 2 (B) covers: GitHub answers 404.
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app" {
			appHandler(w, r)
			return
		}
		if r.Header.Get("Authorization") != tokenA {
			t.Errorf("%s %s: Authorization = %q, want A's token", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	crossed := RepoRef{InstallationID: 1, FullName: "b-org/secret"}

	for name, call := range repoRefCalls(env.c, crossed) {
		t.Run(name, func(t *testing.T) {
			data, err := call(context.Background())
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v, want ErrNotFound", err)
			}
			if data {
				t.Error("data was returned with the error")
			}
		})
	}
	t.Run("Pulls.TeamMember", func(t *testing.T) {
		member, err := env.c.TeamMember(context.Background(), crossed, "b-org/core", "alice")
		if member || err != nil {
			t.Errorf("TeamMember = %v, %v; want false", member, err)
		}
	})
	if env.ts.mintedFor(2) != 0 || env.ts.mintedFor(1) == 0 {
		t.Errorf("tokens minted for %v, want installation 1 only", env.ts.minted)
	}
}

func TestErrorsCarryNoSecretsOrContent(t *testing.T) {
	const (
		tokenMark  = "TOKENMARK"
		userMark   = "USERTOKENMARK"
		pathMark   = "PATHMARK"
		bodyMark   = "BODYMARK"
		respMark   = "RESPMARK"
		repoMark   = "REPOMARK"
		branchMark = "BRANCHMARK"
	)
	markers := []string{tokenMark, userMark, pathMark, bodyMark, respMark, repoMark, branchMark}
	ref := RepoRef{InstallationID: 1, FullName: "o/" + repoMark}

	calls := func(c *Client) map[string]func(context.Context) error {
		run := CheckRun{ID: 5, Name: bodyMark, HeadSHA: "abc", Status: "in_progress", Summary: bodyMark}
		return map[string]func(context.Context) error{
			"File": func(ctx context.Context) error {
				_, _, err := c.File(ctx, ref, branchMark, "dir/"+pathMark+".yaml")
				return err
			},
			"BranchHead": func(ctx context.Context) error { _, err := c.BranchHead(ctx, ref, branchMark); return err },
			"Pull":       func(ctx context.Context) error { _, err := c.Pull(ctx, ref, 1); return err },
			"ListRepos":  func(ctx context.Context) error { _, err := c.ListRepos(ctx, 1); return err },
			"ListInstallations": func(ctx context.Context) error {
				_, err := c.ListInstallations(ctx)
				return err
			},
			"ResolveRepo":    func(ctx context.Context) error { _, err := c.ResolveRepo(ctx, ref.FullName); return err },
			"CreateComment":  func(ctx context.Context) error { _, err := c.CreateComment(ctx, ref, 1, bodyMark); return err },
			"UpdateCheckRun": func(ctx context.Context) error { return c.UpdateCheckRun(ctx, ref, run) },
			"Dispatch": func(ctx context.Context) error {
				return c.Dispatch(ctx, ref, ".github/workflows/"+pathMark+".yaml", branchMark, map[string]string{"k": bodyMark})
			},
			"Artifact": func(ctx context.Context) error { _, err := c.Artifact(ctx, ref, 55, pathMark, 1<<20); return err },
			"Commit": func(ctx context.Context) error {
				_, err := c.Commit(ctx, ref, branchMark, bodyMark, []FileChange{{Path: pathMark, Content: []byte(bodyMark)}})
				return err
			},
			"CreateIssue": func(ctx context.Context) error {
				_, err := c.CreateIssue(ctx, ref, NewIssue{Title: bodyMark, Body: bodyMark, Assignee: userMark})
				return err
			},
			"EnsurePull": func(ctx context.Context) error {
				_, err := c.EnsurePull(ctx, ref, NewPull{Title: bodyMark, Body: bodyMark, Head: branchMark, Base: "main"})
				return err
			},
			"MarkReady":  func(ctx context.Context) error { return c.MarkReady(ctx, ref, 1) },
			"Permission": func(ctx context.Context) error { _, err := c.Permission(ctx, userMark, ref.FullName); return err },
			"UserRepos":  func(ctx context.Context) error { _, err := c.UserRepos(ctx, userMark); return err },
		}
	}
	check := func(t *testing.T, name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: want an error", name)
			return
		}
		for _, m := range markers {
			if strings.Contains(err.Error(), m) {
				t.Errorf("%s: error leaks %s: %v", name, m, err)
			}
		}
	}
	leaky := func(status int, header map[string]string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			reply{status: status, header: header, body: `{"message":"` + respMark + `","documentation_url":"` + respMark + `"}`}.write(w)
		}
	}

	scenarios := map[string]http.HandlerFunc{
		"401":          leaky(401, nil),
		"403":          leaky(403, nil),
		"409":          leaky(409, nil),
		"422":          leaky(422, nil),
		"500":          leaky(500, nil),
		"503":          leaky(503, nil),
		"rate limited": leaky(429, map[string]string{"Retry-After": "600"}),
		"redirect":     leaky(302, map[string]string{"Location": "https://" + blobHost + "/" + respMark}),
		"not JSON": func(w http.ResponseWriter, _ *http.Request) {
			reply{status: 200, body: respMark + " is not JSON"}.write(w)
		},
		"wrong JSON": func(w http.ResponseWriter, _ *http.Request) { reply{status: 200, body: `"` + respMark + `"`}.write(w) },
		"wrong type": func(w http.ResponseWriter, _ *http.Request) {
			reply{status: 200, body: `{"id":"` + respMark + `"}`}.write(w)
		},
		"empty response": func(w http.ResponseWriter, _ *http.Request) { reply{status: 200, body: `{}`}.write(w) },
	}
	// A write needs no answer body, so a malformed 200 is not an error for it.
	answers200 := map[string]bool{"not JSON": true, "wrong JSON": true, "wrong type": true, "empty response": true}
	for name, handler := range scenarios {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, handler)
			env.ts.prefix = tokenMark
			failed := 0
			for callName, call := range calls(env.c) {
				err := call(context.Background())
				if err == nil && answers200[name] {
					continue
				}
				failed++
				check(t, callName, err)
			}
			if failed < 8 {
				t.Errorf("only %d calls failed; the scenario exercises too little", failed)
			}
		})
	}

	t.Run("transport failure", func(t *testing.T) {
		env := newEnv(t, leaky(500, nil))
		env.ts.prefix = tokenMark
		env.gh.fail = errors.New("connection reset by peer")
		for callName, call := range calls(env.c) {
			check(t, callName, call(context.Background()))
		}
	})

	t.Run("graphql errors", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/graphql" {
				writeJSON(w, http.StatusOK, map[string]any{"errors": []map[string]any{{"type": "FORBIDDEN", "message": respMark}}})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"number": 1, "node_id": "PR_1", "draft": true})
		}))
		err := env.c.MarkReady(context.Background(), ref, 1)
		check(t, "MarkReady", err)
		if errors.Is(err, ErrRateLimited) {
			t.Errorf("FORBIDDEN became ErrRateLimited: %v", err)
		}
	})

	t.Run("input guards", func(t *testing.T) {
		env := newEnv(t, leaky(500, nil))
		bad := RepoRef{InstallationID: 1, FullName: "o/" + repoMark + "/extra"}
		_, err := env.c.BranchHead(context.Background(), bad, "main")
		check(t, "bad full name", err)
		_, _, err = env.c.File(context.Background(), repoA, "main", pathMark+"/../x")
		check(t, "bad path", err)
		_, err = env.c.TeamMember(context.Background(), repoA, pathMark, "alice")
		check(t, "bad team", err)
		_, err = env.c.CreateComment(context.Background(), repoA, 1, strings.Repeat(bodyMark, 10000))
		check(t, "long comment", err)
	})
}

func TestGraphQLRateLimitIsImmediate(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			writeJSON(w, http.StatusOK, map[string]any{"errors": []map[string]any{{"type": "RATE_LIMITED"}}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"number": 1, "node_id": "PR_1", "draft": true})
	}))
	err := env.c.MarkReady(context.Background(), repoA, 1)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("MarkReady() error = %v, want ErrRateLimited", err)
	}
	mutations := 0
	for _, r := range env.gh.requests() {
		if r.Path == "/graphql" {
			mutations++
		}
	}
	if mutations != 1 {
		t.Errorf("GraphQL requests = %d, want 1", mutations)
	}
	if sleeps := env.clock.slept(); len(sleeps) != 0 {
		t.Errorf("slept %v, want no backoff", sleeps)
	}
}

func TestAPIImportsOnlyStdlibAndAuth(t *testing.T) {
	const (
		module = "github.com/alexei-led/archfit-app/"
		self   = module + "internal/github/api"
		auth   = module + "internal/github/auth"
	)
	list := func(args ...string) []string {
		out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
		if err != nil {
			t.Fatalf("go list %v: %v", args, err)
		}
		return strings.Fields(string(out))
	}

	for _, dep := range list("-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".") {
		if !strings.HasPrefix(dep, module) {
			t.Errorf("third-party dependency %s", dep)
		}
	}
	for _, imp := range list("-f", `{{join .Imports "\n"}}`, ".") {
		if strings.HasPrefix(imp, module) && imp != auth && imp != self {
			t.Errorf("internal import %s, want only %s", imp, auth)
		}
	}
}
