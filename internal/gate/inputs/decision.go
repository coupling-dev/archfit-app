package inputs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	decisionVersion    = 2
	decisionAttempts   = 5
	decisionHistoryCap = 64
	decisionReasonCap  = 1000
)

var (
	ErrActiveObjections     = errors.New("inputs: active owner objections require an explicit override")
	ErrOwnerDecisionStale   = errors.New("inputs: decision candidate or objection set changed")
	ErrDecisionReason       = errors.New("inputs: this architecture decision requires a reason")
	ErrNoApprovalToWithdraw = errors.New("inputs: no active architecture approval by this owner to withdraw")
	ErrDecisionInvalid      = errors.New("inputs: unknown or malformed architecture decision")
	ErrDecisionHistoryFull  = errors.New("inputs: architecture decision history is full")
	errDecisionContention   = errors.New("inputs: decision store contention")
)

type decisionRecord struct {
	Version int             `json:"version"`
	NextID  uint64          `json:"next_id"`
	History []decisionEntry `json:"history"`
}

type decisionEntry struct {
	ID                      string            `json:"id"`
	Login                   string            `json:"login"`
	Action                  DecisionAction    `json:"action"`
	Reason                  string            `json:"reason,omitempty"`
	HeadSHA                 string            `json:"head_sha"`
	BaseSHA                 string            `json:"base_sha"`
	SettingsDigest          string            `json:"settings_digest"`
	Digests                 map[string]string `json:"digests"`
	At                      time.Time         `json:"at"`
	Overrode                []string          `json:"overrode,omitempty"`
	OverrodeObjections      []Objection       `json:"overrode_objections,omitempty"`
	WithdrawnReviewVersions []string          `json:"withdrawn_review_versions,omitempty"`
	Published               bool              `json:"published,omitempty"`
	CommentID               int64             `json:"comment_id,omitempty"`
}

func decisionKey(t Target) string {
	return "owner-decision/" + strings.ToLower(t.Repo.FullName) + "/" + fmt.Sprint(t.PullRequest)
}

func (r *resolver) readDecisionRecord(ctx context.Context, t Target) (decisionRecord, []byte, error) {
	raw, err := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), decisionKey(t))
	if errors.Is(err, store.ErrNotFound) {
		return decisionRecord{Version: decisionVersion}, nil, nil
	}
	if err != nil {
		return decisionRecord{}, nil, fmt.Errorf("inputs: read owner decisions: %w", err)
	}
	var rec decisionRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Version != decisionVersion || rec.NextID == 0 || len(rec.History) > decisionHistoryCap {
		return decisionRecord{}, nil, ErrDecisionInvalid
	}
	for _, entry := range rec.History {
		if entry.ID == "" || entry.Login == "" || entry.HeadSHA == "" || entry.BaseSHA == "" || len(entry.SettingsDigest) != 64 || entry.At.IsZero() ||
			!validDecisionAction(entry.Action) || !validReason(entry.Reason, entry.Action) || (entry.Published && entry.CommentID <= 0) {
			return decisionRecord{}, nil, ErrDecisionInvalid
		}
		if entry.Action == DecisionOverride {
			if len(entry.Overrode) == 0 || !sameObjectionIDs(entry.Overrode, objectionIDs(entry.OverrodeObjections)) {
				return decisionRecord{}, nil, ErrDecisionInvalid
			}
		} else if len(entry.Overrode) != 0 || len(entry.OverrodeObjections) != 0 {
			return decisionRecord{}, nil, ErrDecisionInvalid
		}
		if entry.Action == DecisionWithdraw {
			if len(entry.WithdrawnReviewVersions) > 1 || (len(entry.WithdrawnReviewVersions) == 1 && !strings.HasPrefix(entry.WithdrawnReviewVersions[0], "github:")) {
				return decisionRecord{}, nil, ErrDecisionInvalid
			}
		} else if len(entry.WithdrawnReviewVersions) != 0 {
			return decisionRecord{}, nil, ErrDecisionInvalid
		}
		for _, objection := range entry.OverrodeObjections {
			if objection.ID == "" || objection.Login == "" || (objection.Source != "app" && objection.Source != "github") ||
				utf8.RuneCountInString(objection.Reason) > decisionReasonCap {
				return decisionRecord{}, nil, ErrDecisionInvalid
			}
		}
	}
	return rec, raw, nil
}

