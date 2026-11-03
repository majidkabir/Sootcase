package recommend

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
)

// analyser holds what one shipment's alternatives are built from.
type analyser struct {
	ctx  context.Context
	c    calc.Calculator
	set  Settings
	s    calc.Shipment // with leg ids
	base calc.Result
	now  Route
	own  map[string]calc.LegResult // the shipment's own legs as calculated
	sr   *ShipmentResult
	// nowTerminals counts the terminal changes of mode now; containers is
	// the shipment's containers for terminal handlings (0: not counted,
	// for the reason in noTerminals) and containerRule how it was set.
	nowTerminals  int
	containers    float64
	containerRule string
	noTerminals   string
}

// IntermodalTerminal is the hub type added for an extra terminal handling.
const IntermodalTerminal = calc.IntermodalTerminal

func terminals(r calc.Result, main map[string]bool) int { return calc.TerminalHandlings(r, main) }

// explicit is r's route as legs to calculate again (map, download): the
// shipment's legs (in) with their ends as calculated, and the added road
// legs.
func explicit(in []calc.Leg, r calc.Result) []calc.Leg {
	byID := map[string]calc.Leg{}
	for _, l := range in {
		byID[l.ID] = l
	}
	out := make([]calc.Leg, 0, len(r.Legs))
	for _, lr := range r.Legs {
		l, ok := byID[lr.LegID]
		if !ok || lr.Added != "" {
			l = calc.Leg{ID: lr.LegID, Mode: lr.Mode, VehicleType: lr.VehicleType}
		}
		if lr.From != nil {
			l.From = *lr.From
		}
		if lr.To != nil {
			l.To = *lr.To
		}
		l.AtTerminals = false
		out = append(out, l)
	}
	return out
}

// setContainers works out the shipment's containers for terminal
// handlings (calc.Calculator.Containers).
func (a *analyser) setContainers() {
	n, rule, err := a.c.Containers(a.s, a.base.WeightKg)
	if err != nil {
		a.noTerminals = err.Error()
		return
	}
	a.containers, a.containerRule = n, rule
}

// addTerminals adds to r the terminal handlings it has beyond now, and
// says so in a few words ("" if none).
func (a *analyser) addTerminals(cc calc.Calculator, r *calc.Result, main map[string]bool) ([]calc.HubResult, string, error) {
	extra := terminals(*r, main) - a.nowTerminals
	if extra <= 0 {
		return nil, "", nil
	}
	if a.containers <= 0 {
		return nil, fmt.Sprintf("%d extra terminal handlings not counted: %s", extra, a.noTerminals), nil
	}
	var hs []calc.HubResult
	kg := 0.0
	for k := range extra {
		h, err := cc.Hub(calc.HubStop{ID: fmt.Sprintf("added-terminal-%d", k+1), HubType: IntermodalTerminal, Containers: a.containers},
			a.s.CargoType, r.WeightKg/1000)
		if err != nil {
			return nil, "", err
		}
		hs = append(hs, h)
		kg += h.WTWKg
	}
	r.Hubs = append(r.Hubs, hs...)
	r.WTWKg += kg
	what := "terminal handlings"
	if extra == 1 {
		what = "terminal handling"
	}
	// No weight-dependent numbers here: results of shipments with the same
	// legs are scaled by weight (rescale); the numbers are in Terminals.
	return hs, fmt.Sprintf("%d extra %s (GLEC intermodal terminal, per TEU; %s)", extra, what, a.containerRule), nil
}

