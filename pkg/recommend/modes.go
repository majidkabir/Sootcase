package recommend

import (
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
)

// roadShift moves the shipment's long road legs to mode m.
func (a *analyser) roadShift(kind string, m calc.Mode, vehicle string) {
	var cands []int
	for j, l := range a.s.Legs {
		r, ok := a.own[l.ID]
		if l.Mode != calc.Road || !ok || l.Fuel != nil || r.DistanceKm <= *a.set.MinRoadKm {
			continue
		}
		if _, ok := a.loc(l, true); !ok {
			continue
		}
		if _, ok := a.loc(l, false); !ok {
			continue
		}
		cands = append(cands, j)
	}
	if len(cands) == 0 {
		if kind == Rail {
			a.note("No road leg over %.0f km to move to another mode", *a.set.MinRoadKm)
		}
		return
	}
	build := func(js []int) (calc.Shipment, calc.Calculator, map[string]bool) {
		s := a.s
		s.Legs = append([]calc.Leg(nil), a.s.Legs...)
		cc := a.c
		cc.NearestTerminal = a.set.Terminal != TerminalBestTrip
		main := map[string]bool{}
		for _, j := range js {
			l := a.s.Legs[j]
			if cc.RoadVehicle == "" {
				cc.RoadVehicle = l.VehicleType
			}
			from, _ := a.loc(l, true)
			to, _ := a.loc(l, false)
			s.Legs[j] = calc.Leg{ID: l.ID, Mode: m, VehicleType: vehicle, From: from, To: to, AtTerminals: true}
			main[l.ID] = true
		}
		return s, cc, main
	}
	portRoad := m == calc.InlandWaterway || m == calc.Sea
	s, cc, main := build(cands)
	sum := func(r calc.Result) string { return a.describe(r, m, main) }
	if a.try(kind, s, cc, m, main, portRoad, sum) || len(cands) == 1 {
		return
	}
	// Legs one at a time; then the ones that work together.
	var good []int
	n := len(a.sr.Options)
	for _, j := range cands {
		s1, cc1, main1 := build([]int{j})
		if a.try(kind, s1, cc1, m, main1, portRoad, func(r calc.Result) string { return a.describe(r, m, main1) }) {
			good = append(good, j)
		}
	}
	if len(good) > 1 {
		a.sr.Options = a.sr.Options[:n]
		s, cc, main = build(good)
		a.try(kind, s, cc, m, main, portRoad, func(r calc.Result) string { return a.describe(r, m, main) })
	}
}

// airShift moves each air leg, with the road legs next to it, to one leg
// in mode m from where the cargo starts to where it ends.
func (a *analyser) airShift(kind string, m calc.Mode, vehicle string) {
	legs := a.s.Legs
	var out []calc.Leg
	main := map[string]bool{}
	cc := a.c
	cc.NearestTerminal = a.set.Terminal != TerminalBestTrip
	for j := 0; j < len(legs); j++ {
		l := legs[j]
		if l.Mode != calc.Air {
			out = append(out, l)
			continue
		}
		start, end := j, j
		// The road leg before, unless it was already taken into a span.
		if j > 0 && len(out) > 0 && out[len(out)-1].ID == legs[j-1].ID && legs[j-1].Mode == calc.Road && legs[j-1].Fuel == nil {
			start = j - 1
			out = out[:len(out)-1]
		}
		if j+1 < len(legs) && legs[j+1].Mode == calc.Road && legs[j+1].Fuel == nil {
			end = j + 1
		}
		from, ok1 := a.loc(legs[start], true)
		to, ok2 := a.loc(legs[end], false)
		if !ok1 || !ok2 {
			a.note("%s: not possible: leg %s has no places to route from", kindName(kind), l.ID)
			return
		}
		if cc.RoadVehicle == "" {
			for k := start; k <= end; k++ {
				if legs[k].Mode == calc.Road {
					cc.RoadVehicle = legs[k].VehicleType
					break
				}
			}
		}
		out = append(out, calc.Leg{ID: l.ID, Mode: m, VehicleType: vehicle, From: from, To: to, AtTerminals: true})
		main[l.ID] = true
		j = end
	}
	if len(main) == 0 {
		return
	}
	s := a.s
	s.Legs = out
	a.try(kind, s, cc, m, main, false, func(r calc.Result) string { return a.describe(r, m, main) })
}

// otherPorts tries the large sea ports near each end of the shipment's sea
// legs, with the leg on the land side of that end going to the new port.
func (a *analyser) otherPorts() {
	f, ok := a.c.Places.(calc.PlaceFinder)
	if !ok {
		return
	}
	found := false
	for j, l := range a.s.Legs {
		if l.Mode != calc.Sea {
			continue
		}
		for _, end := range []struct {
			fromSide bool
			land     int
		}{{true, j - 1}, {false, j + 1}} {
			if end.land < 0 || end.land >= len(a.s.Legs) {
				continue
			}
			ll := a.s.Legs[end.land]
			if (ll.Mode != calc.Road && ll.Mode != calc.Rail) || ll.Fuel != nil {
				continue
			}
			port, ok := a.loc(l, end.fromSide)
			if !ok {
				continue
			}
			found = true
			var cands []calc.Location
			for _, q := range f.Near(port, []string{calc.KindSeaPort}, *a.set.PortRadiusKm, *a.set.MaxPorts+3) {
				if distance.Haversine(q, port) > 20 && len(cands) < *a.set.MaxPorts {
					cands = append(cands, q)
				}
			}
			for _, q := range cands {
				s := a.s
				s.Legs = append([]calc.Leg(nil), a.s.Legs...)
				sea, land := s.Legs[j], s.Legs[end.land]
				sea.DistanceKm, sea.DistanceType = 0, ""
				land.DistanceKm, land.DistanceType = 0, ""
				if end.fromSide {
					sea.From, land.To = q, q
				} else {
					sea.To, land.From = q, q
				}
				// The sea leg's other end as calculated, so only this end changes.
				if other, ok := a.loc(l, !end.fromSide); ok {
					if end.fromSide {
						sea.To = other
					} else {
						sea.From = other
					}
				}
				s.Legs[j], s.Legs[end.land] = sea, land
				main := map[string]bool{sea.ID: true}
				name := placeName(a.c, q)
				a.try(OtherPort, s, a.c, calc.Sea, main, false, func(r calc.Result) string {
					return "via " + name + "; " + a.describe(r, calc.Sea, main)
				})
			}
		}
	}
	if !found && hasSea(a.s.Legs) {
		a.note("Other port: no sea leg with a road or rail leg on its land side")
	}
}
