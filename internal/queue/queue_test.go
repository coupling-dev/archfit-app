package queue_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/queue"
	"github.com/alexei-led/archfit-app/internal/queue/queuetest"
)

func TestInlineConformance(t *testing.T) {
	queuetest.Run(t, func(_ *testing.T, h queue.Handler) queue.Queue { return queue.NewInline(h) })
}

func TestInlineAcceptsAnyName(t *testing.T) {
	ran := 0
	q := queue.NewInline(func(_ context.Context, _ queue.Task) { ran++ })
	for _, name := range []string{"", "not valid/name", strings.Repeat("a", 501)} {
		if err := q.Enqueue(t.Context(), queue.Task{Name: name}); err != nil {
			t.Errorf("Enqueue(%q) = %v; the inline fallback must accept every task", name, err)
		}
	}
	if ran != 3 {
		t.Errorf("handler ran %d times, want 3", ran)
	}
}

func TestValidName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"72d3162e-cc78-11e3-81ab-4c9367dc0958_gate", true},
		{"a", true},
		{strings.Repeat("a", 500), true},
		{"", false},
		{strings.Repeat("a", 501), false},
		{"with/slash", false},
		{"with space", false},
		{"dot.name", false},
		{"ünïcode", false},
	}
	for _, tt := range tests {
		if got := queue.ValidName(tt.name); got != tt.want {
			t.Errorf("ValidName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
