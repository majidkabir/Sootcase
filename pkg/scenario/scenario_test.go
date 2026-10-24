package scenario

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
	"github.com/majidkabir/sootcase/pkg/factors"
)

var (
	hamburg = calc.Location{Name: "Hamburg", Lat: 53.55, Lon: 9.99}
	munich  = calc.Location{Name: "Munich", Lat: 48.14, Lon: 11.58}
	kiel    = calc.Location{Name: "Kiel", Lat: 54.32, Lon: 10.14} // 86 km from Hamburg
)

func setup(t *testing.T) (calc.Calculator, *factors.Dataset) {
	t.Helper()
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return calc.Calculator{Factors: ds, Distances: ds.Distances(), NoRoadLegs: true}, ds
}

func road(id string, from, to calc.Location, km float64) calc.Leg {
	return calc.Leg{ID: id, Mode: calc.Road, VehicleType: "hgv", From: from, To: to, DistanceKm: km, DistanceType: calc.DistSFD}
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func TestValidate_Defaults(t *testing.T) {
	cs, err := Plan{Changes: []Change{{Type: RoadToRail}}}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	c := cs[0]
	if *c.Share != 0.3 || *c.MinRoadKm != 300 || *c.MinRailKm != 100 || c.RailVehicle != "freight_train" || c.Terminal != TerminalNearest {
		t.Errorf("defaults: %+v", c)
	}
	for name, p := range map[string]Plan{
		"empty":            {},
		"unknown":          {Changes: []Change{{Type: "teleport"}}},
		"electric no kwh":  {Changes: []Change{{Type: Electrify, Country: "DNK", Source: "x"}}},
		"electric no src":  {Changes: []Change{{Type: Electrify, KWhPerTkm: 0.05, Country: "DNK"}}},
		"electric no ctry": {Changes: []Change{{Type: Electrify, KWhPerTkm: 0.05, Source: "x"}}},
		"fuel none":        {Changes: []Change{{Type: FuelSwitch}}},
		"ratio no source":  {Changes: []Change{{Type: FuelSwitch, Fuel: "lng", EnergyRatio: ptr(1.2)}}},
		"load none":        {Changes: []Change{{Type: LoadChange}}},
		"load both":        {Changes: []Change{{Type: LoadChange, LoadTonnes: ptr(10), LoadFactor: ptr(0.5)}}},
		"share 0":          {Changes: []Change{{Type: RoadToRail, Share: ptr(0)}}},
		"share > 1":        {Changes: []Change{{Type: RoadToRail, Share: ptr(1.5)}}},
		"bad terminal":     {Changes: []Change{{Type: RoadToRail, Terminal: "closest"}}},
	} {
		if _, err := p.Validate(); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("%s: want ErrInvalidPlan, got %v", name, err)
		}
	}
}

func TestRoadToRail(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	long := calc.Shipment{ID: "long", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	short := calc.Shipment{ID: "short", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 250)}}
	res, err := Run(ctx, c, []calc.Shipment{long, short}, Plan{Changes: []Change{{Type: RoadToRail}}})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.Calculate(ctx, long)
	rail, _ := c.Calculate(ctx, calc.Shipment{WeightKg: 20000, Legs: []calc.Leg{{ID: "r", Mode: calc.Rail, VehicleType: "freight_train", From: hamburg, To: munich}}})
	l := res.Shipments[0]
	if !near(l.Scenario.WTWKg, 0.7*base.WTWKg+0.3*rail.WTWKg) || !near(l.Base.WTWKg, base.WTWKg) {
		t.Errorf("long: base %v scenario %v; want %v", l.Base.WTWKg, l.Scenario.WTWKg, 0.7*base.WTWKg+0.3*rail.WTWKg)
	}
	if s := res.Shipments[1]; s.DiffKg != 0 {
		t.Errorf("a 250 km leg must stay on road: diff %v", s.DiffKg)
	}
	r := res.Changes[0]
	if r.ShipmentsAffected != 1 || r.LegsAffected != 1 || !strings.Contains(strings.Join(r.Sources, "|"), "White Paper") ||
		!strings.Contains(strings.Join(r.Sources, "|"), "92/106/EEC") {
		t.Errorf("report: %+v", r)
	}
	if !near(res.DiffKg, res.Scenario.WTWKg-res.Base.WTWKg) || res.DiffPct >= 0 || !strings.Contains(res.Label, "not reported") {
		t.Errorf("totals: %+v", res)
	}

	// The rail part must be over 100 km in a straight line.
	kielLeg := calc.Shipment{ID: "kiel", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, kiel, 350)}}
	if distance.Haversine(hamburg, kiel) >= 100 {
		t.Fatal("test setup: Kiel must be under 100 km from Hamburg")
	}
	res, err = Run(ctx, c, []calc.Shipment{kielLeg}, Plan{Changes: []Change{{Type: RoadToRail}}})
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Shipments[0]; s.DiffKg != 0 || len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "not more than 100 km") {
		t.Errorf("kiel: %+v", s)
	}
}

