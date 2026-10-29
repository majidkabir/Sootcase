package factors

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/places"
)

func TestEmbeddedSample(t *testing.T) {
	d, err := Embedded("sample")
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.LegFactor(calc.Road, "truck_40t")
	if err != nil {
		t.Fatal(err)
	}
	if f.WTW() != 75 {
		t.Errorf("WTW = %v, want 75", f.WTW())
	}
	if _, err := d.LegFactor(calc.Road, "unicycle"); err == nil {
		t.Error("want error for unknown vehicle type")
	}
}

func TestParse_Rejects(t *testing.T) {
	cases := map[string]string{
		"duplicate": `{"legs":[{"mode":"road","vehicle_type":"a"},{"mode":"road","vehicle_type":"a"}]}`,
		"negative":  `{"legs":[{"mode":"road","vehicle_type":"a","ttw_g_per_tkm":-1}]}`,
		"bad json":  `{`,
	}
	for name, in := range cases {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestEmbeddedSample_Fuel(t *testing.T) {
	d, err := Embedded("sample")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.FuelFactor("diesel"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.FuelFactor("unobtainium"); err == nil {
		t.Error("want error for unknown fuel")
	}
}

func TestOpen(t *testing.T) {
	names := EmbeddedNames()
	if strings.Join(names, ",") != "desnz-2026,eu-mrv-2024,glec-3.3,nl-iww-2026,sample" {
		t.Errorf("EmbeddedNames = %v", names)
	}
	ds, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Name != "desnz-2026+nl-iww-2026+eu-mrv-2024+glec-3.3" || len(ds.Legs) < 100 || len(ds.Fuels) < 100 ||
		!strings.Contains(ds.Note, "Open Government Licence v3.0") || !strings.Contains(ds.Note, "Smart Freight Centre is acknowledged as the source") {
		t.Errorf("default dataset: name=%q legs=%d fuels=%d", ds.Name, len(ds.Legs), len(ds.Fuels))
	}
	// Spot-check one published value end to end (DESNZ 2026 flat file).
	f, err := ds.LegFactor(calc.Rail, "rail/freight_train")
	if err != nil || f.TTW != 25.83 || f.WTT != 6.91 || f.Year != 2026 {
		t.Errorf("rail freight = %+v, %v", f, err)
	}
	// GLEC v3.2: air is measured on great-circle distance, with no +95 km on
	// top; 95 km is only the default DAF from actual flown distance.
	if _, adjusted := ds.Distances().Adjust[calc.Air]; adjusted {
		t.Error("air distances must not be adjusted on top of great-circle")
	}
	if f, _ := ds.LegFactor(calc.Air, "air_freight"); f.DistanceBasis != calc.DistGCD {
		t.Errorf("air factor basis = %q, want gcd", f.DistanceBasis)
	}
	if d, ok := ds.DAF(calc.Air, calc.DistActual, calc.DistGCD); !ok || d.Apply(1095) != 1000 {
		t.Errorf("air actual->gcd DAF = %+v, %v", d, ok)
	}
	if d, ok := ds.DAF(calc.Sea, calc.DistSFD, calc.DistActual); !ok || math.Abs(d.Apply(1000)-1150) > 1e-9 {
		t.Errorf("sea sfd->actual DAF = %+v, %v", d, ok)
	}
	if _, err := Open("testdata-does-not-exist.json"); err == nil {
		t.Error("want error for missing file")
	}
}

func TestParse_DistanceAdjustments(t *testing.T) {
	ds, err := Parse(strings.NewReader(`{"name":"x","distance_adjustments":[{"mode":"air","add_km":95}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if a := ds.Distances().Adjust[calc.Air]; a.AddKm != 95 {
		t.Errorf("air adjustment = %+v", a)
	}
	if _, err := Parse(strings.NewReader(`{"distance_adjustments":[{"mode":"air","add_km":-1}]}`)); err == nil {
		t.Error("want error for negative adjustment")
	}
}

func TestAliases(t *testing.T) {
	base := `"legs":[{"mode":"road","vehicle_type":"long/name","ttw_g_per_tkm":1}],
		"fuels":[{"fuel":"long/diesel","unit":"l","ttw_kg_per_unit":2}]`
	ds, err := Parse(strings.NewReader(`{` + base + `,
		"vehicle_aliases":[{"mode":"road","alias":"hgv","vehicle_type":"long/name"}],
		"fuel_aliases":{"diesel":"long/diesel"}}`))
	if err != nil {
		t.Fatal(err)
	}
	f, err := ds.LegFactor(calc.Road, "hgv")
	if err != nil || f.VehicleType != "long/name" {
		t.Errorf("alias lookup = %+v, %v; want resolved full id", f, err)
	}
	if _, err := ds.LegFactor(calc.Rail, "hgv"); err == nil {
		t.Error("alias must be scoped to its mode")
	}
	if ff, err := ds.FuelFactor("diesel"); err != nil || ff.Fuel != "long/diesel" {
		t.Errorf("fuel alias = %+v, %v", ff, err)
	}
	bad := map[string]string{
		"unknown target": `"vehicle_aliases":[{"mode":"road","alias":"x","vehicle_type":"nope"}]`,
		"clash":          `"vehicle_aliases":[{"mode":"road","alias":"long/name","vehicle_type":"long/name"}]`,
		"fuel unknown":   `"fuel_aliases":{"x":"nope"}`,
	}
	for name, extra := range bad {
		if _, err := Parse(strings.NewReader(`{` + base + `,` + extra + `}`)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestContainerLoads(t *testing.T) {
	ds, err := Parse(strings.NewReader(`{"container_loads":{"average":10}}`))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := ds.ContainerLoad("average"); err != nil || v != 10 {
		t.Errorf("average = %v, %v", v, err)
	}
	if _, err := ds.ContainerLoad("x"); err == nil {
		t.Error("want error for unknown profile")
	}
	if _, err := Parse(strings.NewReader(`{"container_loads":{"x":0}}`)); err == nil {
		t.Error("want error for zero tonnes")
	}
}

// The load models are derived from DESNZ per-km rows; at the published
// load points they must reproduce DESNZ's own per-tonne-km factors.
func TestLoadModelsReproduceDESNZ(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.LoadModels) < 16 {
		t.Fatalf("got %d load models", len(ds.LoadModels))
	}
	worst := 0.0
	for _, m := range ds.LoadModels {
		for _, p := range []struct {
			lf     float64
			suffix string
		}{{1, "100pct_laden"}, {0.5, "50pct_laden"}} {
			want, err := ds.LegFactor(m.Mode, m.VehicleType+"/"+p.suffix)
			if err != nil {
				t.Fatal(err)
			}
			ttw, wtt, err := m.PerTonneKm(p.lf, 0)
			if err != nil {
				t.Fatal(err)
			}
			// TTW and WTW must match DESNZ within rounding. WTT alone may
			// differ by up to ~3.6%: DESNZ's own WTT per-km and per-tonne-km
			// rows imply payloads that differ by that much, and the model
			// follows the per-km rows (WTT proportional to TTW, ratio ~0.234).
			checks := []struct {
				name      string
				got, want float64
				tol       float64
			}{
				{"TTW", ttw, want.TTW, 0.01},
				{"WTW", ttw + wtt, want.WTW(), 0.01},
				{"WTT", wtt, want.WTT, 0.04},
			}
			for _, c := range checks {
				rel := math.Abs(c.got/c.want - 1)
				if c.name == "WTW" {
					worst = math.Max(worst, rel)
				}
				if rel > c.tol {
					t.Errorf("%s %s at %v: model %.3f vs DESNZ %.3f (%.2f%%)", m.VehicleType, c.name, p.lf, c.got, c.want, rel*100)
				}
			}
		}
	}
	t.Logf("worst WTW deviation from DESNZ: %.3f%%", worst*100)

	// Alias and full ids find the class model.
	for _, vt := range []string{"hgv", "hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs/average_laden",
		"hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs"} {
		if m, err := ds.LoadModel(calc.Road, vt); err != nil || m.VehicleType != "hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs" {
			t.Errorf("LoadModel(%q) = %q, %v", vt, m.VehicleType, err)
		}
	}
	if _, err := ds.LoadModel(calc.Road, "van"); err == nil {
		t.Error("vans have no laden rows in DESNZ, want no load model")
	}
}

func TestGrids(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	g, err := ds.Grid("dnk")
	if err != nil || g.Country != "DNK" || g.G <= 0 || g.Year < 2024 {
		t.Errorf("DNK grid = %+v, %v", g, err)
	}
	if !strings.Contains(ds.GridNote, "CC BY 4.0") {
		t.Error("default grid attribution missing")
	}
	f, err := ds.FuelFactor("electricity/DNK")
	if err != nil || f.Unit != "kWh" || f.TTW != 0 || f.WTT != g.G/1000 {
		t.Errorf("electricity fuel = %+v, %v", f, err)
	}
	if _, err := ds.Grid("DK"); err == nil {
		t.Error("want error for 2-letter code")
	}
	if _, err := ds.FuelFactor("electricity/ZZZ"); err == nil {
		t.Error("want error for unknown country")
	}

	// A dataset's own grids replace the default.
	own, err := Parse(strings.NewReader(`{"grids":[{"country":"dnk","g_per_kwh":50}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := own.Grid("DNK"); g.G != 50 {
		t.Errorf("own grid = %+v", g)
	}
	if _, err := own.Grid("SWE"); err == nil {
		t.Error("own grids should not merge with the default")
	}
	for name, in := range map[string]string{
		"dup":      `{"grids":[{"country":"DNK","g_per_kwh":1},{"country":"dnk","g_per_kwh":2}]}`,
		"bad iso":  `{"grids":[{"country":"DK","g_per_kwh":1}]}`,
		"negative": `{"grids":[{"country":"DNK","g_per_kwh":-1}]}`,
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestOpen_Layered(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	hubs := write("hubs.json", `{"name":"my-hubs","note":"own measurements",
		"hubs":[{"hub_type":"transhipment","wtw_g_per_tonne":1300,"source":"own"}],
		"container_loads":{"pallet_wide":12}}`)

	ds, err := Open(Default + "," + hubs)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Name != "desnz-2026+nl-iww-2026+eu-mrv-2024+glec-3.3+my-hubs" || !strings.Contains(ds.Note, "own measurements") || !strings.Contains(ds.Note, "Open Government Licence") {
		t.Errorf("name=%q note=%q", ds.Name, ds.Note)
	}
	if h, err := ds.HubFactor("transhipment"); err != nil || h.WTWPerTonneG != 1300 {
		t.Errorf("hub = %+v, %v", h, err)
	}
	if _, err := ds.LegFactor(calc.Road, "hgv"); err != nil {
		t.Errorf("DESNZ alias lost in layering: %v", err)
	}
	if _, err := ds.Grid("DNK"); err != nil {
		t.Errorf("default grid lost in layering: %v", err)
	}
	if _, err := ds.LoadModel(calc.Road, "hgv"); err != nil {
		t.Errorf("load model lost in layering: %v", err)
	}

	// End to end: a shipment with a hub stop now calculates.
	c := calc.Calculator{Factors: ds, Distances: ds.Distances()}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10_000,
		Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 100}},
		Hubs: []calc.HubStop{{ID: "h", HubType: "transhipment"}}})
	if err != nil || len(r.Hubs) != 1 || r.Hubs[0].WTWKg != 13 {
		t.Errorf("hub calc = %+v, %v", r.Hubs, err)
	}

	clash := map[string]string{
		"duplicate leg":       `{"legs":[{"mode":"rail","vehicle_type":"rail/freight_train","ttw_g_per_tkm":1}]}`,
		"duplicate container": `{"container_loads":{"average":12}}`,
	}
	for name, body := range clash {
		layers := Default + "," + hubs + "," + write(strings.ReplaceAll(name, " ", "_")+".json", body)
		if _, err := Open(layers); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	// A DAF defined again is the later layer's.
	d2, err := Open(Default + "," + write("daf.json", `{"dafs":[{"mode":"air","from":"actual","to":"gcd","add_km":-100,"source":"mine"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if x, ok := d2.DAF(calc.Air, "actual", "gcd"); !ok || x.AddKm != -100 || x.Source != "mine" {
		t.Errorf("later DAF = %+v, %v", x, ok)
	}
	if _, err := Open(Default + "," + dir + "/missing.json"); err == nil {
		t.Error("missing layer: want error")
	}
}

func TestLegFactor_MissingModeHint(t *testing.T) {
	ds, _ := Open("desnz-2026") // DESNZ alone has no inland waterway factors
	_, err := ds.LegFactor(calc.InlandWaterway, "barge")
	if err == nil || !strings.Contains(err.Error(), "has no inland_waterway factors") || !strings.Contains(err.Error(), "sootcase toc") {
		t.Errorf("err = %v", err)
	}
	if _, err := ds.LegFactor(calc.Road, "unicycle"); err == nil || strings.Contains(err.Error(), "has no road factors") {
		t.Errorf("road has factors, want plain not-found error, got %v", err)
	}
}

func TestParse_DAFs(t *testing.T) {
	ok := `{"legs":[{"mode":"sea","vehicle_type":"s","ttw_g_per_tkm":1,"distance_basis":"actual"}],
		"dafs":[{"mode":"sea","from":"sfd","to":"actual","multiplier":1.15,"source":"x"}]}`
	ds, err := Parse(strings.NewReader(ok))
	if err != nil {
		t.Fatal(err)
	}
	if _, found := ds.DAF(calc.Sea, calc.DistSFD, calc.DistActual); !found {
		t.Error("DAF not indexed")
	}
	for name, in := range map[string]string{
		"bad basis":     `{"legs":[{"mode":"sea","vehicle_type":"s","distance_basis":"miles"}]}`,
		"bad type":      `{"dafs":[{"mode":"sea","from":"sfd","to":"odometer"}]}`,
		"same types":    `{"dafs":[{"mode":"sea","from":"sfd","to":"sfd"}]}`,
		"no mode":       `{"dafs":[{"from":"sfd","to":"gcd"}]}`,
		"negative mult": `{"dafs":[{"mode":"sea","from":"sfd","to":"gcd","multiplier":-1}]}`,
		"duplicate":     `{"dafs":[{"mode":"sea","from":"sfd","to":"gcd"},{"mode":"sea","from":"sfd","to":"gcd"}]}`,
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// Inland waterway factors from www.co2emissiefactoren.nl 2026 (CE Delft
// STREAM 2020 v2), checked row by row against the published list.
func TestInlandWaterway_NL2026(t *testing.T) {
	ds, err := Open("desnz-2026,nl-iww-2026")
	if err != nil {
		t.Fatal(err)
	}
	for vt, want := range map[string][2]float64{
		"barge_bulk_small":       {31, 10},
		"barge_bulk_medium":      {23, 8},
		"barge_bulk_large":       {16, 5},
		"barge_container_40teu":  {41, 13},
		"barge_container_96teu":  {39, 13},
		"barge_container_208teu": {24, 8},
		"barge_container_348teu": {20, 7},
		"barge":                  {23, 8},
		"container_barge":        {24, 8},
	} {
		f, err := ds.LegFactor(calc.InlandWaterway, vt)
		if err != nil || f.TTW != want[0] || f.WTT != want[1] || f.DistanceBasis != calc.DistActual {
			t.Errorf("%s = %+v, %v", vt, f, err)
		}
	}

	c := calc.Calculator{Factors: ds, Distances: ds.Distances()}
	ctx := context.Background()
	// Rotterdam-Duisburg, 220 km by river: 1000 t x 220 km x 31 g = 6.82 t.
	r, err := c.Calculate(ctx, calc.Shipment{WeightKg: 1_000_000,
		Legs: []calc.Leg{{Mode: calc.InlandWaterway, VehicleType: "barge", DistanceKm: 220}}})
	if err != nil || math.Abs(r.WTWKg-6820) > 1e-6 {
		t.Errorf("given distance: %v kg, %v", r.WTWKg, err)
	}
	// A shortest feasible distance needs no DAF on inland waterways (GLEC).
	r, err = c.Calculate(ctx, calc.Shipment{WeightKg: 1_000_000,
		Legs: []calc.Leg{{Mode: calc.InlandWaterway, VehicleType: "barge", DistanceKm: 220, DistanceType: calc.DistSFD}}})
	if err != nil || math.Abs(r.WTWKg-6820) > 1e-6 {
		t.Errorf("sfd distance: %v kg, %v", r.WTWKg, err)
	}
	// Straight-line distance from coordinates would undercount a river
	// route, and there is no default DAF for it, so it is refused.
	_, err = c.Calculate(ctx, calc.Shipment{WeightKg: 1000,
		Legs: []calc.Leg{{Mode: calc.InlandWaterway, VehicleType: "barge",
			From: calc.Location{Lat: 51.95, Lon: 4.05}, To: calc.Location{Lat: 51.45, Lon: 6.73}}}})
	if !errors.Is(err, calc.ErrNoDAF) {
		t.Errorf("coordinates only: want ErrNoDAF, got %v", err)
	}
}

func TestShortAlias(t *testing.T) {
	dir := t.TempDir()
	base := dir + "/base.json"
	over := dir + "/over.json"
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(base, `{"name":"base","legs":[{"mode":"sea","vehicle_type":"avg","ttw_g_per_tkm":16}],
		"vehicle_aliases":[{"mode":"sea","alias":"ship","vehicle_type":"avg"}]}`)
	write(over, `{"name":"over","legs":[{"mode":"sea","vehicle_type":"feeder","ttw_g_per_tkm":14},{"mode":"sea","vehicle_type":"ocean","ttw_g_per_tkm":6}],
		"vehicle_aliases":[{"mode":"sea","alias":"ship","vehicle_type":"ocean","short_vehicle_type":"feeder","short_below_km":2000}]}`)
	ds, err := Open(base + "," + over)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		km   float64
		want string
	}{{560, "feeder"}, {1999, "feeder"}, {2000, "ocean"}, {20000, "ocean"}} {
		f, note, err := ds.LegFactorFor(calc.Sea, "ship", tc.km)
		if err != nil || f.VehicleType != tc.want || !strings.Contains(note, tc.want) {
			t.Errorf("%v km: %s %q %v", tc.km, f.VehicleType, note, err)
		}
	}
	// Without a distance, the long meaning; plain types are untouched.
	if f, _ := ds.LegFactor(calc.Sea, "ship"); f.VehicleType != "ocean" {
		t.Errorf("LegFactor: %s", f.VehicleType)
	}
	if f, note, _ := ds.LegFactorFor(calc.Sea, "avg", 100); f.VehicleType != "avg" || note != "" {
		t.Errorf("plain: %s %q", f.VehicleType, note)
	}
	write(over, `{"name":"over","legs":[{"mode":"sea","vehicle_type":"ocean","ttw_g_per_tkm":6}],
		"vehicle_aliases":[{"mode":"sea","alias":"ship2","vehicle_type":"ocean","short_vehicle_type":"nope","short_below_km":2000}]}`)
	if _, err := Open(base + "," + over); err == nil {
		t.Error("unknown short vehicle type accepted")
	}
}

// GLEC v3.2 Section 1 Chapter 4 (road): DESNZ road factors are per km
// actually driven, so a routed (shortest feasible) road distance gets the
// +5% DAF, for fleet averages and load models alike; measured fuel needs no
// distance, and a great-circle road distance has no DAF and is refused.
func TestRoadDAF_GLEC(t *testing.T) {
	const desnzHGV = "hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs/average_laden"
	ds, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances(), NoRoadLegs: true}
	a, b := calc.Location{Lat: 55.6761, Lon: 12.5683}, calc.Location{Lat: 53.5511, Lon: 9.9937}
	calcLeg := func(l calc.Leg) (calc.LegResult, error) {
		r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10000, Legs: []calc.Leg{l}})
		if err != nil {
			return calc.LegResult{}, err
		}
		return r.Legs[0], nil
	}
	f, err := ds.LegFactor(calc.Road, desnzHGV)
	if err != nil || f.DistanceBasis != calc.DistActual {
		t.Fatalf("hgv factor = %+v, %v; want distance basis actual", f, err)
	}
	lr, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: desnzHGV, DistanceKm: 100, DistanceType: calc.DistSFD})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(lr.DAF-1.05) > 1e-12 || !strings.Contains(lr.DAFSource, "GLEC") ||
		math.Abs(lr.WTWKg-10*100*1.05*f.WTW()/1000) > 1e-9 {
		t.Errorf("fleet average: DAF %v (%s), WTW %v", lr.DAF, lr.DAFSource, lr.WTWKg)
	}
	lm, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: "hgv_artic_33t", DistanceKm: 100, DistanceType: calc.DistSFD, LoadTonnes: 14})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(lm.DAF-1.05) > 1e-12 || lm.Method != calc.MethodLoadModel {
		t.Errorf("load model: method %s, DAF %v", lm.Method, lm.DAF)
	}
	act, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: desnzHGV, DistanceKm: 100, DistanceType: calc.DistActual})
	if err != nil || act.DAF != 0 || math.Abs(act.WTWKg*1.05-lr.WTWKg) > 1e-9 {
		t.Errorf("actual distance: DAF %v, WTW %v, %v; want no DAF", act.DAF, act.WTWKg, err)
	}
	fu, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: desnzHGV, DistanceKm: 100, DistanceType: calc.DistSFD,
		Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 30}})
	if err != nil || fu.DAF != 0 {
		t.Errorf("measured fuel: DAF %v, %v; want none", fu.DAF, err)
	}
	if _, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: desnzHGV, From: a, To: b}); !errors.Is(err, calc.ErrNoDAF) {
		t.Errorf("great-circle road leg: err %v, want ErrNoDAF", err)
	}
	// GLEC v3.3 road defaults include the +5%: per shortest feasible
	// distance, so a routed distance needs no DAF and an actual one is
	// divided by 1.05.
	g, err := calcLeg(calc.Leg{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 100, DistanceType: calc.DistSFD})
	if err != nil || g.DAF != 0 || math.Abs(g.WTWKg-125) > 1e-9 {
		t.Errorf("GLEC sfd: %+v, %v", g, err)
	}
	g, err = calcLeg(calc.Leg{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 105, DistanceType: calc.DistActual})
	if err != nil || math.Abs(g.WTWKg-125) > 1e-9 || !strings.Contains(g.DAFSource, "v3.3") {
		t.Errorf("GLEC actual: %+v, %v", g, err)
	}
}

// GLEC v3.2 (Section 1 Chapter 2, p. 21): TEU without a known mass is 10 t
// per TEU on average, 6 t light, 14.5 t heavy.
func TestContainerLoads_GLEC(t *testing.T) {
	ds, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"average": 10, "light": 6, "heavy": 14.5} {
		if v, err := ds.ContainerLoad(name); err != nil || v != want {
			t.Errorf("%s = %v, %v; want %v", name, v, err, want)
		}
	}
	c := calc.Calculator{Factors: ds}
	r, err := c.Calculate(context.Background(), calc.Shipment{TEU: 3, Legs: []calc.Leg{
		{Mode: calc.Sea, VehicleType: "container_ship", DistanceKm: 1000, DistanceType: calc.DistSFD}}})
	if err != nil || r.WeightKg != 30000 || r.ContainerLoad != "average" {
		t.Errorf("TEU alone: weight %v profile %q, %v", r.WeightKg, r.ContainerLoad, err)
	}
}

// DESNZ "outside of scopes": biogenic CO2 per unit, kept out of the totals
// and reported separately (GLEC Framework v3.2 p. 64).
func TestBiogenicCO2(t *testing.T) {
	ds, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]float64{"biofuel/biodiesel_hvo/litres": 2.43, "biofuel/biodiesel_hvo/kg": 3.12,
		"liquid_fuels/diesel_average_biofuel_blend/litres": 0.14, "liquid_fuels/diesel_100pct_mineral_diesel/litres": 0} {
		if f, err := ds.FuelFactor(id); err != nil || f.BiogenicCO2 != want {
			t.Errorf("%s: biogenic %v, %v; want %v", id, f.BiogenicCO2, err, want)
		}
	}
	c := calc.Calculator{Factors: ds}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10000, Legs: []calc.Leg{
		{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 300, Fuel: &calc.FuelUse{Fuel: "hvo", Amount: 100}}}})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := ds.FuelFactor("hvo")
	// "hvo" is GLEC v3.3's European HVO: biogenic 3.12 g/g × 0.77 kg/l.
	if math.Abs(r.BiogenicCO2Kg-100*3.12*0.77) > 1e-9 || math.Abs(r.WTWKg-100*h.WTW()) > 1e-9 || h.Fuel != "glec/fuel/eu/hvo/l" {
		t.Errorf("HVO leg: biogenic %v (want 240.24, outside WTW %v), fuel %s", r.BiogenicCO2Kg, r.WTWKg, h.Fuel)
	}
}

// GLEC v3.3 inland waterway defaults: per shortest feasible distance, with no
// DAF for an actual distance (GLEC Section 1 Chapter 4, p. 44), and a
// straight line from coordinates refused.
func TestInlandWaterway_GLEC33(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	for vt, want := range map[string][2]float64{"barge": {16.4, 4.9}, "container_barge": {22.5, 6.8}} {
		f, err := ds.LegFactor(calc.InlandWaterway, vt)
		if err != nil || f.TTW != want[0] || f.WTT != want[1] || f.DistanceBasis != calc.DistSFD {
			t.Errorf("%s = %+v, %v", vt, f, err)
		}
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances()}
	ctx := context.Background()
	for _, dt := range []string{calc.DistActual, calc.DistSFD} {
		r, err := c.Calculate(ctx, calc.Shipment{WeightKg: 1_000_000,
			Legs: []calc.Leg{{Mode: calc.InlandWaterway, VehicleType: "barge", DistanceKm: 220, DistanceType: dt}}})
		if err != nil || math.Abs(r.WTWKg-1000*220*21.3/1000) > 1e-6 {
			t.Errorf("%s distance: %v kg, %v", dt, r.WTWKg, err)
		}
	}
	_, err = c.Calculate(ctx, calc.Shipment{WeightKg: 1000,
		Legs: []calc.Leg{{Mode: calc.InlandWaterway, VehicleType: "barge",
			From: calc.Location{Lat: 51.95, Lon: 4.05}, To: calc.Location{Lat: 51.45, Lon: 6.73}}}})
	if !errors.Is(err, calc.ErrNoDAF) {
		t.Errorf("coordinates only: want ErrNoDAF, got %v", err)
	}
}

// GLEC v3.3 regional aliases: the region (or container trade lane) of both
// ends picks the factor; mixed or unknown ends use the alias's default.
func TestRegionalAliases_GLEC33(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	w := func(c string, lat, lon float64) calc.Where { return calc.Where{Country: c, Lat: lat, Lon: lon} }
	for _, tc := range []struct {
		mode     calc.Mode
		vt       string
		km       float64
		from, to calc.Where
		want     string
	}{
		{calc.Road, "hgv", 700, w("DE", 53.5, 10), w("DE", 48.1, 11.6), "glec/road/eu_sa/rigid_26_32t/diesel"},
		{calc.Road, "hgv", 700, w("US", 40, -80), w("US", 41, -87), "glec/road/na/tl_dry_van"},
		{calc.Road, "hgv", 700, w("US", 40, -80), w("MX", 25, -100), "glec/road/eu_sa/rigid_26_32t/diesel"},
		{calc.Road, "hgv", 700, w("CN", 31, 121), w("CN", 39.9, 116.4), "glec/road/cn/artic_14_24t/lng"},
		{calc.Road, "hgv", 700, w("IN", 19, 72.8), w("IN", 28.6, 77.1), "glec/road/in/heavy_cv_1/diesel"},
		{calc.Road, "van", 50, w("TH", 13, 100), w("TH", 13.5, 100.5), "glec/road/asia_africa/van_3_5t/diesel"},
		{calc.Road, "hgv_reefer", 500, w("NL", 52, 4.5), w("FR", 48.9, 2.3), "glec/road/eu_sa/temperature_controlled/rigid_26_32t/diesel"},
		{calc.Rail, "freight_train", 800, w("US", 40, -80), w("US", 41, -87), "glec/rail/na/average"},
		{calc.Rail, "freight_train", 800, w("DE", 50, 8.5), w("IT", 45, 9), "glec/rail/eu/average_traction_unknown"},
		{calc.Air, "air_freight", 800, w("IN", 19, 72.8), w("IN", 28.6, 77.1), "glec/air/in/domestic"},
		{calc.Air, "air_freight", 800, w("DE", 50, 8.5), w("FR", 49, 2.5), "glec/air/unknown/short_haul"},
		{calc.Air, "air_freight", 9000, w("DE", 50, 8.5), w("US", 40.6, -73.8), "glec/air/unknown/long_haul"},
		{calc.Sea, "container_ship", 20000, w("CN", 31.2, 121.5), w("DE", 53.5, 9.9), "glec/sea/container/asia_north_europe/dry"},
		{calc.Sea, "container_ship", 3000, w("ES", 39.4, -0.3), w("DE", 53.5, 9.9), "glec/sea/container/north_europe_med_bs/dry"},
		{calc.Sea, "container_ship", 3000, w("ES", 43.3, -2.9), w("DE", 53.5, 9.9), "glec/sea/container/intra_north_europe/dry"},
		{calc.Sea, "container_ship", 9000, w("US", 33.7, -118.2), w("CN", 22.5, 114), "glec/sea/container/asia_na_wc/dry"},
		{calc.Sea, "container_ship", 9000, w("US", 29.7, -95), w("CN", 22.5, 114), "glec/sea/container/asia_na_ec/dry"},
		{calc.Sea, "container_ship", 3000, w("", 0, 0), w("US", 33.7, -118.2), "glec/sea/container/industry_average/dry"},
		{calc.Sea, "container_ship", 9000, w("AU", -33.9, 151.2), w("ZA", -29.9, 31), "glec/sea/container/other/dry"},
		{calc.Sea, "container_ship_reefer", 12000, w("BR", -23.9, -46.3), w("NL", 51.9, 4.1), "glec/sea/container/europe_south_america/reefer"},
	} {
		f, note, err := ds.LegFactorAt(tc.mode, tc.vt, tc.km, tc.from, tc.to)
		if err != nil || f.VehicleType != tc.want || !strings.Contains(note, tc.want) {
			t.Errorf("%s %s %s->%s = %s (%q), %v; want %s", tc.mode, tc.vt, tc.from.Country, tc.to.Country, f.VehicleType, note, err, tc.want)
		}
	}
	// The aliases keep DESNZ's load models for a given load.
	if lm, err := ds.LoadModel(calc.Road, "hgv_artic_33t"); err != nil || lm.VehicleType != "hgv_non_refrigerated_all_diesel/articulated_33t" {
		t.Errorf("load model = %+v, %v", lm, err)
	}
	// Spot checks against GLEC v3.3 tables: Table 18 Asia-North Europe dry 42.9 g/TEU-km = 4.29 g/tkm at 10 t per TEU;
	// Table 3 transshipment 1.2 kg/t.
	if f, _ := ds.LegFactor(calc.Sea, "glec/sea/container/asia_north_europe/dry"); math.Abs(f.WTW()-4.29) > 1e-9 {
		t.Errorf("Asia-North Europe = %v g/tkm", f.WTW())
	}
	if h, err := ds.HubFactor("transshipment"); err != nil || h.WTWPerTonneG != 1200 {
		t.Errorf("transshipment hub = %+v, %v", h, err)
	}
}

// GLEC v3.3 Section 3 Module 4 (sea, p. 134 example): 10 TEU Hamburg to
// Shanghai, 21,000 km, Asia to-from North Europe 42.9 g CO2e/TEU-km = 9.01 t;
// 5 reefer TEU at 105.9 g = 11.12 t. With the 10 t per TEU average profile
// the per-tonne-km factors give the same (reefer: WTT + TTW = 106.0 g, the
// table's WTW rounds to 105.9).
func TestModule4Container_GLEC33(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances(), Places: places.Codes{}, NoRoadLegs: true}
	for _, tc := range []struct {
		vt   string
		teu  float64
		want float64
	}{{"container_ship", 10, 9009}, {"container_ship_reefer", 5, 11130}} {
		r, err := c.Calculate(context.Background(), calc.Shipment{TEU: tc.teu, Legs: []calc.Leg{{Mode: calc.Sea, VehicleType: tc.vt,
			From: calc.Location{Code: "DEHAM"}, To: calc.Location{Code: "CNSGH"}, DistanceKm: 21000, DistanceType: calc.DistSFD}}})
		if err != nil || math.Abs(r.WTWKg-tc.want) > 1e-6 {
			t.Errorf("%s: %v kg, %v; want %v", tc.vt, r.WTWKg, err, tc.want)
		}
	}
}

// GLEC v3.3 Module 1: regional fuel factors per litre (× GLEC's density),
// a later layer's fuel alias wins, electric trucks only where GLEC gives
// them, temperature-controlled rail +12%, intermodal terminals per container.
func TestGLEC33Additions(t *testing.T) {
	ds, err := Open(Default)
	if err != nil {
		t.Fatal(err)
	}
	w := func(c string, lat, lon float64) calc.Where { return calc.Where{Country: c, Lat: lat, Lon: lon} }
	for _, tc := range []struct {
		fuel     string
		from, to calc.Where
		want     string
		wtw      float64
	}{
		{"diesel", w("DE", 53.5, 10), w("FR", 48.9, 2.3), "glec/fuel/eu/diesel/l", 4.19 * 0.83},
		{"diesel", w("US", 41.9, -87.6), w("US", 40.7, -74), "glec/fuel/na/diesel/l", 3.83 * 0.847},
		{"diesel", w("CN", 31.2, 121.5), w("CN", 39.9, 116.4), "glec/fuel/cn/diesel/l", 4.18 * 0.818},
		{"diesel", w("IN", 19, 72.8), w("IN", 28.6, 77.1), "glec/fuel/in/diesel/l", 4.21 * 0.83},
		{"diesel", w("US", 41.9, -87.6), w("MX", 25, -100), "glec/fuel/eu/diesel/l", 4.19 * 0.83},
		{"jet_fuel", w("DE", 50, 8.5), w("US", 40.6, -73.8), "glec/fuel/air/jet_kerosene/l", 3.84 * 0.802},
	} {
		f, note, err := ds.FuelFactorAt(tc.fuel, tc.from, tc.to)
		if err != nil || f.Fuel != tc.want || math.Abs(f.WTW()-tc.wtw) > 1e-6 || f.Unit != "l" {
			t.Errorf("%s %s->%s = %s %v %s (%q), %v", tc.fuel, tc.from.Country, tc.to.Country, f.Fuel, f.WTW(), f.Unit, note, err)
		}
	}
	if f, err := ds.FuelFactor("glec/fuel/marine/hfo_vlsfo/t"); err != nil || math.Abs(f.WTW()-3840) > 1e-9 || f.Unit != "tonnes" {
		t.Errorf("VLSFO per tonne = %+v, %v", f, err)
	}
	if f, err := ds.FuelFactor("glec/fuel/eu/diesel_b7/l"); err != nil || math.Abs(f.WTW()-3.96*0.836) > 1e-9 || f.BiogenicCO2 != 0 {
		t.Errorf("B7 = %+v, %v", f, err)
	}
	// A fuel alias defined again in a later layer is that layer's.
	dir := t.TempDir()
	p := dir + "/fuel.json"
	if err := os.WriteFile(p, []byte(`{"name":"mine","fuel_aliases":{"diesel":"glec/fuel/eu/diesel_b7/l"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d2, err := Open(Default + "," + p)
	if err != nil {
		t.Fatal(err)
	}
	if f, _, err := d2.FuelFactorAt("diesel", w("US", 41.9, -87.6), w("US", 40.7, -74)); err != nil || f.Fuel != "glec/fuel/eu/diesel_b7/l" {
		t.Errorf("later fuel alias = %s, %v", f.Fuel, err)
	}

	// Electric trucks: Europe Table 11 × GLEC's European electricity (92.8 g/MJ = 334.08 g/kWh), all WTT.
	f, _, err := ds.LegFactorAt(calc.Road, "truck_electric", 500, w("DE", 53.5, 10), w("FR", 48.9, 2.3))
	if err != nil || math.Abs(f.WTT-0.12*334.08) > 1e-9 || f.TTW != 0 || f.DistanceBasis != calc.DistSFD {
		t.Errorf("truck_electric Europe = %+v, %v", f, err)
	}
	if f, _, err := ds.LegFactorAt(calc.Road, "van_electric", 50, w("CN", 31.2, 121.5), w("CN", 31.3, 121.4)); err != nil || f.VehicleType != "glec/road/cn/truck_4_5t/electricity" {
		t.Errorf("van_electric China = %+v, %v", f, err)
	}
	if _, _, err := ds.LegFactorAt(calc.Road, "truck_electric", 500, w("US", 41.9, -87.6), w("US", 40.7, -74)); !errors.Is(err, calc.ErrInvalidInput) {
		t.Errorf("truck_electric US: want an error, got %v", err)
	}
	if _, _, err := ds.LegFactorAt(calc.Road, "truck_electric", 500, w("BR", -23.5, -46.6), w("BR", -22.9, -43.2)); !errors.Is(err, calc.ErrInvalidInput) {
		t.Errorf("truck_electric Brazil: want an error, got %v", err)
	}
	// Temperature-controlled rail: +12%, not for North America.
	f, _, err = ds.LegFactorAt(calc.Rail, "freight_train_reefer", 800, w("DE", 50, 8.5), w("IT", 45, 9))
	if err != nil || math.Abs(f.WTW()-1.12*(0.38*7.1+0.62*10.8+0.38*23.6)) > 1e-3 {
		t.Errorf("reefer rail Europe = %v, %v", f.WTW(), err)
	}
	if _, _, err := ds.LegFactorAt(calc.Rail, "freight_train_reefer", 800, w("US", 41.9, -87.6), w("US", 40.7, -74)); !errors.Is(err, calc.ErrInvalidInput) {
		t.Errorf("reefer rail US: want an error, got %v", err)
	}
	// Intermodal terminals: per container handled.
	c := calc.Calculator{Factors: ds}
	ship := calc.Shipment{WeightKg: 20000, Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 10, DistanceType: calc.DistSFD}},
		Hubs: []calc.HubStop{{ID: "t", HubType: "intermodal_terminal", Containers: 2}}}
	r, err := c.Calculate(context.Background(), ship)
	if err != nil || len(r.Hubs) != 1 || math.Abs(r.Hubs[0].WTWKg-36.4) > 1e-9 || r.Hubs[0].Containers != 2 {
		t.Errorf("terminal = %+v, %v", r.Hubs, err)
	}
	ship.Hubs[0].Containers = 0
	if _, err := c.Calculate(context.Background(), ship); !errors.Is(err, calc.ErrInvalidInput) {
		t.Errorf("terminal without containers: want an error, got %v", err)
	}
}
