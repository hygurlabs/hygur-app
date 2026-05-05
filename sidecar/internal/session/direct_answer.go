package session

import (
	"fmt"
	"strings"
)

// DirectAnswer is what the chat handler streams to the user when an entity-type
// follow-up question can be answered from the SessionContext without re-querying.
type DirectAnswer struct {
	// Text is the formatted answer to stream as SSE deltas.
	Text string
	// EntityType is the kind of entity that satisfied the question
	// ("iban", "amount", …). Useful for logging.
	EntityType string
	// SourceIDs are the content_ids that originally produced the entities,
	// re-used as the rag_context.sources of this turn for traceability.
	SourceIDs []string
}

// CanAnswerFromContext returns a DirectAnswer (and true) when the latest user
// question is a tight entity-type follow-up that the SessionContext can answer
// without re-querying. Returns ("", false) otherwise.
//
// Heuristics applied (cheap, no LLM call):
//  1. Detect the entity type from the query via simple keyword matching.
//  2. Require that the SessionContext already holds at least one entity of
//     that type.
//  3. Require that the query is *short* — long queries are likely introducing
//     new context that warrants a fresh retrieval.
//  4. Require that the query does NOT introduce a topic-shifting noun (a
//     proper-noun-ish token absent from any prior resolved query). This is
//     the cheapest topic-shift guard: "show me the IBAN" is fine, but
//     "show me the IBAN of Stripe" should fall through to retrieval.
//
// The risk (knowingly accepted per spec): if the cached entity is stale or
// wrong, we propagate the error. The answer text always opens with "Based on
// what you asked earlier" so the user sees the cache is in play.
func CanAnswerFromContext(query string, ctx *SessionContext) (DirectAnswer, bool) {
	if ctx == nil {
		return DirectAnswer{}, false
	}

	entityType := DetectQueryEntityType(query)
	if entityType == "" {
		return DirectAnswer{}, false
	}

	entities := ctx.GetEntities(entityType)
	if len(entities) == 0 {
		return DirectAnswer{}, false
	}

	// Short queries only — cap at 18 words. A typical entity-type follow-up
	// like "Sur quel compte et quelle communication je dois utiliser pour le
	// virement ?" hovers around 13-15 words, so the cap needs a little air.
	if wordCount(query) > 18 {
		return DirectAnswer{}, false
	}

	if introducesTopicShiftNoun(query, ctx) {
		return DirectAnswer{}, false
	}

	return DirectAnswer{
		Text:       formatDirectAnswer(entityType, entities),
		EntityType: entityType,
		SourceIDs:  uniqueSources(entities),
	}, true
}

// DetectQueryEntityType is the public wrapper around the heuristic used by
// the retrieval boost layer (see internal/retrieval/temporal.go). Duplicated
// here to keep session decoupled from retrieval — the keyword list is small
// and stable enough that drift between the two copies is unlikely to bite.
func DetectQueryEntityType(query string) string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "iban"),
		strings.Contains(q, "account number"),
		strings.Contains(q, "numéro de compte"),
		strings.Contains(q, "compte bancaire"):
		return EntityIBAN
	case strings.Contains(q, "amount"),
		strings.Contains(q, "montant"),
		strings.Contains(q, "balance"),
		strings.Contains(q, "solde"),
		strings.Contains(q, "how much"),
		strings.Contains(q, "combien"):
		return EntityAmount
	case strings.Contains(q, "communication"),
		strings.Contains(q, "reference"),
		strings.Contains(q, "référence"),
		strings.Contains(q, "structured comm"):
		return EntityStructuredCom
	}
	return ""
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}

// introducesTopicShiftNoun returns true when the query contains a *proper-noun*
// that doesn't appear anywhere in prior turns. Heuristic: a token whose first
// letter is uppercase in the original casing AND that isn't in a sentence-
// initial position. This catches "Stripe", "Securex", "Bouygues" while
// allowing common nouns like "compte" or "communication" to pass through.
//
// Sentence-initial capitalization (the first word, or the first word after
// `.!?`) is ignored to avoid false positives on sentence starts.
//
// Returns true with no prior queries — there's nothing to anchor the topic to,
// so we conservatively block the direct-answer path.
func introducesTopicShiftNoun(query string, ctx *SessionContext) bool {
	if len(ctx.ResolvedQueries) == 0 {
		return true
	}
	prior := strings.Builder{}
	for _, rq := range ctx.ResolvedQueries {
		prior.WriteString(strings.ToLower(rq.Question))
		prior.WriteByte(' ')
		prior.WriteString(strings.ToLower(rq.Answer))
		prior.WriteByte(' ')
	}
	priorBlob := prior.String()

	tokens := strings.Fields(query)
	for i, raw := range tokens {
		token := strings.Trim(raw, ".,;:!?'\"()[]{}«» ")
		if len(token) < 4 {
			continue
		}
		// Sentence-initial position is ignored: either the very first token
		// or one whose previous token ends with sentence-terminal punctuation.
		if i == 0 {
			continue
		}
		prev := tokens[i-1]
		if strings.HasSuffix(prev, ".") || strings.HasSuffix(prev, "?") || strings.HasSuffix(prev, "!") {
			continue
		}
		// Must start with an uppercase ASCII letter to count as a proper noun.
		first := token[0]
		if first < 'A' || first > 'Z' {
			continue
		}
		if !strings.Contains(priorBlob, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

// formatDirectAnswer renders a short message acknowledging the cache and
// listing the entity values. Keep it brief — the user is asking a follow-up,
// not for a verbose explanation.
func formatDirectAnswer(entityType string, entities []Entity) string {
	values := make([]string, 0, len(entities))
	for _, e := range entities {
		values = append(values, e.Value)
	}
	label := entityTypeLabel(entityType)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Based on what you asked earlier, the %s ", label))
	if len(values) == 1 {
		sb.WriteString("is: ")
		sb.WriteString(values[0])
	} else {
		sb.WriteString("values are:\n")
		for _, v := range values {
			sb.WriteString("- ")
			sb.WriteString(v)
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(".\n\n_If this is stale or you want me to verify against the latest emails, ask me to look it up again._")
	return sb.String()
}

func entityTypeLabel(t string) string {
	switch t {
	case EntityIBAN:
		return "IBAN / account number"
	case EntityAmount:
		return "amount"
	case EntityStructuredCom:
		return "structured communication"
	case EntityVATNumber:
		return "VAT number"
	case EntityDueDate:
		return "due date"
	default:
		return t
	}
}

func uniqueSources(entities []Entity) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		if e.Source == "" || seen[e.Source] {
			continue
		}
		seen[e.Source] = true
		out = append(out, e.Source)
	}
	return out
}
