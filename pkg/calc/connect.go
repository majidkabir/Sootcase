package calc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Missing road legs.
//
// A shipment's legs should join up, each starting where the one before
// ended, and a sea, rail or inland waterway leg should start and end on its
// network. When they don't, the calculator adds road legs:
//
//   - gap: a leg starts more than GapKm from where the one before ended. A
//     road leg is added across the gap.
//   - access: a sea leg starts or ends inland, a rail or waterway leg
//     starts or ends off its network, or an air leg starts or ends away from
//     an airport. The leg is moved to a nearby port, rail terminal or large
//     airport (the one that makes the trip shortest) and a road leg is added
//     between it and the original point. Air: the GLEC Framework v3.2
//     (Section 1 Chapter 4) measures air distance as the great-circle
//     distance between the origin and destination airports.
//
// Added legs are marked (LegResult.Added) and explained (LegResult.Note);
// so are moved legs. It is on by default; Shipment.AddRoadLegs = false,
// WithoutRoadLegs or Calculator.NoRoadLegs turns it off, and then gaps and
// inland sea ends are only reported in Result.Warnings.

// Why a road leg was added (LegResult.Added).
const (
	AddedGap    = "gap"    // the legs didn't join up
	AddedAccess = "access" // to or from the port or terminal a leg was moved to
)

// GapKm is the largest distance between one leg's end and the next leg's
// start that counts as the same place. Place coordinates are often a
// town's: Hamburg the city and Hamburg the port (DEHAM) are 2 km apart.
const GapKm = 5.0

// SeaAccessKm: a sea leg end farther than this from the sea network, and not
// at a port, is inland.
const SeaAccessKm = 50.0

// SeaPortKm: a large sea port (KindSeaPort) this close to the sea network
// can be routed from, though farther than SeaAccessKm: the network is
// coarse in places (Durban, Kolkata, Paranaguá).
const SeaPortKm = 300.0

// AirportKm: an air leg end farther than this from any airport is away
// from an airport.
const AirportKm = 5.0

// DefaultRoadVehicle is the vehicle type of added road legs when the
// shipment has no road leg to take it from (Calculator.RoadVehicle).
const DefaultRoadVehicle = "hgv"

// Place kinds asked of a PlaceFinder.
const (
	KindPort         = "port"         // sea or inland port
	KindRail         = "rail"         // rail terminal
	KindSeaPort      = "seaport"      // a large cargo sea port
	KindAirport      = "airport"      // an airport with an IATA code
	KindLargeAirport = "largeairport" // a large airport
)

// PlaceFinder is optionally implemented by Calculator.Places: the places
// that are all of the given kinds within maxKm of p, nearest first, at
// most n.
type PlaceFinder interface {
	Near(p Location, kinds []string, maxKm float64, n int) []Location
}

// Countrier is optionally implemented by Calculator.Places: the country a
// point is in (ISO 3166-1 alpha-2), "" when it can't tell.
type Countrier interface {
	Country(p Location) string
}

// Snapper is optionally implemented by a DistanceProvider that can say
// cheaply how far a point is from a mode's network. Beyond maxKm it may
// return +Inf instead of the distance.
type Snapper interface {
	SnapKm(m Mode, p Location, maxKm float64) (km float64, ok bool)
}

// OffNetworkError is returned by a distance provider when a leg's end is
// too far from the mode's network to route from.
type OffNetworkError struct {
	Mode         Mode
	FromKm, ToKm float64 // each end's distance from the network
	MaxKm        float64
}

func (e *OffNetworkError) FromOff() bool { return e.FromKm > e.MaxKm }
func (e *OffNetworkError) ToOff() bool   { return e.ToKm > e.MaxKm }

func (e *OffNetworkError) Error() string {
	var ends []string
	if e.FromOff() {
		ends = append(ends, fmt.Sprintf("origin is %.1f km", e.FromKm))
	}
	if e.ToOff() {
		ends = append(ends, fmt.Sprintf("destination is %.1f km", e.ToKm))
	}
	return fmt.Sprintf("%s from the %s network (limit %.0f km)", strings.Join(ends, " and "), network(e.Mode), e.MaxKm)
}

