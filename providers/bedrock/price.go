package bedrock

import "github.com/abubakarsiddik31/golem/model"

// Price prices usage with per-million-token USD rates, e.g. the list
// rates for a model. Bedrock Converse usage reports cache reads and
// writes beside the input total, so Cost bills Input at the input rate
// and adds CacheRead and CacheWrite at their own rates; a zero cache
// rate prices that cache field's extra tokens at zero. Models without
// prompt caching report no cache fields, so the same rates price them.
// Rates are a snapshot the application owns — verify them against the
// provider's current price list.
type Price struct {
	// InputPerMTok is the USD price per million input tokens.
	InputPerMTok float64
	// OutputPerMTok is the USD price per million output tokens.
	OutputPerMTok float64
	// CacheReadPerMTok is the USD price per million cache-read tokens.
	CacheReadPerMTok float64
	// CacheWritePerMTok is the USD price per million cache-write tokens.
	CacheWritePerMTok float64
}

// Cost implements model.Price.
func (p Price) Cost(usage model.Usage) float64 {
	return perMillion(usage.InputTokens, p.InputPerMTok) +
		perMillion(usage.OutputTokens, p.OutputPerMTok) +
		perMillion(usage.CacheReadTokens, p.CacheReadPerMTok) +
		perMillion(usage.CacheWriteTokens, p.CacheWritePerMTok)
}

// perMillion prices n tokens at a per-million-token rate.
func perMillion(n int, perMTok float64) float64 {
	return float64(n) / 1_000_000 * perMTok
}
