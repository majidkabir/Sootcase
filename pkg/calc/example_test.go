package calc_test

import (
	"context"
	"fmt"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Calculate a two-leg shipment with the built-in UK DESNZ factors.
func Example() {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		panic(err)
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances()}

	res, err := c.Calculate(context.Background(), calc.Shipment{
		ID:       "PO-4711",
		WeightKg: 12_000,
		Legs: []calc.Leg{
			{ID: "truck", Mode: calc.Road, VehicleType: "hgv", DistanceKm: 290},
			{ID: "ship", Mode: calc.Sea, VehicleType: "container_ship", DistanceKm: 560},
		},
	})
	if err != nil {
		panic(err)
	}
	for _, l := range res.Legs {
		fmt.Printf("%s: %.1f kg CO2e (%s, %s data)\n", l.LegID, l.WTWKg, l.Method, l.DataType)
	}
	fmt.Printf("total: %.1f kg CO2e WTW, %.1f g/tkm\n", res.WTWKg, res.IntensityG)
	// Output:
	// truck: 435.0 kg CO2e (intensity, default data)
	// ship: 49.3 kg CO2e (intensity, default data)
	// total: 484.3 kg CO2e WTW, 47.5 g/tkm
}

// A leg with its own load factor and empty running uses the load model.
func Example_loadModel() {
	ds, _ := factors.Open(factors.Default)
	c := calc.Calculator{Factors: ds}
	for _, lf := range []float64{1, 0.5} {
		res, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10_000, Legs: []calc.Leg{
			{Mode: calc.Road, VehicleType: "hgv_artic_33t", DistanceKm: 100, LoadFactor: lf, EmptyTripFactor: 0.25},
		}})
		if err != nil {
			panic(err)
		}
		fmt.Printf("load %.0f%%: %.1f g CO2e/tkm\n", lf*100, res.IntensityG)
	}
	// Output:
	// load 100%: 85.7 g CO2e/tkm
	// load 50%: 141.9 g CO2e/tkm
}

// Measured fuel on a shared truck is allocated by tonne-km.
func Example_fuel() {
	ds, _ := factors.Open(factors.Default)
	c := calc.Calculator{Factors: ds}
	res, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 4_000, Legs: []calc.Leg{{
		Mode: calc.Road, DistanceKm: 400,
		Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 120, VehicleTonneKm: 8_000},
	}}})
	if err != nil {
		panic(err)
	}
	l := res.Legs[0]
	fmt.Printf("share %.0f%%, %.1f kg CO2e WTW, %s data\n", l.AllocationShare*100, l.WTWKg, l.DataType)
	// Output:
	// share 20%, 76.7 kg CO2e WTW, primary data
}
