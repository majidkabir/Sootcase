package calc

import "strings"

// GHGScopes places a result's emissions in the GHG Protocol's scopes, from
// two points of view (GHG Protocol Corporate Value Chain (Scope 3)
// Standard; Technical Guidance for Calculating Scope 3 Emissions, chapters
// 4 and 9):
//
//   - a shipper buying the transport: all of it, transport and hubs, is
//     scope 3, category 4 (upstream transportation and distribution:
//     transport the company pays for, inbound or outbound) or category 9
//     (downstream: sold products moved by transport it doesn't pay for);
//   - the operator running the vehicles: fuel burnt in them is scope 1,
//     electricity they use is scope 2 (location-based; it includes the
//     upstream share the grid or vehicle factor contains, which isn't split
//     off), and producing and delivering the fuel is scope 3 category 3
//     (fuel- and energy-related activities).
//
// Hub operations are given apart: their factors are well-to-wheel only, so
// they can't be split by scope for the operator.
type GHGScopes struct {
	Scope3Kg             float64 `json:"scope3_shipper_kg"`
	OperatorScope1Kg     float64 `json:"operator_scope1_kg"`
	OperatorScope2Kg     float64 `json:"operator_scope2_kg"`
	OperatorScope3Cat3Kg float64 `json:"operator_scope3_cat3_kg"`
	HubsKg               float64 `json:"hubs_kg,omitempty"`
}

// electricLeg says whether a leg (or the part of it left after its parts)
// runs on electricity.
func electricLeg(l LegResult) bool {
	switch {
	case l.Method == MethodEnergy:
		return true
	case l.FuelFactor != nil && strings.HasPrefix(l.FuelFactor.Fuel, "electricity"):
		return true
	case l.Factor != nil && strings.Contains(l.Factor.VehicleType, "battery_electric"):
		return true
	}
	return false
}

func ghgScopes(r Result) GHGScopes {
	g := GHGScopes{Scope3Kg: r.WTWKg}
	add := func(electric bool, ttw, wtw float64) {
		if electric {
			g.OperatorScope2Kg += wtw
			return
		}
		g.OperatorScope1Kg += ttw
		g.OperatorScope3Cat3Kg += wtw - ttw
	}
	for _, l := range r.Legs {
		ttw, wtw := l.TTWKg, l.WTWKg
		for _, p := range l.Parts {
			ttw, wtw = ttw-p.TTWKg, wtw-p.WTWKg
			add(p.Kind == PartElectrified || p.Method == MethodEnergy, p.TTWKg, p.WTWKg)
		}
		add(electricLeg(l), ttw, wtw)
	}
	for _, h := range r.Hubs {
		g.HubsKg += h.WTWKg
	}
	return g
}
