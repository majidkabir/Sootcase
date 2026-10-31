package toc

// Golden tests: worked examples in the GLEC Framework v3.3, Section 3
// Module 4 ("Examples of emission calculations - step-by-step"), reproduced
// end to end through Sootcase: operations -> TOC/HOC intensities (toc.Build)
// -> layered dataset (factors.Open) -> shipment emissions (calc.Calculate).
//
// Source: Smart Freight Centre. Global Logistics Emissions Council Framework
// for Logistics Emissions Accounting and Reporting; v3.3 edition, revised and
// updated (October 2026), SFC-GUID-001 v3.3. Example inputs and results are
// reproduced in part for non-commercial verification, with acknowledgement of
// the source, as the Framework's terms permit.
//
// Each published value is checked to the precision GLEC prints it, i.e.
// within half a unit of its last printed digit, unless noted. Where the
// published example contradicts itself, the comment says so and the test
// follows the printed inputs.

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Fuel factors used by the examples, in kg CO2e per unit. Where an example
// gives only a well-to-wheel value, it is entered as TTW and only WTW is
// checked.
const glecFuels = `{"name": "glec-v3.3-examples", "fuels": [
  {"fuel": "diesel_eu_b5", "unit": "l",   "ttw_kg_per_unit": 3.36, "source": "GLEC v3.3 Module 4 p.119 and Table 13 p.138 (WTW)"},
  {"fuel": "diesel",       "unit": "l",   "ttw_kg_per_unit": 3.48, "source": "GLEC v3.3 Module 4 Table 13 p.138 (WTW); Table 14: 261,000 kg / 75,000 l"},
  {"fuel": "electricity_eu", "unit": "kWh", "wtt_kg_per_unit": 0.33447, "source": "GLEC v3.3 Module 4 p.121, 334.47 g CO2e/kWh as printed"},
  {"fuel": "jet_kg",       "unit": "kg",  "ttw_kg_per_unit": 3.18, "wtt_kg_per_unit": 0.66, "source": "GLEC v3.3 Module 4 p.132"},
  {"fuel": "vlsfo_na_kg",  "unit": "kg",  "ttw_kg_per_unit": 3.16, "wtt_kg_per_unit": 0.68, "source": "GLEC v3.3 Module 4 p.136"},
  {"fuel": "mgo_na_kg",    "unit": "kg",  "ttw_kg_per_unit": 3.26, "wtt_kg_per_unit": 0.61, "source": "GLEC v3.3 Module 4 p.136"},
  {"fuel": "electricity_hub", "unit": "kWh", "wtt_kg_per_unit": 0.335, "source": "GLEC v3.3 Module 4 Table 13 p.138, Germany"},
  {"fuel": "electricity_hub_general", "unit": "kWh", "wtt_kg_per_unit": 0.44, "source": "GLEC v3.3 Module 4 Tables 14-15 p.138-139, 70,400 kg / 160,000 kWh as printed"},
  {"fuel": "natural_gas_kwh", "unit": "kWh", "ttw_kg_per_unit": 0.29121875, "source": "GLEC v3.3 Module 4 Tables 14-15, 9,319 kg / 32,000 kWh (Table 13: 0.29)"}
]}`

// glecWorld builds the operations into a layer and opens fuels + layer.
func glecWorld(t *testing.T, opsJSON string) (*factors.Dataset, factors.Dataset) {
	t.Helper()
	base, err := factors.Parse(strings.NewReader(glecFuels))
	if err != nil {
		t.Fatal(err)
	}
	ops, err := Parse(strings.NewReader(opsJSON))
	if err != nil {
		t.Fatal(err)
	}
	layer, err := Build(ops, base)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var raw any
	_ = json.Unmarshal([]byte(glecFuels), &raw)
	ds, err := factors.Open(write("fuels.json", raw) + "," + write("layer.json", layer))
	if err != nil {
		t.Fatal(err)
	}
	return ds, layer
}

// near checks got against a published value printed with the given number
// of decimals: |got - want| <= half a unit of the last printed digit.
func near(t *testing.T, what string, got, want float64, decimals int) {
	t.Helper()
	if tol := 0.5 * math.Pow(10, -float64(decimals)); math.Abs(got-want) > tol+1e-9 {
		t.Errorf("%s = %.6f, GLEC publishes %.*f", what, got, decimals, want)
	}
}

