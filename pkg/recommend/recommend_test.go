package recommend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
)

var (
	hamburg   = calc.Location{Name: "Hamburg", Lat: 53.55, Lon: 9.99}
	munich    = calc.Location{Name: "Munich", Lat: 48.14, Lon: 11.58}
	kiel      = calc.Location{Name: "Kiel", Lat: 54.32, Lon: 10.14}
	suzhou    = calc.Location{Name: "Suzhou", Lat: 31.30, Lon: 120.59}
	pvg       = calc.Location{Code: "PVG"}
	fra       = calc.Location{Code: "FRA"}
	shanghai  = calc.Location{Code: "CNSHG"}
	rotterdam = calc.Location{Code: "NLRTM"}
)

func setup(t *testing.T) calc.Calculator {
	t.Helper()
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return calc.Calculator{Factors: ds, Distances: toyNet{}, Places: places.Codes{}}
}

// toyNet is a toy router: great circle × 1.2 for every mode, as a shortest
// feasible distance.
type toyNet struct{}

func (toyNet) Distance(_ context.Context, m calc.Mode, a, b calc.Location) (float64, error) {
	if m == calc.Air {
		return distance.Haversine(a, b), nil
	}
	return 1.2 * distance.Haversine(a, b), nil
}
func (toyNet) DistanceType(m calc.Mode) string {
	if m == calc.Air {
		return calc.DistGCD
	}
	return calc.DistSFD
}

func road(id string, from, to calc.Location, km float64) calc.Leg {
	l := calc.Leg{ID: id, Mode: calc.Road, VehicleType: "hgv", From: from, To: to}
	if km > 0 {
		l.DistanceKm, l.DistanceType = km, calc.DistSFD
	}
	return l
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func option(sr ShipmentResult, kind string) *Option {
	for i := range sr.Options {
		if sr.Options[i].Kind == kind {
			return &sr.Options[i]
		}
	}
	return nil
}

func TestValidate(t *testing.T) {
	s, skipped, err := Settings{}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if *s.MinRoadKm != 300 || *s.MinMainKm != 100 || *s.MaxPortRoadKm != 150 || s.Terminal != TerminalNearest ||
		s.RailVehicle != "freight_train" || s.WaterwayVehicle != "container_barge" || s.ShortSeaVehicle != "roro" || s.SeaVehicle != "container_ship" {
		t.Errorf("defaults: %+v", s)
	}
	if s.has(Load) || skipped[Load] == "" || len(s.Kinds) != len(Kinds())-1 {
		t.Errorf("fuller loads without a load should be skipped: %v %v", s.Kinds, skipped)
	}
	s, _, _ = Settings{LoadTonnes: ptr(20.0)}.Validate()
	if !s.has(Load) {
		t.Error("a load turns fuller loads on")
	}
	for name, bad := range map[string]Settings{
		"unknown kind":    {Kinds: []string{"teleport"}},
		"load no value":   {Kinds: []string{Load}},
		"load both":       {LoadTonnes: ptr(10.0), LoadFactor: ptr(0.5)},
		"load factor > 1": {LoadFactor: ptr(1.5)},
		"empty no load":   {EmptyTripFactor: ptr(0.2)},
		"terminal":        {Terminal: "closest"},
		"ports":           {MaxPorts: ptr(0)},
		"radius":          {PortRadiusKm: ptr(0.0)},
		"negative":        {MinRoadKm: ptr(-1.0)},
	} {
		if _, _, err := bad.Validate(); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("%s: want ErrInvalidSettings, got %v", name, err)
		}
	}
}

