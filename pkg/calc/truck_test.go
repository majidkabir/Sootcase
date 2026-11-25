package calc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

func TestVehicleCheck(t *testing.T) {
	bad := map[string]Vehicle{
		"height_m":        {HeightM: fp(9)},
		"width_m":         {WidthM: fp(1)},
		"length_m":        {LengthM: fp(31)},
		"gross_weight_kg": {GrossWeightKg: fp(70000)},
		"axle_load_kg":    {AxleLoadKg: fp(500)},
		"axles":           {Axles: new(int)},
		"trailer":         {Trailer: "dolly"},
		"tunnel_code":     {TunnelCode: "A"},
	}
	for field, v := range bad {
		err := v.check()
		if !errors.Is(err, ErrInvalidVehicle) || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: %v; want ErrInvalidVehicle naming it", field, err)
		}
	}
	no := false
	if err := (Vehicle{TunnelCode: "D", Hazmat: &no}).check(); err == nil {
		t.Error("tunnel code with hazmat false accepted")
	}
	five := 5
	ok := Vehicle{HeightM: fp(4), WidthM: fp(2.55), LengthM: fp(16.5), GrossWeightKg: fp(40000), AxleLoadKg: fp(11500), Axles: &five, Trailer: "semi", TunnelCode: "D"}
	if err := ok.check(); err != nil {
		t.Errorf("a standard truck: %v", err)
	}
}

func TestVehicleExclude(t *testing.T) {
	yes := true
	for _, c := range []struct {
		v    Vehicle
		want []string
		code string
	}{
		{Vehicle{}, []string{"truck"}, ""},
		{Vehicle{HeightM: fp(3.5), GrossWeightKg: fp(12000)}, []string{"truck"}, ""},
		{Vehicle{TunnelCode: "C"}, []string{"truck", "hzb"}, "B"},
		{Vehicle{TunnelCode: "D"}, []string{"truck", "hzd"}, "D"},
		{Vehicle{Hazmat: &yes}, []string{"truck", "hze"}, "E"},
	} {
		ex, lim, err := c.v.Exclude()
		if err != nil || !reflect.DeepEqual(ex, c.want) || lim.TunnelCode != c.code || lim.HeightM != 4 || lim.GrossWeightKg != 40000 {
			t.Errorf("%+v: %v %+v %v; want %v", c.v, ex, lim, err, c.want)
		}
	}
	for _, v := range []Vehicle{{HeightM: fp(4.01)}, {WidthM: fp(2.7)}, {LengthM: fp(20)}, {GrossWeightKg: fp(44000)}, {AxleLoadKg: fp(12000)}} {
		if _, _, err := v.Exclude(); !errors.Is(err, ErrNoTruckRoute) {
			t.Errorf("%+v: %v; want ErrNoTruckRoute", v, err)
		}
	}
}

func TestValidateVehicleOnRoad(t *testing.T) {
	s := Shipment{WeightKg: 1000, Legs: []Leg{{ID: "sea", Mode: Sea, DistanceKm: 100, Vehicle: &Vehicle{}}}}
	if err := validate(s); !errors.Is(err, ErrInvalidVehicle) {
		t.Errorf("vehicle on a sea leg: %v", err)
	}
	s.Legs[0] = Leg{ID: "road", Mode: Road, DistanceKm: 100, Vehicle: &Vehicle{HeightM: fp(9)}}
	if err := validate(s); !errors.Is(err, ErrInvalidVehicle) || !strings.Contains(err.Error(), `leg "road"`) {
		t.Errorf("height 9: %v", err)
	}
}

// seeVehicle routes every road leg 100 km and keeps the vehicle it was asked to route for.
type seeVehicle struct{ got **Vehicle }

func (s seeVehicle) Route(ctx context.Context, m Mode, a, b Location) (Route, error) {
	*s.got = VehicleOf(ctx)
	return Route{Km: 100}, nil
}

func (s seeVehicle) Distance(ctx context.Context, m Mode, a, b Location) (float64, error) {
	rt, err := s.Route(ctx, m, a, b)
	return rt.Km, err
}

// An empty vehicle routes exactly like no vehicle: no exclusions, no restrictions in the result.
func TestEmptyVehicleIsNone(t *testing.T) {
	var got *Vehicle
	c := Calculator{Factors: testFactors, Distances: seeVehicle{&got}}
	leg := Leg{ID: "road", Mode: Road, VehicleType: "truck_40t", From: Location{Lat: 55.68, Lon: 12.57}, To: Location{Lat: 53.55, Lon: 10.0}}
	plain, err := c.Calculate(context.Background(), Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{leg}})
	if err != nil {
		t.Fatal(err)
	}
	empty := &Vehicle{}
	leg.Vehicle = empty
	s := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{leg}}
	res, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatalf("vehicle {}: %v", err)
	}
	if got != nil {
		t.Errorf("vehicle {} was routed as a truck: %+v", got)
	}
	if res.Legs[0].Restrictions != nil || res.WTWKg != plain.WTWKg {
		t.Errorf("vehicle {}: restrictions %+v, %v kg; want none, %v kg", res.Legs[0].Restrictions, res.WTWKg, plain.WTWKg)
	}
	if s.Legs[0].Vehicle != empty {
		t.Error("the caller's leg was changed")
	}
	// A vehicle with a field is still a truck.
	s.Legs[0].Vehicle = &Vehicle{HeightM: fp(4)}
	if _, err := c.Calculate(context.Background(), s); err != nil || got == nil {
		t.Errorf("height 4: routed for %+v, %v", got, err)
	}
	// {} on a sea leg is no vehicle either, so not refused.
	sea := Shipment{ID: "s", WeightKg: 1000, Legs: []Leg{{ID: "sea", Mode: Sea, VehicleType: "container", DistanceKm: 100, Vehicle: &Vehicle{}}}}
	if _, err := c.Calculate(context.Background(), sea); err != nil {
		t.Errorf("vehicle {} on a sea leg: %v", err)
	}
}

// A vehicle on a leg with a given distance isn't applied, and the leg says so.
func TestVehicleWithGivenDistance(t *testing.T) {
	c := Calculator{Factors: testFactors}
	res, err := c.Calculate(context.Background(), Shipment{ID: "v", WeightKg: 10000, Legs: []Leg{
		{Mode: Road, VehicleType: "truck_40t", DistanceKm: 100, Vehicle: &Vehicle{HeightM: fp(4)}}}})
	if err != nil {
		t.Fatal(err)
	}
	l := res.Legs[0]
	if l.Restrictions != nil || !strings.Contains(l.Note, "vehicle isn't applied") {
		t.Errorf("restrictions %+v, note %q", l.Restrictions, l.Note)
	}
}
