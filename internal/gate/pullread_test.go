package gate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

type pullReadGitHub struct {
	pull        api.Pull
	artifact    []byte
	artifactErr error
}

func (g *pullReadGitHub) PullSummary(context.Context, api.RepoRef, int) (api.Pull, error) {
	return g.pull, nil
}
func (g *pullReadGitHub) Artifact(context.Context, api.RepoRef, int64, string, int64) ([]byte, error) {
	return g.artifact, g.artifactErr
}
func (*pullReadGitHub) CheckRuns(context.Context, api.RepoRef, string, string) ([]api.CheckRun, error) {
	return nil, nil
}

func TestPullPageEvidence(t *testing.T) {
	raw, err := os.ReadFile("../wire/result/testdata/result/pass.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := result.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	judged := result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{{Cause: "tool", What: "not available", Next: result.Next{Kind: "fix", Text: "run again"}}}}
	published, err := json.Marshal(judged)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                    string
		artifact                []byte
		artifactErr             error
		digest                  string
		receipt                 *records.Publication
		wantStatus, wantMessage string
		wantErr                 bool
	}{
		{name: "missing artifact", artifactErr: api.ErrNotFound, digest: envelope.PayloadDigest(raw), wantMessage: "No result yet."},
		{name: "digest mismatch", artifact: raw, digest: envelope.PayloadDigest([]byte("other")), wantMessage: "The stored result cannot be trusted."},
		{name: "infrastructure", artifactErr: errors.New("network"), digest: envelope.PayloadDigest(raw), wantErr: true},
		{name: "artifact", artifact: raw, digest: envelope.PayloadDigest(raw), wantStatus: string(decoded.Status)},
		{name: "receipt wins", artifact: raw, digest: envelope.PayloadDigest(raw), receipt: &records.Publication{HeadSHA: "head", RunID: 5, RunAttempt: 1, Result: published, Digest: envelope.PayloadDigest(published)}, wantStatus: "unknown"},
		{name: "receipt digest mismatch", receipt: &records.Publication{HeadSHA: "head", RunID: 5, RunAttempt: 1, Result: published, Digest: envelope.PayloadDigest([]byte("different"))}, wantMessage: "The stored result cannot be trusted."},
		{name: "older receipt", artifact: raw, digest: envelope.PayloadDigest(raw), receipt: &records.Publication{HeadSHA: "head", RunID: 4, RunAttempt: 1, Result: published, Digest: envelope.PayloadDigest(published)}, wantStatus: "pass"},
		{name: "pending publication", artifact: raw, digest: envelope.PayloadDigest(raw), receipt: &records.Publication{HeadSHA: "head", RunID: 5, RunAttempt: 1, Pending: true}, wantMessage: "The App has not published this result yet."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.NewMemory()
			ctx := context.Background()
			r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
			put := func(key string, v records.Value) {
				b, e := records.Encode(v)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.Put(ctx, 1, key, b); e != nil {
					t.Fatal(e)
				}
			}
			put(records.PullKey(r.FullName, 4), &records.Pull{HeadSHA: "head", RunID: 5, RunAttempt: 1, Status: "pass", PolicyDigest: "accepted", PayloadDigest: tc.digest})
			if tc.receipt != nil {
				put(records.PublicationKey(r.FullName, 4, "head"), tc.receipt)
			}
			g := &pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifact: tc.artifact, artifactErr: tc.artifactErr}
			got, err := NewPullViews(g, s).PullPage(ctx, r, 4)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Message != tc.wantMessage {
				t.Fatalf("message %q, want %q", got.Message, tc.wantMessage)
			}
			if tc.wantStatus != "" && (got.View == nil || string(got.View.Status) != tc.wantStatus) {
				t.Fatalf("view %+v, want %s", got.View, tc.wantStatus)
			}
		})
	}
}

