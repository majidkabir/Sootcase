package scenario

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// A load change out of range is refused as a plan (a 400, quota given
// back), as the recommendations' settings are, not run on every shipment.
func TestLoadPlanRefused(t *testing.T) {
	c, _ := setup(t)
	ships := []calc.Shipment{{ID: "a", WeightKg: 10000, Legs: []calc.Leg{road("l", hamburg, munich, 700)}}}
	f := func(v float64) *float64 { return &v }
	for name, ch := range map[string]Change{
		"load_factor 2":        {Type: LoadChange, LoadFactor: f(2)},
		"load_factor -0.5":     {Type: LoadChange, LoadFactor: f(-0.5)},
		"load_factor 0":        {Type: LoadChange, LoadFactor: f(0)},
		"empty_trip_factor -1": {Type: LoadChange, LoadFactor: f(0.5), EmptyTripFactor: f(-1)},
		"load_tonnes 0":        {Type: LoadChange, LoadTonnes: f(0)},
	} {
		if _, err := Run(context.Background(), c, ships, Plan{Changes: []Change{ch}}); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("%s: %v, want an invalid plan", name, err)
		}
	}
	if _, err := Run(context.Background(), c, ships, Plan{Changes: []Change{{Type: LoadChange, LoadFactor: f(0.8), EmptyTripFactor: f(1.2)}}}); err != nil {
		t.Errorf("a valid load change (more empty km than loaded, as a leg allows): %v", err)
	}
}

// road_to_rail treats two legs that share an id each on its own.
func TestRepeatedLegIDs(t *testing.T) {
	c, _ := setup(t)
	run := func(a, b string) float64 {
		t.Helper()
		s := calc.Shipment{ID: "d", WeightKg: 20000, Legs: []calc.Leg{road(a, munich, hamburg, 780), road(b, hamburg, kiel, 100)}}
		res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: RoadToRail, Share: ptr(1)}}})
		if err != nil {
			t.Fatal(err)
		}
		return res.Shipments[0].Scenario.WTWKg
	}
	if same, own := run("truck", "truck"), run("a", "b"); math.Abs(same-own) > 1e-6 {
		t.Errorf("repeated ids %v kg, own ids %v kg", same, own)
	}
}

// A load factor below the shipment's own weight isn't applied to a leg
// (the calculator refuses it), with a note, as load_tonnes below it isn't.
func TestLoadFactorBelowShipment(t *testing.T) {
	c, _ := setup(t)
	s := calc.Shipment{ID: "h", WeightKg: 20000, Legs: []calc.Leg{road("l", hamburg, munich, 700)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: LoadChange, LoadFactor: ptr(0.5)}}})
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if sr.Error != "" || sr.Scenario.WTWKg != sr.Base.WTWKg || len(sr.Notes) == 0 {
		t.Errorf("error %q, %v -> %v kg, notes %v", sr.Error, sr.Base.WTWKg, sr.Scenario.WTWKg, sr.Notes)
	}
}

// A load change leaves a leg it doesn't fit as it was, with a note, and
// changes the others, at any share: no load model (van), a load factor
// below the shipment, load_tonnes beyond the model's range.
func TestLoadNotFitting(t *testing.T) {
	c, _ := setup(t)
	van := road("v", hamburg, munich, 100)
	van.VehicleType = "van"
	rigid := road("r", hamburg, munich, 300)
	rigid.VehicleType = "hgv_rigid"
	artic := road("a", hamburg, munich, 700)
	artic.VehicleType = "hgv_artic_33t"
	for name, tc := range map[string]struct {
		weight float64
		legs   []calc.Leg
		ch     Change
	}{
		"van":               {1000, []calc.Leg{artic, van}, Change{Type: LoadChange, LoadTonnes: ptr(16)}},
		"rigid over range":  {14000, []calc.Leg{artic, rigid}, Change{Type: LoadChange, LoadTonnes: ptr(16)}},
		"factor, share 0.5": {20000, []calc.Leg{road("l", hamburg, munich, 700)}, Change{Type: LoadChange, LoadFactor: ptr(0.5), Share: ptr(0.5)}},
	} {
		res, err := Run(context.Background(), c, []calc.Shipment{{ID: "s", WeightKg: tc.weight, Legs: tc.legs}}, Plan{Changes: []Change{tc.ch}})
		if err != nil {
			t.Fatal(err)
		}
		if sr := res.Shipments[0]; sr.Error != "" || len(sr.Notes) == 0 {
			t.Errorf("%s: error %q, notes %v", name, sr.Error, sr.Notes)
		}
	}
}
