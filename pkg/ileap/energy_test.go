package ileap

import (
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Round 28: the export reads electricity as the GHG scopes do, and writes
// MWh and GWh in kWh.
func TestCarrierFromFuel_Electricity(t *testing.T) {
	for _, c := range []struct {
		f           calc.FuelFactor
		carrier     string
		unit        string
		consumption float64
	}{
		{calc.FuelFactor{Fuel: "Renewable electricity", Unit: "MWh", WTT: 20}, Electric, "kWh", 500},
		{calc.FuelFactor{Fuel: "Grøn strøm", Unit: "GWh", WTT: 20000}, Electric, "kWh", 500000},
		{calc.FuelFactor{Fuel: "Ökostrom-Wasserstoff", Unit: "kg", WTT: 1.2}, "", "", 0},
		{calc.FuelFactor{Fuel: "Electricity + diesel range extender", Unit: "kWh", TTW: 0.1, WTT: 0.1}, "", "", 0},
	} {
		ec, gap := carrierFromFuel(c.f, 0.5)
		if c.carrier == "" {
			if gap == "" {
				t.Errorf("%q: carrier %q, want a gap", c.f.Fuel, ec.EnergyCarrier)
			}
			continue
		}
		got := ec.EnergyConsumption
		if gap != "" || ec.EnergyCarrier != c.carrier || ec.EnergyConsumptionUnit != c.unit || got != dec(c.consumption) {
			t.Errorf("%q: %+v gap %q", c.f.Fuel, ec, gap)
		}
	}
}
