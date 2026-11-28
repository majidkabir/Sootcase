package factors

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// GLEC v3.3 Module 6 Tier 1 on real data: fuel burnt = TTW ÷ the fuel's TTW
// per kg, times Module 6's factors per kg; what a value doesn't tie to a
// fuel is "not known".
func TestAirPollutants(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds}
	run := func(legs ...calc.Leg) calc.Result {
		t.Helper()
		r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10000, Legs: legs})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	near := func(what string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-9*math.Max(1, want) {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}

	// A GLEC European artic on diesel: 3.22 kg CO2e TTW per kg diesel
	// (Module 1), NOx 25.95 g/kg (Module 6 Table 8).
	r := run(calc.Leg{Mode: calc.Road, VehicleType: "glec/road/eu_sa/artic_34_40t/diesel", DistanceKm: 500, DistanceType: calc.DistActual})
	l := r.Legs[0]
	fuel := l.TTWKg / 3.22
	a := l.AirPollutants
	if a == nil || math.Abs(a.FuelKg-fuel) > 1e-9 || a.NotKnown != "" || !strings.Contains(a.Fuel, "heavy-duty") {
		t.Fatalf("artic: %+v (fuel %v)", a, fuel)
	}
	near("artic NOx", a.NOxKg, fuel*0.02595)
	near("artic PM2.5", a.PM25Kg, fuel*0.00055)
	near("artic SO2", a.SO2Kg, fuel*2*3e-6)
	near("total NOx", r.AirPollutants.NOxKg, fuel*0.02595)
	if len(r.AirPollutants.Partial) != 0 {
		t.Errorf("artic partial: %v", r.AirPollutants.Partial)
	}

	// Measured fuel: 100 l of GLEC European diesel on a van, shared by
	// weight with nothing else: 100 l × 0.83 kg/l (2.6726 / 3.22).
	r = run(calc.Leg{Mode: calc.Road, VehicleType: "glec/road/eu_sa/van_3_5t/diesel", DistanceKm: 300, DistanceType: calc.DistActual,
		Fuel: &calc.FuelUse{Fuel: "glec/fuel/eu/diesel/l", Amount: 100}})
	a = r.Legs[0].AirPollutants
	kg := 100 * 2.6726 / 3.22
	if a == nil || math.Abs(a.FuelKg-kg) > 1e-6 || !strings.Contains(a.Fuel, "light commercial") {
		t.Fatalf("van fuel: %+v, want %v kg", a, kg)
	}
	near("van NOx", a.NOxKg, kg*0.01348)

	// GLEC EU rail, traction unknown: its TTW is the diesel trains'.
	r = run(calc.Leg{Mode: calc.Rail, VehicleType: "glec/rail/eu/average_traction_unknown", DistanceKm: 600, DistanceType: calc.DistSFD})
	a = r.Legs[0].AirPollutants
	fuel = r.Legs[0].TTWKg / 3.22
	near("rail NOx", a.NOxKg, fuel*0.0524)
	near("rail BC", a.BCKg, fuel*0.0008905)

	// An electric truck: nothing burnt, so 0, not "not known".
	r = run(calc.Leg{Mode: calc.Road, VehicleType: "glec/road/europe/electric/rigid_7_5_12t", DistanceKm: 200, DistanceType: calc.DistActual})
	a = r.Legs[0].AirPollutants
	near("electric NOx", a.NOxKg, 0)
	if a.NotKnown != "" || len(r.AirPollutants.Partial) != 0 {
		t.Errorf("electric: %+v %+v", a, r.AirPollutants)
	}

	// A GLEC bulk ship on HFO: no PM2.5 factor, so PM2.5 is partial.
	r = run(calc.Leg{Mode: calc.Sea, VehicleType: "glec/sea/bulk_carrier/10000-34999_dwt/hfo", DistanceKm: 2000, DistanceType: calc.DistSFD})
	a = r.Legs[0].AirPollutants
	fuel = r.Legs[0].TTWKg / 3.16
	near("ship SO2", a.SO2Kg, fuel*0.0192)
	if a.PM25Kg != nil || strings.Join(r.AirPollutants.Partial, ",") != "pm2_5_kg" {
		t.Errorf("ship: %+v partial %v", a, r.AirPollutants.Partial)
	}

	// An air leg and a container trade lane: not known, with the reason.
	r = run(calc.Leg{Mode: calc.Air, VehicleType: "air_freighter", DistanceKm: 1000, DistanceType: calc.DistGCD},
		calc.Leg{Mode: calc.Road, VehicleType: "glec/road/eu_sa/artic_34_40t/diesel", DistanceKm: 100, DistanceType: calc.DistActual})
	if a := r.Legs[0].AirPollutants; a.NOxKg != nil || !strings.Contains(a.NotKnown, "jet fuel") {
		t.Errorf("air: %+v", a)
	}
	if tot := r.AirPollutants; len(tot.Partial) != 6 || len(tot.NotKnown) != 1 || tot.NOxKg == nil {
		t.Errorf("air + road totals: %+v", tot)
	}

	// A truck's load (DESNZ load model, diesel): DESNZ's diesel per tonne.
	r = run(calc.Leg{Mode: calc.Road, VehicleType: "hgv_non_refrigerated_all_diesel/articulated_33t", DistanceKm: 400, DistanceType: calc.DistActual, LoadTonnes: 20})
	a = r.Legs[0].AirPollutants
	dz, _ := ds.FuelFactor("liquid_fuels/diesel_average_biofuel_blend/tonnes")
	fuel = r.Legs[0].TTWKg / (dz.TTW / 1000)
	near("load model NOx", a.NOxKg, fuel*0.02595)

	// Measured DESNZ diesel in litres on a truck: litres to kg by GLEC's
	// European density.
	r = run(calc.Leg{Mode: calc.Road, VehicleType: "hgv_artic", DistanceKm: 400, DistanceType: calc.DistActual,
		Fuel: &calc.FuelUse{Fuel: "liquid_fuels/diesel_average_biofuel_blend/litres", Amount: 120}})
	a = r.Legs[0].AirPollutants
	near("DESNZ litres NOx", a.NOxKg, 120*(2.6726/3.22)*0.02595)

	// Measured fuel the factors aren't for (HVO): not known, why given.
	r = run(calc.Leg{Mode: calc.Road, VehicleType: "hgv_artic", DistanceKm: 400, DistanceType: calc.DistActual,
		Fuel: &calc.FuelUse{Fuel: "glec/fuel/eu/hvo/l", Amount: 120}})
	if a := r.Legs[0].AirPollutants; a.NOxKg != nil || !strings.Contains(a.NotKnown, "hvo") {
		t.Errorf("HVO: %+v", a)
	}
}
