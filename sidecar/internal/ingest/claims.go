package ingest

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/keyed"
	"github.com/hygur/sidecar/internal/labelfact"
	"github.com/hygur/sidecar/internal/recognize"
	"github.com/hygur/sidecar/internal/store"
)

// W6 stage 2 — semantic claim extraction wired into ingestion. Claims feed the
// contradiction reconciliation (and project tracking). To bound token cost and
// noise we skip junk: automated senders (no-reply &co.) and the
// "Notifications & Accounts" category never get claims extracted. Quality matters
// here (claims drive conflict detection), so extraction uses the MAIN generation
// client (i.llmClient) — the same model the preview was validated on — not the
// small indexing model used for classification.

const claimsSkipCategory = "Notifications & Accounts"

// claimsEligible reports whether an item should have claims extracted: mail or
// notes only, never an automated/no-reply sender, never the Notifications &
// Accounts category. Cheap (no LLM) — re-evaluated each pass.
func claimsEligible(item *store.KnowledgeItem, cats []string) bool {
	if item == nil {
		return false
	}
	if item.SourceType != store.SourceTypeNote && !store.IsMailSourceType(item.SourceType) {
		return false
	}
	for _, c := range cats {
		if c == claimsSkipCategory {
			return false
		}
	}
	return !isAutomatedSender(senderAddr(item.Metadata))
}

