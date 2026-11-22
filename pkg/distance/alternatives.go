package distance

import (
	"math"
	"sort"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Rules for alternative routes.
const (
	// MaxStretch is how much longer than the primary route an alternative
	// may be.
	MaxStretch = 1.5
	// SameShare is the share of length two routes may have in common
	// before they count as the same route, unless they cross different
	// ferries.
	SameShare = 0.8
)

// pickAlternatives chooses up to n of cands as alternatives to primary:
// no longer than MaxStretch × primary, and not the same route as primary
// or as one already chosen (SameShare in common and the same ferries).
// The choice keeps a land-only route when primary crosses a ferry, a ferry
// route when primary is land-only, and the best route over each other set
// of ferry crossings; the rest go by OSRM's cost. The chosen are returned
// by OSRM's cost (its weight), then distance.
func pickAlternatives(primary osrmRoute, cands []osrmRoute, n int) []calc.Route {
	if n <= 0 {
		return nil
	}
	var ok []osrmRoute
	for _, c := range cands {
		if c.route.Km > 0 && c.route.Km <= MaxStretch*primary.route.Km {
			ok = append(ok, c)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool { return cheaper(ok[i], ok[j]) })

	chosen := []osrmRoute{}
	taken := make([]bool, len(ok))
	take := func(want func(osrmRoute) bool) {
		for i, c := range ok {
			if len(chosen) >= n || taken[i] || !want(c) {
				continue
			}
			if same(c, primary) {
				taken[i] = true
				continue
			}
			dup := false
			for _, k := range chosen {
				if same(c, k) {
					dup = true
					break
				}
			}
			if dup {
				taken[i] = true
				continue
			}
			taken[i] = true
			chosen = append(chosen, c)
			return
		}
	}
	hasFerry := func(r osrmRoute) bool { return r.ferries != "" }
	// Ferry against land.
	if hasFerry(primary) {
		take(func(r osrmRoute) bool { return !hasFerry(r) })
	} else {
		take(hasFerry)
	}
	// The best route over each other set of ferry crossings.
	for {
		before := len(chosen)
		seen := map[string]bool{primary.ferries: true}
		for _, k := range chosen {
			seen[k.ferries] = true
		}
		take(func(r osrmRoute) bool { return hasFerry(r) && !seen[r.ferries] })
		if len(chosen) == before {
			break
		}
	}
	// The rest by cost.
	for len(chosen) < n {
		before := len(chosen)
		take(func(osrmRoute) bool { return true })
		if len(chosen) == before {
			break
		}
	}
	sort.SliceStable(chosen, func(i, j int) bool { return cheaper(chosen[i], chosen[j]) })
	out := make([]calc.Route, len(chosen))
	for i, c := range chosen {
		out[i] = c.route
	}
	return out
}

func cheaper(a, b osrmRoute) bool {
	if a.weight != b.weight {
		return a.weight < b.weight
	}
	return a.route.Km < b.route.Km
}

// same says whether a and b are the same route for choosing alternatives:
// the same ferry crossings, and SameShare or more of either's length in
// common.
func same(a, b osrmRoute) bool {
	if a.ferries != b.ferries {
		return false
	}
	return sharedShare(a, b) >= SameShare
}

// sharedShare is the larger of the shares of a's and b's length that the
// other also drives (by node pair).
func sharedShare(a, b osrmRoute) float64 {
	var shared, la, lb float64
	for e, d := range a.edges {
		la += d
		if _, ok := b.edges[e]; ok {
			shared += d
		}
	}
	for _, d := range b.edges {
		lb += d
	}
	if la <= 0 || lb <= 0 {
		return 0
	}
	return math.Max(shared/la, shared/lb)
}

// Summary rules: a road is named when the route drives at least
// summaryMinKm on it, or summaryMinShare of its length, at most
// summaryMaxRoads of them.
const (
	summaryMinKm    = 20
	summaryMinShare = 0.05
	summaryMaxRoads = 8
)

// summary is a route's main roads (by reference, in the order driven) and
// its ferry crossings (consecutive ferry steps make one), from its steps.
func (o *OSRM) summary(steps []osrmStep, routeKm float64) *calc.RouteSummary {
	s := &calc.RouteSummary{Roads: []string{}, Ferries: []calc.FerryCrossing{}}
	km := map[string]float64{}
	var order []string
	inFerry := false
	for _, st := range steps {
		if o.PartModes[st.Mode] == calc.PartFerry {
			name := strings.TrimSpace(st.Name)
			if name == "" {
				name = strings.TrimSpace(st.Ref)
			}
			if inFerry && len(s.Ferries) > 0 && (name == "" || name == s.Ferries[len(s.Ferries)-1].Name) {
				s.Ferries[len(s.Ferries)-1].DistanceKm += st.Distance / 1000
				continue
			}
			if st.Distance <= 0 {
				continue
			}
			f := calc.FerryCrossing{Name: name, DistanceKm: st.Distance / 1000}
			f.From, f.To = ports(name)
			s.Ferries = append(s.Ferries, f)
			inFerry = true
			continue
		}
		inFerry = false
		ref, _, _ := strings.Cut(st.Ref, ";")
		if ref = strings.TrimSpace(ref); ref == "" {
			continue
		}
		if _, ok := km[ref]; !ok {
			order = append(order, ref)
		}
		km[ref] += st.Distance / 1000
	}
	minKm := math.Max(summaryMinKm, summaryMinShare*routeKm)
	for _, ref := range order {
		if km[ref] >= minKm && len(s.Roads) < summaryMaxRoads {
			s.Roads = append(s.Roads, ref)
		}
	}
	if len(s.Roads) == 0 && len(order) > 0 { // a short route: its longest road
		best := order[0]
		for _, ref := range order {
			if km[ref] > km[best] {
				best = ref
			}
		}
		s.Roads = append(s.Roads, best)
	}
	for i := range s.Ferries {
		s.Ferries[i].DistanceKm = math.Round(s.Ferries[i].DistanceKm*1000) / 1000
	}
	return s
}

// ports reads a ferry's two ports from its name ("Rødby - Puttgarden",
// "Rostock – Gedser"), "" when the name doesn't give them.
func ports(name string) (string, string) {
	for _, sep := range []string{" – ", " — ", " - ", " ↔ ", " <-> "} {
		if a, b, ok := strings.Cut(name, sep); ok && !strings.Contains(b, sep) {
			if a, b = strings.TrimSpace(a), strings.TrimSpace(b); a != "" && b != "" {
				return a, b
			}
		}
	}
	return "", ""
}
