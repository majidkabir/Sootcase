package calc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var truck = LoadModel{
	Mode: Road, VehicleType: "truck", PayloadT: 20,
	Points: []LoadPoint{{0, 600, 100}, {1, 1000, 200}},
}

func TestLoadModel_PerTonneKm(t *testing.T) {
	cases := []struct {
		lf, etf, ttw, wtt float64
	}{
		{1, 0, 50, 10},      // 1000/20, 200/20
		{0.5, 0, 80, 15},    // (800)/10, (150)/10
		{0.5, 0.5, 110, 20}, // (800+0.5*600)/10, (150+0.5*100)/10
		{0.25, 1, 260, 45},  // (700+600)/5, (125+100)/5
	}
	for _, c := range cases {
		ttw, wtt, err := truck.PerTonneKm(c.lf, c.etf)
		if err != nil || !approx(ttw, c.ttw) || !approx(wtt, c.wtt) {
			t.Errorf("lf=%v etf=%v: got %v %v %v, want %v %v", c.lf, c.etf, ttw, wtt, err, c.ttw, c.wtt)
		}
	}
	for _, bad := range [][2]float64{{0, 0}, {1.1, 0}, {-0.5, 0}, {0.5, -1}} {
		if _, _, err := truck.PerTonneKm(bad[0], bad[1]); !errors.Is(err, ErrInvalidLoad) {
			t.Errorf("lf=%v etf=%v: want ErrInvalidLoad, got %v", bad[0], bad[1], err)
		}
	}
}

func TestLoadModel_PiecewiseThroughMidpoint(t *testing.T) {
	m := LoadModel{PayloadT: 10, Points: []LoadPoint{{0, 100, 0}, {0.5, 300, 0}, {1, 400, 0}}}
	if ttw, _, _ := m.PerTonneKm(0.5, 0); !approx(ttw, 60) { // 300 / 5
		t.Errorf("at 0.5 = %v, want 60", ttw)
	}
	if ttw, _, _ := m.PerTonneKm(0.75, 0); !approx(ttw, 350/7.5) {
		t.Errorf("at 0.75 = %v, want %v", ttw, 350/7.5)
	}
}

func TestLoadModel_Validate(t *testing.T) {
	bad := map[string]LoadModel{
		"no payload": {Points: truck.Points},
		"one point":  {PayloadT: 1, Points: truck.Points[:1]},
		"unsorted":   {PayloadT: 1, Points: []LoadPoint{{1, 1, 1}, {0, 1, 1}}},
		"no zero":    {PayloadT: 1, Points: []LoadPoint{{0.1, 1, 1}, {1, 1, 1}}},
		"duplicate":  {PayloadT: 1, Points: []LoadPoint{{0, 1, 1}, {0, 1, 1}, {1, 1, 1}}},
		"negative":   {PayloadT: 1, Points: []LoadPoint{{0, -1, 1}, {1, 1, 1}}},
	}
	for name, m := range bad {
		if m.Validate() == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if err := truck.Validate(); err != nil {
		t.Error(err)
	}
}

type withModels struct {
	fakeFactors
}

func (withModels) LoadModel(m Mode, vt string) (LoadModel, error) {
	if m == Road && vt == "truck" {
		return truck, nil
	}
	return LoadModel{}, errors.New("no model")
}

func TestCalculate_LoadModel(t *testing.T) {
	c := Calculator{Factors: withModels{testFactors}}
	ctx := context.Background()
	// 10 t x 100 km = 1000 tkm; lf 0.5 etf 0.5: 110 TTW + 20 WTT g/tkm
	r, err := c.Calculate(ctx, Shipment{WeightKg: 10_000, Legs: []Leg{
		{ID: "l", Mode: Road, VehicleType: "truck", DistanceKm: 100, LoadFactor: 0.5, EmptyTripFactor: 0.5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	l := r.Legs[0]
	if l.Method != MethodLoadModel || !approx(l.TTWKg, 110) || !approx(l.WTWKg, 130) ||
		l.LoadFactor != 0.5 || l.EmptyTripFactor != 0.5 || l.PayloadT != 20 || l.Factor == nil {
		t.Errorf("got %+v", l)
	}

	fu := FuelUse{Fuel: "diesel", Amount: 1}
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", DistanceKm: 1, LoadFactor: 0.5, Fuel: &fu}}}); err == nil {
		t.Error("fuel and lf: want error")
	}
	bad := map[string]Leg{
		"etf without lf":  {Mode: Road, VehicleType: "truck", DistanceKm: 1, EmptyTripFactor: 0.3},
		"lf out of range": {Mode: Road, VehicleType: "truck", DistanceKm: 1, LoadFactor: 2},
	}
	for name, leg := range bad {
		if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{leg}}); !errors.Is(err, ErrInvalidLoad) {
			t.Errorf("%s: want ErrInvalidLoad, got %v", name, err)
		}
	}
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "bike", DistanceKm: 1, LoadFactor: 0.5}}}); err == nil {
		t.Error("want error for unknown model")
	}
	plain := Calculator{Factors: testFactors}
	if _, err := plain.Calculate(ctx, Shipment{WeightKg: 1, Legs: []Leg{{Mode: Road, VehicleType: "truck", DistanceKm: 1, LoadFactor: 0.5}}}); !errors.Is(err, ErrInvalidLoad) {
		t.Errorf("want ErrInvalidLoad without models, got %v", err)
	}
}

