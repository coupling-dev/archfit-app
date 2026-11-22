package requests

import (
	"testing"
	"time"
)

func TestAskerAtIsUTCOrEmpty(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	for _, tc := range []struct {
		name string
		when time.Time
		want string
	}{
		{"a time in another zone is written in UTC", time.Date(2026, 11, 22, 10, 30, 0, 0, berlin), "2026-11-22T09:30:00Z"},
		{"no time is no value", time.Time{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (asker{"dev", tc.when}).at(); got != tc.want {
				t.Errorf("at() = %q, want %q", got, tc.want)
			}
		})
	}
}
