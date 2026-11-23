package calc

import (
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