func network(m Mode) string {
	if m == InlandWaterway {
		return "waterway"
	}
	return string(m)
}

// awayFrom is what an end that was moved was away from.
func awayFrom(m Mode) string {
	if m == Air {
		return "an airport"
	}
	return "the " + network(m) + " network"
}

type noRoadLegs struct{}

// WithoutRoadLegs makes Calculate add no road legs for shipments that
// don't say (Shipment.AddRoadLegs nil).
func WithoutRoadLegs(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRoadLegs{}, true)
}

func (c Calculator) addRoadLegs(ctx context.Context, s Shipment) bool {
	if s.AddRoadLegs != nil {
		return *s.AddRoadLegs
	}
	off, _ := ctx.Value(noRoadLegs{}).(bool)
	return !off && !c.NoRoadLegs
}

// planned is a leg to calculate, with how it came about.
type planned struct {
	Leg
	added string // AddedGap, AddedAccess or "" for the shipment's own legs
	note  string
}

// moved is a leg end moved to a port or terminal.
type moved struct {
	to    Location
	offKm float64 // how far the original point is from the network
}

// plan returns the legs to calculate: the shipment's, with the added road
// legs and moved ends when add is set, and the warnings.
// A temporary failure (ErrTemporary) while looking stops it: the result
// would depend on it.
func (c Calculator) plan(ctx context.Context, legs []Leg, add bool) ([]planned, []string, error) {
	var warnings []string
	out := make([]planned, 0, len(legs))
	fixed := make([]Leg, len(legs))
	copy(fixed, legs)
	from := make([]*moved, len(legs))
	to := make([]*moved, len(legs))
	for i := range fixed {
		l := &fixed[i]
		if l.DistanceKm > 0 || isZero(l.From) || isZero(l.To) {
			continue
		}
		switch l.Mode {
		case Sea, Rail, InlandWaterway, Air:
		default:
			continue
		}
		offFrom, offTo, err := c.offNetwork(ctx, *l)
		if err != nil {
			return nil, nil, err
		}
		if offFrom > 0 && add {
			p, ok, err := c.accessPoint(ctx, l.Mode, l.From, l.To, true)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				from[i] = &moved{to: p, offKm: offFrom}
				l.From = p
			}
		}
		if offTo > 0 && add {
			p, ok, err := c.accessPoint(ctx, l.Mode, l.To, l.From, false)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				to[i] = &moved{to: p, offKm: offTo}
				l.To = p
			}
		}
		if l.Mode == Sea || l.Mode == Air {
			for _, e := range []struct {
				km    float64
				fixed bool
				end   string
				p     Location
			}{{offFrom, from[i] != nil, "starts", legs[i].From}, {offTo, to[i] != nil, "ends", legs[i].To}} {
				if e.km > 0 && !e.fixed {
					if l.Mode == Air {
						warnings = append(warnings, fmt.Sprintf("leg %s %s at %s, %.0f km from an airport; the GLEC Framework measures air distance between airports, so give the airports",
							legID(legs[i], i), e.end, place(e.p), e.km))
					} else {
						warnings = append(warnings, fmt.Sprintf("leg %s %s at %s, %.0f km from the sea network; that part is counted as sea distance",
							legID(legs[i], i), e.end, place(e.p), e.km))
					}
				}
			}
		}
		if (from[i] != nil || to[i] != nil) && gcKm(l.From, l.To) <= GapKm {
			// The ends met at one port: no sea, rail or waterway left.
			fixed[i], from[i], to[i] = legs[i], nil, nil
		}
	}

	vehicle := c.RoadVehicle
	for _, l := range legs {
		if vehicle == "" && l.Mode == Road && l.VehicleType != "" {
			vehicle = l.VehicleType
		}
	}
	if vehicle == "" {
		vehicle = DefaultRoadVehicle
	}
	ids := map[string]bool{}
	for _, l := range legs {
		ids[l.ID] = true
	}
	n := 0
	road := func(a, b Location, kind, note string) {
		n++
		id := fmt.Sprintf("added-%d", n)
		for ids[id] {
			id += "x"
		}
		ids[id] = true
		out = append(out, planned{Leg: Leg{ID: id, Mode: Road, VehicleType: vehicle, From: a, To: b},
			added: kind, note: note})
	}

	for i, l := range fixed {
		// The road from where the trip is to where this leg starts.
		var prev Location
		var prevMoved *moved
		if i == 0 {
			prev = legs[0].From
		} else {
			prev, prevMoved = fixed[i-1].To, to[i-1]
		}
		if !isZero(prev) && !isZero(l.From) {
			if d := gcKm(prev, l.From); d > GapKm {
				var why []string
				if prevMoved != nil {
					why = append(why, fmt.Sprintf("leg %s ends at %s instead of %s, %.0f km from %s",
						legID(legs[i-1], i-1), place(prev), place(legs[i-1].To), prevMoved.offKm, awayFrom(legs[i-1].Mode)))
				}
				if from[i] != nil {
					why = append(why, fmt.Sprintf("leg %s starts at %s instead of %s, %.0f km from %s",
						legID(legs[i], i), place(l.From), place(legs[i].From), from[i].offKm, awayFrom(l.Mode)))
				}
				kind := AddedAccess
				if len(why) == 0 {
					kind = AddedGap
					why = append(why, fmt.Sprintf("leg %s ends %.0f km from where leg %s starts", legID(legs[i-1], i-1), d, legID(legs[i], i)))
				}
				if add {
					road(prev, l.From, kind, fmt.Sprintf("Added road from %s to %s: %s.", place(prev), place(l.From), strings.Join(why, "; ")))
				} else {
					warnings = append(warnings, why[0]+"; that distance isn't counted (adding road legs is off)")
				}
			}
		}
		p := planned{Leg: l}
		var notes []string
		if from[i] != nil {
			notes = append(notes, fmt.Sprintf("Starts at %s instead of %s, which is %.0f km from %s.",
				place(l.From), place(legs[i].From), from[i].offKm, awayFrom(l.Mode)))
		}
		if to[i] != nil {
			notes = append(notes, fmt.Sprintf("Ends at %s instead of %s, which is %.0f km from %s.",
				place(l.To), place(legs[i].To), to[i].offKm, awayFrom(l.Mode)))
		}
		if len(notes) > 0 {
			p.note = strings.Join(notes, " ")
		}
		out = append(out, p)
	}
	if last := len(fixed) - 1; last >= 0 && to[last] != nil {
		road(fixed[last].To, legs[last].To, AddedAccess, fmt.Sprintf("Added road from %s to %s: leg %s ends at %s instead of %s, %.0f km from %s.",
			place(fixed[last].To), place(legs[last].To), legID(legs[last], last), place(fixed[last].To), place(legs[last].To), to[last].offKm, awayFrom(legs[last].Mode)))
	}
	return out, warnings, nil
}

