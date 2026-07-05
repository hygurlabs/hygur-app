// Package correlate is the AUTO-CORRELATOR CORE (docs/AUTO_CORRELATION_PLAN.md): the graph of a
// digital life SELF-ASSEMBLES from extracted facts, by SHARED KEY — no edge is hand-coded per
// relation type. Two entity observations that share a HARD key (plate, VIN, email, IBAN, contract
// ref…) are the SAME entity and are auto-merged (P≈0). Observations that share only a SOFT signal
// (a name, an org label) are NEVER merged on that alone — corroborated where a hard link already
// exists, otherwise declined (the homonym trap). This is the owner-anchor law (eb7d089/448af00),
// lifted from the person to EVERY keyed entity and to the LINK itself: "auto-link on hard, decline
// on soft; never guess a link."
//
// The merge (Correlate) is GENERIC. It knows nothing about vehicles, brokers or spouses: it unions
// observations that share a hard key, whatever the key TYPE or entity KIND. "Lefevre the broker is
// ONE entity seen in three certificates" and "Alice Durand is the spouse" fall out of the same
// union-find as "the car in the cert is the car in the cotation" — because each is a shared hard
// key, not a special case. The vehicle/insurer TRAVERSAL (Graph.Vehicles) sits on top and enforces
// the one domain law the founder insists on: a courtier is NEVER surfaced as the assureur, and a
// price is NEVER invented — surfaced when present in the facts, honestly declined when absent.
package correlate

import (
	"sort"
	"strings"
	"time"
)

// KeyRef is one HARD, self-identifying key an observation carries: a type tag + a canonical value.
// Hard keys are the only thing that merges two observations. The set is bounded and generic
// (extend by adding a recognizer, not by adding merge logic).
type KeyRef struct {
	Type  string // "plate" | "vin" | "email" | "iban" | "enterprise_number" | "national_number" | "contract_ref"
	Value string // canonical/normalized value (the anchor)
}

func (k KeyRef) id() string { return k.Type + "\x00" + k.Value }

// Role types an attribute so the traversal can enforce the courtier≠assureur law structurally: a
// value carried under RoleCourtier can never be read as the insurer, no matter what it says.
type Role string

const (
	RoleNone     Role = ""          // a plain attribute (modèle, prix…)
	RoleAssureur Role = "assureur"  // the underwriter (AG, Baloise, Sogessur)
	RoleCourtier Role = "courtier"  // the broker (Lefevre) — NEVER the insurer
	RolePJ       Role = "pj"        // protection juridique underwriter (ARAG)
)

// Attr is one typed fact an observation asserts about the entity it anchors: a name, an optional
// role, a canonical value + its raw display form, and (filled on aggregation) its source docs.
type Attr struct {
	Name    string   // "assureur" | "courtier" | "protection juridique" | "prix" | "modele"
	Role    Role     // typed role (enforces courtier≠assureur); RoleNone for a plain attribute
	Value   string   // canonical value (grouping key)
	Raw     string   // display value ("AG Insurance", "102,35 €")
	Sources []string // doc IDs asserting this (Value); filled during aggregation
}

// Observation is one entity as seen in ONE document: the hard keys it names (the anchors), the soft
// names it carries (corroboration only), the typed attributes it asserts, and the doc date. Kind is
// PURELY descriptive — the merge never reads it, which is what keeps the correlator generic.
type Observation struct {
	DocID string
	Kind  string // "vehicle" | "person" | "org" — descriptive; NOT used by the merge
	Hard  []KeyRef
	Soft  []string
	Attrs []Attr
	Date  time.Time
}

// Entity is a canonical, auto-assembled node: the union of every observation that shares a hard key
// with it (transitively). It carries all its hard keys, the kinds it was seen as, its soft names,
// its aggregated typed attributes, and the source docs.
type Entity struct {
	Keys  []KeyRef
	Kinds []string
	Soft  []string
	Attrs []Attr
	Docs  []string
	Obs   int // number of source OBSERVATIONS fused into this entity (≥2 ⇒ an auto-correlated merge)
}

// HasKeyType reports whether the entity carries any hard key of the given type (e.g. "plate").
func (e *Entity) HasKeyType(t string) bool {
	for _, k := range e.Keys {
		if k.Type == t {
			return true
		}
	}
	return false
}

// KeyValues returns the entity's canonical values for one key type, sorted (deterministic display).
func (e *Entity) KeyValues(t string) []string {
	var out []string
	for _, k := range e.Keys {
		if k.Type == t {
			out = append(out, k.Value)
		}
	}
	sort.Strings(out)
	return out
}