func TestRoadToRail(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	long := calc.Shipment{ID: "long", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	short := calc.Shipment{ID: "short", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 250)}}
	res, err := Run(ctx, c, []calc.Shipment{long, short}, Settings{Kinds: []string{Rail}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.Calculate(ctx, long)
	o := option(res.Shipments[0], Rail)
	if o == nil {
		t.Fatalf("no rail option: %+v", res.Shipments[0])
	}
	// Rail from the rail terminal (Hamburg is 6 km from DEHAM), the road to
	// it, and a terminal handling at each end of the rail leg.
	rail, _ := c.Calculate(ctx, calc.Shipment{WeightKg: 20000, Legs: o.Legs})
	want := base.WTWKg - rail.WTWKg - 2*2*18.2
	if !near(o.SavedKg, want) || !near(res.Shipments[0].Now.WTWKg, base.WTWKg) || o.SavedPct <= 0 || len(o.Terminals) != 2 {
		t.Errorf("rail saving %v, want %v", o.SavedKg, want)
	}
	if len(o.Legs) != 2 || o.Legs[0].Mode != calc.Road || o.Legs[0].To.Code != "DEHAM" || o.Legs[1].Mode != calc.Rail || o.Legs[1].From.Code != "DEHAM" ||
		o.Legs[1].To.Name != "Munich" || !strings.Contains(o.Summary, "rail") {
		t.Errorf("rail option: %+v", o)
	}
	if s := res.Shipments[1]; len(s.Options) != 0 || len(s.Notes) == 0 || !strings.Contains(s.Notes[0], "300 km") {
		t.Errorf("a 250 km leg stays on road: %+v", s)
	}
	if res.Shipments[0].Lane != "Hamburg → Munich" {
		t.Errorf("lane %q", res.Shipments[0].Lane)
	}
	if !near(res.SavedKg, o.SavedKg) || res.Kinds[0].Shipments != 1 || res.Kinds[0].BestFor != 1 || len(res.Lanes) != 1 || res.Lanes[0].Shipments != 2 {
		t.Errorf("summary: %+v", res)
	}
	srcs := strings.Join(res.Sources, "|")
	if !strings.Contains(srcs, "White Paper") || !strings.Contains(srcs, "92/106/EEC") || len(res.Limitations) == 0 || !strings.Contains(res.Label, "not reported") {
		t.Errorf("sources: %v", res.Sources)
	}

	// The rail part must be over 100 km in a straight line.
	k := calc.Shipment{ID: "kiel", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, kiel, 350)}}
	res, _ = Run(ctx, c, []calc.Shipment{k}, Settings{Kinds: []string{Rail}}, 1)
	if s := res.Shipments[0]; len(s.Options) != 0 || !strings.Contains(strings.Join(s.Notes, "|"), "not more than 100 km") {
		t.Errorf("kiel: %+v", s)
	}
}

