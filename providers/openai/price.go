package openai

import "github.com/abubakarsiddik31/golem/model"

// Price prices usage with per-million-token USD rates, e.g. the list
// rates for a model. OpenAI-compatible usage reports cached input inside
// the input total, so Cost bills Input−CacheRead at the input rate and
// CacheRead at the cache-read rate; reasoning tokens are part of billed
// output. Rates are a snapshot the application owns — verify them
// against the provider's current price list.
type Price struct {
	// InputPerMTok is the USD price per million input tokens.
	InputPerMTok float64
	// OutputPerMTok is the USD price per million output tokens.
	OutputPerMTok float64
	// CacheReadPerMTok is the USD price per million cached input tokens;
	// zero prices cached input at the input rate.
	CacheReadPerMTok float64
}

// Cost implements model.Price.
func (p Price) Cost(usage model.Usage) float64 {
	uncached := usage.InputTokens - usage.CacheReadTokens
	if uncached < 0 {
		uncached = 0
	}
	cacheRate := p.CacheReadPerMTok
	if cacheRate == 0 {
		cacheRate = p.InputPerMTok
	}
	return perMillion(uncached, p.InputPerMTok) + perMillion(usage.OutputTokens, p.OutputPerMTok) +
		perMillion(usage.CacheReadTokens, cacheRate)
}

// perMillion prices n tokens at a per-million-token rate.
func perMillion(n int, perMTok float64) float64 {
	return float64(n) / 1_000_000 * perMTok
}
