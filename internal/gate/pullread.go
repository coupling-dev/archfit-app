package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	asks "github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/envelope"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
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
	Coverable     bool          `json:"coverable,omitempty"`
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

// The no-result messages. noCheckYet is for a head no check ran on; the others follow a check that did run, so they
// do not claim that none did.
const (
	noCheckYet  = "No check has reported for this pull request. It runs on the next push."
	noResultYet = "No result yet."
	resultGone  = "The check result is no longer available. Run the check again."
)

// PullPage reads the pull request, its check and its stored result. The reads that need only the repository and the
// number (the requests waiting on this pull request) run beside the pull request's own read. The check run needs only
// the head, so it runs beside the stored result, which is the longer chain: record, publication, then the artifact.
// The errors keep the order the reads used to run in: pull request, check run, record, requests, artifact.
func (v *pullViews) PullPage(ctx context.Context, r api.Repo, number int) (PullPage, error) {
	out := PullPage{Waiting: []int{}}
	reqs := v.startRequests(ctx, r, number)
	defer reqs.done.Wait()
	pl, err := v.gh.PullSummary(ctx, r.RepoRef, number)
	if err != nil {
		return out, err
	}
	out.HeadSHA = pl.HeadSHA
	var (
		checks   []api.CheckRun
		checkErr error
		checking sync.WaitGroup
	)
	checking.Go(func() { checks, checkErr = v.gh.CheckRuns(ctx, r.RepoRef, pl.HeadSHA, "archfit/gate") })
	err = v.stored(ctx, r, number, pl, reqs, &out)
	checking.Wait()
	if checkErr != nil {
		return out, checkErr
	}
	var check api.CheckRun
	for _, c := range checks {
		if c.ID > check.ID {
			check = c
		}
	}
	out.CheckStatus, out.CheckConclusion = check.Status, check.Conclusion
	return out, err
}

// stored fills out from the pull request's record, the requests waiting on it and its published or uploaded result.
func (v *pullViews) stored(ctx context.Context, r api.Repo, number int, pl api.Pull, reqs *pullRequests, out *PullPage) error {
	rec, pub, published, err := v.snapshot(ctx, r, number, pl.HeadSHA)
	if err != nil {
		return err
	}
	if rec.HeadSHA != pl.HeadSHA {
		rec = records.Pull{}
	}
	if rec.RunID != 0 {
		state := &PullState{Schema: rec.Schema, HeadSHA: rec.HeadSHA, RunID: rec.RunID, RunAttempt: rec.RunAttempt, PayloadDigest: rec.PayloadDigest,
			PolicyDigest: rec.PolicyDigest, SweepNote: rec.SweepNote, Reason: rec.Reason, Status: rec.Status, Coverable: rec.Coverable, Date: rec.Date, Errors: rec.Errors, RequestKeys: rec.RequestKeys}
		for _, x := range rec.Rechecks {
			state.Rechecks = append(state.Rechecks, PullRecheck{Proposal: x.Proposal, Path: x.Path, Unmet: x.Unmet, HeadSHA: x.HeadSHA})
		}
		out.Record = state
	}
	out.RunID, out.RunAttempt, out.Status = rec.RunID, rec.RunAttempt, rec.Status
	reqs.done.Wait()
	if reqs.err != nil {
		return reqs.err
	}
	out.Waiting = reqs.waiting
	newer := pub.RunID > rec.RunID || pub.RunID == rec.RunID && pub.RunAttempt >= rec.RunAttempt
	if published && pub.HeadSHA == pl.HeadSHA && newer {
		switch {
		case pub.Superseded:
			out.Message = "A newer check is shown for this commit."
		case pub.Unpublished:
			out.Message = noResultYet
		case pub.Pending:
			out.Message = "The App has not published this result yet."
		case pub.Omitted || len(pub.Result) == 0:
			out.Message = "The App published a result it cannot show yet."
		case envelope.PayloadDigest(pub.Result) != pub.Digest:
			out.Message = "The stored result cannot be trusted."
		default:
			var res result.Result
			if err := json.Unmarshal(pub.Result, &res); err != nil {
				out.Message = "The App published a result it cannot show yet."
				return nil
			}
			view := BuildPullView(res)
			out.View = &view
		}
		return nil
	}
	if rec.RunID == 0 {
		out.Message = noCheckYet
		return nil
	}
	if rec.PolicyDigest == "" {
		out.Message = "The App published a result it cannot show yet."
		return nil
	}
	raw, err := v.gh.Artifact(ctx, r.RepoRef, rec.RunID, asks.ArtifactName, 5<<20)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrArtifactInvalid) {
		out.Message = resultGone
		return nil
	}
	if err != nil {
		return fmt.Errorf("pull: read artifact: %w", err)
	}
	if envelope.PayloadDigest(raw) != rec.PayloadDigest {
		out.Message = "The stored result cannot be trusted."
		return nil
	}
	res, err := result.Decode(raw)
	if err != nil {
		out.Message = "The stored result cannot be trusted."
		return nil
	}
	if rec.Status == string(result.StatusWaiting) {
		if res.Status != result.StatusFail {
			out.Message = "The stored result cannot be trusted."
			return nil
		}
	} else if string(res.Status) != rec.Status {
		out.Message = "The stored result cannot be trusted."
		return nil
	}
	if rec.Coverable || rec.Status == string(result.StatusWaiting) {
		res, _ = asks.Cover(res, reqs.index)
	}
	view := BuildPullView(res)
	out.View = &view
	return nil
}

