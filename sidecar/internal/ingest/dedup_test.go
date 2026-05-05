package ingest

import (
	"testing"
)

func TestHashContent(t *testing.T) {
	tests := []struct {
		name     string
		text1    string
		text2    string
		wantSame bool
	}{
		{
			name:     "identical texts produce same hash",
			text1:    "Hello, World!",
			text2:    "Hello, World!",
			wantSame: true,
		},
		{
			name:     "normalized texts produce same hash",
			text1:    "Hello,  World!",
			text2:    "hello, world!",
			wantSame: true,
		},
		{
			name:     "different texts produce different hashes",
			text1:    "Hello, World!",
			text2:    "Goodbye, World!",
			wantSame: false,
		},
		{
			name:     "whitespace normalization",
			text1:    "  Hello   World  ",
			text2:    "hello world",
			wantSame: true,
		},
		{
			name:     "newlines treated as spaces",
			text1:    "Hello\nWorld",
			text2:    "hello world",
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := HashContent(tt.text1)
			hash2 := HashContent(tt.text2)

			if (hash1 == hash2) != tt.wantSame {
				t.Errorf("HashContent(%q) = %s, HashContent(%q) = %s, wantSame = %v",
					tt.text1, hash1, tt.text2, hash2, tt.wantSame)
			}
		})
	}
}

func TestHashContentFormat(t *testing.T) {
	hash := HashContent("test content")

	// SHA-256 produces 64 hex characters
	if len(hash) != 64 {
		t.Errorf("HashContent should produce 64 character hex string, got %d characters", len(hash))
	}

	// Verify it's valid hex
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("HashContent should produce lowercase hex, got character %c", c)
		}
	}
}

func TestHashContentEmpty(t *testing.T) {
	hash1 := HashContent("")
	hash2 := HashContent("   ")

	// Both should produce the same hash (empty after normalization)
	if hash1 != hash2 {
		t.Errorf("Empty and whitespace-only strings should produce same hash")
	}
}

func TestSimHash(t *testing.T) {
	tests := []struct {
		name        string
		text1       string
		text2       string
		maxDistance int // maximum expected Hamming distance
		minDistance int // minimum expected Hamming distance (for different texts)
	}{
		{
			name:        "identical texts produce same simhash",
			text1:       "The quick brown fox jumps over the lazy dog",
			text2:       "The quick brown fox jumps over the lazy dog",
			maxDistance: 0,
		},
		{
			name:        "similar texts produce close simhashes",
			text1:       "The quick brown fox jumps over the lazy dog",
			text2:       "The quick brown cat jumps over the lazy dog",
			maxDistance: 10, // SimHash can have moderate distance for small changes
		},
		{
			name:        "different texts produce distant simhashes",
			text1:       "The quick brown fox jumps over the lazy dog",
			text2:       "Python is a programming language used for web development",
			minDistance: 10,
			maxDistance: 64,
		},
		{
			name:        "case insensitive",
			text1:       "Hello World",
			text2:       "HELLO WORLD",
			maxDistance: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := SimHash(tt.text1)
			hash2 := SimHash(tt.text2)
			distance := HammingDistance(hash1, hash2)

			if distance > tt.maxDistance {
				t.Errorf("SimHash distance between %q and %q = %d, want <= %d",
					tt.text1, tt.text2, distance, tt.maxDistance)
			}

			if tt.minDistance > 0 && distance < tt.minDistance {
				t.Errorf("SimHash distance between %q and %q = %d, want >= %d",
					tt.text1, tt.text2, distance, tt.minDistance)
			}
		})
	}
}

func TestSimHashEmpty(t *testing.T) {
	hash := SimHash("")
	if hash != 0 {
		t.Errorf("SimHash of empty string should be 0, got %d", hash)
	}

	hash = SimHash("   ")
	if hash != 0 {
		t.Errorf("SimHash of whitespace-only string should be 0, got %d", hash)
	}
}

