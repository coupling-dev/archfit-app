package onboarding

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// Each call under a row trace logs one line with its operation and result class; without one nothing is logged.
func TestRowTraceLogsEachCallWithoutItsContent(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := newSetupHub()
	h.files["tip"] = map[string]string{".archfit.yaml": setupPolicy}
	g := tracedGitHub{h}

	if _, _, err := g.File(context.Background(), setupRepo.RepoRef, "tip", ".archfit.yaml"); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("untraced call logged: %s", logs.String())
	}

	ctx := WithRowTrace(context.Background(), 4, time.Now())
	_, _, _ = g.File(ctx, setupRepo.RepoRef, "tip", ".archfit.yaml")
	_, _, err := g.File(ctx, setupRepo.RepoRef, "tip", "missing.yaml")
	if err != api.ErrNotFound {
		t.Fatalf("error = %v; the wrapper must pass errors through", err)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("logged %d lines, want 2:\n%s", len(lines), logs.String())
	}
	for _, want := range []string{"msg=\"ui probe call\"", "row=4", "op=File"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q lacks %s", lines[0], want)
		}
	}
	if !strings.Contains(lines[1], "result=not_found") {
		t.Errorf("line %q lacks result=not_found", lines[1])
	}
	if strings.Contains(logs.String(), "missing.yaml") || strings.Contains(logs.String(), "tip") {
		t.Errorf("trace logs call content: %s", logs.String())
	}
}