func TestGroupsScaleByWeight(t *testing.T) {
	c := setup(t)
	a := calc.Shipment{ID: "a", WeightKg: 10000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	b := a
	b.ID, b.WeightKg = "b", 25000
	res, err := Run(context.Background(), c, []calc.Shipment{a, b}, Settings{Kinds: []string{Rail}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ra, rb := res.Shipments[0], res.Shipments[1]
	if rb.ShipmentID != "b" || rb.WeightKg != 25000 || !near(rb.Now.WTWKg, 2.5*ra.Now.WTWKg) || !near(rb.Options[0].SavedKg, 2.5*ra.Options[0].SavedKg) ||
		!near(rb.Options[0].WTWKg, 2.5*ra.Options[0].WTWKg) {
		t.Errorf("scaled: %+v vs %+v", rb, ra)
	}
	// The same as calculating b on its own.
	alone, _ := Run(context.Background(), c, []calc.Shipment{b}, Settings{Kinds: []string{Rail}}, 1)
	if !near(alone.Shipments[0].Options[0].SavedKg, rb.Options[0].SavedKg) {
		t.Errorf("grouped %v, alone %v", rb.Options[0].SavedKg, alone.Shipments[0].Options[0].SavedKg)
	}
}

// Shipments whose emissions aren't proportional to weight aren't grouped:
// each gets the same "now" as when calculated on its own.
func TestGroupsOnlyProportional(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	base := calc.Shipment{WeightKg: 2000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	reefer := base
	reefer.CargoType = "temperature_controlled"
	dedicated := base
	dedicated.Legs = []calc.Leg{base.Legs[0]}
	dedicated.Legs[0].RepositioningKm = 50
	hub := base
	hub.Hubs = []calc.HubStop{{ID: "h", HubType: calc.IntermodalTerminal, Containers: 1}}
	for name, s := range map[string]calc.Shipment{"cargo type": reefer, "repositioning": dedicated, "hub containers": hub} {
		first := base
		if name != "cargo type" {
			first = s
		}
		first.ID = "first"
		other := s
		other.ID, other.WeightKg = "other", 20000
		res, err := Run(ctx, c, []calc.Shipment{first, other}, Settings{Kinds: []string{Rail}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		alone, err := c.Calculate(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Shipments[1].Now.WTWKg; !near(got, alone.WTWKg) {
			t.Errorf("%s: now %v, alone %v", name, got, alone.WTWKg)
		}
	}
}

// noRail is a distance provider whose rail routing is temporarily down.
type noRail struct{ calc.DistanceProvider }

func (n noRail) Distance(ctx context.Context, m calc.Mode, from, to calc.Location) (float64, error) {
	if m == calc.Rail {
		return 0, fmt.Errorf("rail router: %w", calc.ErrTemporary)
	}
	return n.DistanceProvider.Distance(ctx, m, from, to)
}

// A temporary routing failure makes the shipment fail as retryable rather
// than report rail as not possible.
func TestTemporaryFailure(t *testing.T) {
	c := setup(t)
	c.Distances, c.RetryWaits = noRail{c.Distances}, []time.Duration{}
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Rail}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if sr := res.Shipments[0]; sr.Error == "" || !sr.Retryable {
		t.Errorf("want a retryable failure: %+v", sr)
	}
}

func TestAirToSeaAndRail(t *testing.T) {
	c := setup(t)
	s := calc.Shipment{ID: "air", WeightKg: 2000, Legs: []calc.Leg{
		road("pre", suzhou, pvg, 0),
		{ID: "fly", Mode: calc.Air, VehicleType: "air_freight", From: pvg, To: fra},
		road("on", fra, munich, 0),
	}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{AirToSea, AirToRail}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if sr.Error != "" {
		t.Fatal(sr.Error)
	}
	for _, k := range []struct {
		kind string
		mode calc.Mode
	}{{AirToSea, calc.Sea}, {AirToRail, calc.Rail}} {
		o := option(sr, k.kind)
		if o == nil {
			t.Errorf("%s: no option (%v)", k.kind, sr.Notes)
			continue
		}
		// From Suzhou to Munich in the new mode, between ports or rail
		// terminals, with road to them where needed.
		var main *calc.Leg
		for i := range o.Legs {
			if o.Legs[i].ID == "fly" {
				main = &o.Legs[i]
			}
		}
		first, last := o.Legs[0], o.Legs[len(o.Legs)-1]
		if main == nil || main.Mode != k.mode || first.From.Name != "Suzhou" || last.To.Name != "Munich" {
			t.Errorf("%s legs: %+v", k.kind, o.Legs)
		}
		for i := 1; i < len(o.Legs); i++ {
			if o.Legs[i].From != o.Legs[i-1].To {
				t.Errorf("%s legs don't join: %+v", k.kind, o.Legs)
			}
		}
		if o.SavedPct < 50 {
			t.Errorf("%s: saved only %.0f%%", k.kind, o.SavedPct)
		}
	}
}

func TestOtherPort(t *testing.T) {
	c := setup(t)
	s := calc.Shipment{ID: "sea", WeightKg: 10000, Legs: []calc.Leg{
		{ID: "ship", Mode: calc.Sea, VehicleType: "container_ship", From: shanghai, To: rotterdam},
		road("on", rotterdam, munich, 0),
	}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{OtherPort}, MaxPorts: ptr(6)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if sr.Error != "" {
		t.Fatal(sr.Error)
	}
	if len(sr.Options) == 0 {
		t.Fatalf("no other port lower than Rotterdam for Munich: %v", sr.Notes)
	}
	for _, o := range sr.Options {
		if o.Kind != OtherPort || !strings.HasPrefix(o.Summary, "via ") || o.Legs[0].To != o.Legs[1].From || o.Legs[0].To.Code == "NLRTM" {
			t.Errorf("option: %+v", o)
		}
		if o.SavedKg <= 0 {
			t.Errorf("listed without a saving: %+v", o)
		}
		if len(o.Terminals) != 0 {
			t.Errorf("another port adds no terminal handling: %+v", o.Terminals)
		}
	}
	for i := 1; i < len(sr.Options); i++ {
		if sr.Options[i].SavedKg > sr.Options[i-1].SavedKg {
			t.Error("options not best first")
		}
	}
}

func TestFullerLoads(t *testing.T) {
	c := setup(t)
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{{ID: "r", Mode: calc.Road, VehicleType: "hgv_artic_33t", From: hamburg, To: munich,
		DistanceKm: 780, DistanceType: calc.DistSFD}}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Load}, LoadTonnes: ptr(24.0), EmptyTripFactor: ptr(0.1), Source: "own plan"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := s
	want.Legs = []calc.Leg{s.Legs[0]}
	want.Legs[0].LoadTonnes, want.Legs[0].EmptyTripFactor = 24, 0.1
	r, err := c.Calculate(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	o := option(res.Shipments[0], Load)
	if o == nil {
		t.Fatalf("no option: %+v", res.Shipments[0])
	}
	if !near(o.WTWKg, r.WTWKg) || !strings.Contains(o.Summary, "24 t") || !strings.Contains(strings.Join(res.Sources, "|"), "own plan") {
		t.Errorf("load: %+v, want %v", o, r.WTWKg)
	}
}

func TestFailedShipment(t *testing.T) {
	c := setup(t)
	bad := calc.Shipment{ID: "bad", WeightKg: 1000, Legs: []calc.Leg{{ID: "x", Mode: calc.Road, VehicleType: "nope", From: hamburg, To: munich}}}
	res, err := Run(context.Background(), c, []calc.Shipment{bad}, Settings{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Shipments[0].Error == "" || res.Now.WTWKg != 0 {
		t.Errorf("failed: %+v", res)
	}
}

// An alternative's extra changes of mode at a rail, waterway or container
// terminal count GLEC v3.3's intermodal terminal value per container
// (Table 3: 18.2 kg CO2e, 21.4 temperature-controlled), containers = the
// shipment's TEU or its tonnes ÷ 10 t per TEU.
func TestExtraTerminals(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	set, _, err := Settings{}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	try := func(s calc.Shipment) *Option {
		t.Helper()
		base, err := c.Calculate(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		sr := &ShipmentResult{}
		a := &analyser{ctx: ctx, c: c, set: set, s: s, base: base, now: route(c, base), sr: sr}
		a.nowTerminals = terminals(base, nil)
		a.setContainers()
		alt := s
		alt.Legs = []calc.Leg{road("r1", hamburg, kiel, 0),
			{ID: "r2", Mode: calc.Rail, VehicleType: "freight_train", From: kiel, To: munich}}
		if !a.try(Rail, alt, c, calc.Rail, map[string]bool{"r2": true}, false, func(calc.Result) string { return "rail" }) {
			t.Fatalf("no option: %v", sr.Notes)
		}
		return &sr.Options[0]
	}
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 0)}}
	o := try(s)
	steps := 0.0
	for _, st := range o.Steps {
		steps += st.WTWKg
	}
	// Road → rail at Kiel, and rail → truck at Munich, where the shipment ends.
	if len(o.Terminals) != 2 || o.Terminals[0].HubType != IntermodalTerminal || !near(o.Terminals[0].Containers, 2) ||
		!near(o.Terminals[0].WTWKg, 2*18.2) || !near(o.WTWKg, steps+4*18.2) || !strings.Contains(o.Summary, "2 extra terminal handlings") {
		t.Errorf("terminal: %+v, steps %v", o, steps)
	}
	s.CargoType = "temperature_controlled"
	if o := try(s); len(o.Terminals) != 2 || !near(o.Terminals[0].WTWKg, 2*21.4) {
		t.Errorf("temperature-controlled: %+v", o.Terminals)
	}
	s.CargoType = "heavy"
	if o := try(s); len(o.Terminals) != 2 || !near(o.Terminals[0].Containers, 20/14.5) {
		t.Errorf("heavy: %+v", o.Terminals)
	}
	s.CargoType, s.WeightKg, s.TEU = "", 0, 3
	if o := try(s); len(o.Terminals) != 2 || !near(o.Terminals[0].Containers, 3) {
		t.Errorf("TEU: %+v", o.Terminals)
	}
	// Grouped shipments are scaled by weight, terminals too.
	r := Route{Terminals: []calc.HubResult{{WTWKg: 36.4, Containers: 2}}}.scale(1.5)
	if !near(r.Terminals[0].WTWKg, 54.6) || !near(r.Terminals[0].Containers, 3) {
		t.Errorf("scaled: %+v", r.Terminals)
	}
}

func TestTerminalsCount(t *testing.T) {
	leg := func(m calc.Mode, vt string) calc.LegResult { return calc.LegResult{Mode: m, VehicleType: vt} }
	for _, c := range []struct {
		legs []calc.LegResult
		want int
	}{
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.Rail, "freight_train"), leg(calc.Road, "hgv")}, 2},
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.Sea, "container_ship"), leg(calc.Road, "hgv")}, 2},
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.Sea, "roro"), leg(calc.Road, "hgv")}, 0},
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.Air, "air_freight"), leg(calc.Road, "hgv")}, 0},
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.InlandWaterway, "barge")}, 0},
		{[]calc.LegResult{leg(calc.Rail, "freight_train"), leg(calc.InlandWaterway, "container_barge")}, 1},
		{[]calc.LegResult{leg(calc.Road, "hgv"), leg(calc.Road, "van")}, 0},
	} {
		if got := terminals(calc.Result{Legs: c.legs}, nil); got != c.want {
			t.Errorf("%+v: %d, want %d", c.legs, got, c.want)
		}
	}
	// A new rail leg where the shipment starts or ends: handled there too.
	r := calc.Result{Legs: []calc.LegResult{{LegID: "m", Mode: calc.Rail}, {LegID: "x", Mode: calc.Road}}}
	if got := terminals(r, map[string]bool{"m": true}); got != 2 {
		t.Errorf("start edge: %d, want 2", got)
	}
	r = calc.Result{Legs: []calc.LegResult{{LegID: "m", Mode: calc.Rail}}}
	if got := terminals(r, map[string]bool{"m": true}); got != 2 {
		t.Errorf("door to door: %d, want 2", got)
	}
}