// senderAddr returns the item's sender (mail_from, falling back to from).
func senderAddr(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, k := range []string{"mail_from", "from"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// automatedLocalParts are no-reply-style local-part markers (separators stripped),
// matched as substrings — more reliable than the noisy auto-categories for
// excluding machine senders (LinkedIn/GitHub/Google/Amazon noreply, FR variants).
var automatedLocalParts = []string{"noreply", "donotreply", "nepasrepondre", "pasdereponse", "mailerdaemon"}

// isAutomatedSender reports whether a From header is a no-reply / automated
// address. It reads the address local-part (inside <...> if present) and matches
// separator-insensitively, so no-reply / no_reply / noreply / notifications-noreply
// all hit.
func isAutomatedSender(from string) bool {
	addr := from
	if i := strings.LastIndex(addr, "<"); i >= 0 {
		if j := strings.Index(addr[i:], ">"); j > 0 {
			addr = addr[i+1 : i+j]
		}
	}
	local := addr
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	stripped := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(local))
	for _, p := range automatedLocalParts {
		if strings.Contains(stripped, p) {
			return true
		}
	}
	return false
}

// extractClaimsForItem returns the item's claims (cached or freshly extracted).
// fresh=true means the caller should persist them. No DB writes (safe to run
// concurrently). Skips ineligible items and already-fresh caches without an LLM
// call. An empty result on an eligible item is still fresh=true, so "no claims"
// is cached and not recomputed.
func (i *Ingestor) extractClaimsForItem(ctx context.Context, item *store.KnowledgeItem, cats []string) (claims []contradict.Claim, fresh bool) {
	if item == nil {
		return nil, false
	}
	if cachedFresh(item.Metadata, "extracted_claims", extractedClaimsVersion) {
		return nil, false
	}
	if !claimsEligible(item, cats) {
		return nil, false
	}
	// Extract claims from the display text (raw when available): line breaks + case
	// help the LLM read the content correctly; falls back to normalized_text for
	// pre-raw_text items. The entity keys it derives are re-normalized downstream,
	// so the associative/identifier index stays stable.
	content := item.DisplayText()
	if i.llmClient == nil || strings.TrimSpace(content) == "" {
		return nil, false
	}
	got, err := contradict.ExtractClaims(ctx, i.llmClient, content)
	if err != nil {
		log.Printf("[ingest] claim extraction failed for %s: %v", item.ContentID, err)
		return nil, false
	}
	// Stamp the claim with the message's real date (canonical_date / mail_date /
	// note date), NOT the ingestion time — created_at must never stand in for a
	// real sent date in temporal reasoning (see store.GetCanonicalDate). Falls back
	// to created_at only when the item carries no content date (e.g. an undated
	// note). Detection re-derives this too, so existing caches are corrected there.
	at := item.CreatedAt.UTC().Format(time.RFC3339)
	if d := store.GetCanonicalDate(item); !d.IsZero() {
		at = d.UTC().Format(time.RFC3339)
	}
	for j := range got {
		got[j].SourceID = item.ContentID
		got[j].AssertedAt = at
	}
	return got, true
}

// applyItemClaims caches claims (+ version) in the item metadata. DB write — in a
// concurrent backfill the caller must serialize these (SQLite is single-writer).
// preserveTimestamp writes only the metadata (no updated_at bump) — a full-corpus
// claim backfill must not mark every item "recently modified" (it changes derived
// metadata, not content), which would flood updated_at-based recency queries.
func (i *Ingestor) applyItemClaims(ctx context.Context, item *store.KnowledgeItem, claims []contradict.Claim, fresh, preserveTimestamp bool) {
	if !fresh || item == nil {
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata["extracted_claims"] = claims
	item.Metadata["extracted_claims_version"] = extractedClaimsVersion
	var uerr error
	if preserveTimestamp {
		uerr = i.store.UpdateKnowledgeItemMetadata(ctx, item.ContentID, item.Metadata)
	} else {
		uerr = i.store.UpdateKnowledgeItem(ctx, item)
	}
	if uerr != nil {
		log.Printf("[ingest] claims metadata update failed for %s: %v", item.ContentID, uerr)
	}
	// Keep the associative entity index in step with the freshly-cached claims.
	mentions := entityMentionsFromClaims(claims)
	// Unified entity vocabulary: fold the Tier-2 NER entities (persons/orgs/projects/
	// topics) in alongside the claim-derived ones, so people are first-class in the
	// index and the graph — not just claim subjects.
	mentions = append(mentions, nerEntityMentions(item)...)
	// Checksum-typed identifiers (national number, VAT, IBAN…) as first-class nodes, so a
	// person links to their identifier through the Hebbian graph — deterministic, no LLM.
	mentions = append(mentions, typedIdentifierMentions(item)...)
	// Surprise/novelty (DREAM Phase C): compute BEFORE writing the new mentions, so
	// the item's own entities still read as "new". Best-effort — never blocks ingest.
	i.stampSurprise(ctx, item.ContentID, mentions)
	// Hebbian co-occurrence (DREAM Phase D): wire the item's entities together.
	// Best-effort — never blocks ingestion.
	i.stampCoOccurrence(ctx, item, mentions)
	if rerr := i.store.ReplaceEntityMentions(ctx, item.ContentID, mentions); rerr != nil {
		log.Printf("[ingest] entity-index sync failed for %s: %v", item.ContentID, rerr)
	}
	// Proximity links (person ↔ typed identifier) — the per-doc signal the lookup uses to
	// break the family-member tie. Best-effort.
	if lerr := i.store.ReplaceIdentifierLinks(ctx, item.ContentID, identifierProximityLinks(item)); lerr != nil {
		log.Printf("[ingest] identifier-link sync failed for %s: %v", item.ContentID, lerr)
	}
	// Figure nodes (labelled monetary figures ← FIGURES_TRUTH_PLAN F1): extract + attribute to the
	// nearest entity, so "the owner's VAT to pay" resolves by a deterministic traversal. Best-effort.
	if ferr := i.store.ReplaceFigureNodes(ctx, item.ContentID, figureNodes(item)); ferr != nil {
		log.Printf("[ingest] figure-node sync failed for %s: %v", item.ContentID, ferr)
	}
	// Keyed-entity attribute nodes (GENERALIZATION_PLAN — the universal entity-anchor): anchor each
	// claim to a KEY it names (a vehicle by its plate, generically any keyed entity), so "the model
	// of my vehicle GT-139-RR" resolves to the plate-anchored value. Plus the deterministic
	// vehicle-INSURANCE anchor (assureur/courtier/PJ from a certificate — no claims needed, the certs
	// are no-reply). Best-effort.
	if aerr := i.store.ReplaceAttrNodes(ctx, item.ContentID, allAttrNodes(item, claims)); aerr != nil {
		log.Printf("[ingest] attr-node sync failed for %s: %v", item.ContentID, aerr)
	}
}

// allAttrNodes assembles a document's keyed attribute nodes: the claim-anchored ones (a vehicle's
// modèle etc.) PLUS the deterministic vehicle-INSURANCE anchor (assureur/courtier/PJ read straight
// from an insurance certificate/contract, which carries no semantic claims — its sender is a
// no-reply). Both anchor to the same plate key, so a vehicle's model and its insurer resolve together.
func allAttrNodes(item *store.KnowledgeItem, claims []contradict.Claim) []store.AttrNode {
	nodes := keyed.AttrNodesFromClaims(claims)
	if item != nil {
		nodes = append(nodes, keyed.InsuranceNodes(item.Title, item.DisplayText(), metaStrings(item.Metadata, "extracted_orgs"))...)
	}
	return nodes
}

// entityMentionsFromClaims derives the distinct (entity, attribute) index rows
// from an item's claims, normalizing with the same key the contradiction layer
// uses so the retrieval read side matches. Claims with an empty entity are
// skipped (nothing to look up on).
func entityMentionsFromClaims(claims []contradict.Claim) []store.EntityMention {
	out := make([]store.EntityMention, 0, len(claims))
	for _, c := range claims {
		norm := contradict.NormKey(c.Entity)
		if norm == "" {
			continue
		}
		out = append(out, store.EntityMention{
			EntityNorm: norm,
			EntityRaw:  c.Entity,
			Attribute:  contradict.NormKey(c.Attribute),
			AssertedAt: c.AssertedAt,
		})
	}
	return out
}

// metaStrings reads a metadata field as a []string (handles []string and []any from
// a JSON round-trip); empty/missing → nil.
func metaStrings(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// nerEntityMentions folds an item's Tier-2 NER lists (persons/orgs/projects/topics,
// set in metadata by extract/tier2.go) into entity_mentions rows, tagged by an ner_*
// attribute and dated by the item's canonical content date (the real sent/note date,
// falling back to created_at only when undated — mirrors the claim path, so the
// timeline orders by when entities were actually mentioned, not ingestion time). This
// makes named people/orgs first-class in the entity index — so the subject detector
// AND the Hebbian graph see them, not only claim subjects. Same NormKey as the claim
// path, so the read side matches.
func nerEntityMentions(item *store.KnowledgeItem) []store.EntityMention {
	if item == nil || item.Metadata == nil {
		return nil
	}
	at := ""
	if !item.CreatedAt.IsZero() {
		at = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	if d := store.GetCanonicalDate(item); !d.IsZero() {
		at = d.UTC().Format(time.RFC3339)
	}
	var out []store.EntityMention
	add := func(key, attr string) {
		for _, raw := range metaStrings(item.Metadata, key) {
			norm := contradict.NormKey(raw)
			if norm == "" {
				continue
			}
			out = append(out, store.EntityMention{
				EntityNorm: norm, EntityRaw: raw, Attribute: attr, AssertedAt: at,
			})
		}
	}
	add("extracted_persons", "ner_person")
	add("extracted_orgs", "ner_org")
	add("extracted_projects", "ner_project")
	add("extracted_topics", "ner_topic")
	return out
}

// transactionalMailCats are the automated / transactional mailcat buckets whose documents
// carry machine-generated reference numbers (payment refs, order ids) that can pass a checksum
// by chance but are NEVER the household's stable identity numbers. The document-trust prior
// (IDENTIFIER_TRUTH_PLAN §3, T1) skips typed-identifier extraction from them: payment/receipt
// (Invoicing), notifications (Notifications & Accounts), newsletters (Marketing & Sales,
// Subscriptions). A real identity number lives in an administrative/legal/contract/banking
// document, which is never gated here — so this removes the payment-number-as-NISS at the root
// without dropping any genuine identifier.
var transactionalMailCats = map[string]bool{
	"Invoicing":                true,
	"Notifications & Accounts": true,
	"Marketing & Sales":        true,
	"Subscriptions":            true,
}

// typedIdentifiersSuppressed reports whether an item's mail category is transactional/automated,
// in which case checksum-typed identifiers are NOT extracted from it. Matches on ANY of the
// item's (≤2) categories, so a "Paiement réussi" mail tagged [Invoicing, Banking & Finance] is
// gated. Non-mail items (PDF, note) carry no mailcat → never suppressed.
func typedIdentifiersSuppressed(item *store.KnowledgeItem) bool {
	if item == nil {
		return false
	}
	for _, c := range categoriesFromMetadata(item.Metadata) {
		if transactionalMailCats[c] {
			return true
		}
	}
	return false
}

// typedIdentifierMentions extracts typed identifiers from the item's text and folds them into
// entity_mentions as first-class nodes: node key = canonical value, attribute = "id_<type>". So
// the existing Hebbian graph + NPMI link a person to their identifier by co-occurrence. Two
// sources, deterministic (no LLM): (1) recognize.Recognize — checksum family (national number,
// VAT, IBAN), skipped for transactional/automated docs (document-trust prior); (2) labelfact —
// the GENERIC label→value family (id_duns, id_siret…), which is NOT suppressed: the written label
// is itself the trust signal, so a labelled fact is reliable even in an automated mail, and the
// label keeps it from ever being confused with a checksum type (id_duns ≠ id_national_number).
func typedIdentifierMentions(item *store.KnowledgeItem) []store.EntityMention {
	if item == nil {
		return nil
	}
	at := ""
	if !item.CreatedAt.IsZero() {
		at = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	if d := store.GetCanonicalDate(item); !d.IsZero() {
		at = d.UTC().Format(time.RFC3339)
	}
	text := item.Title + " " + item.NormalizedText
	var typed []recognize.Typed
	if !typedIdentifiersSuppressed(item) {
		typed = recognize.Recognize(text)
	}
	typed = append(typed, labelfact.Extract(text)...)
	var out []store.EntityMention
	for _, t := range typed {
		out = append(out, store.EntityMention{
			EntityNorm: t.Value, EntityRaw: t.Raw, Attribute: "id_" + t.Type, AssertedAt: at,
		})
	}
	return out
}

// BackfillClaims extracts + caches claims across all eligible mail + notes,
// reusing already-cached categories (run RetagItems first so eligibility reads
// cached cats without an extra LLM call). Extraction runs up to retagConcurrency
// in parallel on the main model; metadata writes are serialized. Long-running —
// callers should run it async. Returns items scanned.
func (i *Ingestor) BackfillClaims(ctx context.Context) (int, error) {
	if i.store == nil || i.llmClient == nil {
		return 0, nil
	}
	var items []*store.KnowledgeItem
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := i.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return 0, err
			}
			items = append(items, page...)
			if len(page) < batch {
				break
			}
		}
	}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, retagConcurrency)
		processed int
	)
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *store.KnowledgeItem) {
			defer wg.Done()
			defer func() { <-sem }()
			cats, _ := i.classifyItem(ctx, it) // cached → no LLM when retag ran first
			claims, fresh := i.extractClaimsForItem(ctx, it, cats)
			mu.Lock()
			i.applyItemClaims(ctx, it, claims, fresh, true) // backfill: preserve updated_at
			processed++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	return processed, nil
}

