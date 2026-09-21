package internal_test

import (
	"log/slog"
	"net/http"
	"os"
	"testing"
)

// TestMain discards logs and closes the network: every outbound request of the test binary goes
// through router, which knows loopback and the current test's world only.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	http.DefaultTransport = router
	os.Exit(m.Run())
}
