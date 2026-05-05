package intent

import (
	"math"
	"testing"
	"time"
)

// floatEquals checks if two floats are approximately equal.
func floatEquals(a, b, tolerance float64) bool {
	return math.Abs(a-b) < tolerance
}

// TestNewDetector tests the constructor.
func TestNewDetector(t *testing.T) {
	d := NewDetector()
	if d == nil {
		t.Fatal("NewDetector returned nil")
	}
}

// TestDetector_MailIntent tests detection of mail-only intent.
func TestDetector_MailIntent(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name           string
		prompt         string
		wantMailWeight float64
		wantKBWeight   float64
		wantConfidence float64
	}{
		{
			name:           "French: dans mes mails",
			prompt:         "Qu'est-ce que Jean m'a dit dans mes mails ?",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans mes emails",
			prompt:         "Cherche les factures dans mes emails",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans ma messagerie",
			prompt:         "Trouve le message de Marie dans ma messagerie",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans mes courriels",
			prompt:         "Recherche dans mes courriels",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "English: in my inbox",
			prompt:         "Find the meeting notes in my inbox",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "English: from my emails",
			prompt:         "Get the invoice from my emails",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
		{
			name:           "English: in my email",
			prompt:         "Search for John's message in my email",
			wantMailWeight: 0.9,
			wantKBWeight:   0.1,
			wantConfidence: 0.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			if !intent.ShouldSearchMail() {
				t.Error("expected ShouldSearchMail() to be true")
			}

			if !floatEquals(intent.Weights[SourceMail], tt.wantMailWeight, 0.01) {
				t.Errorf("mail weight = %v, want %v", intent.Weights[SourceMail], tt.wantMailWeight)
			}

			if !floatEquals(intent.Weights[SourceKnowledge], tt.wantKBWeight, 0.01) {
				t.Errorf("knowledge weight = %v, want %v", intent.Weights[SourceKnowledge], tt.wantKBWeight)
			}

			if !floatEquals(intent.Confidence, tt.wantConfidence, 0.01) {
				t.Errorf("confidence = %v, want %v", intent.Confidence, tt.wantConfidence)
			}

			// Primary source should be mail
			if len(intent.Sources) != 1 || intent.Sources[0] != SourceMail {
				t.Errorf("sources = %v, want [mail]", intent.Sources)
			}
		})
	}
}

// TestDetector_KnowledgeIntent tests detection of knowledge-only intent.
func TestDetector_KnowledgeIntent(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name           string
		prompt         string
		wantMailWeight float64
		wantKBWeight   float64
		wantConfidence float64
	}{
		{
			name:           "French: dans mes notes",
			prompt:         "Resume le projet X dans mes notes",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans mes documents",
			prompt:         "Trouve le rapport dans mes documents",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans ma base",
			prompt:         "Cherche dans ma base de connaissances",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "French: dans mes fichiers",
			prompt:         "Le contrat est dans mes fichiers",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "English: in my notes",
			prompt:         "Find the meeting summary in my notes",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "English: in my documents",
			prompt:         "Search for the contract in my documents",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "English: from my knowledge",
			prompt:         "Get information from my knowledge base",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
		{
			name:           "English: in my files",
			prompt:         "Look it up in my files",
			wantMailWeight: 0.1,
			wantKBWeight:   0.9,
			wantConfidence: 0.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			if !intent.ShouldSearchKnowledge() {
				t.Error("expected ShouldSearchKnowledge() to be true")
			}

			if !floatEquals(intent.Weights[SourceMail], tt.wantMailWeight, 0.01) {
				t.Errorf("mail weight = %v, want %v", intent.Weights[SourceMail], tt.wantMailWeight)
			}

			if !floatEquals(intent.Weights[SourceKnowledge], tt.wantKBWeight, 0.01) {
				t.Errorf("knowledge weight = %v, want %v", intent.Weights[SourceKnowledge], tt.wantKBWeight)
			}

			if !floatEquals(intent.Confidence, tt.wantConfidence, 0.01) {
				t.Errorf("confidence = %v, want %v", intent.Confidence, tt.wantConfidence)
			}

			// Primary source should be knowledge
			if len(intent.Sources) != 1 || intent.Sources[0] != SourceKnowledge {
				t.Errorf("sources = %v, want [knowledge]", intent.Sources)
			}
		})
	}
}