func validDecisionAction(action DecisionAction) bool {
	switch action {
	case DecisionApprove, DecisionRequestChanges, DecisionWithdraw, DecisionOverride:
		return true
	default:
		return false
	}
}

func validReason(reason string, action DecisionAction) bool {
	if action == DecisionRequestChanges || action == DecisionWithdraw || action == DecisionOverride {
		return strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= decisionReasonCap
	}
	return utf8.RuneCountInString(reason) <= decisionReasonCap
}

// Decide serializes every App transition through a compare-and-swap on the per-pull decision record. It re-reads the
// protected candidate, owner/team authority, native reviews and current objections after a lost CAS.
func (r *resolver) Decide(ctx context.Context, t Target, login string, seen map[string]string, action DecisionAction, reason string, overrideIDs []string, now time.Time) error {
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return err
	}
	if !validDecisionAction(action) {
		return ErrDecisionInvalid
	}
	reason = strings.TrimSpace(reason)
	if !validReason(reason, action) {
		return ErrDecisionReason
	}
	if action != DecisionOverride && len(overrideIDs) != 0 {
		return ErrDecisionInvalid
	}
	if len(overrideIDs) > decisionHistoryCap {
		return ErrOwnerDecisionStale
	}
	for range decisionAttempts {
		s, paths, err := r.approvable(ctx, t, login)
		if err != nil {
			return err
		}
		digests, err := r.headDigests(ctx, t, paths)
		if err != nil {
			return err
		}
		if !maps.Equal(seen, digests) {
			return ErrDigestsChanged
		}
		reviews, err := r.pulls.Reviews(ctx, t.Repo.RepoRef, t.PullRequest)
		if err != nil {
			return fmt.Errorf("inputs: reviews: %w", err)
		}
		rec, old, err := r.readDecisionRecord(ctx, t)
		if err != nil {
			return err
		}
		var withdrawnReviewVersions []string
		if action == DecisionWithdraw {
			withdrawnReviewVersions = currentNativeApprovalVersions(reviews, login, t.HeadSHA)
		}
		objections, err := r.activeObjections(ctx, t, s, reviews, rec, digests)
		if err != nil {
			return err
		}
		switch action {
		case DecisionApprove:
			if hasBlockingObjection(objections, login) {
				return ErrActiveObjections
			}
			if latest := latestDecisionFor(rec, login); latest != nil && (latest.Action == DecisionApprove || latest.Action == DecisionOverride) && decisionMatches(*latest, t, s, digests) {
				return nil
			}
		case DecisionRequestChanges:
		case DecisionWithdraw:
			grant, err := r.ownerHasGrant(ctx, t, s, login, digests, reviews, rec)
			if err != nil {
				return err
			}
			if !grant {
				return ErrNoApprovalToWithdraw
			}
		case DecisionOverride:
			if !sameObjectionIDs(objectionIDs(objections), overrideIDs) || len(overrideIDs) == 0 {
				return ErrOwnerDecisionStale
			}
		}
		if len(rec.History) >= decisionHistoryCap {
			return ErrDecisionHistoryFull
		}
		rec.NextID++
		var overrodeObjections []Objection
		if action == DecisionOverride {
			overrodeObjections = slices.Clone(objections)
		}
		settingsJSON, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("inputs: encode protected settings: %w", err)
		}
		rec.History = append(rec.History, decisionEntry{
			ID: fmt.Sprintf("app:%d", rec.NextID), Login: login, Action: action, Reason: reason,
			HeadSHA: t.HeadSHA, BaseSHA: t.BaseSHA, SettingsDigest: policy.Digest(settingsJSON), Digests: maps.Clone(digests), At: now.UTC(), Overrode: slices.Sorted(slices.Values(overrideIDs)),
			OverrodeObjections: overrodeObjections, WithdrawnReviewVersions: slices.Clone(withdrawnReviewVersions),
		})
		rec.Version = decisionVersion
		next, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("inputs: encode owner decision: %w", err)
		}
		if len(next) > 512<<10 {
			return ErrDecisionHistoryFull
		}
		swapped, err := r.store.CompareAndSwap(ctx, store.Tenant(t.Repo.InstallationID), decisionKey(t), old, next)
		if err != nil {
			return fmt.Errorf("inputs: write owner decision: %w", err)
		}
		if swapped {
			return nil
		}
	}
	return errDecisionContention
}

