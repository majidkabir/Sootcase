package calc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRouter returns a fixed route with parts.
type fakeRouter Route

func (r fakeRouter) Distance(context.Context, Mode, Location, Location) (float64, error) {
	return r.Km, nil
}
func (r fakeRouter) Route(context.Context, Mode, Location, Location) (Route, error) {
	return Route(r), nil
}
func (fakeRouter) DistanceType(Mode) string { return DistSFD }

var cph, aar = Location{Lat: 55.68, Lon: 12.57}, Location{Lat: 56.15, Lon: 10.2}

var partFactors = withGrid{fakeFactors{
	legs: map[string]Factor{
		"road/truck":   {Mode: Road, VehicleType: "truck", TTW: 60, WTT: 15},
		"sea/ropax":    {Mode: Sea, VehicleType: "ropax", TTW: 300, WTT: 70, Source: "ferry src"},
		"rail/freight": {Mode: Rail, VehicleType: "freight", TTW: 20, WTT: 5},
	},
	fuels: map[string]FuelFactor{"diesel": {Fuel: "diesel", Unit: "l", TTW: 2.5, WTT: 0.5}},
}}

// Worked by hand: 10 t on a 100 km road route with 20 km by ferry.
//
//	road  80 km: 800 tkm x 60/15 g  = 48 / 12 kg
//	ferry 20 km: 200 tkm x 300/70 g = 60 / 14 kg
func TestCalculate_FerryOnRoad(t *testing.T) {
	c := Calculator{Factors: partFactors, Distances: fakeRouter{Km: 100, Parts: map[string]float64{PartFerry: 20}}}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000,
		Legs: []Leg{{Mode: Road, VehicleType: "truck", From: Location{Lat: 55, Lon: 12}, To: Location{Lat: 56, Lon: 11}}}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	if !approx(l.DistanceKm, 100) || !approx(l.TonneKm, 1000) || !approx(l.TTWKg, 108) || !approx(l.WTWKg, 134) ||
		!approx(l.WTTKg, 26) || len(l.Parts) != 1 || l.Method != MethodIntensity || l.Factor.VehicleType != "truck" {
		t.Fatalf("leg %+v", l)
	}
	p := l.Parts[0]
	if p.Kind != PartFerry || p.Mode != Sea || !approx(p.DistanceKm, 20) || !approx(p.WTWKg, 74) || !approx(p.TTWKg, 60) ||
		!approx(p.WTTKg, 14) || p.Factor.Source != "ferry src" || p.DataType != DataDefault {
		t.Errorf("ferry part %+v", p)
	}
	if !approx(r.WTWKg, 134) || !approx(r.TTWKg, 108) || l.RouteParts[PartFerry] != 20 {
		t.Errorf("result %+v", r)
	}

	// Measured truck fuel covers the road; the ferry is added on top.
	r, err = c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{{Mode: Road, VehicleType: "truck",
		Fuel: &FuelUse{Fuel: "diesel", Amount: 10}, From: cph, To: aar}}})
	if err != nil || !approx(r.WTWKg, 30+74) || !approx(r.PrimaryDataShare, 30.0/104) {
		t.Errorf("fuel + ferry: %v kg, share %v, %v", r.WTWKg, r.PrimaryDataShare, err)
	}

	// No ferry factor: a clear error.
	c.FerryVehicle = "hovercraft"
	if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", From: cph, To: aar}}}); err == nil || !strings.Contains(err.Error(), "ferry factor") {
		t.Errorf("want error for missing ferry factor, got %v", err)
	}
	// An all-ferry road route is refused.
	c = Calculator{Factors: partFactors, Distances: fakeRouter{Km: 20, Parts: map[string]float64{PartFerry: 20}}}
	if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", From: cph, To: aar}}}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "all ferry") {
		t.Errorf("all ferry: %v", err)
	}
}