// TestDetector_BothIntents tests detection when both sources are mentioned.
func TestDetector_BothIntents(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "French: mails et notes",
			prompt: "Cherche dans mes mails et dans mes notes",
		},
		{
			name:   "French: documents et emails",
			prompt: "Trouve le rapport dans mes documents et dans mes emails",
		},
		{
			name:   "English: inbox and files",
			prompt: "Search in my inbox and in my files",
		},
		{
			name:   "Mixed: mails and documents",
			prompt: "Look in my mails and in my documents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			if !intent.ShouldSearchMail() {
				t.Error("expected ShouldSearchMail() to be true")
			}

			if !intent.ShouldSearchKnowledge() {
				t.Error("expected ShouldSearchKnowledge() to be true")
			}

			if !floatEquals(intent.Weights[SourceMail], 0.5, 0.01) {
				t.Errorf("mail weight = %v, want 0.5", intent.Weights[SourceMail])
			}

			if !floatEquals(intent.Weights[SourceKnowledge], 0.5, 0.01) {
				t.Errorf("knowledge weight = %v, want 0.5", intent.Weights[SourceKnowledge])
			}

			if !floatEquals(intent.Confidence, 0.8, 0.01) {
				t.Errorf("confidence = %v, want 0.8", intent.Confidence)
			}

			// Should have both sources
			if len(intent.Sources) != 2 {
				t.Errorf("sources count = %d, want 2", len(intent.Sources))
			}
		})
	}
}

// TestDetector_NoIntent tests detection when no explicit source is mentioned.
func TestDetector_NoIntent(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "general question",
			prompt: "Quel est le capital de la France ?",
		},
		{
			name:   "project question",
			prompt: "What is the status of project Alpha?",
		},
		{
			name:   "person question",
			prompt: "Qui est Jean Dupont ?",
		},
		{
			name:   "technical question",
			prompt: "How does the authentication work?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			// Should search both by default
			if !intent.ShouldSearchMail() {
				t.Error("expected ShouldSearchMail() to be true (default)")
			}

			if !intent.ShouldSearchKnowledge() {
				t.Error("expected ShouldSearchKnowledge() to be true (default)")
			}

			// Default weights (equal when no explicit intent)
			if !floatEquals(intent.Weights[SourceMail], 0.5, 0.01) {
				t.Errorf("mail weight = %v, want 0.5 (default)", intent.Weights[SourceMail])
			}

			if !floatEquals(intent.Weights[SourceKnowledge], 0.5, 0.01) {
				t.Errorf("knowledge weight = %v, want 0.5 (default)", intent.Weights[SourceKnowledge])
			}

			if !floatEquals(intent.Confidence, 0.5, 0.01) {
				t.Errorf("confidence = %v, want 0.5 (default)", intent.Confidence)
			}

			// Query should be unchanged
			if intent.Query != tt.prompt {
				t.Errorf("query = %q, want %q (unchanged)", intent.Query, tt.prompt)
			}
		})
	}
}

// TestDetector_CleanQuery tests that source indicators are removed from the query.
func TestDetector_CleanQuery(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name      string
		prompt    string
		wantQuery string
	}{
		{
			name:      "French mail pattern removed",
			prompt:    "Qu'est-ce que Jean m'a dit dans mes mails sur le projet ?",
			wantQuery: "Qu'est-ce que Jean m'a dit sur le projet ?",
		},
		{
			name:      "French notes pattern removed",
			prompt:    "Resume dans mes notes le projet Alpha",
			wantQuery: "Resume le projet Alpha",
		},
		{
			name:      "English inbox pattern removed",
			prompt:    "Find the invoice in my inbox from last week",
			wantQuery: "Find the invoice from last week",
		},
		{
			name:      "English documents pattern removed",
			prompt:    "Search in my documents for the contract",
			wantQuery: "Search for the contract",
		},
		{
			name:      "Both patterns removed",
			prompt:    "Cherche dans mes mails et dans mes notes le rapport",
			wantQuery: "Cherche et le rapport",
		},
		{
			name:      "Pattern at start",
			prompt:    "Dans mes emails trouve le message de Pierre",
			wantQuery: "trouve le message de Pierre",
		},
		{
			name:      "Pattern at end",
			prompt:    "Trouve le rapport dans mes fichiers",
			wantQuery: "Trouve le rapport",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			if intent.Query != tt.wantQuery {
				t.Errorf("query = %q, want %q", intent.Query, tt.wantQuery)
			}

			// RawPrompt should always be preserved
			if intent.RawPrompt != tt.prompt {
				t.Errorf("rawPrompt = %q, want %q", intent.RawPrompt, tt.prompt)
			}
		})
	}
}

