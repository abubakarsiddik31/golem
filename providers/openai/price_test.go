package openai

import (
	"math"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestPriceCombinesCachedInputInsideTheTotal(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2, CacheReadPerMTok: 1}
	cost := price.Cost(model.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000})
	// One million uncached input at 3, one million cached input at 1,
	// one million output at 2.
	if math.Abs(cost-6) > 1e-9 {
		t.Fatalf("Cost = %v, want 6", cost)
	}
}

func TestPriceBillsUnratedCacheAtTheInputRate(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2}
	cost := price.Cost(model.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000})
	// Without a cache rate, cached input cannot look free: all two
	// million input tokens bill at the input rate.
	if math.Abs(cost-8) > 1e-9 {
		t.Fatalf("Cost = %v, want 8", cost)
	}
}
