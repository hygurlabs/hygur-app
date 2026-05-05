// Package handlers provides HTTP handlers for the Hygur API.
package handlers

// RAGConfig holds configuration for RAG-enhanced chat.
type RAGConfig struct {
	// Enabled determines if RAG is active by default.
	Enabled bool
	// TopK is the number of context sources to retrieve.
	TopK int
	// MaxContextTokens is the maximum number of tokens to use for context.
	MaxContextTokens int
	// MinConfidence is the minimum score threshold for including a source.
	MinConfidence float64
	// AlwaysSearch forces context retrieval even for simple queries.
	AlwaysSearch bool
	// ReRankEnabled enables LLM-based re-ranking of search results.
	ReRankEnabled bool
	// TemporalScoringMode is "additive" (default) or "multiplicative" (legacy).
	TemporalScoringMode string
	// CurrentStateFilterDays restricts the candidate pool to the last N days
	// when the query reads as a current-state question. 0 disables.
	CurrentStateFilterDays int
}

// DefaultRAGConfig provides sensible defaults for RAG configuration.
// Scores returned by UnifiedSearcher are normalized (top result = 1.0), so
// MinConfidence is now a meaningful relative threshold (0.05 = at least 5% as
// relevant as the best match).
// MaxContextTokens is set high (30k) for models with large context windows (200k+).
var DefaultRAGConfig = RAGConfig{
	Enabled:                true,
	TopK:                   10,
	MaxContextTokens:       30000,
	MinConfidence:          0.05, // Drop results scoring below 5% of the best match
	AlwaysSearch:           false,
	ReRankEnabled:          false,
	TemporalScoringMode:    "additive",
	CurrentStateFilterDays: 90,
}

// Validate checks if the configuration is valid and applies defaults where needed.
func (c *RAGConfig) Validate() RAGConfig {
	result := *c

	if result.TopK <= 0 {
		result.TopK = DefaultRAGConfig.TopK
	}
	if result.TopK > 50 {
		result.TopK = 50 // Cap at 50 documents max
	}

	if result.MaxContextTokens <= 0 {
		result.MaxContextTokens = DefaultRAGConfig.MaxContextTokens
	}
	if result.MaxContextTokens > 50000 {
		result.MaxContextTokens = 50000 // Cap at 50k tokens (~25% of 200k context)
	}

	if result.MinConfidence < 0 {
		result.MinConfidence = 0
	}
	if result.MinConfidence > 1 {
		result.MinConfidence = 1
	}

	return result
}