// Vehicle changes on the same route, with GLEC v3.3 default values, and
// the GLEC reduction area of each kind.
func TestVehicleChanges(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{LargerTruck, ElectricTruck, BioLNGTruck}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	base, _ := c.Calculate(ctx, s)
	for _, k := range []struct{ kind, alias, area string }{
		// The electric truck of the leg's own size: GLEC's European rigid 28-32 t for the rigid 26-32 t.
		{LargerTruck, "hgv_artic", "energy_efficiency"}, {ElectricTruck, "glec/road/europe/electric/rigid_28_32t", "carbon_content"}, {BioLNGTruck, "truck_bio_lng", "carbon_content"}} {
		o := option(sr, k.kind)
		if o == nil {
			t.Errorf("%s: no option (%v)", k.kind, sr.Notes)
			continue
		}
		want := s
		want.Legs = []calc.Leg{road("r", hamburg, munich, 780)}
		want.Legs[0].VehicleType = k.alias
		r, err := c.Calculate(ctx, want)
		if err != nil {
			t.Fatal(err)
		}
		if o.Area != k.area || len(o.Legs) != 1 || o.Legs[0].VehicleType != k.alias || !near(o.SavedKg, base.WTWKg-r.WTWKg) ||
			o.DistanceKm != base.Legs[0].DistanceKm || !strings.Contains(o.Summary, "same route") {
			t.Errorf("%s: %+v", k.kind, o)
		}
	}
	if len(res.Areas) != 5 || res.Areas[0].ID != "reduce_demand" || len(res.Areas[0].Kinds) != 0 || res.Areas[4].ID != "carbon_content" ||
		len(res.Areas[4].Kinds) != 2 || res.Kinds[0].Area != "energy_efficiency" {
		t.Errorf("areas %+v kinds %+v", res.Areas, res.Kinds)
	}
	if !strings.Contains(strings.Join(res.Sources, "|"), "Beyond reporting") || !strings.Contains(strings.Join(res.Sources, "|"), "Electric truck") {
		t.Errorf("sources %v", res.Sources)
	}
	// No GLEC electric or bio-LNG value in North America: said, not offered.
	us := calc.Shipment{ID: "us", WeightKg: 20000, Legs: []calc.Leg{road("r", calc.Location{Lat: 41.88, Lon: -87.63}, calc.Location{Lat: 40.71, Lon: -74.0}, 1300)}}
	res, _ = Run(ctx, c, []calc.Shipment{us}, Settings{Kinds: []string{ElectricTruck, BioLNGTruck}}, 1)
	if o := res.Shipments[0].Options; len(o) != 0 || !strings.Contains(strings.Join(res.Shipments[0].Notes, "|"), "not possible") {
		t.Errorf("US: %+v %v", o, res.Shipments[0].Notes)
	}
}

