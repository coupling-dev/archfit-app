package gate

import (
	"log/slog"
	"os"
	"testing"
)

// TestMain keeps the children's log lines out of the test output; tests that read logs swap the default themselves.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}