// approvalDecision combines native reviews, active objections, current App grants and legacy approval records.
func (r *resolver) approvalDecision(ctx context.Context, t Target, s settings.Settings, paths []string, reviews []api.Review) (string, []Objection, []DecisionHistory, error) {
	rec, _, err := r.readDecisionRecord(ctx, t)
	if err != nil {
		return "", nil, nil, err
	}
	needCandidate := false
	for _, entry := range rec.History {
		if entry.HeadSHA == t.HeadSHA && (entry.Action == DecisionApprove || entry.Action == DecisionWithdraw || entry.Action == DecisionOverride) {
			needCandidate = true
			break
		}
	}
	_, legacyErr := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), approvalKey(t))
	if legacyErr != nil && !errors.Is(legacyErr, store.ErrNotFound) {
		return "", nil, nil, fmt.Errorf("inputs: read legacy approval: %w", legacyErr)
	}
	if legacyErr == nil {
		needCandidate = true
	}
	var current map[string]string
	if needCandidate {
		current, err = r.headDigests(ctx, t, paths)
		if err != nil {
			return "", nil, nil, err
		}
	}
	objections, err := r.activeObjections(ctx, t, s, reviews, rec, current)
	if err != nil {
		return "", nil, nil, err
	}
	history := make([]DecisionHistory, 0, len(rec.History))
	for _, entry := range rec.History {
		history = append(history, DecisionHistory{ID: entry.ID, Login: entry.Login, Action: entry.Action,
			Reason: entry.Reason, HeadSHA: entry.HeadSHA, BaseSHA: entry.BaseSHA, At: entry.At, Overrode: slices.Clone(entry.Overrode),
			OverrodeObjections: slices.Clone(entry.OverrodeObjections)})
	}
	if slices.ContainsFunc(objections, func(objection Objection) bool { return !objection.Overridden }) {
		return "", objections, history, nil
	}
	nativeReviews := suppressWithdrawnNativeApprovals(reviews, rec)
	by, err := approver(nativeReviews, s, t.HeadSHA, r.member(ctx, t))
	if err != nil {
		return "", nil, nil, fmt.Errorf("inputs: team membership: %w", err)
	}
	if by != "" {
		return by, objections, history, nil
	}
	by, err = r.appGrant(ctx, t, s, current, rec)
	return by, objections, history, err
}

func (r *resolver) activeObjections(ctx context.Context, t Target, s settings.Settings, reviews []api.Review, rec decisionRecord, candidate map[string]string) ([]Objection, error) {
	var objections []Objection
	latestApp := make(map[string]decisionEntry)
	for _, entry := range rec.History {
		latestApp[strings.ToLower(entry.Login)] = entry
	}
	ignored := make(map[string]bool)
	for _, login := range sortedDecisionKeys(latestApp) {
		entry := latestApp[login]
		if entry.Action != DecisionOverride || !decisionMatches(entry, t, s, candidate) || s.IsAgent(entry.Login) {
			continue
		}
		owner, err := isOwner(s.Owners, entry.Login, r.member(ctx, t))
		if err != nil {
			return nil, fmt.Errorf("inputs: team membership: %w", err)
		}
		if owner {
			for _, id := range entry.Overrode {
				ignored[id] = true
			}
		}
	}
	latest := make(map[string]int)
	for i, review := range reviews {
		if review.State != reviewCommented {
			latest[strings.ToLower(review.Reviewer)] = i
		}
	}
	for i, review := range reviews {
		if latest[strings.ToLower(review.Reviewer)] != i || review.Reviewer == "" || s.IsAgent(review.Reviewer) {
			continue
		}
		owner, err := isOwner(s.Owners, review.Reviewer, r.member(ctx, t))
		if err != nil {
			return nil, fmt.Errorf("inputs: team membership: %w", err)
		}
		if owner && review.State == reviewChangesRequested {
			id := nativeReviewID(review)
			objections = append(objections, Objection{ID: id, Login: review.Reviewer, Source: "github",
				Reason: boundedReason(review.Body), HeadSHA: review.CommitSHA, At: review.SubmittedAt, Overridden: ignored[id]})
		}
	}

	for _, login := range sortedDecisionKeys(latestApp) {
		entry := latestApp[login]
		if entry.Action != DecisionRequestChanges || s.IsAgent(entry.Login) {
			continue
		}
		owner, err := isOwner(s.Owners, entry.Login, r.member(ctx, t))
		if err != nil {
			return nil, fmt.Errorf("inputs: team membership: %w", err)
		}
		if owner {
			objections = append(objections, Objection{ID: entry.ID, Login: entry.Login, Source: "app", Reason: entry.Reason,
				HeadSHA: entry.HeadSHA, At: entry.At, Overridden: ignored[entry.ID]})
		}
	}
	return objections, nil
}