// A vehicle change keeps only the legs it lowers: GLEC's Chinese electric
// truck is higher than the diesel artic on that leg, so that leg stays.
func TestVehicleChange_OnlyLowerLegs(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	suzhou, shanghai := calc.Location{Lat: 31.30, Lon: 120.59}, calc.Location{Lat: 31.23, Lon: 121.47}
	cn := road("cn", suzhou, shanghai, 120)
	cn.VehicleType = "hgv_artic"
	de := road("de", hamburg, munich, 780)
	de.VehicleType = "hgv_artic"
	no := false
	s := calc.Shipment{ID: "s", WeightKg: 20000, AddRoadLegs: &no, Legs: []calc.Leg{cn, de}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{ElectricTruck}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	o := option(res.Shipments[0], ElectricTruck)
	if o == nil {
		t.Fatalf("no option: %v", res.Shipments[0].Notes)
	}
	if o.Legs[0].VehicleType != "hgv_artic" || o.Legs[1].VehicleType != "glec/road/europe/electric/artic_34_40t" {
		t.Errorf("legs %s %s, want the Chinese leg kept", o.Legs[0].VehicleType, o.Legs[1].VehicleType)
	}
	base, _ := c.Calculate(ctx, s)
	want := s
	want.Legs = []calc.Leg{cn, de}
	want.Legs[1].VehicleType = "glec/road/europe/electric/artic_34_40t"
	r, _ := c.Calculate(ctx, want)
	if !near(o.SavedKg, base.WTWKg-r.WTWKg) {
		t.Errorf("saved %v, want %v", o.SavedKg, base.WTWKg-r.WTWKg)
	}
}

// The electric truck of the leg's own size, in the region the alias chose.
func TestElectricSize(t *testing.T) {
	a := &analyser{c: setup(t)}
	for _, c := range []struct{ own, got, want string }{
		{"glec/road/in/small_cv/cng", "glec/road/in/artic_55t/electricity", "glec/road/in/rigid_3_5t/electricity"},
		{"glec/road/in/heavy_cv_1/diesel", "glec/road/in/artic_55t/electricity", "glec/road/in/rigid_18_5t/electricity"},
		{"glec/road/cn/dump_3_5_4_5t/diesel", "glec/road/cn/truck_12t/electricity", "glec/road/cn/truck_4_5t/electricity"},
		{"glec/road/cn/artic_35_40t/diesel", "glec/road/cn/truck_12t/electricity", "glec/road/cn/truck_12t/electricity"},
		{"glec/road/eu_sa/van_3_5t/diesel", "glec/road/europe/electric/artic_34_40t", "glec/road/europe/electric/van_3_5t"},
		{"glec/road/eu_sa/rigid_3_5_7_5t/diesel", "glec/road/europe/electric/artic_34_40t", "glec/road/europe/electric/rigid_7_5t"},
		{"glec/road/eu_sa/temperature_controlled/rigid_12_20t/diesel", "glec/road/europe/temperature_controlled/electric/artic_34_40t", "glec/road/europe/electric/rigid_14_20t"},
		{"glec/road/eu_sa/artic_34_40t/diesel", "glec/road/europe/electric/artic_34_40t", "glec/road/europe/electric/artic_34_40t"},
		{"glec/road/eu_sa/artic_60t/diesel", "glec/road/europe/electric/artic_34_40t", "glec/road/europe/electric/artic_40_50t"},
	} {
		if got := a.electricSize(&calc.Factor{VehicleType: c.own}, &calc.Factor{VehicleType: c.got}); got != c.want {
			t.Errorf("%s: %s, want %s", c.own, got, c.want)
		}
	}
}

// A temperature-controlled leg keeps GLEC's uplift on the new vehicle.
func TestVehicleChange_Reefer(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	leg := road("r", hamburg, munich, 776)
	leg.VehicleType = "hgv_reefer"
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{leg}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{ElectricTruck, BioLNGTruck}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.Calculate(ctx, s)
	for _, k := range []string{ElectricTruck, BioLNGTruck} {
		o := option(res.Shipments[0], k)
		if o == nil {
			t.Errorf("%s: no option %v", k, res.Shipments[0].Notes)
			continue
		}
		r, err := c.Calculate(ctx, calc.Shipment{ID: "s", WeightKg: 10000, Legs: o.Legs})
		if err != nil || o.Legs[0].CargoType != "temperature_controlled" || !strings.Contains(r.Legs[0].Factor.VehicleType, "temperature_controlled") ||
			!near(o.SavedKg, base.WTWKg-r.WTWKg) {
			t.Errorf("%s: leg %+v factor %s saved %v (%v)", k, o.Legs[0], r.Legs[0].Factor.VehicleType, o.SavedKg, err)
		}
	}
}

// Vehicle options compare GLEC values only, keep a cargo category given by
// the vehicle type (light), and read cargo_type "average" as none.
func TestVehicleChange_Datasets(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	run := func(vt, cargo string, kinds ...string) ShipmentResult {
		leg := road("r", hamburg, munich, 776)
		leg.VehicleType = vt
		res, err := Run(ctx, c, []calc.Shipment{{ID: "s", WeightKg: 1000, CargoType: cargo, Legs: []calc.Leg{leg}}}, Settings{Kinds: kinds}, 1)
		if err != nil {
			t.Fatal(err)
		}
		return res.Shipments[0]
	}
	// A DESNZ van: no GLEC vehicle swapped in, a note says why.
	if sr := run("vans/average_up_to_3_5_tonnes/diesel", "", ElectricTruck, BioLNGTruck, LargerTruck); len(sr.Options) != 0 ||
		!strings.Contains(strings.Join(sr.Notes, "|"), "only road legs on GLEC default values") {
		t.Errorf("DESNZ van: %+v %v", sr.Options, sr.Notes)
	}
	// A light-load artic: the electric one at light load too.
	if o := option(run("glec/road/eu_sa/artic_40_44t/light/diesel", "", ElectricTruck), ElectricTruck); o == nil ||
		o.Legs[0].CargoType != "light" || !strings.Contains(o.Summary, "artic_40_50t/light") {
		t.Errorf("light: %+v", o)
	}
	// cargo_type average on a reefer: still the reefer electric and bio-LNG values.
	sr := run("hgv_reefer", "average", ElectricTruck, BioLNGTruck)
	for _, k := range []string{ElectricTruck, BioLNGTruck} {
		if o := option(sr, k); o == nil || o.Legs[0].CargoType != "temperature_controlled" {
			t.Errorf("average + reefer %s: %+v %v", k, o, sr.Notes)
		}
	}
}

// A reefer truck given by its vehicle type: the rail alternative, its
// terminals and the larger truck keep temperature control, as with
// cargo_type; a cargo category the new vehicle has no value for is said.
func TestReeferByVehicleType(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	mk := func(vt, cargo string) calc.Shipment {
		leg := road("r", hamburg, munich, 0)
		leg.VehicleType = vt
		return calc.Shipment{ID: "s", WeightKg: 10000, CargoType: cargo, Legs: []calc.Leg{leg}}
	}
	get := func(s calc.Shipment, kind string) *Option {
		res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{kind}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		return option(res.Shipments[0], kind)
	}
	byType, byCargo := get(mk("hgv_reefer", ""), Rail), get(mk("hgv", "temperature_controlled"), Rail)
	if byType == nil || byCargo == nil {
		t.Fatalf("rail options %v %v", byType, byCargo)
	}
	if !near(byType.Route.WTWKg, byCargo.Route.WTWKg) || len(byType.Route.Terminals) == 0 ||
		!strings.Contains(byType.Route.Terminals[0].HubType, "temperature_controlled") {
		t.Errorf("by type %+v\nby cargo %+v", byType.Route, byCargo.Route)
	}
	if o := get(mk("hgv_reefer", ""), LargerTruck); o == nil || o.Legs[0].CargoType != "temperature_controlled" {
		t.Errorf("larger reefer truck: %+v", o)
	}
	res, _ := Run(ctx, c, []calc.Shipment{mk("hgv", "container")}, Settings{Kinds: []string{ElectricTruck}}, 1)
	if n := strings.Join(res.Shipments[0].Notes, "|"); !strings.Contains(n, "is for average/mixed cargo, not for container cargo") || strings.Contains(n, "not lower") {
		t.Errorf("container note: %s", n)
	}
}

// The carried temperature-controlled category also comes from a DESNZ
// refrigerated HGV (a reefer truck with a load), and the terminals are
// temperature-controlled even where the new vehicle has no reefer value.
func TestReeferCarried_DESNZAndTerminals(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	run := func(s calc.Shipment, kind string) *Option {
		res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{kind}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		return option(res.Shipments[0], kind)
	}
	leg := road("r", hamburg, munich, 0)
	leg.VehicleType, leg.LoadTonnes = "hgv_reefer", 20
	o := run(calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{leg}}, Rail)
	if o == nil || len(o.Route.Terminals) == 0 || !strings.Contains(o.Route.Terminals[0].HubType, "temperature_controlled") {
		t.Fatalf("DESNZ reefer with a load: %+v", o)
	}
	for _, l := range o.Legs {
		if l.Mode == calc.Rail && l.CargoType != "temperature_controlled" {
			t.Errorf("rail leg %+v", l)
		}
	}
	// A barge has no GLEC reefer value: its terminals are still temperature-controlled.
	leg = road("r", hamburg, munich, 0)
	leg.VehicleType = "hgv_reefer"
	if o := run(calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{leg}}, Waterway); o != nil {
		for _, h := range o.Route.Terminals {
			if !strings.Contains(h.HubType, "temperature_controlled") {
				t.Errorf("waterway terminal %+v", h)
			}
		}
	}
}