// offNetwork says how far each end of a sea, rail or waterway leg is off
// its network, 0 for an end that is on it (or when it can't tell).
func (c Calculator) offNetwork(ctx context.Context, l Leg) (fromKm, toKm float64, err error) {
	if l.Mode == Air {
		f, ok := c.Places.(PlaceFinder)
		if !ok {
			return 0, 0, nil
		}
		off := func(p Location) float64 {
			if len(f.Near(p, []string{KindAirport}, AirportKm, 1)) > 0 {
				return 0
			}
			near := nearest(f, p, []string{KindAirport}, 1000, 1)
			if len(near) == 0 {
				return 0 // no airport known nearby: can't tell
			}
			return gcKm(p, near[0])
		}
		return off(l.From), off(l.To), nil
	}
	if l.Mode == Sea {
		s, ok := c.Distances.(Snapper)
		if !ok {
			return 0, 0, nil
		}
		off := func(p Location) float64 {
			km, ok := s.SnapKm(Sea, p, math.Inf(1))
			if !ok || km <= SeaAccessKm || c.atSeaPort(s, p) {
				return 0
			}
			return km
		}
		return off(l.From), off(l.To), nil
	}
	_, err = c.route(ctx, l.Mode, l.From, l.To)
	if Temporary(err) {
		return 0, 0, err
	}
	var off *OffNetworkError
	if !errors.As(err, &off) {
		return 0, 0, nil
	}
	if off.FromOff() {
		fromKm = off.FromKm
	}
	if off.ToOff() {
		toKm = off.ToKm
	}
	return fromKm, toKm, nil
}