func TestPullPagePublicationWithoutUpload(t *testing.T) {
	body, err := json.Marshal(result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{{Cause: "tool", What: "the check finished without an uploaded result", Next: result.Next{Kind: "fix", Text: "run the Archfit check again"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, head              string
		pending                 bool
		wantStatus, wantMessage string
	}{
		{name: "confirmed missing upload", head: "head", wantStatus: "unknown"},
		{name: "pending missing upload", head: "head", pending: true, wantMessage: "The App has not published this result yet."},
		{name: "another head", head: "old-head", wantMessage: "No result yet."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := store.NewMemory()
			r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
			receipt := records.Publication{HeadSHA: tc.head, RunID: 42, RunAttempt: 1, Result: body, Digest: envelope.PayloadDigest(body), Source: "missing_upload", Pending: tc.pending}
			raw, err := records.Encode(&receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err = mem.Put(t.Context(), 1, records.PublicationKey(r.FullName, 4, tc.head), raw); err != nil {
				t.Fatal(err)
			}
			gh := &pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifactErr: errors.New("receipt must avoid artifact")}
			got, err := NewPullViews(gh, mem).PullPage(t.Context(), r, 4)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantStatus != "" {
				if got.View == nil || string(got.View.Status) != tc.wantStatus {
					t.Fatalf("view %+v", got.View)
				}
			} else if got.View != nil {
				t.Fatalf("unexpected view %+v", got.View)
			}
			if got.Message != tc.wantMessage {
				t.Fatalf("message %q, want %q", got.Message, tc.wantMessage)
			}
		})
	}
}

func TestPullPageCoversFromLiveRequests(t *testing.T) {
	raw, err := os.ReadFile("../wire/result/testdata/result/graph.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, storedStatus := range []string{"fail", "waiting"} {
		t.Run(storedStatus, func(t *testing.T) {
			mem := store.NewMemory()
			r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
			put := func(key string, value records.Value) {
				raw, err := records.Encode(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = mem.Put(t.Context(), 1, key, raw); err != nil {
					t.Fatal(err)
				}
			}
			put(records.PullKey(r.FullName, 4), &records.Pull{HeadSHA: "head", RunID: 42, RunAttempt: 1, Status: storedStatus, Coverable: true, PolicyDigest: "accepted", PayloadDigest: envelope.PayloadDigest(raw)})
			index := records.Index{Schema: records.SchemaRequestIndex, Entries: []records.Entry{{Key: "graph pkg_tc pkg_util", ID: "431"}}}
			put(records.RequestIndexKey(r.FullName), &index)
			put(records.RequestKey(r.FullName, 431), &records.Request{Key: "graph pkg_tc pkg_util", State: records.StateOpen, Waiting: []int{4}})
			views := NewPullViews(&pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifact: raw}, mem)
			got, err := views.PullPage(t.Context(), r, 4)
			if err != nil {
				t.Fatal(err)
			}
			if got.View == nil || got.View.Status != result.StatusWaiting || len(got.Waiting) != 1 || got.View.Records[0].Request != 431 {
				t.Fatalf("open request: %+v", got)
			}
			put(records.RequestIndexKey(r.FullName), &records.Index{Schema: records.SchemaRequestIndex})
			got, err = views.PullPage(t.Context(), r, 4)
			if err != nil {
				t.Fatal(err)
			}
			if got.View == nil || got.View.Status != result.StatusFail || len(got.Waiting) != 0 || got.View.Records[0].Request != 0 {
				t.Fatalf("answered request: %+v", got)
			}
		})
	}
}

func TestAdvisoryFailureDoesNotWaitForOpenRequest(t *testing.T) {
	raw, err := os.ReadFile("../wire/result/testdata/result/graph.json")
	if err != nil {
		t.Fatal(err)
	}
	mem := store.NewMemory()
	r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}}
	put := func(key string, v records.Value) {
		b, err := records.Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = mem.Put(t.Context(), 1, key, b); err != nil {
			t.Fatal(err)
		}
	}
	put(records.PullKey(r.FullName, 4), &records.Pull{HeadSHA: "head", RunID: 42, RunAttempt: 1, Status: "fail", PolicyDigest: "approved", PayloadDigest: envelope.PayloadDigest(raw)})
	put(records.RequestIndexKey(r.FullName), &records.Index{Schema: records.SchemaRequestIndex, Entries: []records.Entry{{Key: "graph pkg_tc pkg_util", ID: "431"}}})
	put(records.RequestKey(r.FullName, 431), &records.Request{Key: "graph pkg_tc pkg_util", State: records.StateOpen, Waiting: []int{4}})
	got, err := NewPullViews(&pullReadGitHub{pull: api.Pull{HeadSHA: "head"}, artifact: raw}, mem).PullPage(t.Context(), r, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.View == nil || got.View.Status != result.StatusFail || got.View.Records[0].Request != 0 {
		t.Fatalf("advisory result became waiting: %+v", got.View)
	}
}