func TestCalculate_LoadTonnes(t *testing.T) {
	c := Calculator{Factors: withModels{testFactors}}
	ctx := context.Background()
	calc := func(leg Leg) (LegResult, error) {
		leg.ID, leg.Mode, leg.VehicleType, leg.DistanceKm = "l", Road, "truck", 100
		r, err := c.Calculate(ctx, Shipment{WeightKg: 10_000, Legs: []Leg{leg}})
		if err != nil {
			return LegResult{}, err
		}
		return r.Legs[0], nil
	}
	// 10 t on a truck with a 20 t payload is load factor 0.5.
	byLF, err1 := calc(Leg{LoadFactor: 0.5, EmptyTripFactor: 0.5})
	byT, err2 := calc(Leg{LoadTonnes: 10, EmptyTripFactor: 0.5})
	if err1 != nil || err2 != nil || !approx(byT.WTWKg, byLF.WTWKg) || byT.LoadFactor != 0.5 || byT.LoadTonnes != 10 {
		t.Fatalf("tonnes vs factor: %v %v, %+v vs %+v", err1, err2, byT, byLF)
	}
	// The factor's source says what the load factor meant in tonnes.
	if !strings.Contains(byLF.Factor.Source, "load factor 0.5 = 10.0 t on the vehicle, a share of the model's 20.0 t payload") {
		t.Errorf("source: %s", byLF.Factor.Source)
	}
	// 30 t is beyond the 20 t payload: the model goes on along its last
	// segment: 600 + 1.5 × 400 = 1200 g/vkm TTW, 100 + 1.5 × 100 = 250 WTT,
	// per 30 t: 40 and 8.33 g/tkm; 10 t × 100 km: 40 kg TTW.
	over, err := calc(Leg{LoadTonnes: 30})
	if err != nil || !approx(over.TTWKg, 40) || !approx(over.WTWKg, 40+250.0/30) || over.LoadFactor != 1.5 {
		t.Errorf("30 t: %v %+v", err, over)
	}
	for name, leg := range map[string]Leg{
		"beyond 1.6 × payload": {LoadTonnes: 33},
		"negative":             {LoadTonnes: -1},
		"both":                 {LoadTonnes: 10, LoadFactor: 0.5},
	} {
		if _, err := calc(leg); !errors.Is(err, ErrInvalidLoad) {
			t.Errorf("%s: want ErrInvalidLoad, got %v", name, err)
		}
	}
	// A load factor above 1 is still refused: it is a share of the payload.
	if _, err := calc(Leg{LoadFactor: 1.2}); !errors.Is(err, ErrInvalidLoad) {
		t.Errorf("load factor 1.2: %v", err)
	}
}

func TestCalculate_Repositioning(t *testing.T) {
	c := Calculator{Factors: withModels{testFactors}}
	ctx := context.Background()
	calc := func(leg Leg) (LegResult, error) {
		leg.ID, leg.Mode, leg.VehicleType, leg.DistanceKm = "l", Road, "truck", 100
		r, err := c.Calculate(ctx, Shipment{WeightKg: 10_000, Legs: []Leg{leg}})
		if err != nil {
			return LegResult{}, err
		}
		return r.Legs[0], nil
	}
	// 50 km empty on a 100 km loaded trip is an empty trip factor of 0.5 on top.
	repo, err1 := calc(Leg{LoadTonnes: 10, RepositioningKm: 50})
	etf, err2 := calc(Leg{LoadTonnes: 10, EmptyTripFactor: 0.5})
	if err1 != nil || err2 != nil || !approx(repo.WTWKg, etf.WTWKg) || repo.EmptyTripFactor != 0.5 || repo.RepositioningKm != 50 ||
		!strings.Contains(repo.Factor.Source, "including 50 km repositioning") {
		t.Fatalf("repositioning: %v %v %+v vs %+v", err1, err2, repo, etf)
	}
	both, _ := calc(Leg{LoadTonnes: 10, EmptyTripFactor: 0.2, RepositioningKm: 30})
	if !approx(both.EmptyTripFactor, 0.5) {
		t.Errorf("etf 0.2 + 30/100 = %v", both.EmptyTripFactor)
	}
	// Without a load: the shipment is the truck's whole load (dedicated).
	ded, err := calc(Leg{RepositioningKm: 50})
	if err != nil || ded.LoadTonnes != 10 || !approx(ded.WTWKg, repo.WTWKg) {
		t.Errorf("dedicated: %v %+v", err, ded)
	}
	fu := FuelUse{Fuel: "diesel", Amount: 1}
	for name, leg := range map[string]Leg{
		"negative":  {RepositioningKm: -1},
		"with fuel": {RepositioningKm: 10, Fuel: &fu},
	} {
		if _, err := calc(leg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := c.Calculate(ctx, Shipment{WeightKg: 1000, Legs: []Leg{{Mode: Rail, VehicleType: "train", DistanceKm: 100, RepositioningKm: 10}}}); !errors.Is(err, ErrInvalidLoad) {
		t.Errorf("rail: want ErrInvalidLoad, got %v", err)
	}
}
