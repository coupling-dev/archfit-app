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
