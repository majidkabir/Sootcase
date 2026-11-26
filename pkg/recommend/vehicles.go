package recommend

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// loads gives the shipment's road legs the load the user set.
func (a *analyser) loads() {
	s := a.s
	s.Legs = append([]calc.Leg(nil), a.s.Legs...)
	n, unfit := 0, 0
	for j := range s.Legs {
		l := &s.Legs[j]
		if l.Mode != calc.Road || l.Fuel != nil || l.EnergyKWhPerTkm != 0 || l.OwnFactor != nil {
			continue
		}
		vt := l.VehicleType
		if a.set.LoadVehicle != "" {
			l.VehicleType = a.set.LoadVehicle
		}
		// A leg the load doesn't fit (no load model, out of its range) stays as it is.
		if why := calc.LoadFits(a.c.Factors, *l, val(a.set.LoadTonnes, 0), val(a.set.LoadFactor, 0), a.base.WeightKg/1000); why != "" { // the calculated weight (TEU given: 10 t each)
			l.VehicleType = vt
			a.note("Fuller loads: leg %s left as it is: %s", l.ID, why)
			unfit++
			continue
		}
		l.LoadTonnes, l.LoadFactor = val(a.set.LoadTonnes, 0), val(a.set.LoadFactor, 0)
		l.EmptyTripFactor = val(a.set.EmptyTripFactor, 0)
		n++
	}
	if n == 0 {
		if unfit == 0 { // else each leg's note says why
			a.note("Fuller loads: no road leg on a fleet-average factor")
		}
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
//
// A leg on a temperature-controlled value keeps it (GLEC's uplift, p. 106):
// the new vehicle's temperature-controlled value, or the leg stays as it
// is when there is none. size, when given, turns the value the alias chose
// for the leg's region into the vehicle type of the right size (its
// result's factor and the leg's own); "" keeps the alias.
func (a *analyser) vehicles(kind string, pick func(calc.Leg, calc.LegResult) string, size func(own, got *calc.Factor) string) {
	type cand struct {
		j     int
		vt    string
		cargo string // temperature_controlled for a leg whose own value is
	}
	var cands []cand
	for j, l := range a.s.Legs {
		r, ok := a.own[l.ID]
		if l.Mode != calc.Road || !ok || r.Method != calc.MethodIntensity || r.DataType != calc.DataDefault || r.Factor == nil ||
			l.Fuel != nil || l.OwnFactor != nil || l.EnergyKWhPerTkm != 0 || l.LoadFactor != 0 || l.LoadTonnes != 0 {
			continue
		}
		if vt := pick(l, r); vt != "" {
			c := cand{j: j, vt: vt}
			if reefer(r.Factor) && l.CargoType == "" && a.s.CargoType == "" {
				c.cargo = cargoReefer // given as a temperature-controlled vehicle type: the new one is too
			}
			cands = append(cands, c)
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
			if c.cargo != "" {
				s.Legs[c.j].CargoType = c.cargo
			}
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
	if err == nil && size != nil {
		changed := false
		for i, c := range cands {
			for _, l := range r.Legs {
				if l.LegID == a.s.Legs[c.j].ID && l.Factor != nil {
					if vt := size(a.own[l.LegID].Factor, l.Factor); vt != "" && vt != c.vt {
						cands[i].vt, changed = vt, true
					}
				}
			}
		}
		if changed {
			s, r, err = calcWith(cands)
		}
	}
	if err == nil {
		// Only legs the new vehicle makes lower: one that gets higher
		// (another region's value, a smaller vehicle) stays as it is, and
		// so does a temperature-controlled one without such a new value.
		var lower []cand
		for _, c := range cands {
			for _, l := range r.Legs {
				if l.LegID == a.s.Legs[c.j].ID && l.WTWKg < a.own[l.LegID].WTWKg && reefer(l.Factor) == reefer(a.own[l.LegID].Factor) {
					lower = append(lower, c)
					break
				}
			}
		}
		if len(lower) == 0 {
			a.note("%s: possible, but not lower than now", kindName(kind))
			return
		}
		if len(lower) < len(cands) {
			cands = lower
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

const cargoReefer = "temperature_controlled"

// reefer is whether a value is GLEC's for temperature-controlled freight.
func reefer(f *calc.Factor) bool {
	return f != nil && (strings.Contains(f.VehicleType, "temperature_controlled") || strings.HasSuffix(f.VehicleType, "/refrigerated"))
}

// electricSizes are GLEC v3.3's electric truck values by size: Europe
// (Tables 10–11), China (Table 12), India (Table 13a).
var electricSizes = []struct {
	prefix, suffix string
	sizes          []string
}{
	{"glec/road/europe/electric/", "", []string{"van_3_5t", "rigid_7_5t", "rigid_7_5_12t", "rigid_14_20t", "rigid_20_26t", "rigid_28_32t", "artic_28_34t", "artic_34_40t", "artic_40_50t"}},
	{"glec/road/cn/", "/electricity", []string{"truck_4_5t", "truck_4_5_12t", "truck_12t"}},
	{"glec/road/in/", "/electricity", []string{"rigid_3_5t", "rigid_3_5_12t", "rigid_14t", "rigid_18_5t", "artic_55t"}},
}

// electricSize is the electric value, in the region of the one the alias
// chose (got), for the leg's own vehicle's gross vehicle weight: the GLEC
// GVW class that holds the upper end of the own vehicle's class, or the
// nearest one (from the values' texts); "" when a weight isn't known.
func (a *analyser) electricSize(own, got *calc.Factor) string {
	if own == nil || got == nil {
		return ""
	}
	base := func(vt string) string { return strings.Replace(vt, "temperature_controlled/", "", 1) }
	class := func(vt string) (lo, hi float64, ok bool) {
		f, err := a.c.Factors.LegFactor(calc.Road, base(vt))
		if err != nil {
			return 0, 0, false
		}
		return gvw(f.Source)
	}
	g := base(got.VehicleType)
	for _, fam := range electricSizes {
		if !strings.HasPrefix(g, fam.prefix) || !strings.HasSuffix(g, fam.suffix) {
			continue
		}
		_, want, ok := class(own.VehicleType)
		if !ok || math.IsInf(want, 1) {
			return ""
		}
		best, bestD, bestE := "", math.Inf(1), math.Inf(1)
		for _, sz := range fam.sizes {
			vt := fam.prefix + sz + fam.suffix
			lo, hi, ok := class(vt)
			if !ok {
				continue
			}
			d := 0.0 // how far the weight is outside the class
			if want < lo {
				d = lo - want
			} else if want > hi {
				d = want - hi
			}
			if e := math.Abs(math.Min(hi, 1000) - want); d < bestD || (d == bestD && e < bestE) {
				best, bestD, bestE = vt, d, e
			}
		}
		return best
	}
	return ""
}

var gvwClass = regexp.MustCompile(`(?i)(?:(<|>|\bup to|\babove)\s*)?(\d+(?:\.\d+)?)(?:\s*-\s*(\d+(?:\.\d+)?))?\s*t(?:\s+GVW)?\b|GVW\s+(<|>)?\s*(\d+(?:\.\d+)?)(?:\s*-\s*(\d+(?:\.\d+)?))?\s*t\b`)

// gvw is the gross vehicle weight class a GLEC value's text gives:
// "3.5-7.5 t GVW", "GVW 5-12 t", "van < 3.5 t" (from 0), "up to 34 t GVW",
// "14 t GVW" (lo = hi), "above 49 t" (hi = +Inf).
func gvw(src string) (lo, hi float64, ok bool) {
	_, rest, found := strings.Cut(src, "Table ")
	if !found {
		return 0, 0, false
	}
	for _, m := range gvwClass.FindAllStringSubmatch(rest, -1) {
		op, a, b := m[1], m[2], m[3]
		if m[5] != "" {
			op, a, b = m[4], m[5], m[6]
		}
		if !strings.Contains(m[0], "GVW") && op != "<" && !strings.EqualFold(op, "up to") {
			continue // a number that isn't a vehicle weight
		}
		x, err := strconv.ParseFloat(a, 64)
		if err != nil {
			return 0, 0, false
		}
		switch {
		case op == ">" || strings.EqualFold(op, "above"):
			return x, math.Inf(1), true
		case op == "<" || strings.EqualFold(op, "up to"):
			return 0, x, true
		case b != "":
			y, err := strconv.ParseFloat(b, 64)
			return x, y, err == nil
		}
		return x, x, true
	}
	return 0, 0, false
}
