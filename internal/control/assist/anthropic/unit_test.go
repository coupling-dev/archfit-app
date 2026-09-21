package anthropic

import (
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

func TestMaxCostIsAnUpperBound(t *testing.T) {
	tests := []struct {
		name   string
		prompt assist.Prompt
		bytes  int64
		sonnet int64
		opus   int64
	}{
		{"ASCII", assist.Prompt{System: "system", User: "hello", MaxTokens: 1000}, 11, 10534, 26335},
		{"multi-byte UTF-8", assist.Prompt{System: "Ünï", User: "日本語", MaxTokens: 100}, 14, 1540, 3850},
		{"empty system", assist.Prompt{User: "hi", MaxTokens: 1}, 2, 526, 1315},
		{"16 KB user text", assist.Prompt{User: strings.Repeat("x", 16384), MaxTokens: 4096}, 16384, 74240, 185600},
	}
	for _, tt := range tests {
		for model, want := range map[string]int64{"claude-sonnet-5": tt.sonnet, "claude-opus-5": tt.opus} {
			t.Run(tt.name+"/"+model, func(t *testing.T) {
				p := newHarness(t, model).provider
				got := p.MaxCostMicroUSD(tt.prompt)
				if got != want {
					t.Fatalf("MaxCostMicroUSD = %d, want %d", got, want)
				}
				maxIn, maxOut := tt.bytes+256, int64(tt.prompt.MaxTokens)
				usages := []usage{
					{InputTokens: maxIn, OutputTokens: maxOut},
					{InputTokens: maxIn / 2, OutputTokens: maxOut},
					{InputTokens: 1, CacheCreationInputTokens: maxIn / 2, CacheReadInputTokens: maxIn/2 - 1, OutputTokens: maxOut / 2},
					{},
				}
				for _, u := range usages {
					if cost := p.price.cost(u); cost > got {
						t.Errorf("cost %d of usage %+v exceeds the bound %d", cost, u, got)
					}
				}
			})
		}
	}
}

func TestCostFromUsage(t *testing.T) {
	tests := []struct {
		name   string
		usage  usage
		sonnet int64
		opus   int64
	}{
		{"no cache fields", usage{InputTokens: 1000, OutputTokens: 500}, 7000, 17500},
		{"cache creation", usage{InputTokens: 10, CacheCreationInputTokens: 990, OutputTokens: 500}, 7000, 17500},
		{"cache read", usage{InputTokens: 10, CacheReadInputTokens: 90, OutputTokens: 1}, 210, 525},
		{"all cache fields", usage{InputTokens: 1, CacheCreationInputTokens: 2, CacheReadInputTokens: 3, OutputTokens: 4}, 52, 130},
		{"zero", usage{}, 0, 0},
	}
	for _, tt := range tests {
		for model, want := range map[string]int64{"claude-sonnet-5": tt.sonnet, "claude-opus-5": tt.opus} {
			t.Run(tt.name+"/"+model, func(t *testing.T) {
				p := newHarness(t, model).provider
				if got := p.price.cost(tt.usage); got != want {
					t.Errorf("cost = %d, want %d", got, want)
				}
			})
		}
	}
}

func TestRetryDelay(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"absent", "", 2 * time.Second},
		{"1", "1", time.Second},
		{"7", "7", 7 * time.Second},
		{"600", "600", 10 * time.Second},
		{"abc", "abc", 2 * time.Second},
		{"negative", "-5", 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryDelay(tt.header); got != tt.want {
				t.Errorf("retryDelay(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestPriceTable(t *testing.T) {
	got, err := loadPrices()
	if err != nil {
		t.Fatalf("loadPrices: %v", err)
	}
	want := map[string]price{
		"claude-sonnet-5": {Input: 2, Output: 10},
		"claude-opus-5":   {Input: 5, Output: 25},
	}
	if len(got) != len(want) {
		t.Fatalf("price table has %d models, want %d", len(got), len(want))
	}
	for model, p := range want {
		if got[model] != p {
			t.Errorf("price of %s = %+v, want %+v", model, got[model], p)
		}
	}
}