func nativeReviewID(review api.Review) string {
	identity := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s", review.ID, strings.ToLower(review.Reviewer), review.State,
		review.CommitSHA, review.SubmittedAt.UTC().Format(time.RFC3339Nano), review.Body)
	digest := sha256.Sum256([]byte(identity))
	version := hex.EncodeToString(digest[:12])
	if review.ID > 0 {
		return fmt.Sprintf("github:%d:%s", review.ID, version)
	}
	return "github:" + version
}

// currentNativeApprovalVersions returns the single latest native approval version that currently grants Archfit to login.
func currentNativeApprovalVersions(reviews []api.Review, login, headSHA string) []string {
	login = strings.ToLower(login)
	latest := make(map[string]int)
	for i, review := range reviews {
		if review.State != reviewCommented {
			latest[strings.ToLower(review.Reviewer)] = i
		}
	}
	for i, review := range reviews {
		if strings.ToLower(review.Reviewer) == login && latest[login] == i && review.State == reviewApproved && review.CommitSHA == headSHA {
			return []string{nativeApprovalVersion(review)}
		}
	}
	return nil
}

// nativeApprovalVersion identifies the submitted grant, not later edits to its comment text.
func nativeApprovalVersion(review api.Review) string {
	review.Body = ""
	return nativeReviewID(review)
}

// suppressWithdrawnNativeApprovals retains revocation of each submitted native grant across later actions and
// candidate changes. A new GitHub approval or an explicit App grant can authorize again; editing text cannot.
func suppressWithdrawnNativeApprovals(reviews []api.Review, rec decisionRecord) []api.Review {
	withdrawn := make(map[string]map[string]bool)
	for _, entry := range rec.History {
		if entry.Action != DecisionWithdraw {
			continue
		}
		login := strings.ToLower(entry.Login)
		if withdrawn[login] == nil {
			withdrawn[login] = make(map[string]bool)
		}
		for _, version := range entry.WithdrawnReviewVersions {
			withdrawn[login][version] = true
		}
	}
	var out []api.Review
	for i, review := range reviews {
		if review.State != reviewApproved || !withdrawn[strings.ToLower(review.Reviewer)][nativeApprovalVersion(review)] {
			continue
		}
		if out == nil {
			out = slices.Clone(reviews)
		}
		out[i].State = reviewDismissed
	}
	if out == nil {
		return reviews
	}
	return out
}

func boundedReason(reason string) string {
	runes := []rune(strings.TrimSpace(reason))
	if len(runes) > decisionReasonCap {
		runes = runes[:decisionReasonCap]
	}
	return string(runes)
}

func hasBlockingObjection(objections []Objection, login string) bool {
	for _, objection := range objections {
		if !objection.Overridden && (objection.Source == "github" || !strings.EqualFold(objection.Login, login)) {
			return true
		}
	}
	return false
}

func objectionIDs(objections []Objection) []string {
	ids := make([]string, 0, len(objections))
	for _, objection := range objections {
		ids = append(ids, objection.ID)
	}
	slices.Sort(ids)
	return ids
}

func sameObjectionIDs(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	return slices.Equal(slices.Sorted(slices.Values(want)), slices.Sorted(slices.Values(got)))
}

