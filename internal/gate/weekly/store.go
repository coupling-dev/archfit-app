package weekly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

// Caveat must accompany any displayed counts: the best-effort index can miss writes.
const Caveat = "Counts include checks Archfit recorded; some checks may be missing"
const maxEntries = 500
const schema = "archfit-app.weekly.v1"

type observation struct {
	Pull     int       `json:"pull"`
	Run      int64     `json:"run"`
	Attempt  int       `json:"attempt"`
	At       time.Time `json:"at"`
	Category string    `json:"category"`
}
type snapshot struct {
	Schema  string        `json:"schema"`
	Entries []observation `json:"entries"`
}

func key(repo string) string { return "v4/weekly/" + strings.ToLower(repo) }

func decode(raw []byte) (snapshot, error) {
	var s snapshot
	if err := json.Unmarshal(raw, &s); err != nil || s.Schema != schema || len(s.Entries) > maxEntries {
		return snapshot{}, errors.New("weekly: unreadable summary")
	}
	seen := make(map[int]bool)
	for _, e := range s.Entries {
		if e.Pull <= 0 || e.Run <= 0 || e.Attempt <= 0 || e.At.IsZero() || seen[e.Pull] || (e.Category != "pass" && e.Category != "owner" && e.Category != "checked") {
			return snapshot{}, errors.New("weekly: unreadable summary")
		}
		seen[e.Pull] = true
	}
	return s, nil
}

// Record stores one accepted result, best effort; callers must not use errors to decide a gate.
func Record(ctx context.Context, s store.Store, tenant store.Tenant, repo string, pull int, run int64, attempt int, at time.Time, category string) error {
	if pull <= 0 || run <= 0 || attempt <= 0 || at.IsZero() || (category != "pass" && category != "owner" && category != "checked") {
		return errors.New("weekly: invalid observation")
	}
	return store.Update(ctx, s, tenant, []string{key(repo)}, func(cur [][]byte) ([][]byte, error) {
		snap := snapshot{Schema: schema, Entries: []observation{}}
		if cur[0] != nil {
			var err error
			snap, err = decode(cur[0])
			if err != nil {
				return nil, err
			}
		}
		for _, e := range snap.Entries {
			if e.Pull == pull && (e.Run > run || e.Run == run && e.Attempt >= attempt) {
				return cur, nil
			}
		}
		entries := make([]observation, 0, len(snap.Entries)+1)
		for _, e := range snap.Entries {
			if e.Pull != pull && e.At.After(at.Add(-7*24*time.Hour)) {
				entries = append(entries, e)
			}
		}
		entries = append(entries, observation{pull, run, attempt, at.UTC(), category})
		if len(entries) > maxEntries {
			entries = entries[len(entries)-maxEntries:]
		}
		snap.Entries = entries
		b, err := json.Marshal(snap)
		if err != nil {
			return nil, fmt.Errorf("weekly: encode: %w", err)
		}
		return [][]byte{b}, nil
	})
}

// Summary is recorded evidence, not an exhaustive count. Known is false for absent or unreadable evidence.
type Summary struct {
	Checked, Passed, Owner int
	Known                  bool
}

// Read returns an explicitly partial summary. An unreadable or absent record is unknown.
func Read(ctx context.Context, s store.Store, tenant store.Tenant, repo string, now time.Time) (Summary, error) {
	raw, err := s.Get(ctx, tenant, key(repo))
	if errors.Is(err, store.ErrNotFound) {
		return Summary{}, nil
	}
	if err != nil {
		return Summary{}, err
	}
	snap, err := decode(raw)
	if err != nil {
		return Summary{}, err
	}
	entries := make([]entry, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		entries = append(entries, entry{e.At, e.Category})
	}
	c := summarize(entries, now)
	return Summary(c), nil
}
