package anthropic

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// pricesJSON is the price table in micro-USD per token, from the vendor price list of 2026-06.
//
//go:embed prices.json
var pricesJSON []byte

// promptOverheadTokens covers the request framing on top of one token per prompt byte.
const promptOverheadTokens = 256

type price struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (u usage) valid() bool {
	return u.InputTokens >= 0 && u.OutputTokens >= 0 && u.CacheCreationInputTokens >= 0 && u.CacheReadInputTokens >= 0
}

func loadPrices() (map[string]price, error) {
	var table map[string]price
	if err := json.Unmarshal(pricesJSON, &table); err != nil {
		return nil, fmt.Errorf("anthropic: embedded price table: %w", err)
	}
	return table, nil
}

// cost counts cache tokens at the full input price, which stays an upper bound.
func (p price) cost(u usage) int64 {
	return p.Input*(u.InputTokens+u.CacheCreationInputTokens+u.CacheReadInputTokens) + p.Output*u.OutputTokens
}

// maxCost is an upper bound because a token is at least one byte.
func (p price) maxCost(promptBytes, maxTokens int) int64 {
	return p.Input*int64(promptBytes+promptOverheadTokens) + p.Output*int64(max(maxTokens, 0))
}