func latestDecisionFor(rec decisionRecord, login string) *decisionEntry {
	for i := len(rec.History) - 1; i >= 0; i-- {
		if strings.EqualFold(rec.History[i].Login, login) {
			return &rec.History[i]
		}
	}
	return nil
}

func (r *resolver) ownerHasGrant(ctx context.Context, t Target, s settings.Settings, login string, current map[string]string, reviews []api.Review, rec decisionRecord) (bool, error) {
	if latest := latestDecisionFor(rec, login); latest != nil && latest.Action == DecisionRequestChanges {
		return false, nil
	}
	grant, err := r.ownerHasAppGrant(ctx, t, s, login, current, rec)
	if err != nil || grant {
		return grant, err
	}
	return len(currentNativeApprovalVersions(suppressWithdrawnNativeApprovals(reviews, rec), login, t.HeadSHA)) != 0, nil
}

func (r *resolver) ownerHasAppGrant(ctx context.Context, t Target, s settings.Settings, login string, current map[string]string, rec decisionRecord) (bool, error) {
	if latest := latestDecisionFor(rec, login); latest != nil {
		return decisionMatches(*latest, t, s, current) &&
			(latest.Action == DecisionApprove || latest.Action == DecisionOverride), nil
	}
	raw, err := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), approvalKey(t))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inputs: read legacy approval: %w", err)
	}
	var legacy approvalRecord
	if json.Unmarshal(raw, &legacy) != nil || legacy.Login == "" || legacy.HeadSHA != t.HeadSHA ||
		!strings.EqualFold(legacy.Login, login) || !maps.Equal(legacy.Digests, current) || s.IsAgent(legacy.Login) {
		return false, nil
	}
	owner, err := isOwner(s.Owners, legacy.Login, r.member(ctx, t))
	if err != nil || !owner {
		return false, err
	}
	return true, nil
}

func (r *resolver) appGrant(ctx context.Context, t Target, s settings.Settings, current map[string]string, rec decisionRecord) (string, error) {
	raw, err := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), approvalKey(t))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("inputs: read legacy approval: %w", err)
	}
	if err == nil {
		var legacy approvalRecord
		if json.Unmarshal(raw, &legacy) != nil {
			logOutcome(t, "approval", "unreadable")
		} else if legacy.Login != "" && legacy.HeadSHA == t.HeadSHA && maps.Equal(legacy.Digests, current) && !s.IsAgent(legacy.Login) {
			owner, err := isOwner(s.Owners, legacy.Login, r.member(ctx, t))
			if err != nil {
				return "", fmt.Errorf("inputs: team membership: %w", err)
			}
			if owner {
				if latestDecisionFor(rec, legacy.Login) == nil {
					return legacy.Login, nil
				}
			}
		}
	}
	owners := make(map[string]bool)
	for _, entry := range rec.History {
		owners[strings.ToLower(entry.Login)] = true
	}
	for _, owner := range s.Owners {
		if !strings.Contains(owner, "/") {
			owners[strings.ToLower(owner)] = true
		}
	}
	for _, login := range sortedOwnerLogins(owners) {
		if login == "" || s.IsAgent(login) {
			continue
		}
		owner, err := isOwner(s.Owners, login, r.member(ctx, t))
		if err != nil {
			return "", fmt.Errorf("inputs: team membership: %w", err)
		}
		if !owner {
			continue
		}
		if latest := latestDecisionFor(rec, login); latest != nil && decisionMatches(*latest, t, s, current) &&
			(latest.Action == DecisionApprove || latest.Action == DecisionOverride) {
			return login, nil
		}
	}
	return "", nil
}

func sortedDecisionKeys(records map[string]decisionEntry) []string {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortedOwnerLogins(owners map[string]bool) []string {
	keys := make([]string, 0, len(owners))
	for key := range owners {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// decisionMatches binds authority to the settings content, not unrelated commits on the protected branch.
func decisionMatches(entry decisionEntry, t Target, s settings.Settings, current map[string]string) bool {
	raw, err := json.Marshal(s)
	return err == nil && entry.HeadSHA == t.HeadSHA && entry.SettingsDigest == policy.Digest(raw) && maps.Equal(entry.Digests, current)
}
