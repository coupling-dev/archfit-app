package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const okRef = `{"ref":"refs/heads/main","object":{"sha":"abc123"}}`

func TestStatusMapping(t *testing.T) {
	rl := map[string]string{"Retry-After": "1"}
	tests := []struct {
		name      string
		post      bool
		replies   []reply
		wantIs    error
		wantErr   bool
		wantReqs  int
		wantSleep int
		wantText  []string
	}{
		{name: "404", replies: []reply{{status: 404}}, wantIs: ErrNotFound, wantErr: true, wantReqs: 1},
		{name: "410", replies: []reply{{status: 410}}, wantIs: ErrNotFound, wantErr: true, wantReqs: 1},
		{name: "401", replies: []reply{{status: 401}, {status: 401}}, wantErr: true, wantReqs: 2, wantText: []string{"401"}},
		{name: "403 permission", replies: []reply{{status: 403}}, wantErr: true, wantReqs: 1,
			wantText: []string{"GET", "/repos/{r}/git/ref/heads/{branch}", "403", "REQ-1"}},
		{name: "403 remaining 0", replies: []reply{
			{status: 403, header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000003"}},
			{status: 200, body: okRef}}, wantReqs: 2, wantSleep: 1},
		{name: "403 retry-after", replies: []reply{{status: 403, header: rl}, {status: 200, body: okRef}}, wantReqs: 2, wantSleep: 1},
		{name: "429", replies: []reply{{status: 429}, {status: 200, body: okRef}}, wantReqs: 2, wantSleep: 1},
		{name: "422", replies: []reply{{status: 422}}, wantErr: true, wantReqs: 1, wantText: []string{"422"}},
		{name: "500 on POST", post: true, replies: []reply{{status: 500}}, wantErr: true, wantReqs: 1,
			wantText: []string{"POST", "/repos/{r}/issues/{n}/comments", "500"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(tt.replies...))
			var err error
			if tt.post {
				_, err = env.c.CreateComment(context.Background(), repoA, 7, "hello")
			} else {
				_, err = env.c.BranchHead(context.Background(), repoA, "main")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
			}
			if tt.wantIs == nil && (errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited)) {
				t.Errorf("err = %v, want no sentinel", err)
			}
			if got := len(env.gh.requests()); got != tt.wantReqs {
				t.Errorf("requests = %d, want %d", got, tt.wantReqs)
			}
			if got := len(env.clock.slept()); got != tt.wantSleep {
				t.Errorf("sleeps = %d, want %d", got, tt.wantSleep)
			}
			for _, s := range tt.wantText {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q lacks %q", err, s)
				}
			}
		})
	}
}

func TestPermanent(t *testing.T) {
	tests := []struct {
		name    string
		replies []reply
		want    bool
	}{
		{"404", []reply{{status: 404}}, true},
		{"410", []reply{{status: 410}}, true},
		{"400", []reply{{status: 400}}, true},
		{"403 without rate-limit headers", []reply{{status: 403}}, false},
		{"422", []reply{{status: 422}}, true},
		{"401 twice", []reply{{status: 401}, {status: 401}}, false},
		{"408", []reply{{status: 408}}, false},
		{"409", []reply{{status: 409}}, false},
		{"rate limit beyond the budget", []reply{{status: 429, header: map[string]string{"Retry-After": "3600"}}}, false},
		{"500", []reply{{status: 500}}, false},
		{"503", []reply{{status: 503}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(tt.replies...))
			// A POST is never retried on 5xx, so each reply is the final answer.
			_, err := env.c.CreateComment(context.Background(), repoA, 7, "hello")
			if err == nil {
				t.Fatal("CreateComment = nil error")
			}
			if got := Permanent(err); got != tt.want {
				t.Errorf("Permanent(%v) = %v, want %v", err, got, tt.want)
			}
		})
	}
	for _, err := range []error{errors.New("store: unavailable"), context.DeadlineExceeded, fmt.Errorf("wrapped: %w", ErrRateLimited),
		fmt.Errorf("wrapped: %w", ErrUserTokenRejected)} {
		if Permanent(err) {
			t.Errorf("Permanent(%v) = true, want false", err)
		}
	}
	if !Permanent(fmt.Errorf("gate: resolve inputs: %w", ErrNotFound)) {
		t.Error("a wrapped ErrNotFound is not permanent")
	}
}

