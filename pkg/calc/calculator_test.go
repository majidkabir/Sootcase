package calc

import (
	"context"
	"errors"
	"math"
	"testing"
)

type fakeFactors struct {
	legs  map[string]Factor
	hubs  map[string]HubFactor
	fuels map[string]FuelFactor
}

func (f fakeFactors) LegFactor(m Mode, vt string) (Factor, error) {
	if x, ok := f.legs[string(m)+"/"+vt]; ok {
		return x, nil
	}
	return Factor{}, errors.New("no factor")
}

func (f fakeFactors) HubFactor(t string) (HubFactor, error) {
	if x, ok := f.hubs[t]; ok {
		return x, nil
	}
	return HubFactor{}, errors.New("no hub factor")
}

func (f fakeFactors) FuelFactor(fuel string) (FuelFactor, error) {
	if x, ok := f.fuels[fuel]; ok {
		return x, nil
	}
	return FuelFactor{}, errors.New("no fuel factor")
}

type fixedDistance float64

func (d fixedDistance) Distance(context.Context, Mode, Location, Location) (float64, error) {
	return float64(d), nil
}

var testFactors = fakeFactors{
	legs: map[string]Factor{
		"road/truck_40t": {Mode: Road, VehicleType: "truck_40t", TTW: 60, WTT: 15},
		"sea/container":  {Mode: Sea, VehicleType: "container", TTW: 8, WTT: 2},
	},
	hubs:  map[string]HubFactor{"container_terminal": {HubType: "container_terminal", WTWPerTonneG: 1000}},
	fuels: map[string]FuelFactor{"diesel": {Fuel: "diesel", Unit: "l", TTW: 2.5, WTT: 0.5}},
}

// Fuel method golden tests (hand-computed).
func TestCalculate_Fuel(t *testing.T) {
	c := Calculator{Factors: testFactors}
	tests := []struct {
		name             string
		fuel             FuelUse
		wantTTW, wantWTW float64
		wantShare        float64
	}{
		// 10 t x 300 km = 3000 tkm, whole trip: 100 l x 2.5 = 250 TTW, x 3.0 = 300 WTW
		{"dedicated trip", FuelUse{Fuel: "diesel", Amount: 100}, 250, 300, 1},
		// shared trip of 12000 tkm -> share 0.25
		{"shared trip", FuelUse{Fuel: "diesel", Amount: 100, VehicleTonneKm: 12000}, 62.5, 75, 0.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fu := tt.fuel
			r, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{
				{ID: "l1", Mode: Road, DistanceKm: 300, Fuel: &fu},
			}})
			if err != nil {
				t.Fatal(err)
			}
			l := r.Legs[0]
			if l.Method != MethodFuel || !approx(l.TTWKg, tt.wantTTW) || !approx(l.WTWKg, tt.wantWTW) ||
				!approx(l.AllocationShare, tt.wantShare) || !approx(r.TonneKm, 3000) {
				t.Errorf("got %+v", l)
			}
		})
	}
}

