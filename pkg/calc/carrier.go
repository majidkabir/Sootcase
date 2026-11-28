package calc

import "strings"

// Energy carriers named by a factor or fuel id (iLEAP v1.1.0's energy
// carrier types, 6.7.7): which fuel a value is for, when its id or text
// says so. The iLEAP export and the air pollutants both read them.
const (
	CarrierDiesel   = "Diesel"
	CarrierHVO      = "HVO"
	CarrierPetrol   = "Petrol"
	CarrierCNG      = "CNG"
	CarrierLNG      = "LNG"
	CarrierLPG      = "LPG"
	CarrierHFO      = "HFO"
	CarrierMGO      = "MGO"
	CarrierAviation = "Aviation fuel"
	CarrierHydrogen = "Hydrogen"
	CarrierMethanol = "Methanol"
	CarrierElectric = "Electric"
)

// VehicleCarrier names the iLEAP energy carrier of a vehicle factor id,
// "" when the id doesn't say (GLEC rail "traction unknown", container
// ships by trade lane, inland vessels, SmartWay rows, ...). Only what the
// id states is used: GLEC road rows end in their fuel, GLEC rail rows
// name diesel or electric traction, GLEC ship rows name their fuel, and
// air freight factors are for jet fuel.
func VehicleCarrier(id string, mode Mode) string {
	s := strings.ToLower(id)
	switch mode {
	case Road:
		return FuelCarrier(s)
	case Rail:
		switch {
		case !strings.HasPrefix(s, "glec/rail/"):
			return ""
		case strings.Contains(s, "/diesel/"):
			return CarrierDiesel
		case strings.Contains(s, "/electric/"):
			return CarrierElectric
		}
	case Sea:
		if strings.HasPrefix(s, "glec/sea/") {
			switch s[strings.LastIndex(s, "/")+1:] {
			case "hfo", "vlsfo":
				return CarrierHFO
			case "mdo":
				return CarrierMGO
			}
		}
	case Air:
		if strings.HasPrefix(s, "glec/air/") || strings.HasPrefix(s, "freight_flights/") {
			return CarrierAviation
		}
	}
	return ""
}

// FuelCarrier names the energy carrier of a fuel id (or a GLEC/DESNZ
// road vehicle id, which ends in its fuel), "" when the id doesn't say
// which one it is, names one iLEAP has no value for (FAME biodiesel,
// ethanol, natural gas for heating, ...), or several (dual fuel, plug-in
// hybrid). Only what the id states is used.
func FuelCarrier(id string) string {
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
		return CarrierElectric
	case has("hydrogen"):
		return CarrierHydrogen
	case has("methanol"):
		return CarrierMethanol
	case has("biodiesel", "ethanol", "biogas", "natural_gas", "ammonia", "biomass", "coal", "wood", "lubricant", "naphtha", "burning_oil", "waste_oil", "refinery", "coke"):
		return ""
	case has("bio_lng", "bio_cng", "biomethane"):
		// iLEAP's CarrierLNG and CarrierCNG are natural gas; it has no type for biomethane.
		return ""
	case has("diesel_hvo"): // diesel blended with some CarrierHVO is still diesel
		return CarrierDiesel
	case has("hvo"):
		return CarrierHVO
	case has("lng", "biomethane_liquified"):
		return CarrierLNG
	case has("cng", "biomethane_compressed"):
		return CarrierCNG
	case has("lpg", "propane", "butane"):
		return CarrierLPG
	case has("jet_kerosene", "aviation_turbine", "aviation_spirit", "avtur"):
		return CarrierAviation
	case has("mdo", "mgo", "marine_gas_oil"):
		return CarrierMGO
	case has("hfo", "vlsfo", "marine_fuel_oil", "residual_oil", "/fuel_oil/"):
		return CarrierHFO
	case has("diesel"):
		return CarrierDiesel
	case has("petrol", "gasoline"):
		return CarrierPetrol
	}
	return ""
}