func TestStatusOfAnUnmappedAnswer(t *testing.T) {
	tests := []struct {
		name  string
		reply reply
		want  int
	}{
		{"403", reply{status: 403}, 403},
		{"422", reply{status: 422}, 422},
		{"user token rejected is a sentinel", reply{status: 401}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(tt.reply))
			_, err := env.c.Permission(context.Background(), "user-token", "o/r")
			if got := Status(err); got != tt.want {
				t.Errorf("Status(%v) = %d, want %d", err, got, tt.want)
			}
		})
	}
	if got := Status(errors.New("transport")); got != 0 {
		t.Errorf("Status(plain error) = %d, want 0", got)
	}
}

func TestUnauthorizedInvalidatesAndRetriesOnce(t *testing.T) {
	tests := []struct {
		name            string
		replies         []reply
		call            func(*Client) error
		wantErr         bool
		wantReqs        int
		wantInvalidated []int64
		wantAuth        []string
		wantRejected    bool
	}{
		{
			name:    "401 then 200 on GET",
			replies: []reply{{status: 401}, {status: 200, body: okRef}},
			call: func(c *Client) error {
				_, err := c.BranchHead(context.Background(), repoA, "main")
				return err
			},
			wantReqs: 2, wantInvalidated: []int64{1}, wantAuth: []string{"Bearer inst-1-v0", "Bearer inst-1-v1"},
		},
		{
			name:    "401 then 200 on PATCH",
			replies: []reply{{status: 401}, {status: 200, body: `{"id":5}`}},
			call: func(c *Client) error {
				return c.UpdateComment(context.Background(), repoA, 5, "new body")
			},
			wantReqs: 2, wantInvalidated: []int64{1}, wantAuth: []string{"Bearer inst-1-v0", "Bearer inst-1-v1"},
		},
		{
			name:    "401 twice",
			replies: []reply{{status: 401}, {status: 401}, {status: 200, body: okRef}},
			call: func(c *Client) error {
				_, err := c.BranchHead(context.Background(), repoA, "main")
				return err
			},
			wantErr: true, wantReqs: 2, wantInvalidated: []int64{1},
		},
		{
			name:    "401 on an App JWT route",
			replies: []reply{{status: 401}, {status: 200, body: `[]`}},
			call: func(c *Client) error {
				_, err := c.ListInstallations(context.Background())
				return err
			},
			wantErr: true, wantReqs: 1, wantAuth: []string{"Bearer app-jwt"},
		},
		{
			name:    "401 on a Users call",
			replies: []reply{{status: 401}, {status: 200, body: `{}`}},
			call: func(c *Client) error {
				_, err := c.Permission(context.Background(), "user-token", "o/r")
				return err
			},
			wantErr: true, wantReqs: 1, wantAuth: []string{"Bearer user-token"}, wantRejected: true,
		},
		{
			name:    "401 on a later page of a Users list",
			replies: []reply{{status: 200, body: `{"installations":[{"id":7}]}`}, {status: 401}, {status: 200, body: `{}`}},
			call: func(c *Client) error {
				_, err := c.UserRepos(context.Background(), "user-token")
				return err
			},
			wantErr: true, wantReqs: 2, wantAuth: []string{"Bearer user-token", "Bearer user-token"}, wantRejected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(tt.replies...))
			err := tt.call(env.c)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := errors.Is(err, ErrUserTokenRejected); got != tt.wantRejected {
				t.Errorf("errors.Is(err, ErrUserTokenRejected) = %v, want %v (err %v)", got, tt.wantRejected, err)
			}
			reqs := env.gh.requests()
			if len(reqs) != tt.wantReqs {
				t.Fatalf("requests = %d, want %d", len(reqs), tt.wantReqs)
			}
			if !reflect.DeepEqual(env.ts.invalidated, tt.wantInvalidated) {
				t.Errorf("invalidated = %v, want %v", env.ts.invalidated, tt.wantInvalidated)
			}
			for i, want := range tt.wantAuth {
				if reqs[i].Auth != want {
					t.Errorf("request %d Authorization = %q, want %q", i, reqs[i].Auth, want)
				}
			}
			if tt.wantReqs == 2 && !bytes.Equal(reqs[0].Body, reqs[1].Body) {
				t.Errorf("retried body %q differs from %q", reqs[1].Body, reqs[0].Body)
			}
		})
	}
}

