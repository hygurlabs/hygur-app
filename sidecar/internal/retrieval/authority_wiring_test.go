package retrieval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// TestAnnotateConflictValidityWiring exercises the full M1b pass: a reconciled
// conflict written to the durable cache → read + parsed by annotateAuthority →
// applied to CAPTURE-tier results. (The pure mapping is covered by
// TestConflictValidity; this proves the store read + JSON round-trip + apply.)
func TestAnnotateConflictValidityWiring(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	conflicts := []contradict.ReconciledConflict{
		rc("supersedes", false, member("mail:OLD", "2025-01-01"), member("mail:NEW", "2025-06-01")),
		rc("conflict", false, member("mail:X", "2025-02-01"), member("mail:Y", "2025-02-02")),
	}
	blob, _ := json.Marshal(conflicts)
	if err := db.PutContradictionCache(ctx, "", string(blob), len(conflicts)); err != nil {
		t.Fatalf("PutContradictionCache: %v", err)
	}

	us := &UnifiedSearcher{store: db}
	results := []UnifiedResult{
		{ContentID: "mail:OLD", SourceType: store.SourceTypeMail},
		{ContentID: "mail:NEW", SourceType: store.SourceTypeMail},
		{ContentID: "mail:X", SourceType: store.SourceTypeMail},
		{ContentID: "mail:UNRELATED", SourceType: store.SourceTypeMail},
	}
	us.annotateAuthority(ctx, results)

	want := map[string]Validity{
		"mail:OLD":       ValiditySuperseded, // earlier side of supersedes
		"mail:NEW":       ValidityCurrent,    // latest side stays current
		"mail:X":         ValidityConflicted, // member of a conflict
		"mail:UNRELATED": ValidityCurrent,    // not in any conflict
	}
	for _, r := range results {
		if r.Tier != TierCapture {
			t.Errorf("%s tier = %s, want capture", r.ContentID, r.Tier)
		}
		if r.Validity != want[r.ContentID] {
			t.Errorf("%s validity = %q, want %q", r.ContentID, r.Validity, want[r.ContentID])
		}
	}
}
