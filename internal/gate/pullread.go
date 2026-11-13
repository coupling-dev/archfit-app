package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	asks "github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// PullViews supplies the pull page without exposing storage types.
type PullViews interface {
	PullPage(context.Context, api.Repo, int) (PullPage, error)
}

// PullState is the pull record exposed to an authorized reader.
type PullState struct {
	Schema        string        `json:"schema"`
	HeadSHA       string        `json:"head_sha"`
	RunID         int64         `json:"run_id"`
	RunAttempt    int           `json:"run_attempt"`
	PayloadDigest string        `json:"payload_digest"`
	PolicyDigest  string        `json:"policy_digest,omitempty"`
	SweepNote     string        `json:"sweep_note,omitempty"`
	Reason        string        `json:"reason,omitempty"`
	Status        string        `json:"status"`
	Date          string        `json:"date,omitempty"`
	Errors        []string      `json:"errors,omitempty"`
	RequestKeys   []string      `json:"request_keys,omitempty"`
	Rechecks      []PullRecheck `json:"rechecks,omitempty"`
}

// PullRecheck describes one re-check attempt after an answer.
type PullRecheck struct {
	Proposal string `json:"proposal"`
	Path     string `json:"path"`
	Unmet    string `json:"unmet,omitempty"`
	HeadSHA  string `json:"head_sha"`
}

// PullPage is the authorized pull's state and shared feedback form.
type PullPage struct {
	Record          *PullState `json:"record,omitempty"`
	HeadSHA         string     `json:"head_sha,omitempty"`
	RunID           int64      `json:"run_id,omitempty"`
	RunAttempt      int        `json:"run_attempt,omitempty"`
	Status          string     `json:"status,omitempty"`
	CheckStatus     string     `json:"check_status,omitempty"`
	CheckConclusion string     `json:"check_conclusion,omitempty"`
	Waiting         []int      `json:"waiting"`
	View            *PullView  `json:"view,omitempty"`
	Message         string     `json:"message,omitempty"`
}

type pullPageGitHub interface {
	PullSummary(context.Context, api.RepoRef, int) (api.Pull, error)
	Artifact(context.Context, api.RepoRef, int64, string, int64) ([]byte, error)
	CheckRuns(context.Context, api.RepoRef, string, string) ([]api.CheckRun, error)
}
type pullViews struct {
	gh    pullPageGitHub
	store store.Store
}

// NewPullViews reads the recorded result and publication receipt for a pull.
func NewPullViews(gh pullPageGitHub, s store.Store) PullViews { return &pullViews{gh: gh, store: s} }

func (v *pullViews) PullPage(ctx context.Context, r api.Repo, number int) (PullPage, error) {
	out := PullPage{Waiting: []int{}}
	pl, err := v.gh.PullSummary(ctx, r.RepoRef, number)
	if err != nil {
		return out, err
	}
	out.HeadSHA = pl.HeadSHA
	checks, err := v.gh.CheckRuns(ctx, r.RepoRef, pl.HeadSHA, "archfit/gate")
	if err != nil {
		return out, err
	}
	var check api.CheckRun
	for _, c := range checks {
		if c.ID > check.ID {
			check = c
		}
	}
	out.CheckStatus, out.CheckConclusion = check.Status, check.Conclusion
	tenant := store.Tenant(r.InstallationID)
	var rec records.Pull
	err = records.Load(ctx, v.store, tenant, records.PullKey(r.FullName, number), &rec)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return out, err
	}
	if errors.Is(err, records.ErrNoRecord) || rec.HeadSHA != pl.HeadSHA {
		rec = records.Pull{}
	}
	if rec.RunID != 0 {
		state := &PullState{Schema: rec.Schema, HeadSHA: rec.HeadSHA, RunID: rec.RunID, RunAttempt: rec.RunAttempt, PayloadDigest: rec.PayloadDigest,
			PolicyDigest: rec.PolicyDigest, SweepNote: rec.SweepNote, Reason: rec.Reason, Status: rec.Status, Date: rec.Date, Errors: rec.Errors, RequestKeys: rec.RequestKeys}
		for _, x := range rec.Rechecks {
			state.Rechecks = append(state.Rechecks, PullRecheck{Proposal: x.Proposal, Path: x.Path, Unmet: x.Unmet, HeadSHA: x.HeadSHA})
		}
		out.Record = state
	}
	out.RunID, out.RunAttempt, out.Status = rec.RunID, rec.RunAttempt, rec.Status
	index, err := asks.OpenIndex(ctx, v.store, r)
	if err != nil {
		return out, err
	}
	for _, e := range index.Entries {
		n, err := strconv.Atoi(e.ID)
		if err != nil || n <= 0 {
			return out, errors.New("pull: invalid request index")
		}
		if slices.Contains(out.Waiting, n) {
			continue
		}
		var req records.Request
		if err := records.Load(ctx, v.store, tenant, records.RequestKey(r.FullName, n), &req); err != nil {
			return out, err
		}
		if slices.Contains(req.Waiting, number) {
			out.Waiting = append(out.Waiting, n)
		}
	}
	slices.Sort(out.Waiting)
	var pub records.Publication
	err = records.Load(ctx, v.store, tenant, records.PublicationKey(r.FullName, number, pl.HeadSHA), &pub)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		return out, err
	}
	newer := pub.RunID > rec.RunID || pub.RunID == rec.RunID && pub.RunAttempt >= rec.RunAttempt
	if err == nil && pub.HeadSHA == pl.HeadSHA && newer {
		if pub.Pending {
			out.Message = "The App has not published this result yet."
			return out, nil
		}
		if pub.Omitted || len(pub.Result) == 0 || envelope.PayloadDigest(pub.Result) != pub.Digest {
			out.Message = "The App published a result it cannot show yet."
			return out, nil
		}
		var res result.Result
		if err := json.Unmarshal(pub.Result, &res); err != nil {
			out.Message = "The App published a result it cannot show yet."
			return out, nil
		}
		view := BuildPullView(res)
		out.View = &view
		return out, nil
	}
	if rec.RunID == 0 {
		out.Message = "No result yet."
		return out, nil
	}
	if rec.PolicyDigest == "" {
		out.Message = "The App published a result it cannot show yet."
		return out, nil
	}
	raw, err := v.gh.Artifact(ctx, r.RepoRef, rec.RunID, asks.ArtifactName, 5<<20)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrArtifactInvalid) {
		out.Message = "No result yet."
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("pull: read artifact: %w", err)
	}
	if envelope.PayloadDigest(raw) != rec.PayloadDigest {
		out.Message = "The stored result cannot be trusted."
		return out, nil
	}
	res, err := result.Decode(raw)
	if err != nil {
		out.Message = "The stored result cannot be trusted."
		return out, nil
	}
	if rec.Status == string(result.StatusWaiting) {
		if res.Status != result.StatusFail {
			out.Message = "The stored result cannot be trusted."
			return out, nil
		}
		res, _ = asks.Cover(res, index)
	} else if string(res.Status) != rec.Status {
		out.Message = "The stored result cannot be trusted."
		return out, nil
	}
	view := BuildPullView(res)
	out.View = &view
	return out, nil
}
