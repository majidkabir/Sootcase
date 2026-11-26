package factors

import (
	"context"
	"math"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// An air leg on a great circle with a carrier's intensity per km flown
// takes GLEC's GCD + 95 km (Section 1 Chapter 4, air).
func TestAirOwnFactorPerActualKm(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	ttw := 500.0
	r, err := calc.Calculator{Factors: ds}.Calculate(context.Background(), calc.Shipment{WeightKg: 1000, Legs: []calc.Leg{{
		Mode: calc.Air, DistanceKm: 6189, DistanceType: calc.DistGCD,
		OwnFactor: &calc.OwnFactor{WTW: 600, TTW: &ttw, Source: "carrier", DistanceBasis: calc.DistActual}}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := 6284 * 0.6; math.Abs(r.WTWKg-want) > 1e-6 {
		t.Errorf("WTW %v kg, want %v (GCD + 95 km)", r.WTWKg, want)
	}
}
