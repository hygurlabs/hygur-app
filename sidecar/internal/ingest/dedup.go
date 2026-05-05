package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"math/bits"
	"regexp"
	"strings"
	"unicode"
)

// tokenRegex splits text into words, removing punctuation.
var tokenRegex = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// DedupResult contains the result of a duplicate check.
type DedupResult struct {
	// IsExactDuplicate indicates if the content is an exact match.
	IsExactDuplicate bool

	// IsNearDuplicate indicates if the content is similar to existing content.
	IsNearDuplicate bool

	// ExistingID is the ID of the existing content if a duplicate was found.
	ExistingID string

	// Similarity is the similarity score (1.0 - hamming_distance/64).
	// Only meaningful when IsNearDuplicate is true.
	Similarity float64
}

// DedupConfig configures the deduplication behavior.
type DedupConfig struct {
	// NearDuplicateThreshold is the maximum Hamming distance for near-duplicate detection.
	// Recommended value: 3-5. Lower values are more strict.
	NearDuplicateThreshold int
}

// DefaultDedupConfig returns the default deduplication configuration.
func DefaultDedupConfig() DedupConfig {
	return DedupConfig{
		NearDuplicateThreshold: 3,
	}
}

// Deduplicator detects exact and near-duplicate content.
type Deduplicator struct {
	config DedupConfig
}

// NewDeduplicator creates a new Deduplicator with the given configuration.
func NewDeduplicator(config DedupConfig) *Deduplicator {
	return &Deduplicator{
		config: config,
	}
}

// DefaultDeduplicator creates a new Deduplicator with default configuration.
func DefaultDeduplicator() *Deduplicator {
	return NewDeduplicator(DefaultDedupConfig())
}

// HashContent returns the SHA-256 hash of the normalized text.
// The text is normalized before hashing to ensure consistent results.
func HashContent(text string) string {
	normalized := NormalizeText(text)
	h := sha256.New()
	h.Write([]byte(normalized))
	return hex.EncodeToString(h.Sum(nil))
}

// SimHash calculates a 64-bit locality-sensitive hash for near-duplicate detection.
// Similar texts will produce similar hashes (low Hamming distance).
func SimHash(text string) uint64 {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return 0
	}

	// Create a 64-dimension vector for counting
	var v [64]int

	// For each token, compute its hash and adjust the vector
	for _, token := range tokens {
		h := fnv.New64a()
		h.Write([]byte(token))
		hash := h.Sum64()

		for i := 0; i < 64; i++ {
			if (hash>>i)&1 == 1 {
				v[i]++
			} else {
				v[i]--
			}
		}
	}

	// Convert the vector to a binary hash
	var simhash uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			simhash |= (1 << i)
		}
	}

	return simhash
}

// HammingDistance calculates the Hamming distance between two SimHash values.
// The result is the number of differing bits (0-64).
func HammingDistance(a, b uint64) int {
	xor := a ^ b
	return bits.OnesCount64(xor)
}

// IsNearDuplicate returns true if the Hamming distance between two SimHash values
// is less than or equal to the threshold.
// Recommended threshold: 3-5 for near-duplicate detection.
func IsNearDuplicate(a, b uint64, threshold int) bool {
	return HammingDistance(a, b) <= threshold
}

// CheckDuplicate checks if the given text is a duplicate of existing content.
// It first checks for exact duplicates using SHA-256 hashes, then checks for
// near-duplicates using SimHash.
//
// Parameters:
//   - text: the content to check
//   - existingHashes: map of content hash -> content ID for exact matching
//   - existingSimHashes: map of simhash -> content ID for near-duplicate detection
//
// Returns a DedupResult indicating the duplicate status.
func (d *Deduplicator) CheckDuplicate(
	text string,
	existingHashes map[string]string,
	existingSimHashes map[uint64]string,
) DedupResult {
	result := DedupResult{}

	// Check for exact duplicate first
	contentHash := HashContent(text)
	if existingID, found := existingHashes[contentHash]; found {
		result.IsExactDuplicate = true
		result.ExistingID = existingID
		result.Similarity = 1.0
		return result
	}

	// Check for near-duplicate
	simhash := SimHash(text)
	for existingSimHash, existingID := range existingSimHashes {
		distance := HammingDistance(simhash, existingSimHash)
		if distance <= d.config.NearDuplicateThreshold {
			similarity := 1.0 - float64(distance)/64.0
			// Keep the best match (highest similarity)
			if !result.IsNearDuplicate || similarity > result.Similarity {
				result.IsNearDuplicate = true
				result.ExistingID = existingID
				result.Similarity = similarity
			}
		}
	}

	return result
}

// GetContentHash returns the SHA-256 hash for the given text.
// This is useful for storing the hash alongside content.
func (d *Deduplicator) GetContentHash(text string) string {
	return HashContent(text)
}

// GetSimHash returns the SimHash for the given text.
// This is useful for storing the simhash alongside content.
func (d *Deduplicator) GetSimHash(text string) uint64 {
	return SimHash(text)
}

// tokenize splits text into lowercase tokens for SimHash computation.
// It filters out tokens shorter than 2 characters.
func tokenize(text string) []string {
	// Normalize the text first
	normalized := strings.ToLower(text)

	// Split by non-letter/non-number characters
	words := tokenRegex.Split(normalized, -1)

	// Filter tokens
	tokens := make([]string, 0, len(words))
	for _, word := range words {
		// Skip empty strings and tokens shorter than 2 characters
		if len(word) < 2 {
			continue
		}

		// Skip tokens that are only numbers or only whitespace
		hasLetter := false
		for _, r := range word {
			if unicode.IsLetter(r) {
				hasLetter = true
				break
			}
		}
		if !hasLetter {
			continue
		}

		tokens = append(tokens, word)
	}

	return tokens
}
