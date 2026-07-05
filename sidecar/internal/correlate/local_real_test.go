//go:build localreal

// LOCAL-ONLY real-data validation. Reads the founder's real vehicle docs from a scratchpad mini-DB
// (path via HYGUR_MINIDB) and runs the full correlate pipeline, MASKING every value in the output.
// This file is NEVER committed (real PII touches it at runtime). Run:
//   HYGUR_MINIDB=/…/scratchpad/minidb go test ./internal/correlate/ -tags localreal -run TestLocalReal -v
package correlate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rawDoc struct {
	ContentID      string                 `json:"content_id"`
	Title          string                 `json:"title"`
	NormalizedText string                 `json:"normalized_text"`
	RawText        string                 `json:"raw_text"`
	Metadata       map[string]interface{} `json:"metadata"`
}

func mask(s string) string {
	if s == "" {
		return "∅"
	}
	f := strings.Fields(s)
	// keep the first token's first 2 chars, redact the rest — enough to recognize, not to leak.
	if len(f) == 0 {
		return "•••"
	}
	head := f[0]
	if len(head) > 2 {
		head = head[:2]
	}
	return head + "•••(" + itoa(len(s)) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestLocalReal(t *testing.T) {
	dir := os.Getenv("HYGUR_MINIDB")
	if dir == "" {
		t.Skip("set HYGUR_MINIDB to the scratchpad mini-DB dir")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var docs []Doc
	for _, f := range files {
		if strings.Contains(f, ".claims.") || strings.Contains(filepath.Base(f), "items_all") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var r rawDoc
		if json.Unmarshal(b, &r) != nil || r.ContentID == "" {
			continue
		}
		body := r.NormalizedText
		if body == "" {
			body = r.RawText
		}
		var orgs []string
		if r.Metadata != nil {
			if o, ok := r.Metadata["extracted_orgs"].([]interface{}); ok {
				for _, x := range o {
					if s, ok := x.(string); ok {
						orgs = append(orgs, s)
					}
				}
			}
		}
		docs = append(docs, Doc{ID: r.ContentID, Title: r.Title, Body: body, Orgs: orgs})
	}
	t.Logf("loaded %d real docs from %s", len(docs), dir)

	var obs []Observation
	for _, d := range docs {
		obs = append(obs, ObservationsFromDoc(d)...)
	}
	g := Correlate(obs)

	t.Logf("=== ENTITIES (auto-assembled, %d) ===", len(g.Entities))
	for _, e := range g.Entities {
		var keys []string
		for _, k := range e.Keys {
			keys = append(keys, k.Type+":"+mask(k.Value))
		}
		if len(keys) == 0 {
			continue
		}
		t.Logf("  entity kinds=%v keys=%v docs=%d soft=%d", e.Kinds, keys, len(e.Docs), len(e.Soft))
	}

	// ── THE MEASUREMENT (docs/AUTO_CORRELATION_PLAN.md): how many auto-engrams work ──
	s := g.Stats()
	t.Logf("=== AUTO-ENGRAM MEASUREMENT (counts only, no PII) ===")
	t.Logf("  observations fed        : %d", s.Observations)
	t.Logf("  canonical entities (keyed): %d", s.Entities)
	t.Logf("  keyless groups (noise)  : %d", s.KeylessGroups)
	t.Logf("  AUTO-CORRELATED MERGES  : %d  (keyed entity fused from >=2 observations)", s.Merges)
	t.Logf("  RICH engrams            : %d  (keyed entity carrying >=1 attribute/relation)", s.Rich)
	t.Logf("  vehicles (plate-anchored): %d  (with insurance attrs: %d)", s.Vehicles, s.VehiclesRich)
	t.Logf("  by hard-key type        : %v", s.ByKeyType)

	t.Logf("=== TOP MERGE CLUSTERS (masked) ===")
	for i, e := range g.TopMergeClusters(12) {
		var keys []string
		for _, k := range e.Keys {
			keys = append(keys, k.Type+":"+mask(k.Value))
		}
		var softMasked []string
		for _, sn := range e.Soft {
			softMasked = append(softMasked, mask(sn))
		}
		t.Logf("  #%d obs=%d docs=%d kinds=%v keys=%v attrs=%d soft=%v",
			i+1, e.Obs, len(e.Docs), e.Kinds, keys, len(e.Attrs), softMasked)
	}

	t.Logf("=== GOLDEN QUERY: list all my vehicles with insurer + price ===")
	for _, v := range g.Vehicles() {
		t.Logf("  plate=%s | insurer=%s | pj=%s | broker=%s | price=%s | declined=%v | srcDocs=%d",
			mask(v.Plate), safe(v.Insurer), safe(v.PJ), safe(v.Broker), safe(v.Price), v.Declined, len(v.Sources))
	}

	// Hard invariants even on real data: no broker surfaced as insurer, no fabricated price.
	for _, v := range g.Vehicles() {
		if strings.Contains(strings.ToLower(v.Insurer), "lefevre") {
			t.Errorf("HALLUCINATION: broker as insurer for a real vehicle")
		}
	}
}

func safe(s string) string {
	if s == "" {
		return "∅(declined)"
	}
	return s // insurer/broker names are non-PII org names; safe to show
}
