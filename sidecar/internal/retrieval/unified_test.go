package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/intent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedSearcher_NewUnifiedSearcher(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := NewUnifiedSearcher(db, nil)
	assert.NotNil(t, searcher)
	assert.NotNil(t, searcher.detector)
	assert.NotNil(t, searcher.store)
}

func TestUnifiedSearcher_NewUnifiedSearcherWithDetector(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	customDetector := intent.NewDetectorWithOptions(
		intent.WithCustomMailPatterns([]string{"custom mail pattern"}),
	)

	searcher := NewUnifiedSearcherWithDetector(db, nil, customDetector)
	assert.NotNil(t, searcher)
	assert.Equal(t, customDetector, searcher.detector)
}

func TestUnifiedSearcher_EmptyQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := NewUnifiedSearcher(db, nil)
	result, err := searcher.Search(context.Background(), UnifiedSearchRequest{Query: "", TopK: 10})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.Results)
}

func TestUnifiedSearcher_NoLLMClient(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := NewUnifiedSearcher(db, nil)
	_, err := searcher.Search(context.Background(), UnifiedSearchRequest{Query: "test query", TopK: 10})
	assert.ErrorIs(t, err, ErrLLMClientRequired)
}

func TestUnifiedSearcher_IntentDetection(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		wantMail       bool
		wantKnowledge  bool
		wantConfidence float64
	}{
		{
			name:           "explicit mail query",
			query:          "find in my emails about project X",
			wantMail:       true,
			wantKnowledge:  false,
			wantConfidence: intent.ConfidenceExplicit,
		},
		{
			name:           "explicit knowledge query",
			query:          "search in my notes for API documentation",
			wantMail:       false,
			wantKnowledge:  true,
			wantConfidence: intent.ConfidenceExplicit,
		},
		{
			name:           "both sources query",
			query:          "find in my emails and in my notes about budget",
			wantMail:       true,
			wantKnowledge:  true,
			wantConfidence: intent.ConfidenceBoth,
		},
		{
			name:           "default query",
			query:          "how to implement authentication",
			wantMail:       true,
			wantKnowledge:  true,
			wantConfidence: intent.ConfidenceDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := intent.NewDetector()
			detected := detector.Detect(tt.query)

			assert.Equal(t, tt.wantConfidence, detected.Confidence)
			assert.Equal(t, tt.wantMail, detected.ShouldSearchMail())
			assert.Equal(t, tt.wantKnowledge, detected.ShouldSearchKnowledge())
		})
	}
}

func TestUnifiedSearchRequest_SourceOverride(t *testing.T) {
	req := UnifiedSearchRequest{
		Query:   "test query",
		TopK:    10,
		Sources: []intent.SourceType{intent.SourceMail},
	}
	assert.Len(t, req.Sources, 1)
	assert.Equal(t, intent.SourceMail, req.Sources[0])
}

func TestUnifiedSearchRequest_WeightOverride(t *testing.T) {
	req := UnifiedSearchRequest{
		Query: "test query",
		TopK:  10,
		Weights: map[intent.SourceType]float64{
			intent.SourceKnowledge: 0.8,
			intent.SourceMail:      0.2,
		},
	}
	assert.Equal(t, 0.8, req.Weights[intent.SourceKnowledge])
	assert.Equal(t, 0.2, req.Weights[intent.SourceMail])
}

func TestUnifiedSearchResponse_Structure(t *testing.T) {
	resp := UnifiedSearchResponse{
		Results: []UnifiedResult{
			{
				ChunkID:     "chunk1",
				ContentID:   "content1",
				SourceType:  "mail",
				Score:       0.85,
				Excerpt:     "Test excerpt",
				Title:       "Test Mail",
				MailFrom:    "sender@example.com",
				MailDate:    "2024-01-15",
				MailSubject: "Test Subject",
			},
		},
		Intent: &intent.Intent{
			Query:      "test",
			Sources:    []intent.SourceType{intent.SourceMail},
			Weights:    map[intent.SourceType]float64{intent.SourceMail: 0.9, intent.SourceKnowledge: 0.1},
			Confidence: 0.9,
		},
		SearchStats: UnifiedSearchStats{
			TotalResults:     1,
			KnowledgeResults: 0,
			MailResults:      1,
			SearchDuration:   100 * time.Millisecond,
		},
	}

	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "mail", resp.Results[0].SourceType)
	assert.Equal(t, "sender@example.com", resp.Results[0].MailFrom)
	assert.NotNil(t, resp.Intent)
	assert.Equal(t, 1, resp.SearchStats.MailResults)
}