// An option's legs recalculate to the option (added road legs keep the
// cargo they were calculated for), and a leg's own "average" cargo stays
// ambient in the alternative though the shipment is temperature-controlled.
func TestOptionLegsRecalculate_Cargo(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	for _, tc := range []struct{ ship, leg string }{{"", "temperature_controlled"}, {"", "container"}, {"temperature_controlled", "average"}} {
		l := road("r", hamburg, munich, 0)
		l.CargoType = tc.leg
		s := calc.Shipment{ID: "s", WeightKg: 12000, CargoType: tc.ship, Legs: []calc.Leg{l}}
		res, err := Run(ctx, c, []calc.Shipment{s}, Settings{Kinds: []string{Rail}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		o := option(res.Shipments[0], Rail)
		if o == nil {
			t.Fatalf("%+v: no option %v", tc, res.Shipments[0].Notes)
		}
		r, err := c.Calculate(ctx, calc.Shipment{ID: "s", WeightKg: 12000, CargoType: tc.ship, Legs: o.Legs, AddRoadLegs: ptr(false)})
		if err != nil {
			t.Fatal(err)
		}
		hub := 0.0
		for _, h := range o.Route.Terminals {
			hub += h.WTWKg
			if tc.leg == "average" && strings.Contains(h.HubType, "temperature_controlled") {
				t.Errorf("average leg: reefer terminal %s", h.HubType)
			}
		}
		if !near(r.WTWKg+hub, o.Route.WTWKg) {
			t.Errorf("%+v: legs %v + terminals %v, option %v", tc, r.WTWKg, hub, o.Route.WTWKg)
		}
		for _, lr := range r.Legs {
			if tc.leg == "average" && strings.Contains(lr.Factor.VehicleType, "temperature_controlled") {
				t.Errorf("average leg: %s on %s", lr.LegID, lr.Factor.VehicleType)
			}
		}
	}
}