// Graph is the assembled correlation graph: the canonical entities plus a lookup from any hard key
// to the entity that carries it.
type Graph struct {
	Entities []*Entity
	byKey    map[string]*Entity
}

// EntityByKey returns the canonical entity carrying the given hard key, or nil.
func (g *Graph) EntityByKey(k KeyRef) *Entity { return g.byKey[k.id()] }

// Correlate is the CORE: it auto-assembles the entity graph from a flat list of observations by
// UNION-FIND over shared HARD keys. Genericity is intrinsic — the only thing consulted is the hard
// keys; Kind, Soft and Attrs never steer the merge. Two observations merge iff they share at least
// one hard key (same type AND value). Soft names are carried onto the resulting entity but never
// cause a merge: that is the anchor-or-decline law at the LINK level (a shared bare name is a
// homonym risk, so it corroborates within an already-hard-linked cluster but never forms the link).
func Correlate(obs []Observation) *Graph {
	n := len(obs)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// The generic merge: group observation indices by each hard key, union every group. No branch
	// on key TYPE or entity KIND — a plate, a VIN and an email all merge by the identical rule.
	firstForKey := map[string]int{}
	for i, o := range obs {
		for _, k := range o.Hard {
			id := k.id()
			if j, ok := firstForKey[id]; ok {
				union(i, j)
			} else {
				firstForKey[id] = i
			}
		}
	}

	// Materialize connected components into canonical entities, aggregating keys/kinds/soft/attrs.
	comps := map[int][]int{}
	for i := range obs {
		r := find(i)
		comps[r] = append(comps[r], i)
	}
	g := &Graph{byKey: map[string]*Entity{}}
	roots := make([]int, 0, len(comps))
	for r := range comps {
		roots = append(roots, r)
	}
	sort.Ints(roots) // deterministic entity ordering
	for _, r := range roots {
		e := aggregate(obs, comps[r])
		g.Entities = append(g.Entities, e)
		for _, k := range e.Keys {
			g.byKey[k.id()] = e
		}
	}
	return g
}

// aggregate folds a component's observations into one canonical entity: distinct keys, distinct
// kinds, distinct soft names, distinct source docs, and attributes grouped by (Name,Role,Value)
// with their sources unioned (so corroboration count = distinct source docs).
func aggregate(obs []Observation, idx []int) *Entity {
	e := &Entity{Obs: len(idx)}
	keySeen, kindSeen, softSeen, docSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	attrIdx := map[string]int{} // (name|role|value) -> position in e.Attrs
	for _, i := range idx {
		o := obs[i]
		if o.DocID != "" && !docSeen[o.DocID] {
			docSeen[o.DocID] = true
			e.Docs = append(e.Docs, o.DocID)
		}
		if o.Kind != "" && !kindSeen[o.Kind] {
			kindSeen[o.Kind] = true
			e.Kinds = append(e.Kinds, o.Kind)
		}
		for _, k := range o.Hard {
			if !keySeen[k.id()] {
				keySeen[k.id()] = true
				e.Keys = append(e.Keys, k)
			}
		}
		for _, s := range o.Soft {
			s = strings.TrimSpace(s)
			if s != "" && !softSeen[strings.ToLower(s)] {
				softSeen[strings.ToLower(s)] = true
				e.Soft = append(e.Soft, s)
			}
		}
		for _, a := range o.Attrs {
			if a.Name == "" || a.Value == "" {
				continue
			}
			ak := a.Name + "|" + string(a.Role) + "|" + a.Value
			if p, ok := attrIdx[ak]; ok {
				if o.DocID != "" {
					e.Attrs[p].Sources = appendUniq(e.Attrs[p].Sources, o.DocID)
				}
				continue
			}
			attrIdx[ak] = len(e.Attrs)
			na := a
			na.Sources = nil
			if o.DocID != "" {
				na.Sources = []string{o.DocID}
			}
			e.Attrs = append(e.Attrs, na)
		}
	}
	sort.Slice(e.Keys, func(i, j int) bool {
		if e.Keys[i].Type != e.Keys[j].Type {
			return e.Keys[i].Type < e.Keys[j].Type
		}
		return e.Keys[i].Value < e.Keys[j].Value
	})
	sort.Strings(e.Kinds)
	sort.Strings(e.Docs)
	return e
}

func appendUniq(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