// TestIntent_ShouldSearchMail tests the ShouldSearchMail helper.
func TestIntent_ShouldSearchMail(t *testing.T) {
	tests := []struct {
		name    string
		sources []SourceType
		want    bool
	}{
		{
			name:    "mail only",
			sources: []SourceType{SourceMail},
			want:    true,
		},
		{
			name:    "knowledge only",
			sources: []SourceType{SourceKnowledge},
			want:    false,
		},
		{
			name:    "both",
			sources: []SourceType{SourceKnowledge, SourceMail},
			want:    true,
		},
		{
			name:    "all",
			sources: []SourceType{SourceAll},
			want:    true,
		},
		{
			name:    "empty",
			sources: []SourceType{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := Intent{Sources: tt.sources}
			if got := intent.ShouldSearchMail(); got != tt.want {
				t.Errorf("ShouldSearchMail() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIntent_ShouldSearchKnowledge tests the ShouldSearchKnowledge helper.
func TestIntent_ShouldSearchKnowledge(t *testing.T) {
	tests := []struct {
		name    string
		sources []SourceType
		want    bool
	}{
		{
			name:    "mail only",
			sources: []SourceType{SourceMail},
			want:    false,
		},
		{
			name:    "knowledge only",
			sources: []SourceType{SourceKnowledge},
			want:    true,
		},
		{
			name:    "both",
			sources: []SourceType{SourceKnowledge, SourceMail},
			want:    true,
		},
		{
			name:    "all",
			sources: []SourceType{SourceAll},
			want:    true,
		},
		{
			name:    "empty",
			sources: []SourceType{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := Intent{Sources: tt.sources}
			if got := intent.ShouldSearchKnowledge(); got != tt.want {
				t.Errorf("ShouldSearchKnowledge() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIntent_GetWeight tests the GetWeight helper.
func TestIntent_GetWeight(t *testing.T) {
	intent := Intent{
		Weights: map[SourceType]float64{
			SourceMail:      0.7,
			SourceKnowledge: 0.3,
		},
	}

	if got := intent.GetWeight(SourceMail); got != 0.7 {
		t.Errorf("GetWeight(SourceMail) = %v, want 0.7", got)
	}

	if got := intent.GetWeight(SourceKnowledge); got != 0.3 {
		t.Errorf("GetWeight(SourceKnowledge) = %v, want 0.3", got)
	}

	// Unknown source should return 0
	if got := intent.GetWeight(SourceAll); got != 0 {
		t.Errorf("GetWeight(SourceAll) = %v, want 0", got)
	}
}

// TestDetectorWithOptions tests custom pattern options.
func TestDetectorWithOptions(t *testing.T) {
	d := NewDetectorWithOptions(
		WithCustomMailPatterns([]string{`dans mes lettres`}),
		WithCustomKnowledgePatterns([]string{`dans mon wiki`}),
	)

	t.Run("custom mail pattern", func(t *testing.T) {
		intent := d.Detect("Cherche dans mes lettres le message")
		if !intent.ShouldSearchMail() {
			t.Error("expected custom mail pattern to be detected")
		}
		if !floatEquals(intent.Weights[SourceMail], 0.9, 0.01) {
			t.Errorf("mail weight = %v, want 0.9", intent.Weights[SourceMail])
		}
	})

	t.Run("custom knowledge pattern", func(t *testing.T) {
		intent := d.Detect("Trouve la doc dans mon wiki")
		if !intent.ShouldSearchKnowledge() {
			t.Error("expected custom knowledge pattern to be detected")
		}
		if !floatEquals(intent.Weights[SourceKnowledge], 0.9, 0.01) {
			t.Errorf("knowledge weight = %v, want 0.9", intent.Weights[SourceKnowledge])
		}
	})

	t.Run("standard patterns still work", func(t *testing.T) {
		intent := d.Detect("Cherche dans mes mails")
		if !intent.ShouldSearchMail() {
			t.Error("expected standard mail pattern to still work")
		}
	})
}

// TestDefaultWeights tests that DefaultWeights are properly defined.
func TestDefaultWeights(t *testing.T) {
	total := DefaultWeights[SourceKnowledge] + DefaultWeights[SourceMail]
	if !floatEquals(total, 1.0, 0.01) {
		t.Errorf("default weights sum = %v, want 1.0", total)
	}

	// Equal weights ensure fair representation from both sources
	if DefaultWeights[SourceKnowledge] != 0.5 {
		t.Errorf("default knowledge weight = %v, want 0.5", DefaultWeights[SourceKnowledge])
	}

	if DefaultWeights[SourceMail] != 0.5 {
		t.Errorf("default mail weight = %v, want 0.5", DefaultWeights[SourceMail])
	}
}

// TestSourceTypeConstants tests that SourceType constants are properly defined.
func TestSourceTypeConstants(t *testing.T) {
	if SourceKnowledge != "knowledge" {
		t.Errorf("SourceKnowledge = %q, want 'knowledge'", SourceKnowledge)
	}
	if SourceMail != "mail" {
		t.Errorf("SourceMail = %q, want 'mail'", SourceMail)
	}
	if SourceAll != "all" {
		t.Errorf("SourceAll = %q, want 'all'", SourceAll)
	}
}

// TestWeightConstants tests that weight constants are properly defined.
func TestWeightConstants(t *testing.T) {
	if WeightExplicitPrimary != 0.9 {
		t.Errorf("WeightExplicitPrimary = %v, want 0.9", WeightExplicitPrimary)
	}
	if WeightExplicitSecondary != 0.1 {
		t.Errorf("WeightExplicitSecondary = %v, want 0.1", WeightExplicitSecondary)
	}
	if WeightBothSources != 0.5 {
		t.Errorf("WeightBothSources = %v, want 0.5", WeightBothSources)
	}
}

// TestConfidenceConstants tests that confidence constants are properly defined.
func TestConfidenceConstants(t *testing.T) {
	if ConfidenceExplicit != 0.9 {
		t.Errorf("ConfidenceExplicit = %v, want 0.9", ConfidenceExplicit)
	}
	if ConfidenceBoth != 0.8 {
		t.Errorf("ConfidenceBoth = %v, want 0.8", ConfidenceBoth)
	}
	if ConfidenceDefault != 0.5 {
		t.Errorf("ConfidenceDefault = %v, want 0.5", ConfidenceDefault)
	}
}

// TestCaseInsensitivity tests that pattern matching is case-insensitive.
func TestCaseInsensitivity(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name   string
		prompt string
		isMail bool
	}{
		{"lowercase", "dans mes mails", true},
		{"uppercase", "DANS MES MAILS", true},
		{"mixed case", "Dans Mes Mails", true},
		{"lowercase EN", "in my inbox", true},
		{"uppercase EN", "IN MY INBOX", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)
			if tt.isMail && !intent.ShouldSearchMail() {
				t.Errorf("expected case-insensitive match for %q", tt.prompt)
			}
		})
	}
}

// TestPatternRegexAccessors tests the regex accessor functions.
func TestPatternRegexAccessors(t *testing.T) {
	mailRe := GetMailRegex()
	if mailRe == nil {
		t.Fatal("GetMailRegex() returned nil")
	}

	kbRe := GetKnowledgeRegex()
	if kbRe == nil {
		t.Fatal("GetKnowledgeRegex() returned nil")
	}

	temporalRe := GetTemporalRecentRegex()
	if temporalRe == nil {
		t.Fatal("GetTemporalRecentRegex() returned nil")
	}

	// Test that regexes match expected patterns
	if !mailRe.MatchString("dans mes mails") {
		t.Error("mail regex should match 'dans mes mails'")
	}

	if !kbRe.MatchString("dans mes notes") {
		t.Error("knowledge regex should match 'dans mes notes'")
	}

	if !temporalRe.MatchString("les 10 dernières recharges") {
		t.Error("temporal regex should match 'les 10 dernières recharges'")
	}
}

// TestDetector_TemporalIntent tests detection of temporal/recency intent.
func TestDetector_TemporalIntent(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name               string
		prompt             string
		wantTemporalMode   TemporalMode
		wantTemporalWeight float64
	}{
		{
			name:               "French: les 10 dernières",
			prompt:             "Donne-moi les 10 dernières recharges de voiture",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "French: dernières 5",
			prompt:             "Liste les dernières 5 factures",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "French: plus récentes",
			prompt:             "Quelles sont les 3 plus récentes commandes ?",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "French: récemment",
			prompt:             "Qu'est-ce qui s'est passé récemment ?",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "French: cette semaine",
			prompt:             "Les emails reçus cette semaine",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "French: ce mois",
			prompt:             "Résume les recharges de ce mois",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "English: last 10",
			prompt:             "Show me the last 10 invoices",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "English: most recent",
			prompt:             "What are the 5 most recent orders?",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "English: this week",
			prompt:             "Emails received this week",
			wantTemporalMode:   TemporalRecent,
			wantTemporalWeight: 0.7,
		},
		{
			name:               "No temporal intent",
			prompt:             "Qu'est-ce que Jean m'a dit sur le projet ?",
			wantTemporalMode:   TemporalNone,
			wantTemporalWeight: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := d.Detect(tt.prompt)

			if intent.TemporalMode != tt.wantTemporalMode {
				t.Errorf("TemporalMode = %v, want %v", intent.TemporalMode, tt.wantTemporalMode)
			}

			if !floatEquals(intent.TemporalWeight, tt.wantTemporalWeight, 0.01) {
				t.Errorf("TemporalWeight = %v, want %v", intent.TemporalWeight, tt.wantTemporalWeight)
			}
		})
	}
}

// TestIntentWeightSum tests that weights always sum to approximately 1.0.
func TestIntentWeightSum(t *testing.T) {
	d := NewDetector()

	prompts := []string{
		"dans mes mails",
		"dans mes notes",
		"dans mes mails et notes",
		"general question",
	}

	for _, prompt := range prompts {
		t.Run(prompt, func(t *testing.T) {
			intent := d.Detect(prompt)
			sum := intent.Weights[SourceMail] + intent.Weights[SourceKnowledge]
			if !floatEquals(sum, 1.0, 0.01) {
				t.Errorf("weights sum = %v, want 1.0", sum)
			}
		})
	}
}

// BenchmarkDetect benchmarks the Detect function.
func BenchmarkDetect(b *testing.B) {
	d := NewDetector()
	prompts := []string{
		"Qu'est-ce que Jean m'a dit dans mes mails ?",
		"Resume le projet dans mes notes",
		"Cherche dans mes mails et mes documents",
		"Quel est le capital de la France ?",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Detect(prompts[i%len(prompts)])
	}
}

// BenchmarkCleanQuery benchmarks the query cleaning.
func BenchmarkCleanQuery(b *testing.B) {
	d := NewDetector()
	prompt := "Qu'est-ce que Jean m'a dit dans mes mails sur le projet important ?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Detect(prompt)
	}
}

// TestDetector_TemporalRange tests detection of explicit date-range intent (month names).
func TestDetector_TemporalRange(t *testing.T) {
	d := NewDetector()

	tests := []struct {
		name             string
		prompt           string
		wantTemporalMode TemporalMode
	}{
		{
			name:             "french month mars",
			prompt:           "mes mails de mars",
			wantTemporalMode: TemporalRange,
		},
		{
			name:             "french month with year",
			prompt:           "emails de mars 2026",
			wantTemporalMode: TemporalRange,
		},
		{
			name:             "english month march",
			prompt:           "emails from march",
			wantTemporalMode: TemporalRange,
		},
		{
			name:             "french: janvier",
			prompt:           "mes factures de janvier",
			wantTemporalMode: TemporalRange,
		},
		{
			name:             "no temporal intent",
			prompt:           "qu'est-ce que jean m'a dit sur le projet",
			wantTemporalMode: TemporalNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.Detect(tt.prompt)
			if result.TemporalMode != tt.wantTemporalMode {
				t.Errorf("TemporalMode = %v, want %v", result.TemporalMode, tt.wantTemporalMode)
			}
			if tt.wantTemporalMode == TemporalRange {
				if result.DateFrom == nil {
					t.Error("DateFrom should not be nil for TemporalRange")
				}
				if result.DateTo == nil {
					t.Error("DateTo should not be nil for TemporalRange")
				}
				if result.TemporalWeight != 0 {
					t.Errorf("TemporalWeight = %v, want 0 for TemporalRange", result.TemporalWeight)
				}
			}
		})
	}
}

// TestExtractTemporalRange tests the extractTemporalRange helper directly.
func TestExtractTemporalRange(t *testing.T) {
	tests := []struct {
		prompt      string
		wantMatched bool
		wantMonth   time.Month
		wantYear    int // 0 = any recent past year (just check matched)
	}{
		{"mes mails de mars", true, time.March, 0},
		{"emails from march 2025", true, time.March, 2025},
		{"rien à voir", false, 0, 0},
		{"fevrier", true, time.February, 0},
	}
	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			matched, from, _ := extractTemporalRange(tt.prompt)
			if matched != tt.wantMatched {
				t.Errorf("matched=%v, want %v", matched, tt.wantMatched)
			}
			if matched && tt.wantMonth != 0 && from.Month() != tt.wantMonth {
				t.Errorf("month=%v, want %v", from.Month(), tt.wantMonth)
			}
			if matched && tt.wantYear != 0 && from.Year() != tt.wantYear {
				t.Errorf("year=%v, want %v", from.Year(), tt.wantYear)
			}
		})
	}
}
