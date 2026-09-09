package gemini

import (
	"math"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestPriceCombinesCachedInputInsideTheTotal(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2, CacheReadPerMTok: 1}
	// Reasoning (thought) tokens ride inside the billed output total, so
	// they need no rate of their own.
	cost := price.Cost(model.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, ReasoningTokens: 400_000})
	if math.Abs(cost-6) > 1e-9 {
		t.Fatalf("Cost = %v, want 6", cost)
	}
}