func analyse(ctx context.Context, c calc.Calculator, s calc.Shipment, set Settings) ShipmentResult {
	sr := ShipmentResult{ShipmentID: s.ID, WeightKg: s.WeightKg}
	s.Legs = append([]calc.Leg(nil), s.Legs...)
	for i := range s.Legs {
		if s.Legs[i].ID == "" {
			s.Legs[i].ID = fmt.Sprintf("leg-%d", i+1)
		}
	}
	base, err := c.Calculate(ctx, s)
	if err != nil {
		sr.Error = err.Error()
		return sr
	}
	sr.WeightKg = base.WeightKg
	a := &analyser{ctx: ctx, c: c, set: set, s: s, base: base, own: map[string]calc.LegResult{}, sr: &sr}
	for _, l := range base.Legs {
		if l.Added == "" {
			a.own[l.LegID] = l
		}
	}
	a.now = route(c, base)
	a.nowTerminals = terminals(base, nil)
	a.setContainers()
	sr.Now = a.now
	sr.Legs = s.Legs
	sr.Lane = a.lane()

	if set.has(Rail) {
		a.roadShift(Rail, calc.Rail, set.RailVehicle)
	}
	if set.has(Waterway) {
		a.roadShift(Waterway, calc.InlandWaterway, set.WaterwayVehicle)
	}
	if set.has(ShortSea) {
		a.roadShift(ShortSea, calc.Sea, set.ShortSeaVehicle)
	}
	if set.has(AirToSea) {
		a.airShift(AirToSea, calc.Sea, set.SeaVehicle)
	}
	if set.has(AirToRail) {
		a.airShift(AirToRail, calc.Rail, set.RailVehicle)
	}
	if set.has(OtherPort) {
		a.otherPorts()
	}
	if set.has(Load) {
		a.loads()
	}
	if set.has(LargerTruck) {
		a.vehicles(LargerTruck, func(l calc.Leg, r calc.LegResult) string {
			if r.Factor != nil && strings.Contains(r.Factor.VehicleType, "/rigid_") && !strings.Contains(r.Factor.VehicleType, "temperature_controlled") {
				return vehArtic
			}
			return ""
		})
	}
	if set.has(ElectricTruck) {
		a.vehicles(ElectricTruck, func(l calc.Leg, r calc.LegResult) string {
			switch vt := r.Factor.VehicleType; {
			case strings.Contains(vt, "electric"):
				return ""
			case strings.Contains(vt, "/van"):
				return vehElectricVan
			}
			return vehElectric
		})
	}
	if set.has(BioLNGTruck) {
		a.vehicles(BioLNGTruck, func(l calc.Leg, r calc.LegResult) string {
			if vt := r.Factor.VehicleType; strings.Contains(vt, "/van") || strings.Contains(vt, "electric") || strings.Contains(vt, "bio_lng") {
				return ""
			}
			return vehBioLNG
		})
	}
	sort.SliceStable(sr.Options, func(i, j int) bool { return sr.Options[i].SavedKg > sr.Options[j].SavedKg })
	return sr
}

func (a *analyser) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	for _, m := range a.sr.Notes {
		if m == n {
			return
		}
	}
	a.sr.Notes = append(a.sr.Notes, n)
}

// loc is where a leg end was calculated, or as given.
func (a *analyser) loc(l calc.Leg, from bool) (calc.Location, bool) {
	r, ok := a.own[l.ID]
	if ok && from && r.From != nil {
		return *r.From, true
	}
	if ok && !from && r.To != nil {
		return *r.To, true
	}
	p := l.To
	if from {
		p = l.From
	}
	if p.Lat == 0 && p.Lon == 0 && p.Code == "" {
		return calc.Location{}, false
	}
	return p, true
}

func (a *analyser) lane() string {
	if len(a.s.Legs) == 0 {
		return ""
	}
	from, ok1 := a.loc(a.s.Legs[0], true)
	to, ok2 := a.loc(a.s.Legs[len(a.s.Legs)-1], false)
	if !ok1 || !ok2 {
		return ""
	}
	return a.name(from) + " → " + a.name(to)
}

// name labels a place: its name, else the nearest town within 30 km, else
// its coordinates.
func (a *analyser) name(p calc.Location) string {
	return placeName(a.c, p)
}

func placeName(c calc.Calculator, p calc.Location) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Code != "" && p.Lat == 0 && p.Lon == 0 {
		return p.Code
	}
	if t, ok := c.Places.(interface {
		Town(calc.Location) (string, float64, bool)
	}); ok {
		if n, km, ok := t.Town(p); ok {
			if km > 5 {
				return "near " + n
			}
			return n
		}
	} else if f, ok := c.Places.(calc.PlaceFinder); ok {
		if near := f.Near(p, []string{"city"}, 30, 1); len(near) > 0 {
			n := near[0].Name
			if i := strings.Index(n, " ("); i > 0 {
				n = n[:i]
			}
			if distance.Haversine(p, near[0]) > 3 {
				return "near " + n
			}
			return n
		}
	}
	return fmt.Sprintf("%.3f, %.3f", p.Lat, p.Lon)
}

func route(c calc.Calculator, r calc.Result) Route {
	rt := Route{Totals: r.Emissions()}
	var last calc.Mode
	for i, l := range r.Legs {
		st := Step{Mode: l.Mode, VehicleType: l.VehicleType, DistanceKm: l.DistanceKm, WTWKg: l.WTWKg, Added: l.Added != ""}
		if l.From != nil {
			st.From = placeName(c, *l.From)
		}
		if l.To != nil {
			st.To = placeName(c, *l.To)
		}
		rt.Steps = append(rt.Steps, st)
		rt.DistanceKm += l.DistanceKm
		if i > 0 && l.Mode != last {
			rt.Transfers++
		}
		if i == 0 || l.Mode != last {
			rt.Modes = append(rt.Modes, string(l.Mode))
		}
		last = l.Mode
	}
	return rt
}

