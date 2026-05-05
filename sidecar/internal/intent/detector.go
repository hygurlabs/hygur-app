package intent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Weight constants for different intent scenarios.
const (
	// WeightExplicitPrimary is the weight for the explicitly mentioned source.
	WeightExplicitPrimary = 0.9
	// WeightExplicitSecondary is the weight for the non-mentioned source.
	WeightExplicitSecondary = 0.1
	// WeightBothSources is the weight when both sources are explicitly mentioned.
	WeightBothSources = 0.5
	// WeightSemanticHint is the weight for mail when semantic keywords are detected.
	WeightSemanticHint = 0.7
)

// Confidence levels for different detection outcomes.
const (
	// ConfidenceExplicit is the confidence when an explicit source is detected.
	ConfidenceExplicit = 0.9
	// ConfidenceBoth is the confidence when both sources are detected.
	ConfidenceBoth = 0.8
	// ConfidenceSemantic is the confidence when semantic hints are detected.
	ConfidenceSemantic = 0.7
	// ConfidenceDefault is the confidence when no explicit source is detected.
	ConfidenceDefault = 0.5
)

// Detector analyzes user prompts to extract source intents.
type Detector struct {
	// customMailPatterns allows adding custom mail detection patterns.
	customMailPatterns []*regexp.Regexp
	// customKnowledgePatterns allows adding custom knowledge detection patterns.
	customKnowledgePatterns []*regexp.Regexp
}

// NewDetector creates a new Detector with default configuration.
func NewDetector() *Detector {
	return &Detector{}
}

// DetectorOption configures a Detector.
type DetectorOption func(*Detector)

// WithCustomMailPatterns adds custom mail detection patterns.
func WithCustomMailPatterns(patterns []string) DetectorOption {
	return func(d *Detector) {
		for _, p := range patterns {
			if re, err := regexp.Compile(`(?i)` + p); err == nil {
				d.customMailPatterns = append(d.customMailPatterns, re)
			}
		}
	}
}

// WithCustomKnowledgePatterns adds custom knowledge detection patterns.
func WithCustomKnowledgePatterns(patterns []string) DetectorOption {
	return func(d *Detector) {
		for _, p := range patterns {
			if re, err := regexp.Compile(`(?i)` + p); err == nil {
				d.customKnowledgePatterns = append(d.customKnowledgePatterns, re)
			}
		}
	}
}

