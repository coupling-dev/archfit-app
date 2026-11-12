package requests

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// errNotOpen is a request the index names but whose record is not open: the index and the record disagree, so
// nothing joins it.
var errNotOpen = errors.New("requests: the request is not open")

func tenant(r api.Repo) store.Tenant { return store.Tenant(r.InstallationID) }

// OpenIndex reads the open requests of a repository by request key; a missing index is empty.
func OpenIndex(ctx context.Context, s store.Store, r api.Repo) (*records.Index, error) {
	x := &records.Index{Schema: records.SchemaRequestIndex}
	err := records.Load(ctx, s, tenant(r), records.RequestIndexKey(r.FullName), x)
	if errors.Is(err, records.ErrNoRecord) {
		return &records.Index{Schema: records.SchemaRequestIndex}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("requests: read the request index: %w", err)
	}
	return x, nil
}

// Wait records that pull waits on each request of ids, in the store only: the issue body is not edited, so an
// upload stays within its deadline. A request already listing pull is not written.
func Wait(ctx context.Context, s store.Store, r api.Repo, pull int, ids []int) error {
	for _, id := range ids {
		if _, _, _, err := join(ctx, s, r, id, pull, nil); err != nil && !errors.Is(err, errNotOpen) {
			return err
		}
	}
	return nil
}

// confirmation is what a pull request's verified result says of a request: its record and balance. It replaces
// numbers "as reported" by the CLI.
type confirmation struct {
	record  string
	balance *records.Balance
}

// join adds pull to the waiting pull requests of the open request id, and confirms its numbers when they are as
// reported and c is set. joined is true when pull was not waiting yet; changed when the record moved at all.
func join(ctx context.Context, s store.Store, r api.Repo, id, pull int, c *confirmation) (req records.Request, joined, changed bool, err error) {
	err = records.Indexed(ctx, s, tenant(r), records.RequestKey(r.FullName, id), records.RequestIndexKey(r.FullName),
		records.SchemaRequestIndex, func(cur []byte, _ *records.Index) ([]byte, error) {
			req, joined, changed = records.Request{}, false, false
			if cur == nil {
				return nil, errNotOpen
			}
			if err := records.Decode(cur, &req); err != nil {
				return nil, err
			}
			if req.State != records.StateOpen {
				return nil, errNotOpen
			}
			if !slices.Contains(req.Waiting, pull) {
				req.Waiting, joined, changed = append(req.Waiting, pull), true, true
			}
			if c != nil && !req.Numbers.Verified {
				req.Numbers, req.Record, changed = records.Numbers{Verified: true, Balance: c.balance}, c.record, true
			}
			if !changed {
				return cur, nil
			}
			return records.Encode(&req)
		})
	if err != nil && !errors.Is(err, errNotOpen) {
		err = fmt.Errorf("requests: join request %d: %w", id, err)
	}
	return req, joined, changed, err
}

// put keeps a new open request at the issue's number with its index entry, in one swap. The key held by another
// request is records.ErrExists, and the other's number is returned; the same request again is no change.
func put(ctx context.Context, s store.Store, r api.Repo, issue int, req records.Request) (other int, err error) {
	req.State = records.StateOpen
	err = records.Indexed(ctx, s, tenant(r), records.RequestKey(r.FullName, issue), records.RequestIndexKey(r.FullName),
		records.SchemaRequestIndex, func(cur []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(req.Key, strconv.Itoa(issue)); err != nil {
				id, _ := x.Lookup(req.Key)
				other, _ = strconv.Atoi(id)
				return nil, err
			}
			if cur != nil {
				return cur, nil
			}
			return records.Encode(&req)
		})
	return other, err
}

// PullClosed removes a closed pull request from the pull index; its pull record stays. A reopened pull request is
// indexed again by its next upload.
func PullClosed(ctx context.Context, s store.Store, r api.Repo, pull int) error {
	entry := records.PullEntry(pull)
	err := records.Indexed(ctx, s, tenant(r), records.PullKey(r.FullName, pull), records.PullIndexKey(r.FullName),
		records.SchemaPullIndex, func(cur []byte, x *records.Index) ([]byte, error) {
			x.Remove(entry)
			return cur, nil
		})
	if err != nil {
		return fmt.Errorf("requests: drop pull request %d from the pull index: %w", pull, err)
	}
	return nil
}
