package scenario

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/places"
)

// Without a road router the road legs to and from the rail terminals can't
// be calculated: the leg must stay on road, with a note, not lose them.
func TestRoadToRail_AccessLegsFail(t *testing.T) {
	c, _ := setup(t)
	c.NoRoadLegs, c.Places = false, places.Codes{}
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 500)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: RoadToRail, Share: ptr(1)}}})
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if sr.DiffKg != 0 || !strings.Contains(strings.Join(sr.Notes, "|"), "couldn't be calculated") {
		t.Errorf("want the leg on road with a note: %+v", sr)
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

// A temporary routing failure makes the shipment fail as retryable; it
// isn't reported as "no rail route" with no change.
func TestRoadToRail_TemporaryFailure(t *testing.T) {
	c, _ := setup(t)
	c.Distances, c.RetryWaits = noRail{c.Distances}, []time.Duration{}
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{road("r", hamburg, munich, 780)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: RoadToRail}}})
	if err != nil {
		t.Fatal(err)
	}
	if sr := res.Shipments[0]; sr.Error == "" || !sr.Retryable || len(sr.Notes) != 0 {
		t.Errorf("want a retryable failure: %+v", sr)
	}
}

// fuel_switch can't apply to a road factor that isn't for diesel vehicles
// (GLEC North America's SmartWay rows don't say the fuel): it says so.
func TestFuelSwitch_NotDieselNoted(t *testing.T) {
	c, _ := setup(t)
	c.Places = places.Codes{} // the countries pick GLEC's region
	us := calc.Shipment{ID: "us", WeightKg: 10000, Legs: []calc.Leg{{ID: "r", Mode: calc.Road, VehicleType: "hgv",
		From: calc.Location{Lat: 41.88, Lon: -87.63}, To: calc.Location{Lat: 39.10, Lon: -84.51}, DistanceKm: 480, DistanceType: calc.DistSFD}}}
	res, err := Run(context.Background(), c, []calc.Shipment{us}, Plan{Changes: []Change{{Type: FuelSwitch, Fuel: "hvo"}}})
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if sr.DiffKg != 0 || len(sr.Notes) != 1 || !strings.Contains(sr.Notes[0], "isn't given for diesel vehicles") {
		t.Errorf("want no change and a note: %+v", sr)
	}
}

// A partial load change counts each leg once, though it makes two variants.
func TestLoad_PartialLegsCountedOnce(t *testing.T) {
	c, _ := setup(t)
	s := calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{
		{ID: "a", Mode: calc.Road, VehicleType: "hgv_artic_33t", DistanceKm: 300, DistanceType: calc.DistSFD},
		{ID: "b", Mode: calc.Road, VehicleType: "hgv_artic_33t", DistanceKm: 200, DistanceType: calc.DistSFD}}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{
		{Type: FuelSwitch, Fuel: "hvo", Share: ptr(0.5)},
		{Type: LoadChange, LoadTonnes: ptr(20), Share: ptr(0.5)}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Changes[1].LegsAffected; n != 2 {
		t.Errorf("legs affected by the load change: %d, want 2", n)
	}
}

// A vehicle load below the shipment's own weight is refused; a load change
// that would set one leaves the shipment's load as it was, with a note.
func TestLoad_BelowShipmentWeight(t *testing.T) {
	c, _ := setup(t)
	s := calc.Shipment{ID: "s", WeightKg: 20000, Legs: []calc.Leg{{ID: "r", Mode: calc.Road, VehicleType: "hgv_artic_33t",
		DistanceKm: 500, DistanceType: calc.DistSFD}}}
	bad := s
	bad.Legs = []calc.Leg{s.Legs[0]}
	bad.Legs[0].LoadTonnes = 5
	if _, err := c.Calculate(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "less than the shipment's own") {
		t.Errorf("load 5 t for a 20 t shipment: %v", err)
	}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Plan{Changes: []Change{{Type: LoadChange, LoadTonnes: ptr(15)}}})
	if err != nil {
		t.Fatal(err)
	}
	if sr := res.Shipments[0]; sr.Error != "" || sr.DiffKg != 0 || len(sr.Notes) != 1 || res.Changes[0].LegsAffected != 0 {
		t.Errorf("want no change and a note: %+v", sr)
	}
}