func TestCalculate_FuelErrors(t *testing.T) {
	c := Calculator{Factors: testFactors}
	cases := map[string]FuelUse{
		"negative amount": {Fuel: "diesel", Amount: -1},
		"share above one": {Fuel: "diesel", Amount: 1, VehicleTonneKm: 10},
		"unknown fuel":    {Fuel: "unobtainium", Amount: 1},
	}
	for name, fu := range cases {
		fu := fu
		_, err := c.Calculate(context.Background(), Shipment{WeightKg: 1000, Legs: []Leg{
			{ID: "l", Mode: Road, DistanceKm: 100, Fuel: &fu},
		}})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < eps }

// Golden tests: hand-computed expected values. Add GLEC Framework worked
// examples here as they are transcribed.
func TestCalculate_Golden(t *testing.T) {
	tests := []struct {
		name                      string
		shipment                  Shipment
		wantTkm, wantTTW, wantWTW float64
		wantIntensity             float64
	}{
		{
			name: "single road leg: 10 t x 500 km",
			shipment: Shipment{ID: "s1", WeightKg: 10_000, Legs: []Leg{
				{ID: "l1", Mode: Road, VehicleType: "truck_40t", DistanceKm: 500},
			}},
			wantTkm: 5000, wantTTW: 300, wantWTW: 375, wantIntensity: 75,
		},
		{
			name: "road + sea + terminal: 20 t",
			shipment: Shipment{ID: "s2", WeightKg: 20_000,
				Legs: []Leg{
					{ID: "l1", Mode: Road, VehicleType: "truck_40t", DistanceKm: 100},
					{ID: "l2", Mode: Sea, VehicleType: "container", DistanceKm: 1000},
				},
				Hubs: []HubStop{{ID: "h1", HubType: "container_terminal"}},
			},
			// road: 2000 tkm -> TTW 120, WTW 150; sea: 20000 tkm -> TTW 160, WTW 200; hub: 20 kg
			wantTkm: 22000, wantTTW: 280, wantWTW: 370, wantIntensity: 350.0 * 1000 / 22000,
		},
	}
	c := Calculator{Factors: testFactors}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := c.Calculate(context.Background(), tt.shipment)
			if err != nil {
				t.Fatal(err)
			}
			if !approx(r.TonneKm, tt.wantTkm) || !approx(r.TTWKg, tt.wantTTW) ||
				!approx(r.WTWKg, tt.wantWTW) || !approx(r.IntensityG, tt.wantIntensity) {
				t.Errorf("got tkm=%v ttw=%v wtw=%v int=%v, want %v %v %v %v",
					r.TonneKm, r.TTWKg, r.WTWKg, r.IntensityG,
					tt.wantTkm, tt.wantTTW, tt.wantWTW, tt.wantIntensity)
			}
		})
	}
}

func TestCalculate_UsesDistanceProvider(t *testing.T) {
	c := Calculator{Factors: testFactors, Distances: fixedDistance(250)}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 1000, Legs: []Leg{
		{ID: "l1", Mode: Road, VehicleType: "truck_40t", From: Location{Lat: 55, Lon: 12}, To: Location{Lat: 53, Lon: 10}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !approx(r.Legs[0].DistanceKm, 250) {
		t.Errorf("distance = %v, want 250", r.Legs[0].DistanceKm)
	}
}

func TestCalculate_Errors(t *testing.T) {
	c := Calculator{Factors: testFactors}
	ctx := context.Background()
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 0, Legs: []Leg{{DistanceKm: 1}}}); !errors.Is(err, ErrInvalidWeight) {
		t.Errorf("want ErrInvalidWeight, got %v", err)
	}
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1}); !errors.Is(err, ErrNoLegs) {
		t.Errorf("want ErrNoLegs, got %v", err)
	}
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{ID: "x", Mode: Air, DistanceKm: 1}}}); err == nil {
		t.Error("want error for missing factor")
	}
	// Coordinates but no distance provider.
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{ID: "x", Mode: Road, VehicleType: "truck_40t",
		From: Location{Lat: 55, Lon: 12}, To: Location{Lat: 53, Lon: 10}}}}); err == nil {
		t.Error("want error for missing distance provider")
	}
}