// BackfillEntityIndex (re)builds the entity_mentions index from the claims
// already cached on each item — deterministic, no LLM. Run after BackfillClaims
// (or any time) to populate the index for the existing corpus. Covers the item
// kinds that carry claims (mail, notes, decisions). Idempotent (ReplaceEntityMentions
// clears then re-inserts per item). Returns items scanned.
func (i *Ingestor) BackfillEntityIndex(ctx context.Context) (int, error) {
	if i.store == nil {
		return 0, nil
	}
	var processed int
	var attrNodesWritten int
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeDecision) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := i.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return processed, err
			}
			for _, it := range page {
				if ctx.Err() != nil {
					return processed, ctx.Err()
				}
				claims := contradict.ClaimsFromMetadata(it.Metadata)
				mentions := entityMentionsFromClaims(claims)
				mentions = append(mentions, nerEntityMentions(it)...)
				mentions = append(mentions, typedIdentifierMentions(it)...)
				if rerr := i.store.ReplaceEntityMentions(ctx, it.ContentID, mentions); rerr != nil {
					log.Printf("[ingest] entity-index backfill failed for %s: %v", it.ContentID, rerr)
				}
				_ = i.store.ReplaceIdentifierLinks(ctx, it.ContentID, identifierProximityLinks(it))
				_ = i.store.ReplaceFigureNodes(ctx, it.ContentID, figureNodes(it))
				// Keyed-entity attribute nodes (GENERALIZATION_PLAN): its error was previously
				// SWALLOWED here (`_ =`), so a failing write left entity_attr_nodes silently empty on
				// home while the sibling indexes populated. Check + log it, and count what we write so
				// a backfill run is observable. Includes the deterministic vehicle-insurance anchor.
				attrNodes := allAttrNodes(it, claims)
				if aerr := i.store.ReplaceAttrNodes(ctx, it.ContentID, attrNodes); aerr != nil {
					log.Printf("[ingest] attr-node backfill failed for %s: %v", it.ContentID, aerr)
				} else {
					attrNodesWritten += len(attrNodes)
				}
				processed++
			}
			if len(page) < batch {
				break
			}
		}
	}
	log.Printf("[ingest] entity-index backfill: processed=%d attr_nodes_written=%d", processed, attrNodesWritten)
	return processed, nil
}
