package feedback

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

var errPublicationChanged = errors.New("feedback: publication changed")

func (s *serialized) markPending(ctx context.Context, p Publication4) error {
	rec := records.Publication{HeadSHA: p.Target.HeadSHA, RunID: p.RunID, RunAttempt: p.RunAttempt, Pending: true, Source: p.Source, Token: p.token}
	raw, err := records.Encode(&rec)
	if err != nil {
		return err
	}
	key := records.PublicationKey(p.Target.Repo.FullName, p.Target.PullRequest, p.Target.HeadSHA)
	return store.Update(ctx, s.store, store.Tenant(p.Target.Repo.InstallationID), []string{key}, func(cur [][]byte) ([][]byte, error) {
		if cur[0] != nil {
			var old records.Publication
			if err := records.Decode(cur[0], &old); err != nil {
				return nil, err
			}
			if old.RunID > p.RunID || old.RunID == p.RunID && old.RunAttempt > p.RunAttempt {
				return nil, errPublicationChanged
			}
		}
		return [][]byte{raw}, nil
	})
}

func (s *serialized) finishPublication(ctx context.Context, p Publication4) error {
	var raw []byte
	if p.Generated {
		body, err := json.Marshal(p.Result)
		if err != nil {
			return err
		}
		rec := records.Publication{HeadSHA: p.Target.HeadSHA, RunID: p.RunID, RunAttempt: p.RunAttempt, Result: body,
			Digest: envelope.PayloadDigest(body), PublishedAt: s.now(), Source: p.Source}
		raw, err = records.Encode(&rec)
		if errors.Is(err, records.ErrTooLarge) {
			rec.Result, rec.Omitted = nil, true
			raw, err = records.Encode(&rec)
		}
		if err != nil {
			return err
		}
	}
	key := records.PublicationKey(p.Target.Repo.FullName, p.Target.PullRequest, p.Target.HeadSHA)
	return store.Update(ctx, s.store, store.Tenant(p.Target.Repo.InstallationID), []string{key}, func(cur [][]byte) ([][]byte, error) {
		if cur[0] == nil {
			return nil, errPublicationChanged
		}
		var pending records.Publication
		if err := records.Decode(cur[0], &pending); err != nil {
			return nil, err
		}
		if !pending.Pending || pending.Token != p.token {
			return nil, errPublicationChanged
		}
		return [][]byte{raw}, nil
	})
}

func (s *serialized) newerPublication(ctx context.Context, p Publication4) (bool, error) {
	var rec records.Publication
	err := records.Load(ctx, s.store, store.Tenant(p.Target.Repo.InstallationID), records.PublicationKey(p.Target.Repo.FullName, p.Target.PullRequest, p.Target.HeadSHA), &rec)
	if errors.Is(err, records.ErrNoRecord) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rec.RunID > p.RunID || rec.RunID == p.RunID && rec.RunAttempt > p.RunAttempt, nil
}