func TestCalculate_InvalidInput(t *testing.T) {
	c := Calculator{Factors: testFactors, Distances: fixedDistance(100)}
	ctx := context.Background()
	nan, inf := math.NaN(), math.Inf(1)
	cph, ham := Location{Lat: 55.7, Lon: 12.6}, Location{Lat: 53.6, Lon: 10}
	leg := func(mod func(*Leg)) Leg {
		l := Leg{ID: "l", Mode: Road, VehicleType: "truck_40t", DistanceKm: 100}
		mod(&l)
		return l
	}
	cases := map[string]Shipment{
		"NaN weight":            {WeightKg: nan, Legs: []Leg{leg(func(*Leg) {})}},
		"Inf weight":            {WeightKg: inf, Legs: []Leg{leg(func(*Leg) {})}},
		"negative weight":       {WeightKg: -5, Legs: []Leg{leg(func(*Leg) {})}},
		"NaN teu":               {TEU: nan, TEUNetTonnes: 10, Legs: []Leg{leg(func(*Leg) {})}},
		"negative distance":     {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = -50 })}},
		"NaN distance":          {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = nan })}},
		"NaN load factor":       {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.LoadFactor = nan })}},
		"Inf energy":            {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.EnergyKWhPerTkm = inf; l.Country = "DNK" })}},
		"NaN fuel":              {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.Fuel = &FuelUse{Fuel: "diesel", Amount: nan} })}},
		"no distance or coords": {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = 0 })}},
		"only from":             {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = 0; l.From = cph })}},
		"lat 500":               {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = 0; l.From = Location{Lat: 500, Lon: 1}; l.To = ham })}},
		"lon -200":              {WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.From = Location{Lat: 1, Lon: -200} })}},
		"overflow":              {WeightKg: 1e308, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = 1e308 })}},
	}
	for name, s := range cases {
		if _, err := c.Calculate(ctx, s); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
	}
	// Coordinates without distance are fine.
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{leg(func(l *Leg) { l.DistanceKm = 0; l.From, l.To = cph, ham })}}); err != nil {
		t.Errorf("coordinates only: %v", err)
	}
}

// Property: more weight or more distance never means fewer emissions.
func FuzzCalculate_Monotonic(f *testing.F) {
	f.Add(1000.0, 100.0, 500.0, 50.0)
	c := Calculator{Factors: testFactors}
	f.Fuzz(func(t *testing.T, w, d, dw, dd float64) {
		if !(w > 0 && w < 1e7 && d > 0 && d < 1e5 && dw >= 0 && dw < 1e7 && dd >= 0 && dd < 1e5) {
			t.Skip()
		}
		run := func(w, d float64) float64 {
			r, err := c.Calculate(context.Background(), Shipment{WeightKg: w, Legs: []Leg{
				{ID: "l", Mode: Road, VehicleType: "truck_40t", DistanceKm: d},
			}})
			if err != nil {
				t.Fatal(err)
			}
			return r.WTWKg
		}
		if run(w+dw, d+dd) < run(w, d) {
			t.Errorf("emissions decreased: w=%v d=%v dw=%v dd=%v", w, d, dw, dd)
		}
	})
}

type withLoads struct {
	fakeFactors
	loads map[string]float64
}

func (w withLoads) ContainerLoad(name string) (float64, error) {
	if t, ok := w.loads[name]; ok {
		return t, nil
	}
	return 0, errors.New("unknown profile")
}

func TestCalculate_TEU(t *testing.T) {
	legs := []Leg{{ID: "l", Mode: Sea, VehicleType: "container", DistanceKm: 1000}}
	c := Calculator{Factors: withLoads{testFactors, map[string]float64{"average": 10}}}
	ctx := context.Background()

	// 2 TEU x 10 t = 20 t; 20 t x 1000 km x 10 g/tkm WTW = 200 kg
	for name, s := range map[string]Shipment{
		"profile":  {TEU: 2, ContainerLoad: "average", Legs: legs},
		"explicit": {TEU: 2, TEUNetTonnes: 10, Legs: legs},
	} {
		r, err := c.Calculate(ctx, s)
		if err != nil || r.WeightKg != 20000 || r.WeightBasis != "teu" || !approx(r.WTWKg, 200) {
			t.Errorf("%s: %+v, %v", name, r, err)
		}
	}
	if r, _ := c.Calculate(ctx, Shipment{WeightKg: 500, Legs: legs}); r.WeightBasis != "weight" || r.WeightKg != 500 {
		t.Errorf("weight basis = %q %v", r.WeightBasis, r.WeightKg)
	}

	bad := map[string]Shipment{
		"weight and teu":  {WeightKg: 1, TEU: 1, TEUNetTonnes: 10},
		"zero teu":        {TEU: 0, ContainerLoad: "average"},
		"both per-TEU":    {TEU: 1, TEUNetTonnes: 10, ContainerLoad: "average"},
		"unknown profile": {TEU: 1, ContainerLoad: "feathers"},
	}
	for name, s := range bad {
		s.Legs = legs
		if _, err := c.Calculate(ctx, s); !errors.Is(err, ErrInvalidTEU) {
			t.Errorf("%s: want ErrInvalidTEU, got %v", name, err)
		}
	}
	// A provider without profiles can't resolve container_load.
	plain := Calculator{Factors: testFactors}
	if _, err := plain.Calculate(ctx, Shipment{TEU: 1, ContainerLoad: "average", Legs: legs}); !errors.Is(err, ErrInvalidTEU) {
		t.Errorf("want ErrInvalidTEU without profiles, got %v", err)
	}
	// TEU alone: GLEC's average profile, when the dataset has one.
	if _, err := plain.Calculate(ctx, Shipment{TEU: 1, Legs: legs}); !errors.Is(err, ErrInvalidTEU) {
		t.Errorf("TEU alone without profiles: want ErrInvalidTEU, got %v", err)
	}
	r, err := c.Calculate(ctx, Shipment{TEU: 2, Legs: legs})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := c.Factors.(ContainerLoadProvider).ContainerLoad(DefaultContainerLoad)
	if r.WeightBasis != "teu" || r.ContainerLoad != DefaultContainerLoad || r.WeightKg != 2*want*1000 {
		t.Errorf("TEU alone: basis %q profile %q weight %v", r.WeightBasis, r.ContainerLoad, r.WeightKg)
	}
}

