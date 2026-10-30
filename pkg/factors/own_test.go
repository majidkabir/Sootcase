package factors

import (
	"context"
	"math"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// An own intensity per actual km on a road leg routed by shortest feasible
// distance gets the dataset's road DAF (GLEC: × 1.05).
func TestOwnFactor_DAF(t *testing.T) {
	ds, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds}
	leg := calc.Leg{ID: "r", Mode: calc.Road, DistanceKm: 500, DistanceType: calc.DistSFD,
		OwnFactor: &calc.OwnFactor{WTW: 70, TTW: 56, Source: "carrier", DistanceBasis: calc.DistActual}}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10000, Legs: []calc.Leg{leg}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	if want := 10 * 500 * 1.05 * 70 / 1000; math.Abs(l.WTWKg-want) > 1e-9 || math.Abs(l.DAF-1.05) > 1e-9 || l.DAFSource == "" {
		t.Errorf("WTW %v (want %v), DAF %v %q", l.WTWKg, want, l.DAF, l.DAFSource)
	}
}
