package ileap

import (
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// iLEAP energy carrier types (6.7.7) and feedstocks (6.7.8).
const (
	Diesel   = "Diesel"
	HVO      = "HVO"
	Petrol   = "Petrol"
	CNG      = "CNG"
	LNG      = "LNG"
	LPG      = "LPG"
	HFO      = "HFO"
	MGO      = "MGO"
	Aviation = "Aviation fuel"
	Hydrogen = "Hydrogen"
	Methanol = "Methanol"
	Electric = "Electric"

	FeedstockGrid = "Grid"
)

// vehicleCarrier names the iLEAP energy carrier of a vehicle factor id,
// "" when the id doesn't say (GLEC rail "traction unknown", container
// ships by trade lane, inland vessels, SmartWay rows, ...). Only what the
// id states is used: GLEC road rows end in their fuel, GLEC rail rows
// name diesel or electric traction, GLEC ship rows name their fuel, and
// air freight factors are for jet fuel.
func vehicleCarrier(id string, mode calc.Mode) string {
	s := strings.ToLower(id)
	switch mode {
	case calc.Road:
		return carrier(s)
	case calc.Rail:
		switch {
		case !strings.HasPrefix(s, "glec/rail/"):
			return ""
		case strings.Contains(s, "/diesel/"):
			return Diesel
		case strings.Contains(s, "/electric/"):
			return Electric
		}
	case calc.Sea:
		if strings.HasPrefix(s, "glec/sea/") {
			switch s[strings.LastIndex(s, "/")+1:] {
			case "hfo", "vlsfo":
				return HFO
			case "mdo":
				return MGO
			}
		}
	case calc.Air:
		if strings.HasPrefix(s, "glec/air/") || strings.HasPrefix(s, "freight_flights/") {
			return Aviation
		}
	}
	return ""
}

// carrier names the iLEAP energy carrier of a fuel id (or a GLEC/DESNZ
// road vehicle id, which ends in its fuel), "" when the id doesn't say
// which one it is, names one iLEAP has no value for (FAME biodiesel,
// ethanol, natural gas for heating, ...), or several (dual fuel, plug-in
// hybrid). Only what the id states is used.
func carrier(id string) string {
	s := strings.ToLower(id)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("hybrid", "cng_diesel", "lng_diesel", "dual", "unknown"):
		return ""
	case strings.HasPrefix(s, "electricity"), has("battery_electric", "/electric", "/electricity"):
		return Electric
	case has("hydrogen"):
		return Hydrogen
	case has("methanol"):
		return Methanol
	case has("biodiesel", "ethanol", "biogas", "natural_gas", "ammonia", "biomass", "coal", "wood", "lubricant", "naphtha", "burning_oil", "waste_oil", "refinery", "coke"):
		return ""
	case has("diesel_hvo"): // diesel blended with some HVO is still diesel
		return Diesel
	case has("hvo"):
		return HVO
	case has("lng", "biomethane_liquified"):
		return LNG
	case has("cng", "biomethane_compressed"):
		return CNG
	case has("lpg", "propane", "butane"):
		return LPG
	case has("jet_kerosene", "aviation_turbine", "aviation_spirit", "avtur"):
		return Aviation
	case has("mdo", "mgo", "marine_gas_oil"):
		return MGO
	case has("hfo", "vlsfo", "marine_fuel_oil", "residual_oil", "/fuel_oil/"):
		return HFO
	case has("diesel"):
		return Diesel
	case has("petrol", "gasoline"):
		return Petrol
	}
	return ""
}

// unit converts a fuel factor unit to an iLEAP energy consumption unit:
// the iLEAP unit, and what to multiply an amount in the factor's unit by
// to get it in the iLEAP unit. ok is false for units iLEAP has none for.
func unit(u string) (string, float64, bool) {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "l", "litre", "litres", "liter", "liters":
		return "l", 1, true
	case "kg":
		return "kg", 1, true
	case "t", "tonne", "tonnes":
		return "kg", 1000, true
	case "kwh", "kwh (net cv)", "kwh (gross cv)":
		return "kWh", 1, true
	case "mj":
		return "MJ", 1, true
	case "gj":
		return "MJ", 1000, true
	}
	return "", 0, false
}

// glecFuel is the GLEC v3.3 Module 1 fuel factor for a carrier in a
// GLEC road region (from the vehicle id) or for ships and aircraft, ""
// when GLEC has none that fits.
func glecFuel(carrierType, vehicleID string, mode calc.Mode) string {
	switch mode {
	case calc.Sea:
		switch carrierType {
		case HFO:
			return "glec/fuel/marine/hfo/kg"
		case MGO:
			return "glec/fuel/marine/mdo_mgo/kg"
		case LNG:
			return "glec/fuel/marine/lng/kg"
		}
		return ""
	case calc.Air:
		if carrierType == Aviation {
			return "glec/fuel/air/jet_kerosene/kg"
		}
		return ""
	}
	region := ""
	for _, r := range []string{"eu_sa", "na", "cn", "in"} {
		if strings.Contains(vehicleID, "/"+r+"/") || strings.HasPrefix(vehicleID, "glec/rail/eu/") && r == "eu_sa" {
			region = strings.TrimSuffix(r, "_sa")
			break
		}
	}
	if region == "" || !strings.HasPrefix(vehicleID, "glec/") {
		return ""
	}
	name := map[string]string{Diesel: "diesel/l", Petrol: "gasoline/l", CNG: "cng/kg", LNG: "lng/kg", LPG: "lpg/kg", HVO: "hvo/l"}[carrierType]
	if name == "" {
		return ""
	}
	if region == "in" && carrierType == Petrol {
		name = "petrol/l"
	}
	return "glec/fuel/" + region + "/" + name
}

// desnzFuel is the UK DESNZ fuel factor behind a DESNZ freight factor's
// fuel, "" when there is none.
func desnzFuel(carrierType string) string {
	return map[string]string{
		Diesel: "liquid_fuels/diesel_average_biofuel_blend/litres",
		Petrol: "liquid_fuels/petrol_average_biofuel_blend/litres",
		CNG:    "gaseous_fuels/cng/tonnes",
		LPG:    "gaseous_fuels/lpg/litres",
	}[carrierType]
}

// carrierFromFuel makes an EnergyCarrier from a fuel factor, with the
// factor's emission factors in an iLEAP unit. perActivity, when > 0, is
// the amount of fuel (in the factor's unit) used per unit of activity.
func carrierFromFuel(f calc.FuelFactor, perActivity float64) (EnergyCarrier, string) {
	c := carrier(f.Fuel)
	if c == "" {
		return EnergyCarrier{}, "iLEAP has no energy carrier type for fuel " + f.Fuel
	}
	u, k, ok := unit(f.Unit)
	if !ok {
		return EnergyCarrier{}, "iLEAP has no energy consumption unit for " + f.Unit + " (" + f.Fuel + ")"
	}
	ec := EnergyCarrier{
		EnergyCarrier:         c,
		EnergyConsumptionUnit: u,
		EmissionFactorWTW:     dec(f.WTW() / k),
		EmissionFactorTTW:     dec(f.TTW / k),
	}
	if perActivity > 0 {
		ec.EnergyConsumption = dec(perActivity * k)
	}
	if c == Electric {
		fs := Feedstock{Feedstock: FeedstockGrid}
		if rest, ok := strings.CutPrefix(f.Fuel, "electricity/"); ok && rest != "" {
			fs.RegionProvenance = strings.ToUpper(rest)
		}
		ec.Feedstocks = []Feedstock{fs}
	}
	return ec, ""
}