func TestBackoffBudget(t *testing.T) {
	reset := func(in int) map[string]string {
		return map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.Itoa(1_800_000_000 + in)}
	}
	after := func(s string) map[string]string { return map[string]string{"Retry-After": s} }
	sec := func(n ...int) []time.Duration {
		out := []time.Duration{}
		for _, s := range n {
			out = append(out, time.Duration(s)*time.Second)
		}
		return out
	}
	tests := []struct {
		name       string
		patch      bool
		replies    []reply
		cancelAt   int
		wantIs     error
		wantErr    bool
		wantSleeps []time.Duration
		wantReqs   int
	}{
		{name: "Retry-After 2 then 200", replies: []reply{{status: 429, header: after("2")}, {status: 200, body: okRef}},
			wantSleeps: sec(2), wantReqs: 2},
		{name: "reset in 10 s then 200", replies: []reply{{status: 403, header: reset(10)}, {status: 200, body: okRef}},
			wantSleeps: sec(10), wantReqs: 2},
		{name: "reset in 31 s", replies: []reply{{status: 403, header: reset(31)}, {status: 200, body: okRef}},
			wantIs: ErrRateLimited, wantErr: true, wantSleeps: sec(), wantReqs: 1},
		{name: "three waits of 12 s", replies: []reply{{status: 429, header: after("12")}, {status: 429, header: after("12")},
			{status: 429, header: after("12")}, {status: 200, body: okRef}},
			wantIs: ErrRateLimited, wantErr: true, wantSleeps: sec(12, 12), wantReqs: 3},
		{name: "ctx cancelled mid-wait", replies: []reply{{status: 429, header: after("5")}, {status: 200, body: okRef}},
			cancelAt: 1, wantIs: context.Canceled, wantErr: true, wantSleeps: sec(5), wantReqs: 1},
		{name: "503 x3 then 200 on GET", replies: []reply{{status: 503}, {status: 502}, {status: 504}, {status: 200, body: okRef}},
			wantSleeps: sec(1, 2, 4), wantReqs: 4},
		{name: "503 x4 on GET", replies: []reply{{status: 503}, {status: 503}, {status: 503}, {status: 503}, {status: 200, body: okRef}},
			wantErr: true, wantSleeps: sec(1, 2, 4), wantReqs: 4},
		{name: "503 on PATCH", patch: true, replies: []reply{{status: 503}, {status: 200, body: `{}`}},
			wantErr: true, wantSleeps: sec(), wantReqs: 1},
		{name: "unset backoff doubles", replies: []reply{{status: 429}, {status: 429}, {status: 429}, {status: 200, body: okRef}},
			wantSleeps: sec(1, 2, 4), wantReqs: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, script(tt.replies...))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelAt > 0 {
				env.clock.onSleep = func(n int) {
					if n == tt.cancelAt {
						cancel()
					}
				}
			}
			var err error
			if tt.patch {
				err = env.c.UpdateComment(ctx, repoA, 5, "body")
			} else {
				_, err = env.c.BranchHead(ctx, repoA, "main")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
			}
			if tt.wantIs == nil && errors.Is(err, ErrRateLimited) {
				t.Errorf("err = %v, want no ErrRateLimited", err)
			}
			if got := env.clock.slept(); !reflect.DeepEqual(got, tt.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", got, tt.wantSleeps)
			}
			if got := len(env.gh.requests()); got != tt.wantReqs {
				t.Errorf("requests = %d, want %d", got, tt.wantReqs)
			}
		})
	}

	t.Run("transport error is retried for GET only", func(t *testing.T) {
		env := newEnv(t, script())
		env.gh.fail = errors.New("connection refused")
		if _, err := env.c.BranchHead(context.Background(), repoA, "main"); err == nil {
			t.Fatal("GET: want error")
		}
		if got := env.clock.slept(); !reflect.DeepEqual(got, sec(1, 2, 4)) {
			t.Errorf("GET sleeps = %v, want 1s 2s 4s", got)
		}
		env.clock.sleeps = nil
		if err := env.c.UpdateComment(context.Background(), repoA, 5, "body"); err == nil {
			t.Fatal("PATCH: want error")
		}
		if got := env.clock.slept(); len(got) != 0 {
			t.Errorf("PATCH sleeps = %v, want none", got)
		}
	})
}

