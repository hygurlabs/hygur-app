// Package intent provides user intent detection for routing RAG queries to appropriate sources.
package intent

import "time"

// SourceType represents a data source that can be queried.
type SourceType string

const (
	// SourceKnowledge represents the knowledge base (notes, documents, files).
	SourceKnowledge SourceType = "knowledge"
	// SourceMail represents the email source (inbox, sent, etc.).
	SourceMail SourceType = "mail"
	// SourceAll represents all available sources.
	SourceAll SourceType = "all"
)

// TemporalMode indicates how to handle time-based ranking.
type TemporalMode string

const (
	// TemporalNone means no temporal preference detected.
	TemporalNone TemporalMode = ""
	// TemporalRecent means prioritize most recent items ("dernières", "récentes").
	TemporalRecent TemporalMode = "recent"
	// TemporalOldest means prioritize oldest items ("premières", "anciennes").
	TemporalOldest TemporalMode = "oldest"
	// TemporalRange means a specific date range was specified.
	TemporalRange TemporalMode = "range"
)

// Intent represents the detected intent from a user prompt.
type Intent struct {
	// Query is the cleaned prompt without source indicators.
	Query string
	// Sources lists the data sources to query.
	Sources []SourceType
	// Weights maps each source to its relative weight (sum should equal 1.0).
	Weights map[SourceType]float64
	// Confidence indicates how confident the detection is (0.0-1.0).
	Confidence float64
	// RawPrompt is the original user prompt before cleaning.
	RawPrompt string

	// Temporal fields for time-based ranking
	// TemporalMode indicates how to handle time-based ranking.
	TemporalMode TemporalMode
	// TemporalWeight is the weight to apply to recency (0.0-1.0).
	// Higher values prioritize recency over relevance.
	TemporalWeight float64
	// DateFrom is the start of the detected date range (inclusive). Nil if not detected.
	DateFrom *time.Time
	// DateTo is the end of the detected date range (inclusive). Nil if not detected.
	DateTo *time.Time
}

// DefaultWeights are applied when no specific source intent is detected.
// Equal weights ensure both sources are fairly represented.
var DefaultWeights = map[SourceType]float64{
	SourceKnowledge: 0.5,
	SourceMail:      0.5,
}

// ShouldSearchMail returns true if mail should be searched based on the intent.
func (i Intent) ShouldSearchMail() bool {
	for _, s := range i.Sources {
		if s == SourceMail || s == SourceAll {
			return true
		}
	}
	return false
}

// ShouldSearchKnowledge returns true if knowledge base should be searched based on the intent.
func (i Intent) ShouldSearchKnowledge() bool {
	for _, s := range i.Sources {
		if s == SourceKnowledge || s == SourceAll {
			return true
		}
	}
	return false
}

// GetWeight returns the weight for a given source type, defaulting to 0 if not found.
func (i Intent) GetWeight(source SourceType) float64 {
	if w, ok := i.Weights[source]; ok {
		return w
	}
	return 0
}