func TestUnifiedResult_MailSpecificFields(t *testing.T) {
	result := UnifiedResult{
		ChunkID:     "chunk1",
		ContentID:   "content1",
		SourceType:  "mail",
		Score:       0.9,
		Excerpt:     "Email body excerpt",
		Title:       "Re: Project Update",
		MailFrom:    "john@example.com",
		MailDate:    "2024-04-22",
		MailSubject: "Project Update",
	}
	assert.Equal(t, "mail", result.SourceType)
	assert.Equal(t, "john@example.com", result.MailFrom)
}

func TestUnifiedResult_KnowledgeFields(t *testing.T) {
	result := UnifiedResult{
		ChunkID:    "chunk2",
		ContentID:  "content2",
		SourceType: "file",
		Score:      0.85,
		Excerpt:    "Document excerpt",
		Title:      "API Documentation.md",
		Metadata:   map[string]any{"file_path": "/docs/api.md"},
	}
	assert.Equal(t, "file", result.SourceType)
	assert.Empty(t, result.MailFrom)
}

// TestFreshnessFactor_DefaultApplied verifies that the freshness penalty
// applies to all results by default (not just "recent" intent).
func TestFreshnessFactor_DefaultApplied(t *testing.T) {
	now := time.Now()
	score := 1.0

	// Old document (1 year ago) should be penalized even without temporal intent.
	oldDate := now.Add(-365 * 24 * time.Hour)
	oldScore := freshnessFactor(score, oldDate, now, nil)

	// Fresh document should have a higher score.
	freshDate := now.Add(-1 * 24 * time.Hour)
	freshScore := freshnessFactor(score, freshDate, now, nil)

	assert.Greater(t, freshScore, oldScore, "fresh doc should score higher than old doc")
	assert.GreaterOrEqual(t, freshScore, 0.4, "freshness floor is 0.4")
	assert.LessOrEqual(t, freshScore, 1.0, "freshness max is 1.0")
}

// TestFreshnessFactor_TemporalRangePassthrough verifies that TemporalRange
// mode returns the score unchanged (date filter already applied upstream).
func TestFreshnessFactor_TemporalRangePassthrough(t *testing.T) {
	det := &intent.Intent{TemporalMode: intent.TemporalRange}
	score := 0.7
	date := time.Now().Add(-400 * 24 * time.Hour)

	result := freshnessFactor(score, date, time.Now(), det)
	assert.Equal(t, score, result, "TemporalRange should return score unchanged")
}

// TestFreshnessFactor_ZeroDatePenalty verifies that docs with no date get a mild penalty.
func TestFreshnessFactor_ZeroDatePenalty(t *testing.T) {
	score := 1.0
	result := freshnessFactor(score, time.Time{}, time.Now(), nil)
	assert.Equal(t, 0.7, result, "zero date should apply 0.7 multiplier")
}

// TestFreshnessFactor_RecentIntentStronger verifies TemporalRecent applies
// a stronger freshness bias (lower floor, shorter half-life).
func TestFreshnessFactor_RecentIntentStronger(t *testing.T) {
	det := &intent.Intent{TemporalMode: intent.TemporalRecent}
	now := time.Now()
	score := 1.0
	oldDate := now.Add(-90 * 24 * time.Hour)

	defaultScore := freshnessFactor(score, oldDate, now, nil)
	recentScore := freshnessFactor(score, oldDate, now, det)

	assert.Less(t, recentScore, defaultScore, "TemporalRecent should penalize old docs more aggressively")
}