// pagedRepos serves pages of 100 repositories under /installation/repositories.
func pagedRepos(pages int, lastLink string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		repos := make([]map[string]any, 100)
		for i := range repos {
			id := (page-1)*100 + i
			repos[i] = map[string]any{"id": id, "full_name": fmt.Sprintf("o/r%d", id), "default_branch": "main"}
		}
		switch {
		case page < pages:
			w.Header().Set("Link", linkHeader("/installation/repositories", page+1))
		case lastLink != "":
			w.Header().Set("Link", lastLink)
		}
		writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
	}
}

func TestPaginationFollowsLinkToCap(t *testing.T) {
	tests := []struct {
		name     string
		pages    int
		lastLink string
		want     int
		wantErr  bool
		wantReqs int
	}{
		{name: "3 pages of 100", pages: 3, want: 300, wantReqs: 3},
		{name: "10 pages of 100", pages: 10, want: 1000, wantReqs: 10},
		{name: "11 pages", pages: 11, wantErr: true},
		{name: "next link to another host", pages: 1, wantErr: true, wantReqs: 1,
			lastLink: `<https://evil.example/installation/repositories?page=2>; rel="next"`},
		{name: "next link over http", pages: 1, wantErr: true, wantReqs: 1,
			lastLink: `<http://api.github.com/installation/repositories?page=2>; rel="next"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, pagedRepos(tt.pages, tt.lastLink))
			got, err := env.c.ListRepos(context.Background(), 1)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && got != nil {
				t.Errorf("got %d items with an error, want no partial list", len(got))
			}
			if len(got) != tt.want {
				t.Errorf("items = %d, want %d", len(got), tt.want)
			}
			reqs := env.gh.requests()
			if tt.wantReqs > 0 && len(reqs) != tt.wantReqs {
				t.Errorf("requests = %d, want %d", len(reqs), tt.wantReqs)
			}
			for _, r := range reqs {
				if r.Host != "api.github.com" {
					t.Errorf("request sent to host %q", r.Host)
				}
			}
			if reqs[0].Query.Get("per_page") != "100" {
				t.Errorf("per_page = %q, want 100", reqs[0].Query.Get("per_page"))
			}
		})
	}
}

func TestETagCache(t *testing.T) {
	refHandler := func(w http.ResponseWriter, r *http.Request) {
		etagJSON(w, r, `"v1"`, map[string]any{"object": map[string]any{"sha": "sha-of-" + r.URL.Path}})
	}

	t.Run("GET twice with 304 on the second", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(refHandler))
		first, err := env.c.BranchHead(context.Background(), repoA, "main")
		if err != nil {
			t.Fatal(err)
		}
		second, err := env.c.BranchHead(context.Background(), repoA, "main")
		if err != nil {
			t.Fatal(err)
		}
		reqs := env.gh.requests()
		if reqs[0].IfNoneMatch != "" || reqs[1].IfNoneMatch != `"v1"` {
			t.Errorf("If-None-Match = %q then %q, want none then \"v1\"", reqs[0].IfNoneMatch, reqs[1].IfNoneMatch)
		}
		if first != second || first == "" {
			t.Errorf("cached result %q differs from %q", second, first)
		}
	})

	t.Run("entry 1001 evicts the oldest", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(refHandler))
		for i := range 1001 {
			if _, err := env.c.BranchHead(context.Background(), repoA, fmt.Sprintf("b%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		env.gh.reset()
		if _, err := env.c.BranchHead(context.Background(), repoA, "b1000"); err != nil {
			t.Fatal(err)
		}
		if _, err := env.c.BranchHead(context.Background(), repoA, "b0"); err != nil {
			t.Fatal(err)
		}
		reqs := env.gh.requests()
		if reqs[0].IfNoneMatch == "" {
			t.Error("newest entry was not kept")
		}
		if reqs[1].IfNoneMatch != "" {
			t.Error("oldest entry was not evicted")
		}
	})

	t.Run("a 2 MiB body is not cached", func(t *testing.T) {
		pad := strings.Repeat("x", 2<<20)
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			etagJSON(w, r, `"big"`, map[string]any{"pad": pad, "repositories": []any{}})
		}))
		for range 2 {
			if _, err := env.c.ListRepos(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
		}
		if got := env.gh.requests()[1].IfNoneMatch; got != "" {
			t.Errorf("second request If-None-Match = %q, want none", got)
		}
	})

	t.Run("bodies are bounded to 64 MiB", func(t *testing.T) {
		c := newETagCache()
		body := make([]byte, 1<<20)
		for i := range 70 {
			c.put(fmt.Sprintf("k%d", i), &cacheEntry{etag: "e", body: body})
		}
		if c.bytes > 64<<20 {
			t.Errorf("cache holds %d bytes", c.bytes)
		}
		if c.get("k0") != nil || c.get("k69") == nil {
			t.Error("want the oldest entry evicted and the newest kept")
		}
	})

	t.Run("no entry is shared between principals", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(refHandler))
		for _, id := range []int64{1, 2, 1, 2} {
			if _, err := env.c.BranchHead(context.Background(), RepoRef{InstallationID: id, FullName: "o/r"}, "main"); err != nil {
				t.Fatal(err)
			}
		}
		reqs := env.gh.requests()
		if reqs[1].IfNoneMatch != "" {
			t.Error("inst:2 sent the ETag cached by inst:1")
		}
		if reqs[2].IfNoneMatch == "" || reqs[3].IfNoneMatch == "" {
			t.Error("each principal keeps its own entry")
		}
	})

	t.Run("a 404 after a cached 200", func(t *testing.T) {
		var mu sync.Mutex
		gone := false
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if gone {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			refHandler(w, r)
		}))
		if _, err := env.c.BranchHead(context.Background(), repoA, "main"); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		gone = true
		mu.Unlock()
		if got, err := env.c.BranchHead(context.Background(), repoA, "main"); !errors.Is(err, ErrNotFound) || got != "" {
			t.Errorf("got %q, %v; want ErrNotFound", got, err)
		}
		mu.Lock()
		gone = false
		mu.Unlock()
		if _, err := env.c.BranchHead(context.Background(), repoA, "main"); err != nil {
			t.Fatal(err)
		}
		if got := env.gh.requests()[2].IfNoneMatch; got != "" {
			t.Errorf("entry survived the 404: If-None-Match = %q", got)
		}
	})

	t.Run("Users calls are not cached", func(t *testing.T) {
		env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			etagJSON(w, r, `"u"`, map[string]any{"permissions": map[string]bool{"pull": true}})
		}))
		for range 2 {
			if _, err := env.c.Permission(context.Background(), "user-token", "o/r"); err != nil {
				t.Fatal(err)
			}
		}
		if got := env.gh.requests()[1].IfNoneMatch; got != "" {
			t.Errorf("If-None-Match = %q on a Users call", got)
		}
	})
}

func TestInputGuards(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents/") {
			writeJSON(w, http.StatusOK, map[string]any{"type": "file", "encoding": "base64", "content": "aGk=", "sha": "blob"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "abc"}})
	}))
	ctx := context.Background()

	for _, name := range []string{"a/b/c", "../x", "x/..", "./x", "a", "a/b?x=1", "a/b#f", "a/b%2Fc", "a b/c", "/b", "a/", ""} {
		t.Run("FullName "+name, func(t *testing.T) {
			if _, err := env.c.BranchHead(ctx, RepoRef{InstallationID: 1, FullName: name}, "main"); err == nil {
				t.Error("RepoRef: want error")
			}
			if _, err := env.c.ResolveRepo(ctx, name); err == nil {
				t.Error("ResolveRepo: want error")
			}
			if _, err := env.c.Permission(ctx, "user-token", name); err == nil {
				t.Error("Permission: want error")
			}
		})
	}
	for _, id := range []int64{0, -1} {
		if _, err := env.c.BranchHead(ctx, RepoRef{InstallationID: id, FullName: "o/r"}, "main"); err == nil {
			t.Errorf("InstallationID %d: want error", id)
		}
		if _, err := env.c.ListRepos(ctx, id); err == nil {
			t.Errorf("ListRepos(%d): want error", id)
		}
	}
	for _, path := range []string{"a/../b", "../b", "a/./b", "", "a//b", "/a"} {
		if _, _, err := env.c.File(ctx, repoA, "main", path); err == nil {
			t.Errorf("File path %q: want error", path)
		}
	}
	for _, branch := range []string{"", "..", "a/../b"} {
		if _, err := env.c.BranchHead(ctx, repoA, branch); err == nil {
			t.Errorf("branch %q: want error", branch)
		}
	}
	if _, err := env.c.UserRepos(ctx, ""); err == nil {
		t.Error("empty user token: want error")
	}
	if got := env.gh.requests(); len(got) != 0 {
		t.Fatalf("guards let requests through: %v", got)
	}

	if _, err := env.c.BranchHead(ctx, repoA, "feat/x y"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.c.File(ctx, repoA, "feat/x y", "dir/a b/c?d#e%f.yaml"); err != nil {
		t.Fatal(err)
	}
	reqs := env.gh.requests()
	if want := "/repos/o/r/git/ref/heads/feat/x%20y"; reqs[0].Path != want {
		t.Errorf("branch path = %q, want %q", reqs[0].Path, want)
	}
	if want := "/repos/o/r/contents/dir/a%20b/c%3Fd%23e%25f.yaml"; reqs[1].Path != want {
		t.Errorf("file path = %q, want %q", reqs[1].Path, want)
	}
	if got := reqs[1].Query.Get("ref"); got != "feat/x y" {
		t.Errorf("ref = %q", got)
	}
}

type zipEntry struct {
	name    string
	content []byte
}

func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// lyingZip stores content but declares a smaller uncompressed size.
func lyingZip(t *testing.T, content []byte, declared uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "report.json", Method: zip.Store, CRC32: crc32.ChecksumIEEE(content),
		CompressedSize64: uint64(len(content)), UncompressedSize64: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZipExtraction(t *testing.T) {
	small := []byte(`{"ok":true}`)
	large := bytes.Repeat([]byte("x"), 200)
	tests := []struct {
		name    string
		data    []byte
		want    []byte
		wantErr bool
	}{
		{name: "one file under the cap", data: zipOf(t, zipEntry{"report.json", small}), want: small},
		{name: "one file at the cap", data: zipOf(t, zipEntry{"report.json", bytes.Repeat([]byte("y"), 100)}),
			want: bytes.Repeat([]byte("y"), 100)},
		{name: "one file over", data: zipOf(t, zipEntry{"report.json", large}), wantErr: true},
		{name: "declared size small but content large", data: lyingZip(t, large, 10), wantErr: true},
		{name: "two files", data: zipOf(t, zipEntry{"a.json", small}, zipEntry{"b.json", small}), wantErr: true},
		{name: "a directory entry only", data: zipOf(t, zipEntry{"dir/", nil}), wantErr: true},
		{name: "empty zip", data: zipOf(t), wantErr: true},
		{name: "not a zip", data: []byte("plain text"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractSingleFile(tt.data, 100)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("got %d bytes, want %d", len(got), len(tt.want))
			}
		})
	}
}