// Module 4, 1. Road transport (p.119): 0.15 l diesel per tkm at 3.36 kg/l
// gives 0.504 kg CO2e/tkm; a 450 kg consignment over 20 km emits 4.54 kg.
func TestGLEC_Road(t *testing.T) {
	ds, layer := glecWorld(t, `{"name": "road", "period": "year",
	  "tocs": [{"id": "road_toc", "mode": "road", "tonne_km": 1000000,
	            "fuels": [{"fuel": "diesel_eu_b5", "amount": 150000}]}]}`)
	near(t, "road TOC intensity (kg/tkm)", layer.Legs[0].WTW()/1000, 0.504, 3)

	c := calc.Calculator{Factors: ds}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 450, Legs: []calc.Leg{
		{Mode: calc.Road, VehicleType: "road_toc", DistanceKm: 20}}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "road consignment (kg)", r.WTWKg, 4.54, 2)
	if r.Legs[0].DataType != calc.DataPrimary {
		t.Errorf("TOC from measured fuel should be primary data, got %q", r.Legs[0].DataType)
	}

	// Transport activity, p.119: Hamburg-Munich, 1,200 t over an SFD of 658 km.
	r, _ = c.Calculate(context.Background(), calc.Shipment{WeightKg: 1_200_000, Legs: []calc.Leg{
		{Mode: calc.Road, VehicleType: "road_toc", DistanceKm: 658, DistanceType: calc.DistSFD}}})
	near(t, "Hamburg-Munich transport activity (tkm)", r.TonneKm, 789_600, 0)
}

// Module 4, 1.1 (p.121): a hybrid fleet using 100,000 l of diesel with 5%
// biodiesel and 100,000 kWh of electricity: 336,000 + 33,447 = 369,447 kg.
// GLEC prints the EU electricity factor as 334.47 g/kWh, "equivalent" to
// Module 1's 92.8 g/MJ; 92.8 x 3.6 is 334.08. The test uses the printed
// 334.47 (Sootcase's own GLEC dataset uses 92.8 g/MJ).
func TestGLEC_RoadHybrid(t *testing.T) {
	_, layer := glecWorld(t, `{"name": "hybrid", "period": "year",
	  "tocs": [{"id": "hybrid_toc", "mode": "road", "tonne_km": 1000000,
	            "fuels": [{"fuel": "diesel_eu_b5", "amount": 100000}, {"fuel": "electricity_eu", "amount": 100000}]}]}`)
	f := layer.Legs[0]
	near(t, "diesel part (kg)", f.TTW/1000*1_000_000, 336_000, 0)
	near(t, "electricity part (kg)", f.WTT/1000*1_000_000, 33_447, 0)
	near(t, "hybrid total (kg)", f.WTW()/1000*1_000_000, 369_447, 0)
}

// Module 4, 3.1 Air (p.131-132): 400 flights x 7,000 kg fuel over 7,160,000
// tkm (Table 8); a 275 kg consignment flown 1,000 km.
func TestGLEC_Air(t *testing.T) {
	ds, layer := glecWorld(t, `{"name": "air", "period": "year",
	  "tocs": [{"id": "air_toc", "mode": "air", "tonne_km": 7160000, "distance_basis": "gcd",
	            "fuels": [{"fuel": "jet_kg", "amount": 2800000}]}]}`)
	f := layer.Legs[0]
	near(t, "air TOC WTT (kg/tkm)", f.WTT/1000, 0.258, 3)
	near(t, "air TOC TTW (kg/tkm)", f.TTW/1000, 1.244, 3)
	near(t, "air TOC WTW (kg/tkm)", f.WTW()/1000, 1.502, 3)

	c := calc.Calculator{Factors: ds}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 275, Legs: []calc.Leg{
		{Mode: calc.Air, VehicleType: "air_toc", DistanceKm: 1000, DistanceType: calc.DistGCD}}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "air transport activity (tkm)", r.TonneKm, 275, 0)
	near(t, "air consignment WTT (kg)", r.WTTKg, 71.0, 1)
	// GLEC multiplies 275 tkm by intensities first rounded to 3 decimals
	// (1.244, 1.502), printing 342.1 and 413.1; unrounded it is 342.0 and
	// 413.0. Allow that rounding: 0.05%.
	for _, c := range []struct {
		what      string
		got, want float64
	}{{"air consignment TTW (kg)", r.TTWKg, 342.1}, {"air consignment WTW (kg)", r.WTWKg, 413.1}} {
		if math.Abs(c.got/c.want-1) > 0.0005 {
			t.Errorf("%s = %.3f, GLEC publishes %.1f", c.what, c.got, c.want)
		}
	}
	// The consignment's distance is great-circle, like the TOC's: no DAF.
	if r.Legs[0].DAF != 0 {
		t.Errorf("unexpected DAF %v", r.Legs[0].DAF)
	}
}

