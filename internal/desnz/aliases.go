package desnz

import (
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Short names for the most-used factors. Targets are DESNZ label slugs; if
// DESNZ renames a category, the importer warns and drops that alias.
//
// Choices: road aliases are "average laden" (DESNZ's fleet-average load),
// air excludes radiative forcing (as ISO 14083 reports), fuel aliases are
// per litre.
var vehicleAliases = []factors.VehicleAlias{
	{Mode: calc.Road, Alias: "hgv", VehicleType: "hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs/average_laden"},
	{Mode: calc.Road, Alias: "hgv_rigid", VehicleType: "hgv_non_refrigerated_all_diesel/average_non_refrigerated_rigids/average_laden"},
	{Mode: calc.Road, Alias: "hgv_artic", VehicleType: "hgv_non_refrigerated_all_diesel/average_non_refrigerated_artics/average_laden"},
	{Mode: calc.Road, Alias: "hgv_artic_33t", VehicleType: "hgv_non_refrigerated_all_diesel/articulated_33t/average_laden"},
	{Mode: calc.Road, Alias: "hgv_reefer", VehicleType: "hgv_refrigerated_all_diesel/average_refrigerated_hgvs/average_laden"},
	{Mode: calc.Road, Alias: "van", VehicleType: "vans/average_up_to_3_5_tonnes/diesel"},
	{Mode: calc.Road, Alias: "van_electric", VehicleType: "vans/average_up_to_3_5_tonnes/battery_electric_vehicle"},
	{Mode: calc.Rail, Alias: "freight_train", VehicleType: "rail/freight_train"},
	{Mode: calc.Sea, Alias: "container_ship", VehicleType: "cargo_ship/container_ship/average"},
	{Mode: calc.Sea, Alias: "bulk_carrier", VehicleType: "cargo_ship/bulk_carrier/average"},
	{Mode: calc.Sea, Alias: "general_cargo", VehicleType: "cargo_ship/general_cargo/average"},
	{Mode: calc.Sea, Alias: "roro", VehicleType: "cargo_ship/roro_ferry/average"},
	{Mode: calc.Sea, Alias: "ropax", VehicleType: "cargo_ship/large_ropax_ferry/average"},
	{Mode: calc.Air, Alias: "air_freight", VehicleType: "freight_flights/international_to_from_non_uk/without_rf"},
}

var fuelAliases = map[string]string{
	"diesel":          "liquid_fuels/diesel_average_biofuel_blend/litres",
	"diesel_mineral":  "liquid_fuels/diesel_100pct_mineral_diesel/litres",
	"petrol":          "liquid_fuels/petrol_average_biofuel_blend/litres",
	"hvo":             "biofuel/biodiesel_hvo/litres",
	"marine_gas_oil":  "liquid_fuels/marine_gas_oil/litres",
	"marine_fuel_oil": "liquid_fuels/marine_fuel_oil/litres",
	"jet_fuel":        "liquid_fuels/aviation_turbine_fuel/litres",
}

// dafs are GLEC Framework v3.3 default distance adjustment factors, used
// only when a leg's distance type differs from a factor's distance basis.
var dafs = []calc.DAF{
	{Mode: calc.Air, From: calc.DistActual, To: calc.DistGCD, AddKm: -95,
		Source: "GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 1 Chapter 4 (air): without specific data, DAF = GCD / (GCD + 95 km), i.e. actual flown distance minus 95 km"},
	{Mode: calc.Road, From: calc.DistSFD, To: calc.DistActual, Multiplier: 1.05,
		Source: "GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 1 Chapter 4 (road): when the emission intensity is based on actual distance, a DAF must be applied; 5% as in GLEC's own road default values (Section 3 Module 2), which include a +5% distance conversion between actual and shortest feasible distance"},
	{Mode: calc.Sea, From: calc.DistSFD, To: calc.DistActual, Multiplier: 1.15,
		Source: "GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 1 Chapter 4 (sea), citing Clean Cargo: actual container distances average 15% above the shortest feasible route"},
}

// addAliases attaches the aliases whose targets exist in ds.
// containerLoads are the GLEC Framework v3.3 net tonnes per TEU for when
// the cargo mass is known only in TEU (Section 1 Chapter 2, p. 21).
var containerLoads = map[string]float64{"average": 10, "light": 6, "heavy": 14.5}

func addAliases(ds *factors.Dataset, warn func(string, ...any)) {
	ds.DAFs = append(ds.DAFs, dafs...)
	ds.ContainerLoads = containerLoads
	// GLEC and ISO 14083 measure air transport on great-circle distance.
	// DESNZ road factors come from fuel used per km actually driven (DfT
	// road freight survey; real-world uplift for vans): actual distance.
	for i := range ds.Legs {
		switch ds.Legs[i].Mode {
		case calc.Air:
			ds.Legs[i].DistanceBasis = calc.DistGCD
		case calc.Road:
			ds.Legs[i].DistanceBasis = calc.DistActual
		}
	}
	legs := map[string]bool{}
	for _, f := range ds.Legs {
		legs[string(f.Mode)+"/"+f.VehicleType] = true
	}
	for _, a := range vehicleAliases {
		if !legs[string(a.Mode)+"/"+a.VehicleType] {
			warn("alias %s/%s: target %s not in this file, dropped", a.Mode, a.Alias, a.VehicleType)
			continue
		}
		ds.VehicleAliases = append(ds.VehicleAliases, a)
	}
	fuels := map[string]bool{}
	for _, f := range ds.Fuels {
		fuels[f.Fuel] = true
	}
	for alias, id := range fuelAliases {
		if !fuels[id] {
			warn("fuel alias %s: target %s not in this file, dropped", alias, id)
			continue
		}
		if ds.FuelAliases == nil {
			ds.FuelAliases = map[string]string{}
		}
		ds.FuelAliases[alias] = id
	}
}