// try calculates an alternative and adds it when it is lower than now.
// main are the ids of the new legs in mode m, which must be longer than
// min_main_km in a straight line; portRoad limits the added road legs.
func (a *analyser) try(kind string, s calc.Shipment, cc calc.Calculator, m calc.Mode, main map[string]bool, portRoad bool, summary func(calc.Result) string) bool {
	s.AddRoadLegs = ptr(true)
	r, err := cc.Calculate(a.ctx, s)
	if err != nil {
		a.note("%s: not possible: %v", kindName(kind), shortErr(err))
		return false
	}
	if len(r.Warnings) > len(a.base.Warnings) {
		a.note("%s: not possible: %s", kindName(kind), r.Warnings[len(r.Warnings)-1].Text)
		return false
	}
	for _, l := range r.Legs {
		if !main[l.LegID] || l.Mode != m || l.From == nil || l.To == nil {
			continue
		}
		if km := distance.Haversine(*l.From, *l.To); km <= *a.set.MinMainKm {
			a.note("%s: not possible: the %s part would be %.0f km in a straight line, not more than %.0f km", kindName(kind), network(m), km, *a.set.MinMainKm)
			return false
		}
	}
	if portRoad {
		for _, l := range r.Legs {
			if l.Added != calc.AddedAccess || l.From == nil || l.To == nil {
				continue
			}
			if km := distance.Haversine(*l.From, *l.To); km > *a.set.MaxPortRoadKm {
				a.note("%s: not possible: the nearest %s port is %.0f km away in a straight line, more than %.0f km", kindName(kind), network(m), km, *a.set.MaxPortRoadKm)
				return false
			}
		}
	}
	// The new legs' ends where the shipment starts or ends were on a truck
	// before, except for another port: the sea leg was a sea leg already.
	edges := main
	if kind == OtherPort {
		edges = nil
	}
	hubs, hubText, err := a.addTerminals(cc, &r, edges)
	if err != nil {
		a.note("%s: not possible: %v", kindName(kind), shortErr(err))
		return false
	}
	saved := a.base.WTWKg - r.WTWKg
	if !(saved > 1e-9) {
		a.note("%s: possible, but not lower than now", kindName(kind))
		return false
	}
	rt := route(cc, r)
	rt.Terminals = hubs
	o := Option{Kind: kind, Area: AreaOf(kind), Route: rt, SavedKg: saved, Legs: explicit(s.Legs, r), Summary: summary(r)}
	if hubText != "" {
		o.Summary += "; " + hubText
	}
	if a.base.WTWKg > 0 {
		o.SavedPct = saved / a.base.WTWKg * 100
	}
	if a.now.DistanceKm > 0 {
		o.ExtraKmPct = (rt.DistanceKm - a.now.DistanceKm) / a.now.DistanceKm * 100
	}
	a.sr.Options = append(a.sr.Options, o)
	return true
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func network(m calc.Mode) string {
	switch m {
	case calc.InlandWaterway:
		return "waterway"
	default:
		return string(m)
	}
}

func kindName(k string) string {
	switch k {
	case Rail:
		return "Road to rail"
	case Waterway:
		return "Road to inland waterway"
	case ShortSea:
		return "Road to short sea"
	case AirToSea:
		return "Air to sea"
	case AirToRail:
		return "Air to rail"
	case OtherPort:
		return "Other port"
	case Load:
		return "Fuller loads"
	case LargerTruck:
		return "Larger truck"
	case ElectricTruck:
		return "Electric truck"
	case BioLNGTruck:
		return "Bio-LNG truck"
	}
	return k
}

// describe summarises a route: the main legs in mode m with their ends.
func (a *analyser) describe(r calc.Result, m calc.Mode, main map[string]bool) string {
	var parts []string
	for _, l := range r.Legs {
		if main[l.LegID] && l.Mode == m && l.From != nil && l.To != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f km, %s → %s", network(m), l.DistanceKm, placeName(a.c, *l.From), placeName(a.c, *l.To)))
		}
	}
	road := 0.0
	for _, l := range r.Legs {
		if l.Mode == calc.Road {
			road += l.DistanceKm
		}
	}
	s := strings.Join(parts, "; ")
	if road > 0 {
		s += fmt.Sprintf("; road %.0f km in all", road)
	}
	return s
}
