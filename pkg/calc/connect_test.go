package calc

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// world is a toy geography: sea and rail networks as sets of points,
// distances as great circles.
type world struct {
	sea, rail []Location
}

func nearestKm(ps []Location, p Location) float64 {
	best := math.Inf(1)
	for _, q := range ps {
		best = math.Min(best, gcKm(p, q))
	}
	return best
}

func (w world) Distance(ctx context.Context, m Mode, a, b Location) (float64, error) {
	r, err := w.Route(ctx, m, a, b)
	return r.Km, err
}

func (w world) Route(_ context.Context, m Mode, a, b Location) (Route, error) {
	if m == Rail {
		e := &OffNetworkError{Mode: Rail, FromKm: nearestKm(w.rail, a), ToKm: nearestKm(w.rail, b), MaxKm: 5}
		if e.FromOff() || e.ToOff() {
			return Route{}, e
		}
	}
	return Route{Km: gcKm(a, b)}, nil
}

func (w world) SnapKm(m Mode, p Location, _ float64) (float64, bool) {
	if m != Sea {
		return 0, false
	}
	return nearestKm(w.sea, p), true
}

type toyPlace struct {
	Location
	kinds []string
}

type gazetteer []toyPlace

func (g gazetteer) Resolve(string) (Location, bool) { return Location{}, false }

func (g gazetteer) Near(p Location, kinds []string, maxKm float64, n int) []Location {
	var out []Location
	for _, q := range g {
		all := true
		for _, k := range kinds {
			all = all && strings.Contains(strings.Join(q.kinds, ","), k)
		}
		if all && gcKm(p, q.Location) <= maxKm {
			out = append(out, q.Location)
		}
	}
	// nearest first
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if gcKm(p, out[j]) < gcKm(p, out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out[:min(n, len(out))]
}

var (
	munich   = Location{Name: "Munich", Lat: 48.14, Lon: 11.58}
	kassel   = Location{Name: "Kassel", Lat: 51.31, Lon: 9.49}
	hamburg  = Location{Name: "Hamburg", Lat: 53.55, Lon: 9.99}
	trieste  = Location{Name: "Trieste", Lat: 45.65, Lon: 13.77}
	westEnd  = Location{Name: "West", Lat: 54, Lon: -10}
	eastEnd  = Location{Name: "East", Lat: 36, Lon: 24}
	terminal = Location{Name: "Munich Riem", Lat: 48.13, Lon: 11.69}
	berlin   = Location{Name: "Berlin", Lat: 52.52, Lon: 13.40}
	village  = Location{Name: "Village", Lat: 48.40, Lon: 11.20} // 47 km from the rail terminal
)

var toy = world{
	sea:  []Location{hamburg, trieste, westEnd, eastEnd, {Lat: 54, Lon: 0}, {Lat: 40, Lon: 18}},
	rail: []Location{terminal, berlin, hamburg},
}

var toyPlaces = gazetteer{
	{hamburg, []string{KindPort, KindRail, KindSeaPort}},
	{trieste, []string{KindPort, KindRail, KindSeaPort}},
	{terminal, []string{KindRail}},
	{berlin, []string{KindRail}},
}

var connectFactors = fakeFactors{legs: map[string]Factor{
	"road/hgv":       {Mode: Road, VehicleType: "hgv", TTW: 60, WTT: 15},
	"road/truck_40t": {Mode: Road, VehicleType: "truck_40t", TTW: 50, WTT: 10},
	"sea/container":  {Mode: Sea, VehicleType: "container", TTW: 8, WTT: 2},
	"rail/train":     {Mode: Rail, VehicleType: "train", TTW: 20, WTT: 5},
}}

func toyCalc() Calculator {
	return Calculator{Factors: connectFactors, Distances: toy, Places: toyPlaces}
}

func TestRoadLegs_Gap(t *testing.T) {
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{
		{ID: "a", Mode: Road, VehicleType: "truck_40t", From: munich, To: berlin},
		{ID: "b", Mode: Sea, VehicleType: "container", From: hamburg, To: westEnd},
	}}
	r, err := toyCalc().Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 3 || r.Legs[1].Added != AddedGap || r.Legs[1].Mode != Road || r.Legs[1].VehicleType != "truck_40t" {
		t.Fatalf("want a gap road leg between a and b with the shipment's road vehicle, got %+v", r.Legs)
	}
	if !approx(r.Legs[1].DistanceKm, gcKm(berlin, hamburg)) || r.Legs[1].From.Name != "Berlin" || r.Legs[1].To.Name != "Hamburg" {
		t.Errorf("gap leg: %+v", r.Legs[1])
	}
	if !strings.Contains(r.Legs[1].Note, `leg "a" ends 255 km from where leg "b" starts`) {
		t.Errorf("note: %s", r.Legs[1].Note)
	}
	var sum float64
	for _, l := range r.Legs {
		sum += l.WTWKg
	}
	if !approx(sum, r.WTWKg) || len(r.Warnings) != 0 {
		t.Errorf("total %v, legs %v, warnings %v", r.WTWKg, sum, r.Warnings)
	}

	// Off: no leg added, a warning instead.
	off := false
	s.AddRoadLegs = &off
	r, err = toyCalc().Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 2 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "isn't counted") {
		t.Errorf("off: legs %d, warnings %q", len(r.Legs), r.Warnings)
	}
	// Off by context, unless the shipment says.
	s.AddRoadLegs = nil
	if r, _ = toyCalc().Calculate(WithoutRoadLegs(context.Background()), s); len(r.Legs) != 2 {
		t.Errorf("WithoutRoadLegs: %d legs", len(r.Legs))
	}
	on := true
	s.AddRoadLegs = &on
	if r, _ = toyCalc().Calculate(WithoutRoadLegs(context.Background()), s); len(r.Legs) != 3 {
		t.Errorf("shipment overrides the context: %d legs", len(r.Legs))
	}
}

