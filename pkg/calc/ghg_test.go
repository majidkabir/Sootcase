package calc

import "testing"

func TestGHGScopes(t *testing.T) {
	r := Result{WTWKg: 160, Legs: []LegResult{
		{Method: MethodIntensity, TTWKg: 80, WTWKg: 100, Factor: &Factor{VehicleType: "hgv_x/diesel"}},
		{Method: MethodEnergy, TTWKg: 0, WTWKg: 20},
		{Method: MethodIntensity, TTWKg: 10, WTWKg: 30, Factor: &Factor{VehicleType: "rail/freight_train"},
			Parts: []LegPart{{Kind: PartElectrified, Method: MethodEnergy, TTWKg: 0, WTWKg: 12}}},
	}, Hubs: []HubResult{{WTWKg: 10}}}
	g := ghgScopes(r)
	// Scope 1: 80 + 10; scope 2: 20 + 12; cat 3: 20 + (30-12-10) = 28; hubs 10.
	if g.Scope3Kg != 160 || !approx(g.OperatorScope1Kg, 90) || !approx(g.OperatorScope2Kg, 32) || !approx(g.OperatorScope3Cat3Kg, 28) || g.HubsKg != 10 {
		t.Errorf("%+v", g)
	}
	if s := g.OperatorScope1Kg + g.OperatorScope2Kg + g.OperatorScope3Cat3Kg + g.HubsKg; !approx(s, 160) {
		t.Errorf("operator scopes add up to %v, want 160", s)
	}
	bev := ghgScopes(Result{Legs: []LegResult{{Method: MethodIntensity, TTWKg: 0, WTWKg: 5, Factor: &Factor{VehicleType: "vans/x/battery_electric_vehicle"}}}})
	if bev.OperatorScope2Kg != 5 || bev.OperatorScope1Kg != 0 {
		t.Errorf("battery electric: %+v", bev)
	}
	for id, want := range map[string]bool{
		"glec/road/europe/electric/artic_34_40t":                        true,
		"glec/road/cn/truck_12t/electricity":                            true,
		"glec/rail/eu/electric/container":                               true,
		"glec/rail/eu/temperature_controlled/electric/cars":             true,
		"vans/average_up_to_3_5_tonnes/plug_in_hybrid_electric_vehicle": false,
		"glec/rail/eu/diesel/container":                                 false,
		"glec/road/europe/artic_34_40t":                                 false,
	} {
		if got := electricFactor(id); got != want {
			t.Errorf("electricFactor(%q) = %v, want %v", id, got, want)
		}
	}
}
