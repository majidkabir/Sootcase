package recommend

import (
	"fmt"
	"slices"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// loads gives the shipment's road legs the load the user set.
func (a *analyser) loads() {
	s := a.s
	s.Legs = append([]calc.Leg(nil), a.s.Legs...)
	n := 0
	for j := range s.Legs {
		l := &s.Legs[j]
		if l.Mode != calc.Road || l.Fuel != nil || l.EnergyKWhPerTkm != 0 || l.OwnFactor != nil {
			continue
		}
		if a.set.LoadVehicle != "" {
			l.VehicleType = a.set.LoadVehicle
		}
		l.LoadTonnes, l.LoadFactor = val(a.set.LoadTonnes, 0), val(a.set.LoadFactor, 0)
		l.EmptyTripFactor = val(a.set.EmptyTripFactor, 0)
		n++
	}
	if n == 0 {
		a.note("Fuller loads: no road leg on a fleet-average factor")
		return
	}
	load := ""
	if a.set.LoadTonnes != nil {
		load = fmt.Sprintf("%g t on the vehicle", *a.set.LoadTonnes)
	} else {
		load = fmt.Sprintf("load factor %g", *a.set.LoadFactor)
	}
	load += fmt.Sprintf(", empty trip factor %g", val(a.set.EmptyTripFactor, 0))
	if a.set.LoadVehicle != "" {
		load += ", " + a.set.LoadVehicle
	}
	s.AddRoadLegs = a.s.AddRoadLegs
	r, err := a.calc(a.c, s)
	if err != nil {
		a.note("Fuller loads: not possible: %v", shortErr(err))
		return
	}
	saved := a.base.WTWKg - r.WTWKg
	if !(saved > 1e-9) {
		a.note("Fuller loads: %s is not lower than now", load)
		return
	}
	rt := route(a.c, r)
	o := Option{Kind: Load, Area: AreaOf(Load), Route: rt, SavedKg: saved, Legs: s.Legs, Summary: fmt.Sprintf("%d road legs at %s", n, load)}
	if n == 1 {
		o.Summary = "1 road leg at " + load
	}
	if a.base.WTWKg > 0 {
		o.SavedPct = saved / a.base.WTWKg * 100
	}
	if a.now.DistanceKm > 0 {
		o.ExtraKmPct = (rt.DistanceKm - a.now.DistanceKm) / a.now.DistanceKm * 100
	}
	a.sr.Options = append(a.sr.Options, o)
}

// vehicles tries the shipment's road legs, on a fleet-average default value,
// with the vehicle type pick gives (GLEC v3.3 default values; "" leaves a
// leg as it is), on the same route. Legs where the new vehicle has no GLEC
// value for the region are left as they are; if none is left, it says why.
func (a *analyser) vehicles(kind string, pick func(calc.Leg, calc.LegResult) string) {
	type cand struct {
		j  int
		vt string
	}
	var cands []cand
	for j, l := range a.s.Legs {
		r, ok := a.own[l.ID]
		if l.Mode != calc.Road || !ok || r.Method != calc.MethodIntensity || r.DataType != calc.DataDefault || r.Factor == nil ||
			l.Fuel != nil || l.OwnFactor != nil || l.EnergyKWhPerTkm != 0 || l.LoadFactor != 0 || l.LoadTonnes != 0 {
			continue
		}
		if vt := pick(l, r); vt != "" {
			cands = append(cands, cand{j, vt})
		}
	}
	if len(cands) == 0 {
		return
	}
	calcWith := func(cs []cand) (calc.Shipment, calc.Result, error) {
		s := a.s
		s.Legs = append([]calc.Leg(nil), a.s.Legs...)
		for _, c := range cs {
			s.Legs[c.j].VehicleType = c.vt
		}
		r, err := a.calc(a.c, s)
		return s, r, err
	}
	s, r, err := calcWith(cands)
	if err != nil && len(cands) > 1 {
		var good []cand
		for _, c := range cands {
			if _, _, err1 := calcWith([]cand{c}); err1 == nil {
				good = append(good, c)
			}
		}
		if len(good) > 0 {
			cands = good
			s, r, err = calcWith(cands)
		}
	}
	if err != nil {
		a.note("%s: not possible: %v", kindName(kind), shortErr(err))
		return
	}
	saved := a.base.WTWKg - r.WTWKg
	if !(saved > 1e-9) {
		a.note("%s: possible, but not lower than now", kindName(kind))
		return
	}
	var vts []string
	for _, l := range r.Legs {
		for _, c := range cands {
			if l.LegID == a.s.Legs[c.j].ID && l.Factor != nil && !slices.Contains(vts, l.Factor.VehicleType) {
				vts = append(vts, l.Factor.VehicleType)
			}
		}
	}
	what := "road legs"
	if len(cands) == 1 {
		what = "road leg"
	}
	o := Option{Kind: kind, Area: AreaOf(kind), Route: route(a.c, r), SavedKg: saved, Legs: s.Legs,
		Summary: fmt.Sprintf("%d %s, same route: %s (GLEC default value)", len(cands), what, strings.Join(vts, ", "))}
	if a.base.WTWKg > 0 {
		o.SavedPct = saved / a.base.WTWKg * 100
	}
	if a.now.DistanceKm > 0 {
		o.ExtraKmPct = (o.Route.DistanceKm - a.now.DistanceKm) / a.now.DistanceKm * 100
	}
	a.sr.Options = append(a.sr.Options, o)
}