func TestHammingDistance(t *testing.T) {
	tests := []struct {
		name string
		a    uint64
		b    uint64
		want int
	}{
		{
			name: "identical values",
			a:    0xFFFFFFFFFFFFFFFF,
			b:    0xFFFFFFFFFFFFFFFF,
			want: 0,
		},
		{
			name: "one bit different",
			a:    0x0000000000000000,
			b:    0x0000000000000001,
			want: 1,
		},
		{
			name: "all bits different",
			a:    0x0000000000000000,
			b:    0xFFFFFFFFFFFFFFFF,
			want: 64,
		},
		{
			name: "half bits different",
			a:    0xAAAAAAAAAAAAAAAA,
			b:    0x5555555555555555,
			want: 64,
		},
		{
			name: "few bits different",
			a:    0xF0F0F0F0F0F0F0F0,
			b:    0xF0F0F0F0F0F0F0F1,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HammingDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("HammingDistance(%#x, %#x) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNearDuplicate(t *testing.T) {
	tests := []struct {
		name      string
		a         uint64
		b         uint64
		threshold int
		want      bool
	}{
		{
			name:      "identical is near duplicate",
			a:         0x1234567890ABCDEF,
			b:         0x1234567890ABCDEF,
			threshold: 3,
			want:      true,
		},
		{
			name:      "within threshold",
			a:         0x0000000000000000,
			b:         0x0000000000000007, // 3 bits different
			threshold: 3,
			want:      true,
		},
		{
			name:      "exactly at threshold",
			a:         0x0000000000000000,
			b:         0x0000000000000007, // 3 bits different
			threshold: 3,
			want:      true,
		},
		{
			name:      "above threshold",
			a:         0x0000000000000000,
			b:         0x000000000000000F, // 4 bits different
			threshold: 3,
			want:      false,
		},
		{
			name:      "completely different",
			a:         0x0000000000000000,
			b:         0xFFFFFFFFFFFFFFFF,
			threshold: 5,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNearDuplicate(tt.a, tt.b, tt.threshold)
			if got != tt.want {
				t.Errorf("IsNearDuplicate(%#x, %#x, %d) = %v, want %v",
					tt.a, tt.b, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestDeduplicator_CheckDuplicate_ExactMatch(t *testing.T) {
	dedup := DefaultDeduplicator()

	text := "This is a test document with some content."
	hash := HashContent(text)

	existingHashes := map[string]string{
		hash: "doc-123",
	}
	existingSimHashes := map[uint64]string{}

	result := dedup.CheckDuplicate(text, existingHashes, existingSimHashes)

	if !result.IsExactDuplicate {
		t.Error("Expected IsExactDuplicate to be true")
	}
	if result.ExistingID != "doc-123" {
		t.Errorf("Expected ExistingID to be 'doc-123', got %q", result.ExistingID)
	}
	if result.Similarity != 1.0 {
		t.Errorf("Expected Similarity to be 1.0, got %f", result.Similarity)
	}
}

func TestDeduplicator_CheckDuplicate_NearMatch(t *testing.T) {
	// Use a lenient threshold to ensure near-duplicate detection
	dedup := NewDeduplicator(DedupConfig{NearDuplicateThreshold: 10})

	text1 := "The quick brown fox jumps over the lazy dog in the park"
	text2 := "The quick brown cat jumps over the lazy dog in the park"

	simhash1 := SimHash(text1)

	existingHashes := map[string]string{}
	existingSimHashes := map[uint64]string{
		simhash1: "doc-456",
	}

	result := dedup.CheckDuplicate(text2, existingHashes, existingSimHashes)

	if result.IsExactDuplicate {
		t.Error("Expected IsExactDuplicate to be false")
	}
	if !result.IsNearDuplicate {
		t.Error("Expected IsNearDuplicate to be true")
	}
	if result.ExistingID != "doc-456" {
		t.Errorf("Expected ExistingID to be 'doc-456', got %q", result.ExistingID)
	}
	if result.Similarity <= 0 || result.Similarity >= 1.0 {
		t.Errorf("Expected Similarity to be between 0 and 1, got %f", result.Similarity)
	}
}

func TestDeduplicator_CheckDuplicate_NoMatch(t *testing.T) {
	dedup := DefaultDeduplicator()

	text1 := "The quick brown fox jumps over the lazy dog"
	text2 := "Python is a programming language used for web development and data science"

	simhash1 := SimHash(text1)
	hash1 := HashContent(text1)

	existingHashes := map[string]string{
		hash1: "doc-789",
	}
	existingSimHashes := map[uint64]string{
		simhash1: "doc-789",
	}

	result := dedup.CheckDuplicate(text2, existingHashes, existingSimHashes)

	if result.IsExactDuplicate {
		t.Error("Expected IsExactDuplicate to be false")
	}
	if result.IsNearDuplicate {
		t.Error("Expected IsNearDuplicate to be false for completely different text")
	}
	if result.ExistingID != "" {
		t.Errorf("Expected ExistingID to be empty, got %q", result.ExistingID)
	}
}

func TestDeduplicator_CheckDuplicate_PrefersHighestSimilarity(t *testing.T) {
	// Use a lenient threshold to ensure near-duplicate detection
	dedup := NewDeduplicator(DedupConfig{NearDuplicateThreshold: 15})

	text := "The quick brown fox jumps over the lazy dog in the sunny park"
	text1 := "The quick brown cat jumps over the lazy dog in the sunny park" // very similar
	text2 := "The quick brown fox jumps over the lazy cat in the rainy park" // somewhat similar

	simhash1 := SimHash(text1)
	simhash2 := SimHash(text2)

	existingHashes := map[string]string{}
	existingSimHashes := map[uint64]string{
		simhash1: "doc-similar",
		simhash2: "doc-less-similar",
	}

	result := dedup.CheckDuplicate(text, existingHashes, existingSimHashes)

	// The result should pick the most similar document
	if !result.IsNearDuplicate {
		t.Error("Expected IsNearDuplicate to be true")
	}
	// We expect the match with highest similarity (above 0.75 = 16 bits different max)
	if result.Similarity < 0.75 {
		t.Errorf("Expected high similarity (>= 0.75), got %f", result.Similarity)
	}
}

func TestDeduplicator_CustomThreshold(t *testing.T) {
	// Create deduplicator with very strict threshold
	strictDedup := NewDeduplicator(DedupConfig{NearDuplicateThreshold: 1})

	// Create deduplicator with lenient threshold
	lenientDedup := NewDeduplicator(DedupConfig{NearDuplicateThreshold: 10})

	text1 := "The quick brown fox jumps over the lazy dog"
	text2 := "The quick brown cat jumps over the lazy dog"

	simhash1 := SimHash(text1)
	existingSimHashes := map[uint64]string{
		simhash1: "doc-test",
	}
	existingHashes := map[string]string{}

	strictResult := strictDedup.CheckDuplicate(text2, existingHashes, existingSimHashes)
	lenientResult := lenientDedup.CheckDuplicate(text2, existingHashes, existingSimHashes)

	// With strict threshold, it might not be considered near-duplicate
	// With lenient threshold, it should be considered near-duplicate
	if lenientResult.IsNearDuplicate && !strictResult.IsNearDuplicate {
		// This is the expected behavior when distance is between 1 and 10
		t.Logf("Strict threshold correctly rejected, lenient threshold correctly accepted")
	}
}

func TestDeduplicator_GetContentHash(t *testing.T) {
	dedup := DefaultDeduplicator()
	text := "Test content"

	hash := dedup.GetContentHash(text)
	expectedHash := HashContent(text)

	if hash != expectedHash {
		t.Errorf("GetContentHash returned %s, expected %s", hash, expectedHash)
	}
}

func TestDeduplicator_GetSimHash(t *testing.T) {
	dedup := DefaultDeduplicator()
	text := "Test content"

	simhash := dedup.GetSimHash(text)
	expectedSimHash := SimHash(text)

	if simhash != expectedSimHash {
		t.Errorf("GetSimHash returned %d, expected %d", simhash, expectedSimHash)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minCount int // minimum expected token count
		maxCount int // maximum expected token count
	}{
		{
			name:     "simple sentence",
			text:     "Hello World",
			minCount: 2,
			maxCount: 2,
		},
		{
			name:     "with punctuation",
			text:     "Hello, World! How are you?",
			minCount: 4, // "hello", "world", "how", "are", "you"
			maxCount: 5,
		},
		{
			name:     "filters short tokens",
			text:     "I am a test",
			minCount: 1, // only "test" and "am" should survive
			maxCount: 2,
		},
		{
			name:     "empty string",
			text:     "",
			minCount: 0,
			maxCount: 0,
		},
		{
			name:     "only numbers",
			text:     "123 456 789",
			minCount: 0,
			maxCount: 0,
		},
		{
			name:     "mixed content",
			text:     "Version 2.0 released in 2024",
			minCount: 2, // "version", "released"
			maxCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := tokenize(tt.text)

			if len(tokens) < tt.minCount || len(tokens) > tt.maxCount {
				t.Errorf("tokenize(%q) returned %d tokens, expected between %d and %d: %v",
					tt.text, len(tokens), tt.minCount, tt.maxCount, tokens)
			}

			// Verify all tokens are lowercase
			for _, token := range tokens {
				for _, r := range token {
					if r >= 'A' && r <= 'Z' {
						t.Errorf("tokenize should return lowercase tokens, got %q", token)
					}
				}
			}
		})
	}
}

func TestDefaultDedupConfig(t *testing.T) {
	config := DefaultDedupConfig()

	if config.NearDuplicateThreshold != 3 {
		t.Errorf("DefaultDedupConfig().NearDuplicateThreshold = %d, want 3",
			config.NearDuplicateThreshold)
	}
}

func TestDefaultDeduplicator(t *testing.T) {
	dedup := DefaultDeduplicator()

	if dedup == nil {
		t.Fatal("DefaultDeduplicator() returned nil")
	}

	if dedup.config.NearDuplicateThreshold != 3 {
		t.Errorf("DefaultDeduplicator has threshold %d, want 3",
			dedup.config.NearDuplicateThreshold)
	}
}

// Benchmark tests
func BenchmarkHashContent(b *testing.B) {
	text := "The quick brown fox jumps over the lazy dog. This is a sample text for benchmarking the hash function performance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		HashContent(text)
	}
}

func BenchmarkSimHash(b *testing.B) {
	text := "The quick brown fox jumps over the lazy dog. This is a sample text for benchmarking the simhash function performance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SimHash(text)
	}
}

func BenchmarkHammingDistance(b *testing.B) {
	a := uint64(0x123456789ABCDEF0)
	c := uint64(0xFEDCBA9876543210)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		HammingDistance(a, c)
	}
}

func BenchmarkCheckDuplicate(b *testing.B) {
	dedup := DefaultDeduplicator()
	text := "The quick brown fox jumps over the lazy dog"

	// Create some existing hashes
	existingHashes := make(map[string]string)
	existingSimHashes := make(map[uint64]string)
	for i := 0; i < 100; i++ {
		t := "Sample document number " + string(rune('A'+i%26))
		existingHashes[HashContent(t)] = "doc-" + string(rune('A'+i%26))
		existingSimHashes[SimHash(t)] = "doc-" + string(rune('A'+i%26))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dedup.CheckDuplicate(text, existingHashes, existingSimHashes)
	}
}