func TestElectrify(t *testing.T) {
	c, ds := setup(t)
	ctx := context.Background()
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: Electrify, KWhPerTkm: 0.06, Country: "DEU", Source: "maker data"}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := ds.Grid("DEU")
	if err != nil {
		t.Fatal(err)
	}
	got := res.Shipments[0].Scenario
	want := 10 * 780 * 0.06 * g.G / 1000
	if got.TTWKg > 1e-9 || !near(got.WTTKg, want) || !near(got.WTWKg, want) {
		t.Errorf("electric: %+v, want WTT %v", got, want)
	}
	if !strings.Contains(strings.Join(res.Changes[0].Sources, "|"), "Given: maker data") {
		t.Errorf("sources: %v", res.Changes[0].Sources)
	}

	// Half the cargo: halfway between.
	res, _ = Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: Electrify, KWhPerTkm: 0.06, Country: "DEU", Source: "x", Share: ptr(0.5)}}})
	b := res.Shipments[0].Base
	if !near(res.Shipments[0].Scenario.WTWKg, 0.5*b.WTWKg+0.5*want) {
		t.Errorf("half electric: %v", res.Shipments[0].Scenario.WTWKg)
	}
}

func TestFuelSwitch_HVO(t *testing.T) {
	c, ds := setup(t)
	ctx := context.Background()
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: FuelSwitch, Fuel: "hvo"}}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ds.FuelFactor(DieselFuel)
	h, _ := ds.FuelFactor("biofuel/biodiesel_hvo/gj")
	base := res.Shipments[0].Base
	gj := base.TTWKg / (d.TTW * kWhPerGJ)
	got := res.Shipments[0].Scenario
	if !near(got.BiogenicCO2Kg, gj*h.BiogenicCO2) || h.BiogenicCO2 != 70.83 || base.BiogenicCO2Kg != 0 {
		t.Errorf("HVO biogenic CO2: %v, want %v (factor %v)", got.BiogenicCO2Kg, gj*h.BiogenicCO2, h.BiogenicCO2)
	}
	if !near(got.TTWKg, gj*h.TTW) || !near(got.WTTKg, gj*h.WTT) {
		t.Errorf("HVO: %+v; want TTW %v WTT %v", got, gj*h.TTW, gj*h.WTT)
	}
	if got.WTWKg >= base.WTWKg {
		t.Errorf("HVO should be lower than diesel here: %v vs %v", got.WTWKg, base.WTWKg)
	}
	srcs := strings.Join(res.Changes[0].Sources, "|")
	if !strings.Contains(srcs, "biogenic") || !strings.Contains(srcs, "energy_ratio 1") {
		t.Errorf("sources: %s", srcs)
	}
	for _, name := range FuelNames() {
		if _, _, err := energyFactor(ds, name); err != nil {
			t.Errorf("fuel %s: %v", name, err)
		}
	}
}

func TestLoad_AndOrder(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{{ID: "r", Mode: calc.Road, VehicleType: "hgv_artic_33t",
		DistanceKm: 500, DistanceType: calc.DistSFD}}}
	res, err := Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: LoadChange, LoadTonnes: ptr(20), EmptyTripFactor: ptr(0.1)}}})
	if err != nil {
		t.Fatal(err)
	}
	want := s
	want.Legs = []calc.Leg{s.Legs[0]}
	want.Legs[0].LoadTonnes, want.Legs[0].EmptyTripFactor = 20, 0.1
	r, _ := c.Calculate(ctx, want)
	if !near(res.Shipments[0].Scenario.WTWKg, r.WTWKg) || res.Changes[0].LegsAffected != 1 {
		t.Errorf("load: %v, want %v (%+v)", res.Shipments[0].Scenario.WTWKg, r.WTWKg, res.Changes[0])
	}

	// Electrify half, then switch the rest to HVO: nothing left on diesel.
	res, err = Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{
		{Type: Electrify, KWhPerTkm: 0.06, Country: "DEU", Source: "x", Share: ptr(0.5)},
		{Type: FuelSwitch, Fuel: "hvo"}}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: Electrify, KWhPerTkm: 0.06, Country: "DEU", Source: "x"}}})
	h, _ := Run(ctx, c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: FuelSwitch, Fuel: "hvo"}}})
	if !near(res.Shipments[0].Scenario.WTWKg, 0.5*e.Shipments[0].Scenario.WTWKg+0.5*h.Shipments[0].Scenario.WTWKg) {
		t.Errorf("half electric, half HVO: %v", res.Shipments[0].Scenario.WTWKg)
	}
}

func TestFailedShipment(t *testing.T) {
	c, _ := setup(t)
	bad := calc.Shipment{ID: "bad", WeightKg: 1000, Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", From: hamburg, To: munich}}}
	res, err := Run(context.Background(), c, []calc.Shipment{bad}, Plan{Changes: []Change{{Type: RoadToRail}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Shipments[0].Error == "" || res.Base.WTWKg != 0 {
		t.Errorf("failed shipment: %+v", res)
	}
}
