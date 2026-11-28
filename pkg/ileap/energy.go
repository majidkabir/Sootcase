package ileap

import (
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// iLEAP energy carrier types (6.7.7) and feedstocks (6.7.8).
const (
	Diesel   = calc.CarrierDiesel
	HVO      = calc.CarrierHVO
	Petrol   = calc.CarrierPetrol
	CNG      = calc.CarrierCNG
	LNG      = calc.CarrierLNG
	LPG      = calc.CarrierLPG
	HFO      = calc.CarrierHFO
	MGO      = calc.CarrierMGO
	Aviation = calc.CarrierAviation
	Hydrogen = calc.CarrierHydrogen
	Methanol = calc.CarrierMethanol
	Electric = calc.CarrierElectric

	FeedstockGrid = "Grid"
)

// The carrier and fuel rules live in calc (the air pollutants use them too).
func vehicleCarrier(id string, mode calc.Mode) string { return calc.VehicleCarrier(id, mode) }
func carrier(id string) string                        { return calc.FuelCarrier(id) }
func glecFuel(c, vehicleID string, mode calc.Mode) string {
	return calc.GLECFuelFor(c, vehicleID, mode)
}
func desnzFuel(c string) string          { return calc.DESNZFuelFor(c) }
func factorCarrier(f calc.Factor) string { return calc.FactorCarrier(f) }

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
	case "mwh":
		return "kWh", 1000, true
	case "gwh":
		return "kWh", 1e6, true
	case "mj":
		return "MJ", 1, true
	case "gj":
		return "MJ", 1000, true
	}
	return "", 0, false
}

// carrierFromFuel makes an EnergyCarrier from a fuel factor, with the
// factor's emission factors in an iLEAP unit. perActivity, when > 0, is
// the amount of fuel (in the factor's unit) used per unit of activity.
func carrierFromFuel(f calc.FuelFactor, perActivity float64) (EnergyCarrier, string) {
	c := carrier(f.Fuel)
	if c == Electric && !calc.ElectricFuel(f) {
		c = "" // burnt in the vehicle, or not per energy: not what the GHG scopes call electricity
	}
	if c == "" && calc.ElectricFuel(f) {
		c = Electric // a supplier's electricity factor (a green tariff), as the GHG scopes read it
	}
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
