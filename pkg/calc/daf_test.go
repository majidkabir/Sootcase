package calc

import (
	"context"
	"errors"
	"testing"
)

// dafFactors: an air factor per great-circle tkm and a sea factor per
// actual tkm, with the GLEC default DAFs.
type dafFactors struct{ fakeFactors }

func (dafFactors) LegFactor(m Mode, vt string) (Factor, error) {
	switch m {
	case Air:
		return Factor{Mode: Air, VehicleType: vt, TTW: 1000, WTT: 200, DistanceBasis: DistGCD}, nil
	case Sea:
		return Factor{Mode: Sea, VehicleType: vt, TTW: 10, WTT: 2, DistanceBasis: DistActual}, nil
	}
	return Factor{Mode: m, VehicleType: vt, TTW: 50}, nil // no basis
}

func (dafFactors) DAF(m Mode, from, to string) (DAF, bool) {
	switch {
	case m == Air && from == DistActual && to == DistGCD:
		return DAF{Mode: Air, From: from, To: to, AddKm: -95, Source: "glec air"}, true
	case m == Sea && from == DistSFD && to == DistActual:
		return DAF{Mode: Sea, From: from, To: to, Multiplier: 1.15, Source: "glec sea"}, true
	}
	return DAF{}, false
}

// typedDistance returns 1000 km of the given type.
type typedDistance string

func (typedDistance) Distance(context.Context, Mode, Location, Location) (float64, error) {
	return 1000, nil
}
func (t typedDistance) DistanceType(Mode) string { return string(t) }

func TestDAF(t *testing.T) {
	ctx := context.Background()
	c := Calculator{Factors: dafFactors{testFactors}}
	one := func(l Leg) (LegResult, error) {
		r, err := c.Calculate(ctx, Shipment{WeightKg: 1000, Legs: []Leg{l}})
		if err != nil {
			return LegResult{}, err
		}
		return r.Legs[0], nil
	}

	// Actual flown 1095 km -> 1000 km GCD: 1 t x 1000 km x 1200 g/tkm = 1200 kg.
	l, err := one(Leg{ID: "a", Mode: Air, DistanceKm: 1095, DistanceType: DistActual})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(l.WTWKg, 1200) || !approx(l.TonneKm, 1095) || !approx(l.DAF, 1000.0/1095) || l.DAFSource != "glec air" || l.DistanceType != DistActual {
		t.Errorf("air actual: %+v", l)
	}

	// Already great-circle: no DAF.
	if l, _ := one(Leg{Mode: Air, DistanceKm: 1000, DistanceType: DistGCD}); l.DAF != 0 || !approx(l.WTWKg, 1200) {
		t.Errorf("air gcd: %+v", l)
	}
	// Type not given: no DAF (factor basis assumed).
	if l, _ := one(Leg{Mode: Air, DistanceKm: 1000}); l.DAF != 0 || !approx(l.WTWKg, 1200) {
		t.Errorf("air untyped: %+v", l)
	}
	// Factor without a basis: no DAF whatever the leg says.
	if l, _ := one(Leg{Mode: Road, DistanceKm: 100, DistanceType: DistActual}); l.DAF != 0 || !approx(l.WTWKg, 5) {
		t.Errorf("road no basis: %+v", l)
	}

	// Computed distance carries its type: sea network SFD -> factor per actual: x1.15.
	cs := Calculator{Factors: dafFactors{testFactors}, Distances: typedDistance(DistSFD)}
	r, err := cs.Calculate(ctx, Shipment{WeightKg: 1000, Legs: []Leg{{Mode: Sea, From: Location{Lat: 1, Lon: 1}, To: Location{Lat: 2, Lon: 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if l := r.Legs[0]; l.DistanceType != DistSFD || !approx(l.DAF, 1.15) || !approx(l.WTWKg, 12*1.15) || !approx(r.IntensityG, 12*1.15) {
		t.Errorf("sea sfd: %+v", l)
	}

	// Mismatch with no DAF to bridge it.
	if _, err := one(Leg{Mode: Sea, DistanceKm: 100, DistanceType: DistGCD}); !errors.Is(err, ErrNoDAF) {
		t.Errorf("sea gcd: want ErrNoDAF, got %v", err)
	}
	// DAF that would make the distance non-positive.
	if _, err := one(Leg{Mode: Air, DistanceKm: 50, DistanceType: DistActual}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("short flight: want ErrInvalidInput, got %v", err)
	}
	// Bad or misplaced distance_type.
	if _, err := one(Leg{Mode: Air, DistanceKm: 50, DistanceType: "odometer"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad type: %v", err)
	}
	cg := Calculator{Factors: dafFactors{testFactors}, Distances: typedDistance(DistGCD)}
	if _, err := cg.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{Mode: Air, DistanceType: DistActual,
		From: Location{Lat: 1, Lon: 1}, To: Location{Lat: 2, Lon: 2}}}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("distance_type without distance_km: %v", err)
	}
}

// A great circle stands in for no road, rail, sea or waterway distance,
// whatever the method and even when the factor states no distance basis.
// For air it is the distance to use.
func TestGreatCircleOnlyForAir(t *testing.T) {
	c := Calculator{Factors: dafFactors{testFactors}, Distances: typedDistance(DistGCD)}
	from, to := Location{Lat: 1, Lon: 1}, Location{Lat: 2, Lon: 2}
	ttw := 40.0
	for name, l := range map[string]Leg{
		"default value": {Mode: Road, VehicleType: "truck_40t"},
		"rail":          {Mode: Rail, VehicleType: "train"},
		"waterway":      {Mode: InlandWaterway, VehicleType: "barge"},
		"measured fuel": {Mode: Road, Fuel: &FuelUse{Fuel: "diesel", Amount: 10}},
		"own factor":    {Mode: Road, OwnFactor: &OwnFactor{WTW: 50, TTW: &ttw, Source: "carrier"}},
		"given as gcd":  {Mode: Road, VehicleType: "truck_40t", DistanceKm: 100, DistanceType: DistGCD},
	} {
		if l.DistanceKm == 0 {
			l.From, l.To = from, to
		}
		if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 1000, Legs: []Leg{l}}); !errors.Is(err, ErrNoDAF) {
			t.Errorf("%s: want ErrNoDAF, got %v", name, err)
		}
	}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 1000, Legs: []Leg{{Mode: Air, VehicleType: "freighter", From: from, To: to}}})
	if err != nil || r.Legs[0].DistanceType != DistGCD || r.Legs[0].DAF != 0 {
		t.Errorf("air on a great circle: %+v, %v", r.Legs, err)
	}
}

func TestDAF_Validate(t *testing.T) {
	good := DAF{Mode: Air, From: DistActual, To: DistGCD, AddKm: -95}
	if err := good.Validate(); err != nil || good.Apply(1095) != 1000 {
		t.Errorf("good DAF: %v", err)
	}
	for name, d := range map[string]DAF{
		"no mode":  {From: DistSFD, To: DistGCD},
		"same":     {Mode: Sea, From: DistSFD, To: DistSFD},
		"bad type": {Mode: Sea, From: "km", To: DistGCD},
		"neg mult": {Mode: Sea, From: DistSFD, To: DistGCD, Multiplier: -1},
		"empty to": {Mode: Sea, From: DistSFD},
	} {
		if d.Validate() == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
