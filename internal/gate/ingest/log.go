package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

// facts are the only values of an upload that reach the log: IDs, digests and the runner
// environment. Names, refs, paths, tokens, envelope JSON, payload and error text stay out.
type facts struct {
	repositoryID       int64
	installationID     int64
	runID              int64
	runAttempt         int
	runnerEnvironment  string
	digest             string
	policyChangeUnread bool // the envelope carried a policy_change that report.DecodePolicyChange refused
}

func (f *facts) log(ctx context.Context, kind envelope.Kind, problems []Problem, err error, elapsed time.Duration) {
	level, attrs := slog.LevelInfo, []slog.Attr{slog.String("kind", string(kind))}
	var rej *Rejection
	switch {
	case errors.As(err, &rej):
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("outcome", "rejected"), slog.Int("status", rej.Status), slog.String("code", rej.Code))
	case err != nil:
		level = slog.LevelError
		attrs = append(attrs, slog.String("outcome", "error"))
	default:
		attrs = append(attrs, slog.String("outcome", "accepted"), slog.Any("problems", problems))
	}
	if f.repositoryID != 0 { // set once the token is verified
		attrs = append(attrs,
			slog.Int64("repository_id", f.repositoryID),
			slog.Int64("run_id", f.runID),
			slog.Int("run_attempt", f.runAttempt),
			slog.String("runner_environment", f.runnerEnvironment))
	}
	if f.installationID != 0 {
		attrs = append(attrs, slog.Int64("installation_id", f.installationID))
	}
	if f.digest != "" {
		attrs = append(attrs, slog.String("digest", f.digest))
	}
	if f.policyChangeUnread {
		attrs = append(attrs, slog.Bool("policy_change_unread", true))
	}
	attrs = append(attrs, slog.Int64("duration_ms", elapsed.Milliseconds()))
	slog.Default().LogAttrs(ctx, level, "ingest upload", attrs...)
}