// atSeaPort says whether p is at (within 10 km of) a sea port: a large
// cargo sea port up to SeaPortKm from the network, or any port up to
// 2 × SeaAccessKm from it. The network is coarse in places (Paranaguá is
// 245 km from it), and UN/LOCODE doesn't tell sea ports from river ports.
func (c Calculator) atSeaPort(s Snapper, p Location) bool {
	f, ok := c.Places.(PlaceFinder)
	if !ok {
		return false
	}
	for _, q := range f.Near(p, []string{KindSeaPort}, 10, 3) {
		if km, ok := s.SnapKm(Sea, q, SeaPortKm); ok && km <= SeaPortKm {
			return true
		}
	}
	for _, q := range f.Near(p, []string{KindPort}, 10, 5) {
		if km, ok := s.SnapKm(Sea, q, 2*SeaAccessKm); ok && km <= 2*SeaAccessKm {
			return true
		}
	}
	return false
}

func (c Calculator) route(ctx context.Context, m Mode, from, to Location) (Route, error) {
	if r, ok := c.Distances.(Router); ok {
		return r.Route(ctx, m, from, to)
	}
	km, err := c.Distances.Distance(ctx, m, from, to)
	return Route{Km: km}, err
}

// accessPoint finds the port or terminal to move a leg end p to: of those
// near p on the network, the one with the best trip from p by road and on
// to the leg's other end, other, counting a road km as roadWeight km of
// the leg's mode (shippers go to a port nearer by road unless the sea
// route from a farther one is much shorter) and a border crossing on the
// road as borderKm.
func (c Calculator) accessPoint(ctx context.Context, m Mode, p, other Location, isFrom bool) (Location, bool, error) {
	f, ok := c.Places.(PlaceFinder)
	if !ok {
		return Location{}, false, nil
	}
	const (
		roadDetour = 1.2 // road km per great-circle km, roughly
		roadWeight = 3
		borderKm   = 1000
	)
	countries, _ := c.Places.(Countrier)
	home := ""
	if countries != nil {
		home = countries.Country(p)
	}
	var cands []Location
	switch m {
	case Sea:
		// Large sea ports; where there are none, any port with a rail
		// terminal on the network.
		s, _ := c.Distances.(Snapper)
		var ports []Location
		for _, q := range f.Near(p, []string{KindSeaPort}, 3000, 40) {
			if km, ok := s.SnapKm(Sea, q, SeaPortKm); ok && km <= SeaPortKm {
				ports = append(ports, q)
			}
		}
		if len(ports) == 0 {
			for _, q := range f.Near(p, []string{KindPort, KindRail}, 1500, 400) {
				if km, ok := s.SnapKm(Sea, q, SeaAccessKm); ok && km <= SeaAccessKm {
					ports = append(ports, q)
				}
			}
		}
		// The nearest, and those most on the way by great circle; the sea
		// route decides between them.
		cands = append(cands, ports[:min(8, len(ports))]...)
		guess := func(q Location) float64 {
			km := roadWeight*roadDetour*gcKm(p, q) + gcKm(q, other)
			if home != "" && countries.Country(q) != home {
				km += borderKm
			}
			return km
		}
		sort.SliceStable(ports, func(i, j int) bool { return guess(ports[i]) < guess(ports[j]) })
		for _, q := range ports[:min(8, len(ports))] {
			if !contains(cands, q) {
				cands = append(cands, q)
			}
		}
	case Rail:
		cands = f.Near(p, []string{KindRail}, 300, 8)
	case InlandWaterway:
		cands = f.Near(p, []string{KindPort}, 300, 8)
	case Air:
		// Large airports; where there are none, any airport.
		if cands = nearest(f, p, []string{KindLargeAirport}, 500, 8); len(cands) == 0 {
			cands = nearest(f, p, []string{KindAirport}, 300, 8)
		}
	}
	type scored struct {
		q  Location
		km float64
	}
	var good []scored
	for _, q := range cands {
		if err := ctx.Err(); err != nil {
			return Location{}, false, err
		}
		a, b := q, other
		if !isFrom {
			a, b = other, q
		}
		var mainKm float64
		r, err := c.route(ctx, m, a, b)
		var off *OffNetworkError
		switch {
		case Temporary(err):
			return Location{}, false, err
		case err == nil && r.Km <= 0:
			continue // no leg left between the candidate and the other end
		case err == nil:
			mainKm = r.Km
		case errors.As(err, &off) && ((isFrom && off.FromOff()) || (!isFrom && off.ToOff())):
			continue // the candidate isn't on the network either
		case errors.As(err, &off):
			mainKm = gcKm(q, other) // the other end is off too
		default:
			continue
		}
		km := roadWeight*roadDetour*gcKm(p, q) + mainKm
		if home != "" && countries.Country(q) != home {
			km += borderKm
		}
		good = append(good, scored{q, km})
	}
	if c.NearestTerminal && m == Rail {
		sort.SliceStable(good, func(i, j int) bool { return gcKm(p, good[i].q) < gcKm(p, good[j].q) })
	} else {
		sort.SliceStable(good, func(i, j int) bool { return good[i].km < good[j].km })
	}
	// The best that can be reached by road.
	for _, s := range good {
		a, b := p, s.q
		if !isFrom {
			a, b = s.q, p
		}
		_, err := c.route(ctx, Road, a, b)
		if err == nil {
			return s.q, true, nil
		}
		if Temporary(err) {
			return Location{}, false, err
		}
	}
	return Location{}, false, nil
}