func TestRoadLegs_SeaFromInland(t *testing.T) {
	for _, tc := range []struct {
		from, to Location
		port     string
	}{{kassel, westEnd, "Hamburg"}, {munich, eastEnd, "Trieste"}} {
		s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "sea", Mode: Sea, VehicleType: "container", From: tc.from, To: tc.to}}}
		r, err := toyCalc().Calculate(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Legs) != 2 {
			t.Fatalf("to %s: want an access road and the sea leg, got %+v", tc.to.Name, r.Legs)
		}
		road, sea := r.Legs[0], r.Legs[1]
		if road.Added != AddedAccess || road.VehicleType != DefaultRoadVehicle || road.From.Name != tc.from.Name || road.To.Name != tc.port {
			t.Errorf("to %s: access road %+v", tc.to.Name, road)
		}
		if sea.Added != "" || sea.From.Name != tc.port || !strings.Contains(sea.Note, "Starts at "+tc.port+" instead of "+tc.from.Name) {
			t.Errorf("to %s: sea leg %+v", tc.to.Name, sea)
		}
		if !approx(sea.DistanceKm, gcKm(tc.to, map[string]Location{"Hamburg": hamburg, "Trieste": trieste}[tc.port])) {
			t.Errorf("to %s: sea distance %v", tc.to.Name, sea.DistanceKm)
		}
	}

	// Off: the sea leg starts inland, with a warning.
	off := false
	s := Shipment{ID: "s", WeightKg: 1000, AddRoadLegs: &off, Legs: []Leg{{ID: "sea", Mode: Sea, VehicleType: "container", From: munich, To: westEnd}}}
	r, err := toyCalc().Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 1 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "from the sea network") {
		t.Errorf("off: %+v %q", r.Legs, r.Warnings)
	}
}

func TestRoadLegs_RailOffNetwork(t *testing.T) {
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "r", Mode: Rail, VehicleType: "train", From: berlin, To: village}}}
	r, err := toyCalc().Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 2 || r.Legs[0].To.Name != "Munich Riem" || r.Legs[1].Added != AddedAccess ||
		r.Legs[1].From.Name != "Munich Riem" || r.Legs[1].To.Name != "Village" {
		t.Fatalf("want the rail leg to the terminal and a road on, got %+v", r.Legs)
	}
	if !strings.Contains(r.Legs[0].Note, "Ends at Munich Riem instead of Village, which is 47 km from the rail network") {
		t.Errorf("note: %s", r.Legs[0].Note)
	}

	// Off: the rail leg fails, as before.
	off := false
	s.AddRoadLegs = &off
	_, err = toyCalc().Calculate(context.Background(), s)
	var one *OffNetworkError
	if !errors.As(err, &one) || !one.ToOff() || one.FromOff() {
		t.Errorf("off: want an off-network error for the destination, got %v", err)
	}
}

func TestRoadLegs_AddedLegFails(t *testing.T) {
	c := toyCalc()
	c.RoadVehicle = "no_such_truck"
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{
		{ID: "a", Mode: Sea, VehicleType: "container", From: westEnd, To: hamburg},
		{ID: "b", Mode: Rail, VehicleType: "train", From: berlin, To: terminal},
	}}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 2 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "isn't counted") {
		t.Errorf("want the added leg dropped with a warning, got %d legs, %q", len(r.Legs), r.Warnings)
	}
}

func TestRoadLegs_GivenDistancesUntouched(t *testing.T) {
	// No coordinates: nothing to compare, nothing added.
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{
		{ID: "a", Mode: Road, VehicleType: "hgv", DistanceKm: 100},
		{ID: "b", Mode: Sea, VehicleType: "container", DistanceKm: 1000},
	}}
	r, err := toyCalc().Calculate(context.Background(), s)
	if err != nil || len(r.Legs) != 2 || len(r.Warnings) != 0 {
		t.Errorf("%v %+v %q", err, r.Legs, r.Warnings)
	}
}

