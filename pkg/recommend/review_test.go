package recommend

import (
	"context"
	"math"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Shipments with the same leg and load_tonnes are grouped only when they
// fit the load: one heavier than it is refused on its own, whatever the
// order, and a lighter one gets its own result.
func TestGroupOverLoad(t *testing.T) {
	c := setup(t)
	leg := road("a", hamburg, munich, 0)
	leg.LoadTonnes = 20
	heavy := calc.Shipment{ID: "heavy", WeightKg: 25000, Legs: []calc.Leg{leg}}
	light := calc.Shipment{ID: "light", WeightKg: 5000, Legs: []calc.Leg{leg}}
	for _, order := range [][]calc.Shipment{{light, heavy}, {heavy, light}} {
		res, err := Run(context.Background(), c, order, Settings{Kinds: []string{LargerTruck}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, sr := range res.Shipments {
			if (sr.ShipmentID == "heavy") != (sr.Error != "") {
				t.Errorf("order %s first: %s error %q", order[0].ID, sr.ShipmentID, sr.Error)
			}
		}
	}
}

// Scaling a grouped result scales a split hub's TTW and WTT too, on
// copies: the first shipment's result stays as it was.
func TestScaleHubSplit(t *testing.T) {
	ttw, wtt := 10.0, 26.4
	in := Route{Terminals: []calc.HubResult{{WTWKg: 36.4, Containers: 2, TTWKg: &ttw, WTTKg: &wtt}}}
	h := in.scale(1.5).Terminals[0]
	if math.Abs(*h.TTWKg+*h.WTTKg-h.WTWKg) > 1e-9 || ttw != 10 || wtt != 26.4 {
		t.Errorf("scaled wtw %v ttw %v wtt %v; original ttw %v wtt %v", h.WTWKg, *h.TTWKg, *h.WTTKg, ttw, wtt)
	}
}

// A shipment's own cargo_type comes back with its result, so the page's
// routes CSV can calculate it again the same way.
func TestCargoTypeReturned(t *testing.T) {
	c := setup(t)
	s := calc.Shipment{ID: "r", WeightKg: 5000, CargoType: "temperature_controlled", Legs: []calc.Leg{road("a", hamburg, munich, 0)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{LargerTruck}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Shipments[0].CargoType; got != s.CargoType {
		t.Errorf("cargo_type %q, want %q", got, s.CargoType)
	}
}

// Legs that share an id are each judged on their own: the same result
// as with their own ids.
func TestRepeatedLegIDs(t *testing.T) {
	c := setup(t)
	run := func(a, b string) ShipmentResult {
		t.Helper()
		s := calc.Shipment{ID: "dup", WeightKg: 20000, Legs: []calc.Leg{road(a, kiel, hamburg, 100), road(b, hamburg, munich, 780)}}
		res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Rail}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		return res.Shipments[0]
	}
	same, own := run("x", "x"), run("a", "b")
	if same.Lane != own.Lane || len(same.Options) != len(own.Options) {
		t.Fatalf("repeated ids: lane %q, %d options; own ids: %q, %d", same.Lane, len(same.Options), own.Lane, len(own.Options))
	}
	for i := range same.Options {
		if math.Abs(same.Options[i].WTWKg-own.Options[i].WTWKg) > 1e-6 {
			t.Errorf("option %d: %v kg, with own ids %v", i, same.Options[i].WTWKg, own.Options[i].WTWKg)
		}
	}
}

// Fuller loads leave a leg the load doesn't fit (a van: no load model) as
// it is, with a note, and still give the option for the others.
func TestFullerLoadsSkipLegs(t *testing.T) {
	c := setup(t)
	artic := road("a", hamburg, munich, 0)
	artic.VehicleType = "hgv_artic_33t"
	van := road("v", munich, hamburg, 0)
	van.VehicleType = "van"
	s := calc.Shipment{ID: "s", WeightKg: 1000, Legs: []calc.Leg{artic, van}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Load}, LoadTonnes: ptr(24.0), Source: "own plan"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if len(sr.Options) != 1 || sr.Options[0].Kind != Load {
		t.Errorf("options %+v, notes %v", sr.Options, sr.Notes)
	}
}

// A shipment given in TEU is judged by its calculated weight, as one given
// in kg: the rigid leg a load factor can't carry is left as it is.
func TestFullerLoadsTEU(t *testing.T) {
	c := setup(t)
	artic := road("a", hamburg, munich, 0)
	artic.VehicleType = "hgv_artic_33t"
	rigid := road("v", munich, hamburg, 0)
	rigid.VehicleType = "hgv_rigid"
	for _, s := range []calc.Shipment{{ID: "kg", WeightKg: 10000, Legs: []calc.Leg{artic, rigid}}, {ID: "teu", TEU: 1, Legs: []calc.Leg{artic, rigid}}} {
		res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Load}, LoadFactor: ptr(0.9), Source: "own plan"}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if sr := res.Shipments[0]; len(sr.Options) != 1 {
			t.Errorf("%s: %d options, notes %v", s.ID, len(sr.Options), sr.Notes)
		}
	}
}