// requestLoads bounds how many request records one pull page reads at once.
const requestLoads = 8

// pullRequests are the open requests and the numbers of those that wait on one pull request.
type pullRequests struct {
	done    sync.WaitGroup
	index   *records.Index
	waiting []int // sorted, each number once
	err     error
}

// startRequests reads the open request index and every request in it. Read it only after done.Wait.
func (v *pullViews) startRequests(ctx context.Context, r api.Repo, pull int) *pullRequests {
	p := &pullRequests{waiting: []int{}}
	p.done.Go(func() { p.index, p.waiting, p.err = v.waitingOn(ctx, r, pull) })
	return p
}

// waitingOn reads the request index and the requests in it, and returns the requests that list pull. A request that
// cannot be read, or an entry that names no request, fails the page; the first such entry in index order wins.
func (v *pullViews) waitingOn(ctx context.Context, r api.Repo, pull int) (*records.Index, []int, error) {
	index, err := asks.OpenIndex(ctx, v.store, r)
	if err != nil {
		return nil, nil, err
	}
	numbers := make([]int, len(index.Entries))
	stop := len(numbers) // the first entry that names no request: entries after it are never read
	for i, e := range index.Entries {
		if n, err := strconv.Atoi(e.ID); err != nil || n <= 0 {
			stop = i
			break
		} else {
			numbers[i] = n
		}
	}
	reqs := make([]records.Request, stop)
	errs := make([]error, stop)
	read := make(map[int]bool, stop)
	slots := make(chan struct{}, requestLoads)
	var loads sync.WaitGroup
	tenant := store.Tenant(r.InstallationID)
	for i, n := range numbers[:stop] {
		if read[n] {
			continue
		}
		read[n] = true
		loads.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			errs[i] = records.Load(ctx, v.store, tenant, records.RequestKey(r.FullName, n), &reqs[i])
		})
	}
	loads.Wait()
	waiting := []int{}
	for i, n := range numbers[:stop] {
		if errs[i] != nil {
			return nil, nil, errs[i]
		}
		if slices.Contains(reqs[i].Waiting, pull) && !slices.Contains(waiting, n) {
			waiting = append(waiting, n)
		}
	}
	if stop < len(numbers) {
		return nil, nil, errors.New("pull: invalid request index")
	}
	slices.Sort(waiting)
	return index, waiting, nil
}

// snapshot validates both captured values in one no-change swap; separate reads
// could pair an unpublished upload with a later publication's cleared marker.
func (v *pullViews) snapshot(ctx context.Context, r api.Repo, number int, head string) (records.Pull, records.Publication, bool, error) {
	var pull records.Pull
	var publication records.Publication
	var published bool
	keys := []string{records.PullKey(r.FullName, number), records.PublicationKey(r.FullName, number, head)}
	err := store.Update(ctx, v.store, store.Tenant(r.InstallationID), keys, func(cur [][]byte) ([][]byte, error) {
		pull, publication, published = records.Pull{}, records.Publication{}, cur[1] != nil
		if cur[0] != nil {
			if err := records.Decode(cur[0], &pull); err != nil {
				return nil, err
			}
		}
		if cur[1] != nil {
			if err := records.Decode(cur[1], &publication); err != nil {
				return nil, err
			}
		}
		return cur, nil
	})
	return pull, publication, published, err
}
