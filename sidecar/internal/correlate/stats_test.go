package correlate

import "testing"

// TestStatsOnGolden pins the measurement semantics on the synthetic golden corpus (no PII). It proves
// Stats counts what the founder's question means: canonical (keyed) entities, auto-correlated MERGES
// (≥2 observations fused by a shared hard key), and RICH engrams (a keyed entity carrying attributes).
func TestStatsOnGolden(t *testing.T) {
	g := buildGraph(goldenDocs())
	s := g.Stats()

	// Four plate-anchored vehicles, all four carrying insurance attributes.
	if s.Vehicles != 4 {
		t.Errorf("Vehicles = %d, want 4", s.Vehicles)
	}
	if s.VehiclesRich != 4 {
		t.Errorf("VehiclesRich = %d, want 4", s.VehiclesRich)
	}

	// The broker (email shared across 3 certs + the Zoé doc) is a merge: ≥2 observations fused.
	broker := g.EntityByKey(KeyRef{Type: "email", Value: brokerEmail})
	if broker == nil || broker.Obs < 3 {
		t.Fatalf("broker hub not assembled with ≥3 observations: %+v", broker)
	}

	// At least the broker merges; Merges counts every keyed entity fused from ≥2 observations.
	if s.Merges < 1 {
		t.Errorf("Merges = %d, want ≥1 (the broker hub at minimum)", s.Merges)
	}

	// Every surfaced vehicle is rich, so Rich ≥ Vehicles.
	if s.Rich < s.Vehicles {
		t.Errorf("Rich = %d should be ≥ Vehicles = %d", s.Rich, s.Vehicles)
	}

	// Entities are canonical (keyed): the count must equal the number of graph entities carrying a key.
	var keyed int
	for _, e := range g.Entities {
		if len(e.Keys) > 0 {
			keyed++
		}
	}
	if s.Entities != keyed {
		t.Errorf("Entities = %d, want %d (keyed graph entities)", s.Entities, keyed)
	}

	// TopMergeClusters is ordered by observation count, descending.
	top := g.TopMergeClusters(5)
	for i := 1; i < len(top); i++ {
		if top[i-1].Obs < top[i].Obs {
			t.Errorf("TopMergeClusters not sorted by Obs desc: %d then %d", top[i-1].Obs, top[i].Obs)
		}
	}
}