// Worked by hand: 20 t on a 200 km rail route, 30 km of it tagged as not
// electrified, 10 km without a tag; 0.03 kWh/tkm on a 100 g/kWh grid.
//
//	electric 170 km: 3,400 tkm x 3 g (all WTT) = 10.2 kg
//	diesel    30 km:   600 tkm x 20/5 g        = 12 / 3 kg
func TestCalculate_RailElectrification(t *testing.T) {
	route := fakeRouter{Km: 200, Parts: map[string]float64{PartElectrified: 160, PartNotElectrified: 30, PartElectrificationUnknown: 10}}
	c := Calculator{Factors: partFactors, Distances: route}
	leg := Leg{Mode: Rail, VehicleType: "freight", EnergyKWhPerTkm: 0.03, Country: "DNK", From: cph, To: aar}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 20_000, Legs: []Leg{leg}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	if l.Method != MethodIntensity || !approx(l.TonneKm, 4000) || !approx(l.TTWKg, 12) || !approx(l.WTWKg, 25.2) ||
		len(l.Parts) != 1 || l.DataType != DataDefault {
		t.Fatalf("leg %+v", l)
	}
	p := l.Parts[0]
	if p.Kind != PartElectrified || !approx(p.DistanceKm, 170) || !approx(p.WTWKg, 10.2) || p.TTWKg != 0 ||
		p.Method != MethodEnergy || p.DataType != DataModelled || p.Country != "DNK" {
		t.Errorf("electric part %+v", p)
	}

	// Fully electrified (or unknown) track: the whole leg on electricity.
	c.Distances = fakeRouter{Km: 200, Parts: map[string]float64{PartElectrified: 200}}
	r, _ = c.Calculate(context.Background(), Shipment{WeightKg: 20_000, Legs: []Leg{leg}})
	if l := r.Legs[0]; l.Method != MethodEnergy || len(l.Parts) != 0 || !approx(l.WTWKg, 12) {
		t.Errorf("all electric %+v", l)
	}
	// No electrified track: the vehicle's own factor for all of it.
	c.Distances = fakeRouter{Km: 200, Parts: map[string]float64{PartNotElectrified: 200}}
	r, _ = c.Calculate(context.Background(), Shipment{WeightKg: 20_000, Legs: []Leg{leg}})
	if l := r.Legs[0]; l.Method != MethodIntensity || len(l.Parts) != 0 || !approx(l.WTWKg, 100) {
		t.Errorf("all diesel %+v", l)
	}
	// Without energy input, electrification is reported but not used.
	c.Distances = route
	r, _ = c.Calculate(context.Background(), Shipment{WeightKg: 20_000, Legs: []Leg{{Mode: Rail, VehicleType: "freight", From: cph, To: aar}}})
	if l := r.Legs[0]; len(l.Parts) != 0 || l.RouteParts[PartElectrified] != 160 || !approx(l.WTWKg, 100) {
		t.Errorf("info only %+v", l)
	}
}

func TestCalculate_BadRouteParts(t *testing.T) {
	c := Calculator{Factors: partFactors, Distances: fakeRouter{Km: 10, Parts: map[string]float64{PartFerry: 11}}}
	if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", From: cph, To: aar}}}); err == nil || !strings.Contains(err.Error(), "route part") {
		t.Errorf("want error for a part longer than the route, got %v", err)
	}
}

func TestCalculate_CountryKm(t *testing.T) {
	ck := map[string]float64{"DK": 50, "DE": 30}
	c := Calculator{Factors: partFactors, Distances: fakeRouter{Km: 100, Parts: map[string]float64{PartFerry: 20}, CountryKm: ck}}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", From: cph, To: aar}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Legs[0].CountryKm; got["DK"] != 50 || got["DE"] != 30 || len(got) != 2 {
		t.Errorf("CountryKm = %v, want %v", got, ck)
	}
	// More km by country than the route has is the router's error.
	c.Distances = fakeRouter{Km: 10, CountryKm: map[string]float64{"DK": 11}}
	if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", From: cph, To: aar}}}); err == nil || !strings.Contains(err.Error(), "by country") {
		t.Errorf("want error for country km over the route, got %v", err)
	}
	if WantCountryKm(context.Background()) || !WantCountryKm(WithCountryKm(context.Background())) {
		t.Error("WithCountryKm / WantCountryKm")
	}
}

// Region km can't add up to more than their countries' km.
func TestKmWithin(t *testing.T) {
	if _, err := kmWithin(map[string]float64{"BE-VLG": 60, "BE-WAL": 50}, 100, "region", "km by country"); err == nil {
		t.Error("110 km of regions in 100 km accepted")
	}
	if _, err := kmWithin(map[string]float64{"DK": -1}, 100, "country", "route"); err == nil {
		t.Error("negative km accepted")
	}
	if sum, err := kmWithin(map[string]float64{"DK": 60.00004, "DE": 40}, 100, "country", "route"); err != nil || sum != 100.00004 {
		t.Errorf("within rounding: %v, %v", sum, err)
	}
}
