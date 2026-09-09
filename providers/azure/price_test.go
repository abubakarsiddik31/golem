package azure

import (
	"math"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestPriceCombinesCachedInputInsideTheTotal(t *testing.T) {
	price := Price{InputPerMTok: 3, OutputPerMTok: 2, CacheReadPerMTok: 1}
	cost := price.Cost(model.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000})
	if math.Abs(cost-6) > 1e-9 {
		t.Fatalf("Cost = %v, want 6", cost)
	}
}
