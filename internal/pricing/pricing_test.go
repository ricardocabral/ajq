package pricing

import "testing"

func TestEstimateKnownModelUsesStableTokenFormula(t *testing.T) {
	usd, known := Estimate("claude-haiku-4-5", 2, 400, 512)
	if !known {
		t.Fatal("known = false, want true")
	}
	inputTokens := float64(400)/4.0 + InputTokenOverhead
	want := 2 * ((inputTokens / tokensPerMTok * 1.00) + (float64(512) / tokensPerMTok * 5.00))
	if usd != want {
		t.Fatalf("Estimate = %.12f, want %.12f", usd, want)
	}
}

func TestEstimateNormalizesProviderPrefixedModelIDs(t *testing.T) {
	raw, rawKnown := Estimate("claude-sonnet-5", 1, 100, 10)
	prefixed, prefixedKnown := Estimate("openrouter/anthropic/claude-sonnet-5", 1, 100, 10)
	if !rawKnown || !prefixedKnown {
		t.Fatalf("known raw=%v prefixed=%v, want both true", rawKnown, prefixedKnown)
	}
	if prefixed != raw {
		t.Fatalf("prefixed estimate = %v, want raw %v", prefixed, raw)
	}
}

func TestEstimateUnknownModel(t *testing.T) {
	usd, known := Estimate("gpt-test", 10, 1000, 100)
	if known || usd != 0 {
		t.Fatalf("Estimate unknown = (%v, %v), want (0, false)", usd, known)
	}
}

func TestEstimateZeroCallsKnownModel(t *testing.T) {
	usd, known := Estimate("anthropic/claude-opus-4-8", 0, 1000, 100)
	if !known || usd != 0 {
		t.Fatalf("Estimate zero calls = (%v, %v), want (0, true)", usd, known)
	}
}