// NewDetectorWithOptions creates a Detector with custom options.
func NewDetectorWithOptions(opts ...DetectorOption) *Detector {
	d := NewDetector()
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Detect analyzes a prompt and returns the detected intent.
func (d *Detector) Detect(prompt string) Intent {
	intent := Intent{
		RawPrompt:      prompt,
		Query:          prompt,
		Sources:        []SourceType{},
		Weights:        make(map[SourceType]float64),
		Confidence:     ConfidenceDefault,
		TemporalMode:   TemporalNone,
		TemporalWeight: 0,
	}

	// Check for explicit mail intent
	hasMailIntent := d.matchesMail(prompt)

	// Check for explicit knowledge intent
	hasKnowledgeIntent := d.matchesKnowledge(prompt)

	// Check for semantic mail keywords (transactional content hints)
	hasMailSemanticHint := d.matchesMailSemantic(prompt)

	// Check for temporal/recency intent
	hasTemporalRecent := d.matchesTemporalRecent(prompt)
	if hasTemporalRecent {
		intent.TemporalMode = TemporalRecent
		// Weight: how much to prioritize recency over relevance
		// 0.7 means 70% recency, 30% relevance
		intent.TemporalWeight = 0.7
	}

	// Check for temporal range (specific month / date range).
	// TemporalRange takes precedence over TemporalRecent — it is more specific.
	if matched, from, to := extractTemporalRange(prompt); matched {
		intent.TemporalMode = TemporalRange
		intent.TemporalWeight = 0
		intent.DateFrom = &from
		intent.DateTo = &to
	}

	// Determine sources and weights based on detected intents
	switch {
	case hasMailIntent && !hasKnowledgeIntent:
		// Only mail explicitly mentioned
		intent.Sources = []SourceType{SourceMail}
		intent.Weights[SourceMail] = WeightExplicitPrimary
		intent.Weights[SourceKnowledge] = WeightExplicitSecondary
		intent.Confidence = ConfidenceExplicit
		intent.Query = d.cleanQuery(prompt, mailRegex)

	case hasKnowledgeIntent && !hasMailIntent:
		// Only knowledge explicitly mentioned
		intent.Sources = []SourceType{SourceKnowledge}
		intent.Weights[SourceKnowledge] = WeightExplicitPrimary
		intent.Weights[SourceMail] = WeightExplicitSecondary
		intent.Confidence = ConfidenceExplicit
		intent.Query = d.cleanQuery(prompt, knowledgeRegex)

	case hasMailIntent && hasKnowledgeIntent:
		// Both explicitly mentioned
		intent.Sources = []SourceType{SourceKnowledge, SourceMail}
		intent.Weights[SourceKnowledge] = WeightBothSources
		intent.Weights[SourceMail] = WeightBothSources
		intent.Confidence = ConfidenceBoth
		// Clean both patterns from query
		cleaned := d.cleanQuery(prompt, mailRegex)
		intent.Query = d.cleanQuery(cleaned, knowledgeRegex)

	case hasMailSemanticHint:
		// Semantic keywords suggest mail content (recharge, facture, etc.)
		intent.Sources = []SourceType{SourceKnowledge, SourceMail}
		intent.Weights[SourceMail] = WeightSemanticHint
		intent.Weights[SourceKnowledge] = 1.0 - WeightSemanticHint
		intent.Confidence = ConfidenceSemantic

	default:
		// No explicit source intent - search all with equal weights
		intent.Sources = []SourceType{SourceKnowledge, SourceMail}
		intent.Weights = copyWeights(DefaultWeights)
		intent.Confidence = ConfidenceDefault
	}

	return intent
}

// matchesMail checks if the prompt matches any mail pattern.
func (d *Detector) matchesMail(prompt string) bool {
	if mailRegex.MatchString(prompt) {
		return true
	}
	for _, re := range d.customMailPatterns {
		if re.MatchString(prompt) {
			return true
		}
	}
	return false
}

// matchesKnowledge checks if the prompt matches any knowledge pattern.
func (d *Detector) matchesKnowledge(prompt string) bool {
	if knowledgeRegex.MatchString(prompt) {
		return true
	}
	for _, re := range d.customKnowledgePatterns {
		if re.MatchString(prompt) {
			return true
		}
	}
	return false
}

// matchesMailSemantic checks if the prompt contains semantic keywords
// that suggest transactional/email content (recharge, facture, etc.).
func (d *Detector) matchesMailSemantic(prompt string) bool {
	return mailSemanticRegex.MatchString(prompt)
}

// matchesTemporalRecent checks if the prompt indicates a preference for recent items.
func (d *Detector) matchesTemporalRecent(prompt string) bool {
	return temporalRecentRegex.MatchString(prompt)
}

// cleanQuery removes source indicator patterns from the prompt.
func (d *Detector) cleanQuery(prompt string, pattern *regexp.Regexp) string {
	cleaned := pattern.ReplaceAllString(prompt, "")
	// Normalize whitespace: collapse multiple spaces and trim
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned)
}

// extractTemporalRange looks for a month name (and optional explicit year) in
// the prompt. Returns matched=false when nothing is found.
// When matched, [from, to] spans the full calendar month, UTC.
func extractTemporalRange(prompt string) (matched bool, from, to time.Time) {
	loc := time.UTC
	now := time.Now().In(loc)
	lower := strings.ToLower(prompt)

	// Try to find a month name.
	match := temporalRangeRegex.FindString(lower)
	if match == "" {
		return false, time.Time{}, time.Time{}
	}

	monthNum, ok := temporalRangeMonths[match]
	if !ok {
		return false, time.Time{}, time.Time{}
	}

	// Try to extract an explicit 4-digit year near the month name.
	yearRe := regexp.MustCompile(`\b(20\d{2})\b`)
	year := now.Year()
	if m := yearRe.FindStringSubmatch(prompt); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil {
			year = y
		}
	} else {
		// No explicit year: use the most-recent past occurrence of this month.
		if monthNum > int(now.Month()) {
			year = now.Year() - 1
		}
	}

	from = time.Date(year, time.Month(monthNum), 1, 0, 0, 0, 0, loc)
	// Last day of month: first day of next month minus 1 ns.
	to = time.Date(year, time.Month(monthNum+1), 1, 0, 0, 0, 0, loc).Add(-time.Nanosecond)
	return true, from, to
}

// copyWeights creates a copy of a weights map.
func copyWeights(src map[SourceType]float64) map[SourceType]float64 {
	dst := make(map[SourceType]float64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