func TestCalculate_ReportFields(t *testing.T) {
	c := Calculator{Factors: testFactors}
	fu := FuelUse{Fuel: "diesel", Amount: 100} // 300 kg WTW (2.5 + 0.5 kg/l)
	// intensity leg: 10 t x 100 km = 1000 tkm x 75 g = 75 kg WTW (60 TTW, 15 WTT)
	// hub: 10 t x 1000 g/t = 10 kg WTW
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000,
		Legs: []Leg{
			{ID: "measured", Mode: Road, DistanceKm: 300, Fuel: &fu},
			{ID: "default", Mode: Road, VehicleType: "truck_40t", DistanceKm: 100},
		},
		Hubs: []HubStop{{ID: "h", HubType: "container_terminal"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, d := r.Legs[0], r.Legs[1]
	if m.DataType != DataPrimary || d.DataType != DataDefault || r.Hubs[0].DataType != DataDefault {
		t.Errorf("data types: %q %q %q", m.DataType, d.DataType, r.Hubs[0].DataType)
	}
	if !approx(m.WTTKg, 50) || !approx(d.WTTKg, 15) || !approx(d.IntensityG, 75) || !approx(m.IntensityG, 100) {
		t.Errorf("leg WTT/intensity: %+v / %+v", m, d)
	}
	// totals: TTW 250+60, WTW 300+75+10, WTT excludes the WTW-only hub
	if !approx(r.TTWKg, 310) || !approx(r.WTWKg, 385) || !approx(r.WTTKg, 65) {
		t.Errorf("totals TTW %v WTT %v WTW %v", r.TTWKg, r.WTTKg, r.WTWKg)
	}
	if !approx(r.PrimaryDataShare, 300.0/385) {
		t.Errorf("primary share = %v, want %v", r.PrimaryDataShare, 300.0/385)
	}
	if r.Hubs[0].HubType != "container_terminal" {
		t.Errorf("hub = %+v", r.Hubs[0])
	}
}

// Property: any input either errors or gives finite, non-negative results.
func FuzzCalculate_Finite(f *testing.F) {
	f.Add(1000.0, 100.0, 0.0, 0.0, 55.0, 12.0, 53.0, 10.0, 0.0, 0.0)
	f.Add(1e300, 1e300, 0.5, 0.2, 0.0, 0.0, 0.0, 0.0, 2.0, 1e6)
	c := Calculator{Factors: withModels{testFactors}, Distances: fixedDistance(100)}
	f.Fuzz(func(t *testing.T, w, d, lf, etf, lat1, lon1, lat2, lon2, teu, fuel float64) {
		s := Shipment{WeightKg: w, Legs: []Leg{{Mode: Road, VehicleType: "truck", DistanceKm: d, LoadFactor: lf,
			EmptyTripFactor: etf, From: Location{Lat: lat1, Lon: lon1}, To: Location{Lat: lat2, Lon: lon2}}}}
		if teu != 0 {
			s.WeightKg, s.TEU, s.TEUNetTonnes = 0, teu, 10
		}
		if fuel != 0 {
			s.Legs = append(s.Legs, Leg{Mode: Road, DistanceKm: d, Fuel: &FuelUse{Fuel: "diesel", Amount: fuel}})
		}
		r, err := c.Calculate(context.Background(), s)
		if err != nil {
			return
		}
		for _, v := range []float64{r.WeightKg, r.TonneKm, r.TTWKg, r.WTTKg, r.WTWKg, r.IntensityG, r.PrimaryDataShare} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < -1e-9 {
				t.Fatalf("bad result %v in %+v", v, r)
			}
		}
	})
}

// Own intensity (primary data): no DAF when its basis is the leg's distance;
// it must have a source, and TTW within WTW.
func TestCalculate_OwnFactor(t *testing.T) {
	c := Calculator{Factors: testFactors}
	leg := Leg{ID: "l1", Mode: Road, DistanceKm: 300, DistanceType: DistSFD, OwnFactor: &OwnFactor{WTW: 80, TTW: 64, Source: "Carrier X, 2025 TOC report"}}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{leg}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	// 3000 tkm × 80 g = 240 kg WTW, × 64 g = 192 kg TTW.
	if l.Method != MethodIntensity || l.DataType != DataPrimary || !approx(l.WTWKg, 240) || !approx(l.TTWKg, 192) ||
		l.Factor.Source != "Own value: Carrier X, 2025 TOC report" || l.Factor.VehicleType != "own" || l.DAF != 0 || r.PrimaryDataShare != 1 {
		t.Errorf("got %+v %+v", l, l.Factor)
	}
	for name, o := range map[string]OwnFactor{
		"no source":   {WTW: 80, TTW: 64},
		"ttw > wtw":   {WTW: 80, TTW: 90, Source: "x"},
		"zero wtw":    {Source: "x"},
		"bad basis":   {WTW: 80, Source: "x", DistanceBasis: "miles"},
		"negative tw": {WTW: 80, TTW: -1, Source: "x"},
	} {
		l := leg
		l.OwnFactor = &o
		if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{l}}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	l2 := leg
	l2.Fuel = &FuelUse{Fuel: "diesel", Amount: 10}
	if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{l2}}); err == nil {
		t.Error("own_factor with fuel accepted")
	}
}

