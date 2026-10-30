package recommend

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

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