// Module 4, 4.4 Bulk sea transport (p.135-136): a TOC fuelled by 1,327.48 t
// VLSFO and 19.88 t MGO over 755,830,574 tkm (Table 10); cargo owner A's
// 39,369 t travel two TCEs and pass two bulk terminals at a default 1.2 kg
// CO2e/t.
func TestGLEC_BulkSea(t *testing.T) {
	_, layer := glecWorld(t, `{"name": "bulk", "period": "charter",
	  "tocs": [{"id": "bulk_toc", "mode": "sea", "tonne_km": 755830574,
	            "fuels": [{"fuel": "vlsfo_na_kg", "amount": 1327480}, {"fuel": "mgo_na_kg", "amount": 19880}]}]}`)
	f := layer.Legs[0]
	near(t, "bulk TOC WTT (kg/tkm)", f.WTT/1000, 0.00121, 5)
	near(t, "bulk TOC TTW (kg/tkm)", f.TTW/1000, 0.00564, 5)
	// The text gives 0.00685 (5,174,459 / 755,830,574); Table 11 prints
	// 0.00676 as the overall intensity, which isn't WTT + TTW.
	near(t, "bulk TOC WTW (kg/tkm)", f.WTW()/1000, 0.00685, 5)

	// Default bulk-terminal intensity used by the example, as a hub factor.
	merged, err := factors.Parse(strings.NewReader(mustJSON(t, map[string]any{
		"name": "bulk+hub", "legs": layer.Legs,
		"hubs": []calc.HubFactor{{HubType: "bulk_terminal", WTWPerTonneG: 1200, Source: "GLEC v3.3 Module 4 p.136 default"}},
	})))
	if err != nil {
		t.Fatal(err)
	}

	const mass = 39_369.0 // tonnes, from the hub activity of TCE 2 and 6
	c := calc.Calculator{Factors: merged}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: mass * 1000,
		Legs: []calc.Leg{
			{ID: "TCE3", Mode: calc.Sea, VehicleType: "bulk_toc", DistanceKm: 57_400_002 / mass},
			{ID: "TCE5", Mode: calc.Sea, VehicleType: "bulk_toc", DistanceKm: 466_286_436 / mass},
		},
		Hubs: []calc.HubStop{{ID: "TCE2", HubType: "bulk_terminal"}, {ID: "TCE6", HubType: "bulk_terminal"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tce3, tce5 := r.Legs[0], r.Legs[1]
	near(t, "TCE3 WTT (kg)", tce3.WTTKg, 69_474, 0)
	near(t, "TCE3 TTW (kg)", tce3.TTWKg, 323_490, 0)
	near(t, "TCE3 WTW (kg)", tce3.WTWKg, 392_964, 0)
	near(t, "TCE5 WTT (kg)", tce5.WTTKg, 564_366, 0)
	near(t, "TCE5 TTW (kg)", tce5.TTWKg, 2_627_857, 0)
	near(t, "TCE5 WTW (kg)", tce5.WTWKg, 3_192_223, 0)
	near(t, "hub TCE2 (kg)", r.Hubs[0].WTWKg, 47_243, 0)
	near(t, "transport chain transport activity (tkm)", r.TonneKm, 523_686_438, 0)
	near(t, "chain energy production (kg)", r.WTTKg, 633_839, 0)
	// GLEC counts the default hub value as operational (Table 11); Sootcase
	// keeps hub values as WTW only, so compare legs' TTW plus the hubs.
	near(t, "chain operational incl. hubs (kg)", r.TTWKg+r.Hubs[0].WTWKg+r.Hubs[1].WTWKg, 3_045_833, 0)
	near(t, "chain total (kg)", r.WTWKg, 3_679_673, 0)
	near(t, "chain intensity (kg/tkm)", r.WTWKg/r.TonneKm, 0.00703, 5)
}

// Module 4, 5. Hub operations (p.138-139): a container terminal in Germany
// handling 4,250,000 t a year, of which 50,000 t refrigerated, with the
// emissions per activity of Tables 14 and 15.
//
// The published v3.3 example contradicts itself in three places; the test
// follows the printed table items:
//   - "General processes" electricity: 160,000 kWh -> 70,400 kg, i.e. 0.44
//     kg/kWh, while Table 13 gives German electricity as 0.335 (used for the
//     other two electricity items).
//   - The items of "unspecified group" add up to 810,019 kg; the table and
//     text print 814,019 (and 0.192 kg/t from it).
//   - 5.1 divides 991,519 kg (the v3.2 edition's total) by 4,250,000 t for
//     0.23 kg/t; Table 14's own total, 860,269 kg, gives 0.20.
func TestGLEC_Hub(t *testing.T) {
	ds, layer := glecWorld(t, `{"name": "terminal", "period": "year",
	  "hocs": [
	    {"id": "terminal_all", "tonnes": 4250000, "energy": [
	      {"fuel": "electricity_hub", "amount": 1100000}, {"fuel": "diesel", "amount": 75000},
	      {"fuel": "diesel_eu_b5", "amount": 30000}, {"fuel": "natural_gas_kwh", "amount": 32000},
	      {"fuel": "electricity_hub_general", "amount": 160000}, {"fuel": "electricity_hub", "amount": 150000}]},
	    {"id": "terminal_general", "tonnes": 4250000, "energy": [
	      {"fuel": "electricity_hub", "amount": 1100000}, {"fuel": "diesel", "amount": 75000},
	      {"fuel": "diesel_eu_b5", "amount": 30000}, {"fuel": "natural_gas_kwh", "amount": 32000},
	      {"fuel": "electricity_hub_general", "amount": 160000}]},
	    {"id": "terminal_reefer_extra", "tonnes": 50000, "energy": [{"fuel": "electricity_hub", "amount": 150000}]}
	  ]}`)
	byID := map[string]float64{}
	for _, h := range layer.Hubs {
		byID[h.HubType] = h.WTWPerTonneG / 1000
	}
	near(t, "Table 14 item: handling electricity (kg)", 1_100_000*0.335, 368_500, 0)
	near(t, "Table 14 item: reefer station (kg)", 150_000*0.335, 50_250, 0)
	near(t, "HOC total emissions, Table 14 (kg)", byID["terminal_all"]*4_250_000, 860_269, 0)
	near(t, "HOC average intensity from Table 14's total (kg/t)", byID["terminal_all"], 0.20, 2)
	near(t, "emissions 'unspecified group', sum of items (kg)", byID["terminal_general"]*4_250_000, 810_019, 0)
	near(t, "intensity 'refrigerated freight' (kg/t)", byID["terminal_reefer_extra"], 1.01, 2)

	c := calc.Calculator{Factors: ds}
	leg := []calc.Leg{{Mode: calc.Road, DistanceKm: 1, Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 0}}}
	ambient, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 87_000, Legs: leg,
		Hubs: []calc.HubStop{{HubType: "terminal_general"}}})
	if err != nil {
		t.Fatal(err)
	}
	// GLEC: 87 t x 0.192 kg/t = 16.7 kg, on its 814,019 kg. On the items'
	// 810,019 kg the intensity is 0.1906 kg/t and the transfer 16.6 kg.
	near(t, "ambient client transfer, GLEC arithmetic (kg)", 87*0.192, 16.7, 1)
	near(t, "ambient client transfer (kg)", ambient.WTWKg, 16.6, 1)

	// Refrigerated freight takes both: general + reefer station, 1.2 kg/t.
	reefer, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 100_000, Legs: leg,
		Hubs: []calc.HubStop{{HubType: "terminal_general"}, {HubType: "terminal_reefer_extra"}}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "refrigerated intensity (kg/t)", byID["terminal_general"]+byID["terminal_reefer_extra"], 1.2, 1)
	near(t, "refrigerated client transfer (kg)", reefer.WTWKg, 120, 0)
	if reefer.Hubs[0].DataType != calc.DataPrimary {
		t.Errorf("HOC from measured energy should be primary data, got %q", reefer.Hubs[0].DataType)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
