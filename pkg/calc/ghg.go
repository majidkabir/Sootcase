package calc

import (
	"strings"
	"unicode"
)

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
//     electricity they use is scope 2 (location-based with a grid or
//     vehicle factor, market-based with a supplier's own factor; it
//     includes the upstream share the factor contains, which isn't split
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

// ElectricLeg says whether a leg (or the part of it left after its parts)
// runs on electricity: an energy leg, metered electricity, or a dataset's
// electric vehicle row.
func ElectricLeg(l LegResult) bool { return electricLeg(l) }

// electricLeg is ElectricLeg.
func electricLeg(l LegResult) bool {
	switch {
	case l.Method == MethodEnergy:
		return true
	case l.FuelFactor != nil && ElectricFuel(*l.FuelFactor):
		return true
	case l.Factor != nil && (l.Factor.DataType == "" || l.Factor.DataType == DataDefault) && electricFactor(l.Factor.VehicleType):
		return true // a dataset's electric row; an operator's TOC (any id) says its electricity share itself
	}
	return false
}

// ElectricFuel says whether a fuel factor is for electricity: nothing is
// burnt in the vehicle (TTW 0), and its name says electricity
// before it names another fuel (a dataset's "electricity/<country>";
// "Ökostrom", "Grøn el", "Renewable electricity", but not "Wasserstoff aus
// Ökostrom": hydrogen), or, naming neither, it is per kWh, MWh, GJ or MJ
// (a green tariff's).
func ElectricFuel(f FuelFactor) bool {
	if f.TTW != 0 || !(f.WTT >= 0) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(f.Fuel))
	if name == "electricity" || strings.HasPrefix(name, "electricity/") {
		return true
	}
	el, other := firstWord(name, electricWords), firstWord(name, otherFuelWords)
	switch {
	case el >= 0 && (other < 0 || el < other):
		return true
	case other >= 0:
		return false
	}
	switch strings.ToLower(f.Unit) {
	case "kwh", "mwh", "gwh", "gj", "mj":
		return true
	}
	return false
}

// Words naming electricity, and other fuels, in the site's languages
// ("el" only as a word of its own).
var (
	electricWords  = []string{"electric", "elektri", "elektry", "électri", "eléctri", "elettri", "strom", "strøm", "prąd", "stroom", " el "}
	otherFuelWords = []string{"hydrog", "wasserstoff", "brint", "wodór", "waterstof", "idrog", "idróg", "h2", "methan", "méthan", "metan",
		"gas", "lng", "cng", "lpg", "diesel", "petrol", "benzin", "hvo", "oil", "kerosene", "jet", "ammonia", "ethanol", "biodiesel"}
)

// firstWord is where in name the first of words starts, -1 if none.
func firstWord(name string, words []string) int {
	padded := " " + strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, name) + " "
	first := -1
	for _, w := range words {
		if i := strings.Index(padded, w); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	return first
}

// electricFactor says whether a factor id is for vehicles that run on
// electricity only: DESNZ battery electric vans, GLEC's electric road and
// rail rows (".../electric/...", ".../electricity"). Hybrids are not.
func electricFactor(id string) bool {
	for _, part := range strings.Split(id, "/") {
		switch part {
		case "electric", "electricity", "battery_electric_vehicle":
			return true
		}
	}
	return false
}

// electricityShare is the share of a leg's emissions (of what is left
// after its parts) that is electricity its vehicles use: all of it for an
// electric leg, the factor's stated part for a factor that mixes fuel and
// electric traction, else none.
func electricityShare(l LegResult) float64 {
	switch {
	case electricLeg(l):
		return 1
	case l.Method == MethodIntensity && l.Factor != nil && l.Factor.ElectricityG > 0 && l.Factor.WTW() > 0:
		return l.Factor.ElectricityG / l.Factor.WTW()
	}
	return 0
}

func ghgScopes(r Result) GHGScopes {
	g := GHGScopes{Scope3Kg: r.WTWKg}
	// add places emissions of which the share electricity is scope 2; the
	// rest is fuel: burnt (TTW) in scope 1, its supply in scope 3.
	add := func(electricity, ttw, wtw float64) {
		scope2 := wtw * electricity
		g.OperatorScope2Kg += scope2
		if electricity < 1 {
			g.OperatorScope1Kg += ttw
			g.OperatorScope3Cat3Kg += wtw - ttw - scope2
		}
	}
	for _, l := range r.Legs {
		ttw, wtw := l.TTWKg, l.WTWKg
		for _, p := range l.Parts {
			ttw, wtw = ttw-p.TTWKg, wtw-p.WTWKg
			electricity := 0.0
			if p.Kind == PartElectrified || p.Method == MethodEnergy {
				electricity = 1
			}
			add(electricity, p.TTWKg, p.WTWKg)
		}
		add(electricityShare(l), ttw, wtw)
	}
	for _, h := range r.Hubs {
		g.HubsKg += h.WTWKg
	}
	return g
}
