package ui

import (
	"container/list"
	"context"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// reportCacheBytes bounds the report artifacts one instance keeps **[default]**: a few hundred typical reports or a
// dozen at the 5 MiB cap, a small share of the instance's 512 MiB.
const reportCacheBytes = 32 << 20

// artifactKey names one repository's artifact. The repository is part of the key, so one repository never reads
// another's artifact, whatever IDs GitHub hands out.
type artifactKey struct {
	installationID int64
	repo           string
	id             int64
}

// reportCache keeps the report artifacts the App downloaded, the least recently used evicted first. An artifact never
// changes, so an entry never goes stale. It sits behind the per-request authorization: a handler reads it only for
// the repository its scope authorized. Entries are shared: nobody may modify a payload it returns.
type reportCache struct {
	mu    sync.Mutex
	max   int64
	size  int64
	order *list.List // of *cachedArtifact, the most recently used first
	byKey map[artifactKey]*list.Element
}

type cachedArtifact struct {
	key     artifactKey
	payload []byte
}

func newReportCache(maxBytes int64) *reportCache {
	return &reportCache{max: maxBytes, order: list.New(), byKey: map[artifactKey]*list.Element{}}
}

func (c *reportCache) get(k artifactKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*cachedArtifact).payload, true
}

// put keeps payload under k unless it alone is over the bound, then evicts until the cache fits.
func (c *reportCache) put(k artifactKey, payload []byte) {
	n := int64(len(payload))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byKey[k]; ok || n > c.max {
		return
	}
	c.byKey[k] = c.order.PushFront(&cachedArtifact{k, payload})
	for c.size += n; c.size > c.max; {
		old := c.order.Remove(c.order.Back()).(*cachedArtifact)
		delete(c.byKey, old.key)
		c.size -= int64(len(old.payload))
	}
}

// reportPayload is the report artifact of a run. Listing the run's artifacts names the newest one; its download
// comes from the cache when this repository already fetched it. Callers check its digest as they did before.
func (s *server) reportPayload(ctx context.Context, repo api.RepoRef, runID int64) ([]byte, error) {
	id, err := s.gh.actions.ArtifactID(ctx, repo, runID, reportArtifact)
	if err != nil {
		return nil, err
	}
	k := artifactKey{repo.InstallationID, repo.FullName, id}
	if payload, ok := s.reports.get(k); ok {
		return payload, nil
	}
	payload, err := s.gh.actions.ArtifactByID(ctx, repo, id, report.MaxBytes)
	if err != nil {
		return nil, err
	}
	s.reports.put(k, payload)
	return payload, nil
}