// GLECFuelFor is the GLEC v3.3 Module 1 fuel factor for a carrier in a
// GLEC road region (from the vehicle id) or for ships and aircraft, ""
// when GLEC has none that fits.
func GLECFuelFor(carrierType, vehicleID string, mode Mode) string {
	switch mode {
	case Sea:
		// GLEC's Tables 14-17 name the fuel CarrierHFO, VLSFO or MDO; their WTT/TTW
		// splits are those of Module 1's CarrierHFO (HSHFO), CarrierHFO (VLSFO) and MDO/CarrierMGO
		// (VLSFO) rows (Module 1's "HFO Default" has the VLSFO numbers), so
		// those are the factors behind them (checked in TestCarrierSplits).
		if strings.HasPrefix(vehicleID, "glec/sea/") {
			switch vehicleID[strings.LastIndex(vehicleID, "/")+1:] {
			case "hfo":
				return "glec/fuel/marine/hfo_hshfo/kg"
			case "vlsfo":
				return "glec/fuel/marine/hfo_vlsfo/kg"
			case "mdo":
				return "glec/fuel/marine/mdo_mgo_vlsfo/kg"
			}
		}
		return ""
	case Air:
		if carrierType == CarrierAviation {
			return "glec/fuel/air/jet_kerosene/kg"
		}
		return ""
	case InlandWaterway:
		// GLEC's inland waterway values are mainly European data (Table 2).
		if carrierType == CarrierDiesel && strings.HasPrefix(vehicleID, "glec/iww/") {
			return "glec/fuel/eu/diesel/l"
		}
		return ""
	}
	region := ""
	// GLEC's Asia (except China and India) and Africa road values are its
	// Europe and South America values with an uplift (p. 106; each value's
	// source text says so), so their fuel is the European one.
	if strings.HasPrefix(vehicleID, "glec/road/asia_africa/") {
		vehicleID = strings.Replace(vehicleID, "/asia_africa/", "/eu_sa/", 1)
	}
	for _, r := range []string{"eu_sa", "na", "cn", "in"} {
		if strings.Contains(vehicleID, "/"+r+"/") || strings.HasPrefix(vehicleID, "glec/rail/eu/") && r == "eu_sa" {
			region = strings.TrimSuffix(r, "_sa")
			break
		}
	}
	if region == "" || !strings.HasPrefix(vehicleID, "glec/") {
		return ""
	}
	name := map[string]string{CarrierDiesel: "diesel/l", CarrierPetrol: "gasoline/l", CarrierCNG: "cng/kg", CarrierLNG: "lng/kg", CarrierLPG: "lpg/kg", CarrierHVO: "hvo/l"}[carrierType]
	if name == "" {
		return ""
	}
	if region == "in" && carrierType == CarrierPetrol {
		name = "petrol/l"
	}
	return "glec/fuel/" + region + "/" + name
}

// DESNZFuelFor is the UK DESNZ fuel factor behind a DESNZ freight factor's
// fuel, "" when there is none.
func DESNZFuelFor(carrierType string) string {
	return map[string]string{
		CarrierDiesel: "liquid_fuels/diesel_average_biofuel_blend/litres",
		CarrierPetrol: "liquid_fuels/petrol_average_biofuel_blend/litres",
		CarrierCNG:    "gaseous_fuels/cng/tonnes",
		CarrierLPG:    "gaseous_fuels/lpg/litres",
	}[carrierType]
}

// FactorCarrier is VehicleCarrier, plus what a GLEC value's own text
// says: GLEC's inland waterway rows (Table 2) and its US rail average are
// for diesel.
func FactorCarrier(f Factor) string {
	if c := VehicleCarrier(f.VehicleType, f.Mode); c != "" {
		return c
	}
	src, _, _ := strings.Cut(strings.ToLower(f.Source), "; wtw ") // without the note on GLEC's printed WTW
	switch {
	case !strings.HasPrefix(f.Source, "GLEC Framework") || strings.Contains(src, "electric"):
		return ""
	case f.Mode == InlandWaterway && strings.HasPrefix(f.VehicleType, "glec/iww/") && strings.HasSuffix(src, ", diesel"):
		return CarrierDiesel
	case f.Mode == Rail && strings.HasPrefix(f.VehicleType, "glec/rail/na/") && strings.Contains(src, "(diesel)"):
		return CarrierDiesel
	}
	return ""
}
