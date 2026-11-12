package requests

import (
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

var testToday = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

func mustPolicy(t *testing.T, text string) policy3.File {
	t.Helper()
	pol, err := policy3.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return pol
}