// nearest is f.Near(p, kinds, maxKm, n), looking in a small radius first:
// when n places are that near, they are the n nearest.
func nearest(f PlaceFinder, p Location, kinds []string, maxKm float64, n int) []Location {
	for r := 100.0; r < maxKm; r *= 3 {
		if got := f.Near(p, kinds, r, n); len(got) == n {
			return got
		}
	}
	return f.Near(p, kinds, maxKm, n)
}

func contains(ls []Location, q Location) bool {
	for _, l := range ls {
		if l.Lat == q.Lat && l.Lon == q.Lon {
			return true
		}
	}
	return false
}

func legID(l Leg, i int) string {
	if l.ID != "" {
		return fmt.Sprintf("%q", l.ID)
	}
	return fmt.Sprint(i + 1)
}

func place(l Location) string {
	if l.Name != "" {
		return l.Name
	}
	if l.Code != "" {
		return l.Code
	}
	return fmt.Sprintf("%.4f, %.4f", l.Lat, l.Lon)
}

// gcKm is the great-circle distance in km.
func gcKm(a, b Location) float64 {
	const r = 6371.0088
	φ1, φ2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dφ, dλ := φ2-φ1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

// gapLanes are the road lanes across gaps between legs, for Prefetch.
func gapLanes(legs []Leg) []Lane {
	var out []Lane
	for i := 1; i < len(legs); i++ {
		a, b := legs[i-1].To, legs[i].From
		if !isZero(a) && !isZero(b) && gcKm(a, b) > GapKm {
			out = append(out, Lane{Mode: Road, From: Location{Lat: a.Lat, Lon: a.Lon}, To: Location{Lat: b.Lat, Lon: b.Lon}})
		}
	}
	return out
}
