package anthropic

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestMain discards the package's log lines; captureLogs swaps in its own handler and restores this one.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
