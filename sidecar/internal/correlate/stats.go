package correlate

import "sort"

// Stats is the measurement the founder's question asks for — « le nombre d'engrammes auto qui
// fonctionnent » (docs/AUTO_CORRELATION_PLAN.md). It is computed over the assembled Graph and carries
// NO PII (only counts + key TYPES), so it is safe to log/report verbatim.
type Stats struct {
	Observations  int            // total observations fed to Correlate
	Entities      int            // canonical entities carrying ≥1 HARD key (a keyed node)
	KeylessGroups int            // components with no hard key (dropped from Entities; noise/soft-only)
	Merges        int            // keyed entities fused from ≥2 observations — the AUTO-CORRELATED engrams
	Rich          int            // keyed entities carrying ≥1 typed attribute/relation (a "rich" engram)
	Vehicles      int            // keyed entities anchored by a plate (the golden domain)
	VehiclesRich  int            // …of those, carrying ≥1 insurance attribute (insurer/broker/pj/price)
	ByKeyType     map[string]int // canonical entities per hard-key type (plate/vin/email/iban/…)
}

// Stats folds the graph into the headline counts. An "entity" here is CANONICAL: it must carry at
// least one hard key (a keyless component — a bare soft name — is not a resolved node and is counted
// separately as noise). A "merge" is a keyed entity assembled from ≥2 observations: the union-find
// actually fused evidence, which is exactly an auto-correlated engram. "Rich" adds the attribute test.
func (g *Graph) Stats() Stats {
	s := Stats{ByKeyType: map[string]int{}}
	for _, e := range g.Entities {
		s.Observations += e.Obs
		if len(e.Keys) == 0 {
			s.KeylessGroups++
			continue
		}
		s.Entities++
		typeSeen := map[string]bool{}
		for _, k := range e.Keys {
			if !typeSeen[k.Type] {
				typeSeen[k.Type] = true
				s.ByKeyType[k.Type]++
			}
		}
		if e.Obs >= 2 {
			s.Merges++
		}
		if len(e.Attrs) > 0 {
			s.Rich++
		}
		if e.HasKeyType("plate") {
			s.Vehicles++
			for _, a := range e.Attrs {
				if a.Role == RoleAssureur || a.Role == RoleCourtier || a.Role == RolePJ || a.Name == "prix" {
					s.VehiclesRich++
					break
				}
			}
		}
	}
	return s
}

// TopMergeClusters returns the keyed entities assembled from the most observations (the biggest
// auto-correlated clusters), highest first, capped at n. These are the entities to eyeball in a report.
func (g *Graph) TopMergeClusters(n int) []*Entity {
	var keyed []*Entity
	for _, e := range g.Entities {
		if len(e.Keys) > 0 && e.Obs >= 2 {
			keyed = append(keyed, e)
		}
	}
	sort.SliceStable(keyed, func(i, j int) bool {
		if keyed[i].Obs != keyed[j].Obs {
			return keyed[i].Obs > keyed[j].Obs
		}
		return len(keyed[i].Docs) > len(keyed[j].Docs)
	})
	if n > 0 && len(keyed) > n {
		keyed = keyed[:n]
	}
	return keyed
}