// GLEC v3.2 Section 1 Chapter 4 (air): the distance is the great circle
// between the origin and destination airports, so an air leg from a city is
// moved to a large airport, with the road to it added.
func TestRoadLegs_AirFromCity(t *testing.T) {
	muc := Location{Name: "Munich Airport", Lat: 48.354, Lon: 11.786}
	ham := Location{Name: "Hamburg Airport", Lat: 53.630, Lon: 9.988}
	small := Location{Name: "Small field", Lat: 48.20, Lon: 11.55} // nearer, not large
	places := append(gazetteer{}, toyPlaces...)
	places = append(places, toyPlace{muc, []string{KindAirport, KindLargeAirport}},
		toyPlace{ham, []string{KindAirport, KindLargeAirport}}, toyPlace{small, []string{KindAirport}})
	factors := fakeFactors{legs: map[string]Factor{
		"road/hgv": {Mode: Road, VehicleType: "hgv", TTW: 60, WTT: 15},
		"air/air":  {Mode: Air, VehicleType: "air", TTW: 600, WTT: 150},
	}}
	c := Calculator{Factors: factors, Distances: toy, Places: places}

	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "fly", Mode: Air, VehicleType: "air", From: munich, To: ham}}}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 2 {
		t.Fatalf("want a road to the airport and the flight, got %+v", r.Legs)
	}
	road, air := r.Legs[0], r.Legs[1]
	if road.Added != AddedAccess || road.Mode != Road || road.From.Name != "Munich" || road.To.Name != "Munich Airport" {
		t.Errorf("road to the airport: %+v", road)
	}
	if air.From.Name != "Munich Airport" || !approx(air.DistanceKm, gcKm(muc, ham)) ||
		!strings.Contains(air.Note, "Starts at Munich Airport instead of Munich, which is 7 km from an airport") {
		t.Errorf("flight: from %s, %v km, note %q", air.From.Name, air.DistanceKm, air.Note)
	}

	// Already at airports: nothing added.
	s.Legs[0].From = muc
	if r, err = c.Calculate(context.Background(), s); err != nil || len(r.Legs) != 1 || r.Legs[0].Note != "" {
		t.Errorf("airport to airport: %+v, %v", r.Legs, err)
	}

	// Off: the distance stays the cities', with a warning.
	off := false
	s = Shipment{ID: "s", WeightKg: 1000, AddRoadLegs: &off, Legs: []Leg{{ID: "fly", Mode: Air, VehicleType: "air", From: munich, To: ham}}}
	if r, err = c.Calculate(context.Background(), s); err != nil || len(r.Legs) != 1 || len(r.Warnings) != 1 ||
		!strings.Contains(r.Warnings[0], "from an airport") {
		t.Errorf("off: %+v %q %v", r.Legs, r.Warnings, err)
	}
}

// NearestTerminal: the nearest rail terminal on the network, as the
// Combined Transport Directive puts it, instead of the best trip.
func TestRoadLegs_NearestTerminal(t *testing.T) {
	dest := Location{Name: "Dest", Lat: 50.0, Lon: 12.0}
	south := Location{Name: "South terminal", Lat: 49.64, Lon: 12.0} // 40 km from Dest, away from Berlin
	north := Location{Name: "North terminal", Lat: 50.45, Lon: 12.0} // 50 km from Dest, towards Berlin
	w := world{rail: []Location{berlin, south, north}}
	places := gazetteer{{berlin, []string{KindRail}}, {south, []string{KindRail}}, {north, []string{KindRail}}}
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "r", Mode: Rail, VehicleType: "train", From: berlin, To: dest}}}

	c := Calculator{Factors: connectFactors, Distances: w, Places: places}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Legs[0].To.Name != "North terminal" {
		t.Errorf("best trip: rail to %s, want North terminal", r.Legs[0].To.Name)
	}
	c.NearestTerminal = true
	if r, err = c.Calculate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if r.Legs[0].To.Name != "South terminal" || r.Legs[1].From.Name != "South terminal" {
		t.Errorf("nearest: rail to %s, want South terminal", r.Legs[0].To.Name)
	}
}

// AtTerminals: an end on the network that isn't at a rail terminal moves
// to the nearest one, with a road leg; an end at one stays.
func TestRoadLegs_AtTerminals(t *testing.T) {
	dest := Location{Name: "Dest", Lat: 50.0, Lon: 12.0}
	south := Location{Name: "South terminal", Lat: 49.64, Lon: 12.0}
	w := world{rail: []Location{berlin, dest, south}} // Dest is on the network
	places := gazetteer{{berlin, []string{KindRail}}, {south, []string{KindRail}}}
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "r", Mode: Rail, VehicleType: "train", From: berlin, To: dest}}}
	c := Calculator{Factors: connectFactors, Distances: w, Places: places, NearestTerminal: true}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 1 {
		t.Fatalf("without AtTerminals: %d legs", len(r.Legs))
	}
	s.Legs[0].AtTerminals = true
	if r, err = c.Calculate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(r.Legs) != 2 || r.Legs[0].From.Name != "Berlin" || r.Legs[0].To.Name != "South terminal" || r.Legs[1].Mode != Road ||
		r.Legs[1].Added != AddedAccess || !strings.Contains(r.Legs[0].Note, "not at a rail terminal") {
		t.Errorf("legs: %+v", r.Legs)
	}
}
