package correlate

import (
	"sort"
	"strings"
)

// VehicleView is the golden-query row for one vehicle: its plate (the anchor), its model, its
// insurer + protection-juridique underwriter + broker, its price, and — the honesty ledger — the
// fields that were DECLINED because the facts do not carry them. Sources are the corroborating docs.
type VehicleView struct {
	Plate    string
	Model    string
	Insurer  string   // role=assureur only — a courtier can NEVER land here
	PJ       string   // role=pj (protection juridique)
	Broker   string   // role=courtier — surfaced AS the broker, never as the insurer
	Price    string   // a real figure from the facts; "" when none (see Declined)
	Declined []string // fields honestly declined ("insurer", "price", "broker"…)
	Sources  []string
}

// Vehicles is the traversal that answers « list all my vehicles with their insurer and price ». It
// enumerates every entity anchored by a PLATE, then reads its typed attributes into the view under
// the courtier≠assureur law: Insurer is filled ONLY from RoleAssureur attributes, Broker ONLY from
// RoleCourtier, and Price ONLY from a real `prix` fact. Any field with no backing fact is DECLINED
// (recorded in Declined), never guessed. Deterministic order by plate.
func (g *Graph) Vehicles() []VehicleView {
	var out []VehicleView
	for _, e := range g.Entities {
		if !e.HasKeyType("plate") {
			continue
		}
		v := VehicleView{Plate: strings.Join(e.KeyValues("plate"), ", ")}
		srcSeen := map[string]bool{}
		addSrc := func(ss []string) {
			for _, s := range ss {
				if !srcSeen[s] {
					srcSeen[s] = true
					v.Sources = append(v.Sources, s)
				}
			}
		}
		// Collect DISTINCT display values per role/name from the entity's aggregated attributes.
		insurers := valueSet{}
		pjs := valueSet{}
		brokers := valueSet{}
		models := valueSet{}
		prices := valueSet{}
		for _, a := range e.Attrs {
			switch {
			case a.Role == RoleAssureur:
				insurers.add(a.Raw, a.Value)
				addSrc(a.Sources)
			case a.Role == RolePJ:
				pjs.add(a.Raw, a.Value)
				addSrc(a.Sources)
			case a.Role == RoleCourtier:
				brokers.add(a.Raw, a.Value)
				addSrc(a.Sources)
			case a.Name == "prix":
				prices.add(a.Raw, a.Value)
				addSrc(a.Sources)
			case a.Name == "modele":
				models.add(a.Raw, a.Value)
			}
		}
		v.Insurer = insurers.join()
		v.PJ = pjs.join()
		v.Broker = brokers.join()
		v.Model = models.join()
		// Price: surface ONLY when the facts carry EXACTLY ONE — several distinct amounts on one
		// vehicle is ambiguous (which prime? PJ vs omnium vs renting), so decline rather than pick.
		if prices.len() == 1 {
			v.Price = prices.join()
		}
		if v.Insurer == "" {
			v.Declined = append(v.Declined, "insurer")
		}
		if v.Price == "" {
			v.Declined = append(v.Declined, "price")
		}
		if v.Broker == "" {
			v.Declined = append(v.Declined, "broker")
		}
		sort.Strings(v.Sources)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Plate < out[j].Plate })
	return out
}

// valueSet collects distinct display values keyed by their canonical form (so "AG" and "ag" don't
// double-count), preserving the display casing and yielding a deterministic "+"-joined string.
type valueSet struct {
	seen map[string]string
}

func (s *valueSet) add(raw, canon string) {
	if canon == "" {
		return
	}
	if s.seen == nil {
		s.seen = map[string]string{}
	}
	if _, ok := s.seen[canon]; !ok {
		if raw == "" {
			raw = canon
		}
		s.seen[canon] = raw
	}
}

func (s *valueSet) len() int { return len(s.seen) }

func (s *valueSet) join() string {
	if len(s.seen) == 0 {
		return ""
	}
	vals := make([]string, 0, len(s.seen))
	for _, v := range s.seen {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return strings.Join(vals, " + ")
}
