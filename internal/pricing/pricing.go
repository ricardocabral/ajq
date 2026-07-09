// Package pricing provides static, dated cost estimates for semantic backend calls.
package pricing

import "strings"

const (
	// TableDate records when the editable static price table below was last checked.
	TableDate = "2026-07"
	// InputTokenOverhead is the fixed per-judgement estimate added to promptChars/4.
	InputTokenOverhead = 256
	tokensPerMTok      = 1_000_000.0
)

type modelPrice struct {
	InputUSDPerMTok  float64
	OutputUSDPerMTok float64
}

var modelPrices = map[string]modelPrice{
	"claude-haiku-4-5": {InputUSDPerMTok: 1.00, OutputUSDPerMTok: 5.00},
	"claude-sonnet-5":  {InputUSDPerMTok: 3.00, OutputUSDPerMTok: 15.00},
	"claude-opus-4-8":  {InputUSDPerMTok: 5.00, OutputUSDPerMTok: 25.00},
}

// Estimate returns an unrounded estimated USD cost for calls judgements using a
// static $/MTok table. modelID may be raw (claude-haiku-4-5) or provider-prefixed
// (anthropic/claude-haiku-4-5, openrouter/anthropic/claude-haiku-4-5). Unknown
// models return known=false. Display rounding belongs to callers.
func Estimate(modelID string, calls int, promptChars int, maxOutputTokens int) (usd float64, known bool) {
	price, ok := modelPrices[normalizeModelID(modelID)]
	if !ok {
		return 0, false
	}
	if calls <= 0 {
		return 0, true
	}
	if promptChars < 0 {
		promptChars = 0
	}
	if maxOutputTokens < 0 {
		maxOutputTokens = 0
	}
	inputTokens := float64(promptChars)/4.0 + InputTokenOverhead
	inputCost := inputTokens / tokensPerMTok * price.InputUSDPerMTok
	outputCost := float64(maxOutputTokens) / tokensPerMTok * price.OutputUSDPerMTok
	return float64(calls) * (inputCost + outputCost), true
}

func normalizeModelID(modelID string) string {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if strings.HasPrefix(part, "claude-") {
			return part
		}
	}
	return trimmed
}
