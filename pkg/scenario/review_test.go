package scenario

import (
	"context"
	"errors"
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
