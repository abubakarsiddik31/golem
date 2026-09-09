package bedrock

import (
	"math"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestPriceCombinesCacheBesideTheInputTotal(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2, CacheReadPerMTok: 1, CacheWritePerMTok: 4}
	cost := price.Cost(model.Usage{
		InputTokens:      2_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  1_000_000,
		CacheWriteTokens: 500_000,
	})
	if math.Abs(cost-11) > 1e-9 {
		t.Fatalf("Cost = %v, want 11", cost)
	}
}

func TestPricePricesCachelessModelsFromTheSameRates(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2}
	cost := price.Cost(model.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000})
	if math.Abs(cost-8) > 1e-9 {
		t.Fatalf("Cost = %v, want 8", cost)
	}
}