// A fuel supplier's own emission factor replaces the dataset's.
func TestCalculate_OwnFuelFactor(t *testing.T) {
	c := Calculator{Factors: testFactors}
	fu := FuelUse{Fuel: "HVO from supplier Y", Amount: 100, Factor: &OwnFuelFactor{Unit: "l", WTW: 0.5, TTW: 0.03, Source: "Supplier Y, 2026", BiogenicCO2: 2.4}}
	r, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{{ID: "l1", Mode: Road, DistanceKm: 300, Fuel: &fu}}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	if l.Method != MethodFuel || l.DataType != DataPrimary || !approx(l.WTWKg, 50) || !approx(l.TTWKg, 3) || !approx(l.BiogenicCO2Kg, 240) ||
		l.FuelFactor.Source != "Own value: Supplier Y, 2026" || l.FuelFactor.Unit != "l" {
		t.Errorf("got %+v %+v", l, l.FuelFactor)
	}
	for name, o := range map[string]OwnFuelFactor{
		"no source": {Unit: "l", WTW: 1},
		"no unit":   {WTW: 1, Source: "x"},
		"ttw > wtw": {Unit: "l", WTW: 1, TTW: 2, Source: "x"},
		"zero wtw":  {Unit: "l", Source: "x"},
	} {
		f := fu
		f.Factor = &o
		if _, err := c.Calculate(context.Background(), Shipment{WeightKg: 10_000, Legs: []Leg{{ID: "l1", Mode: Road, DistanceKm: 300, Fuel: &f}}}); !errors.Is(err, ErrInvalidFuel) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
