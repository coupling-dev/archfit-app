package weekly

import (
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		entries []entry
		want    counts
	}{
		{"absent", nil, counts{}},
		{"recent pass and owner", []entry{{now, "pass"}, {now, "owner"}}, counts{Checked: 2, Passed: 1, Owner: 1, Known: true}},
		{"expired", []entry{{now.Add(-8 * 24 * time.Hour), "pass"}}, counts{Known: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarize(tt.entries, now); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
