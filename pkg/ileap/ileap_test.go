package ileap

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
	"github.com/majidkabir/sootcase/pkg/toc"
)

func dataset(t *testing.T) *factors.Dataset {
	t.Helper()
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func opts(ds *factors.Dataset) Options {
	return Options{
		CompanyName: "Acme Logistics", CreatedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Fuels: ds, Country: places.Codes{}.Country, City: places.Codes{}.Town,
	}
}

func calculate(t *testing.T, ds *factors.Dataset, s calc.Shipment) calc.Result {
	t.Helper()
	r, err := calc.Calculator{Factors: ds}.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var (
	cph = calc.Location{Name: "Copenhagen", Lat: 55.6761, Lon: 12.5683}
	ham = calc.Location{Name: "Hamburg", Lat: 53.5511, Lon: 9.9937}
	rtm = calc.Location{Name: "Rotterdam", Lat: 51.9244, Lon: 4.4777}
)

// A road leg on a GLEC default value is fully conformant: the TOC names
// diesel (the GLEC row says so) with GLEC Module 1's EU diesel factors,
// and the TCE equals transport activity x the TOC's intensity.
func TestRoadDefault(t *testing.T) {
	ds := dataset(t)
	r := calculate(t, ds, calc.Shipment{ID: "S1", WeightKg: 12000, Legs: []calc.Leg{
		{ID: "l1", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD},
	}})
	ex := FromResults([]calc.Result{r}, opts(ds))
	if !ex.Conformant || len(ex.Gaps) > 0 {
		t.Fatalf("want conformant, gaps %+v, problems %v", ex.Gaps, Validate(ex))
	}
	sf := ex.ShipmentFootprints[0]
	tce := sf.TCEs[0]
	if tce.TCEID != "S1/l1" || tce.Mass != "12000" || tce.Distance.SFD != "332" || tce.TransportActivity != "3984" ||
		tce.CO2eWTW != "498" || tce.CO2eTTW != "382.464" || tce.PrevTCEIDs == nil || len(tce.PrevTCEIDs) != 0 {
		t.Errorf("tce %+v", tce)
	}
	if tce.Origin == nil || tce.Origin.City != "Copenhagen" || tce.Origin.Country != "DK" || tce.Destination.Country != "DE" {
		t.Errorf("locations %+v %+v", tce.Origin, tce.Destination)
	}
	toc := ex.TOCs[0]
	if toc.TOCID != tce.TOCID || toc.Mode != "Road" || toc.CO2eIntensityWTW != "0.125" || toc.CO2eIntensityTTW != "0.096" ||
		toc.TransportActivityUnit != "tkm" || len(toc.EnergyCarriers) != 1 {
		t.Fatalf("toc %+v", toc)
	}
	ec := toc.EnergyCarriers[0]
	diesel, _ := ds.FuelFactor("glec/fuel/eu/diesel/l")
	if ec.EnergyCarrier != Diesel || ec.RelativeShare != "1" || ec.EnergyConsumptionUnit != "l" ||
		ec.EmissionFactorWTW != dec(diesel.WTW()) || ec.EmissionFactorTTW != dec(diesel.TTW) {
		t.Errorf("energy carrier %+v (GLEC diesel %+v)", ec, diesel)
	}
	if len(sf.SecondaryEmissionFactorSources) != 1 || sf.SecondaryEmissionFactorSources[0] != (SourceRef{"GLEC Framework", "3.3"}) {
		t.Errorf("sources %+v", sf.SecondaryEmissionFactorSources)
	}
	// Decimals are JSON strings.
	b, _ := json.Marshal(sf)
	if !strings.Contains(string(b), `"co2eTTW":"382.464"`) || !strings.Contains(string(b), `"mass":"12000"`) {
		t.Errorf("json %s", b)
	}
}

// What a default value doesn't say stays out and is listed: the Clean
// Cargo container factor names no fuel; GLEC hub values have no TTW.
func TestGaps(t *testing.T) {
	ds := dataset(t)
	r := calculate(t, ds, calc.Shipment{ID: "S2", WeightKg: 12000, Legs: []calc.Leg{
		{ID: "road", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD},
		{ID: "sea", Mode: calc.Sea, VehicleType: "container_ship", From: ham, To: rtm, DistanceKm: 560},
	}, Hubs: []calc.HubStop{{ID: "ham", HubType: "transshipment"}}})
	ex := FromResults([]calc.Result{r}, opts(ds))
	if ex.Conformant {
		t.Fatal("want not conformant")
	}
	want := map[string]bool{"toc energyCarriers": false, "tce co2eTTW": false, "hoc co2eIntensityTTW": false, "hoc energyCarriers": false}
	for _, g := range ex.Gaps {
		k := g.Object + " " + g.Field
		if _, ok := want[k]; ok {
			want[k] = true
		} else {
			t.Errorf("unexpected gap %+v", g)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing gap %s", k)
		}
	}
	sf := ex.ShipmentFootprints[0]
	if len(sf.TCEs) != 3 || sf.TCEs[0].PrevTCEIDs != nil {
		t.Errorf("tces %+v (no order with hubs)", sf.TCEs)
	}
	hub := sf.TCEs[2]
	if hub.HOCID == "" || hub.TOCID != "" || hub.Distance.Actual != "0" || hub.TransportActivity != "0" || hub.CO2eWTW != "14.4" || hub.CO2eTTW != "" {
		t.Errorf("hub tce %+v", hub)
	}
	h := ex.HOCs[0]
	if h.HubType != "Transshipment" || h.TemperatureControl != "ambient" || h.CO2eIntensityWTW != "1.2" || h.HubActivityUnit != "tonnes" {
		t.Errorf("hoc %+v", h)
	}
	sea := sf.TCEs[1]
	if sea.Distance.SFD != "560" || sea.PackagingOrTrEq != "Container-TEU" || sea.PackagingAmount != "1.2" {
		t.Errorf("sea tce %+v (the factor's distance basis; 12 t / 10 t per TEU)", sea)
	}
	// A container leg's TOC is per TEU-km, and TEU x km x intensity = the TCE.
	for _, x := range ex.TOCs {
		if x.TOCID != sea.TOCID {
			continue
		}
		w, _ := num(x.CO2eIntensityWTW)
		e, _ := num(sea.CO2eWTW)
		if x.TransportActivityUnit != "TEUkm" || math.Abs(1.2*560*w-e) > 1e-6*e {
			t.Errorf("sea toc %+v vs tce %s", x, sea.CO2eWTW)
		}
	}
}

// The operator's own data makes complete TOCs and HOCs: energy carriers
// with consumption per unit of activity, and the activity shares the
// operator gives when there are several carriers.
func TestOperations(t *testing.T) {
	ds := dataset(t)
	f, err := os.Open("../../examples/operations.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ops, err := toc.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	ex := FromOperations(ops, ds, opts(ds))
	gaps := map[string]string{}
	for _, g := range ex.Gaps {
		gaps[g.ID+" "+g.Field] = g.Reason
	}
	// The example gives no hub types and no activity shares for mixed fuels.
	for _, k := range []string{"dk_road_ambient energyCarriers.relativeShare", "cph_crossdock hubType", "cph_crossdock energyCarriers.relativeShare"} {
		if gaps[k] == "" {
			t.Errorf("missing gap %s (have %v)", k, gaps)
		}
	}
	var el TOC
	for _, x := range ex.TOCs {
		if x.TOCID == "dk_road_electric" {
			el = x
		}
	}
	grid, _ := ds.FuelFactor("electricity/DNK")
	ec := el.EnergyCarriers[0]
	if ec.EnergyCarrier != Electric || ec.EnergyConsumptionUnit != "kWh" || ec.EnergyConsumption != dec(260000.0/900000) ||
		ec.EmissionFactorWTW != dec(grid.WTW()) || len(ec.Feedstocks) != 1 || ec.Feedstocks[0].Feedstock != "Grid" ||
		ec.Feedstocks[0].RegionProvenance != "DNK" || el.CO2eIntensityWTW != dec(260000*grid.WTW()/900000) {
		t.Errorf("electric toc %+v / %+v", el, ec)
	}

	// With shares and hub types given, everything is there.
	share := func(x float64) *float64 { return &x }
	ops.TOCs[0].Fuels[0].ActivityShare, ops.TOCs[0].Fuels[1].ActivityShare = share(0.75), share(0.25)
	for i := range ops.HOCs {
		ops.HOCs[i].HubType = "warehouse"
		for j := range ops.HOCs[i].Energy {
			ops.HOCs[i].Energy[j].ActivityShare = share(1 / float64(len(ops.HOCs[i].Energy)))
		}
	}
	ops.HOCs[2].HubType = "maritime_container_terminal"
	ex = FromOperations(ops, ds, opts(ds))
	if !ex.Conformant {
		t.Fatalf("gaps %+v, problems %v", ex.Gaps, Validate(ex))
	}
	for _, h := range ex.HOCs {
		switch h.HOCID {
		case "aarhus_terminal":
			if h.HubActivityUnit != "TEU" || h.HubType != "MaritimeContainerTerminal" || !strings.Contains(h.Comment, "modelled") {
				t.Errorf("terminal %+v", h)
			}
		case "cph_cold_store":
			if !strings.Contains(h.Comment, "refrigerant") {
				t.Errorf("cold store comment %q", h.Comment)
			}
			wtw, _ := num(h.CO2eIntensityWTW)
			ttw, _, _ := toc.Totals(ops.HOCs[1].Energy, ops.HOCs[1].Refrigerants, ds)
			if ttwI, _ := num(h.CO2eIntensityTTW); math.Abs(ttwI-ttw/30000) > 1e-9 || wtw < ttwI {
				t.Errorf("cold store intensities %+v", h)
			}
		}
	}

	// Shipments on the operator's TOC refer to it.
	layer, err := toc.Build(ops, ds)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/layer.json"
	raw, _ := json.Marshal(layer)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	both, err := factors.Open(factors.Default + "," + path)
	if err != nil {
		t.Fatal(err)
	}
	r := calculate(t, both, calc.Shipment{ID: "S3", WeightKg: 5000, Legs: []calc.Leg{
		{ID: "l1", Mode: calc.Road, VehicleType: "dk_road_electric", From: cph, To: ham, DistanceKm: 50, DistanceType: calc.DistSFD},
	}})
	o := opts(ds)
	o.Operator = Operator(ex)
	sx := FromResults([]calc.Result{r}, o)
	if sx.ShipmentFootprints[0].TCEs[0].TOCID != "dk_road_electric" || len(sx.TOCs) != 1 || !sx.Conformant {
		t.Errorf("shipment on operator TOC: %+v gaps %+v", sx.TOCs, sx.Gaps)
	}
}

func TestDecimal(t *testing.T) {
	for x, want := range map[float64]Decimal{0.1 + 0.2: "0.3", 1e-7: "0.0000001", 12000: "12000", 2.5e9: "2500000000", 0: "0"} {
		if got := dec(x); got != want {
			t.Errorf("dec(%v) = %q, want %q", x, got, want)
		}
	}
	for _, s := range []Decimal{"1e5", "1,5", "", "abc", "+1"} {
		if _, ok := num(s); ok {
			t.Errorf("num(%q) ok", s)
		}
	}
}

func TestCarrier(t *testing.T) {
	for id, want := range map[string]string{
		"glec/road/eu_sa/artic_34_40t/diesel": Diesel, "glec/road/eu_sa/artic_34_40t/bio_lng": LNG,
		"glec/road/eu_sa/artic_34_40t/lng_diesel": "", "glec/road/eu_sa/rigid_7_5_12t/electricity": Electric,
		"glec/road/na/tl_dry_van": "", "vans/average_up_to_3_5_tonnes/plug_in_hybrid_electric_vehicle": "",
		"vans/average_up_to_3_5_tonnes/battery_electric_vehicle": Electric, "hgv_refrigerated_all_diesel/rigid_17_tonnes/average_laden": Diesel,
	} {
		if got := vehicleCarrier(id, calc.Road); got != want {
			t.Errorf("road %s: %q, want %q", id, got, want)
		}
	}
	for id, want := range map[string]string{
		"glec/rail/eu/average_traction_unknown": "", "glec/rail/eu/diesel/container": Diesel, "glec/rail/eu/electric/cars": Electric,
		"rail/freight_train": "",
	} {
		if got := vehicleCarrier(id, calc.Rail); got != want {
			t.Errorf("rail %s: %q, want %q", id, got, want)
		}
	}
	for id, want := range map[string]string{
		"glec/sea/container/intra_north_europe/dry": "", "glec/sea/bulk_carrier/0-9999_dwt/mdo": MGO,
		"glec/sea/bulk_carrier/0-9999_dwt/vlsfo": HFO, "sea_tanker/lpg_tanker/average": "", "sea_tanker/lng_tanker/average": "",
	} {
		if got := vehicleCarrier(id, calc.Sea); got != want {
			t.Errorf("sea %s: %q, want %q", id, got, want)
		}
	}
	if vehicleCarrier("glec/air/unknown/long_haul", calc.Air) != Aviation || vehicleCarrier("glec/iww/tanker", calc.InlandWaterway) != "" {
		t.Error("air / waterway")
	}
	for id, want := range map[string]string{"glec/fuel/eu/diesel_b7/l": Diesel, "glec/fuel/eu/hvo/l": HVO, "glec/fuel/eu/biodiesel/l": "",
		"electricity/DNK": Electric, "gaseous_fuels/natural_gas/kwh_net_cv": "", "glec/fuel/marine/hfo_vlsfo/kg": HFO, "glec/fuel/eu/diesel_hvo20/l": Diesel} {
		if got := carrier(id); got != want {
			t.Errorf("fuel %s: %q, want %q", id, got, want)
		}
	}
}

func TestAir(t *testing.T) {
	for id, want := range map[string][2]string{
		"glec/air/freighter/long_haul": {"freighter", "long-haul"}, "glec/air/belly/short_haul": {"belly freight", "short-haul"},
		"glec/air/unknown/short_haul": {"", "short-haul"}, "glec/air/in/domestic": {"", ""}, "freight_flights/x/y": {"", ""},
	} {
		if o, l := air(id); o != want[0] || l != want[1] {
			t.Errorf("%s: %q %q", id, o, l)
		}
	}
}

// Given in TEU: the TCE carries the TEU given, on any sea leg; road stays per tonne-km.
func TestTEUGiven(t *testing.T) {
	ds := dataset(t)
	r := calculate(t, ds, calc.Shipment{ID: "T", TEU: 2, Legs: []calc.Leg{
		{ID: "road", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD},
		{ID: "sea", Mode: calc.Sea, VehicleType: "container_ship", From: ham, To: rtm, DistanceKm: 560},
	}})
	if r.TEU != 2 {
		t.Fatalf("result teu %v", r.TEU)
	}
	ex := FromResults([]calc.Result{r}, opts(ds))
	tces := ex.ShipmentFootprints[0].TCEs
	if tces[0].PackagingAmount != "" || tces[1].PackagingAmount != "2" {
		t.Errorf("tces %+v", tces)
	}
	units := map[string]string{}
	for _, x := range ex.TOCs {
		units[x.TOCID] = x.TransportActivityUnit
	}
	if units[tces[0].TOCID] != "tkm" || units[tces[1].TOCID] != "TEUkm" {
		t.Errorf("units %v", units)
	}
}

// GLEC's inland waterway rows and US rail average say diesel in their text.
func TestCarrierFromSource(t *testing.T) {
	ds := dataset(t)
	for id, want := range map[string]string{"glec/iww/motor_vessel_85_110m": Diesel, "glec/iww/container_110m": Diesel,
		"glec/rail/na/average": Diesel, "glec/rail/in/average": "", "glec/rail/eu/average_traction_unknown": "",
		"barge_container_96teu": ""} {
		var f calc.Factor
		for _, x := range ds.Legs {
			if x.VehicleType == id {
				f = x
			}
		}
		if f.VehicleType == "" {
			t.Fatalf("no factor %s", id)
		}
		if got := factorCarrier(f); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	b := newBuilder(opts(ds))
	var iww calc.Factor
	for _, x := range ds.Legs {
		if x.VehicleType == "glec/iww/container_110m" {
			iww = x
		}
	}
	ec, why, src := b.defaultCarrier(iww)
	if why != "" || len(ec) != 1 || ec[0].EmissionFactorWTW == "" || !strings.HasPrefix(src, "GLEC Framework v3.3") {
		t.Errorf("iww carrier %+v %q %q", ec, why, src)
	}
}

// Every TCE holds together: activity = mass x distance, emissions =
// activity x its TOC's intensity (TEU x km for TEU-km TOCs), and the TCEs
// add up to the shipment.
func TestConsistency(t *testing.T) {
	ds := dataset(t)
	ships := []calc.Shipment{
		{ID: "road", WeightKg: 12000, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD}}},
		{ID: "actual", WeightKg: 8000, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "hgv_artic", From: cph, To: ham, DistanceKm: 350, DistanceType: calc.DistActual}}},
		{ID: "load", WeightKg: 8000, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 350, DistanceType: calc.DistSFD, LoadFactor: 0.6, EmptyTripFactor: 0.25}}},
		{ID: "electric", WeightKg: 5000, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "truck_electric", From: cph, To: ham, DistanceKm: 300, DistanceType: calc.DistSFD, EnergyKWhPerTkm: 0.08, Country: "DNK"}}},
		{ID: "fuel", WeightKg: 3500, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "hgv", DistanceKm: 610, DistanceType: calc.DistActual, Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 190, VehicleTonneKm: 14000}}}},
		{ID: "teu", TEU: 3, Legs: []calc.Leg{{ID: "s", Mode: calc.Sea, VehicleType: "container_ship", From: ham, To: rtm, DistanceKm: 560}, {ID: "b", Mode: calc.InlandWaterway, VehicleType: "container_barge", From: rtm, To: ham, DistanceKm: 200, DistanceType: calc.DistSFD}}},
		{ID: "air", WeightKg: 300, Legs: []calc.Leg{{ID: "f", Mode: calc.Air, VehicleType: "air_freighter", From: cph, To: rtm, DistanceKm: 600, DistanceType: calc.DistGCD}}},
		{ID: "hub", WeightKg: 10000, Legs: []calc.Leg{{ID: "a", Mode: calc.Road, VehicleType: "hgv", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD}}, Hubs: []calc.HubStop{{ID: "h", HubType: "warehouse"}}},
	}
	var results []calc.Result
	for _, s := range ships {
		results = append(results, calculate(t, ds, s))
	}
	// A road leg with a ferry part, as routing gives it.
	r := results[0]
	l := r.Legs[0]
	ferry := calc.LegPart{Kind: calc.PartFerry, Mode: calc.Sea, DistanceKm: 20, TonneKm: 240, TTWKg: 5, WTTKg: 1, WTWKg: 6, Method: calc.MethodIntensity, DataType: calc.DataDefault,
		Factor: &calc.Factor{Mode: calc.Sea, VehicleType: "cargo_ship/large_ropax_ferry/average", TTW: 20.8, WTT: 4.2, Source: "DESNZ 2026: test"}}
	l.DistanceKm, l.TonneKm, l.TTWKg, l.WTTKg, l.WTWKg = l.DistanceKm+20, l.TonneKm+240, l.TTWKg+5, l.WTTKg+1, l.WTWKg+6
	l.Parts = []calc.LegPart{ferry}
	r.ShipmentID, r.Legs, r.WTWKg = "ferry", []calc.LegResult{l}, r.WTWKg+6
	results = append(results, r)

	ex := FromResults(results, opts(ds))
	tocs := map[string]TOC{}
	for _, x := range ex.TOCs {
		tocs[x.TOCID] = x
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }
	for i, sf := range ex.ShipmentFootprints {
		sum := 0.0
		for _, tce := range sf.TCEs {
			mass, _ := num(tce.Mass)
			act, _ := num(tce.TransportActivity)
			wtw, _ := num(tce.CO2eWTW)
			sum += wtw
			if tce.HOCID != "" {
				continue
			}
			km := 0.0
			for _, d := range []Decimal{tce.Distance.Actual, tce.Distance.GCD, tce.Distance.SFD} {
				if x, ok := num(d); ok {
					km = x
				}
			}
			if !near(act, mass/1000*km) {
				t.Errorf("%s: activity %v != %v t x %v km", tce.TCEID, act, mass/1000, km)
			}
			o := tocs[tce.TOCID]
			iw, _ := num(o.CO2eIntensityWTW)
			base := act
			if o.TransportActivityUnit == UnitTEUkm {
				teu, _ := num(tce.PackagingAmount)
				base = teu * km
			}
			if !near(wtw, base*iw) {
				t.Errorf("%s: WTW %v != %v x %v (%s)", tce.TCEID, wtw, base, iw, o.TransportActivityUnit)
			}
		}
		if !near(sum, results[i].WTWKg) {
			t.Errorf("%s: TCEs add up to %v, shipment %v", sf.ShipmentID, sum, results[i].WTWKg)
		}
	}
	// The ferry is a TCE of its own; the load model's empty factor is empty km / all km.
	f := ex.ShipmentFootprints[len(ex.ShipmentFootprints)-1]
	if len(f.TCEs) != 2 || f.TCEs[1].TCEID != "ferry/a/ferry" || tocs[f.TCEs[1].TOCID].Mode != "Sea" || f.TCEs[0].PrevTCEIDs != nil {
		t.Errorf("ferry split %+v", f.TCEs)
	}
	if o := tocs[ex.ShipmentFootprints[2].TCEs[0].TOCID]; o.EmptyDistanceFactor != "0.2" || o.LoadFactor != "0.6" ||
		len(o.EnergyCarriers) != 1 || o.EnergyCarriers[0].EmissionFactorWTW == "" || len(o.SecondaryEmissionFactorSources) == 0 ||
		o.SecondaryEmissionFactorSources[0].Name != "UK Government GHG Conversion Factors for Company Reporting (DESNZ)" {
		t.Errorf("load toc %+v (0.25 empty per loaded km = 0.2 of all km)", o)
	}
	if o := tocs[ex.ShipmentFootprints[3].TCEs[0].TOCID]; len(o.EnergyCarriers) != 1 || o.EnergyCarriers[0].EnergyCarrier != Electric ||
		o.EnergyCarriers[0].EnergyConsumption != "0.08" || o.EnergyCarriers[0].Feedstocks[0].RegionProvenance != "DNK" {
		t.Errorf("electric toc %+v", o)
	}
	if p := Validate(ex); len(p) == 0 {
		t.Log("valid")
	}
}

func TestLocationCodeName(t *testing.T) {
	b := &builder{opts: Options{Country: func(calc.Location) string { return "CN" }}}
	l := b.location(&calc.Location{Name: "Shanghai Pt (CNSHG)", Code: "CNSHG", Lat: 30.6, Lon: 122.1})
	if l == nil || l.City != "Shanghai Pt" || l.LOCODE != "CNSHG" || l.Country != "CN" {
		t.Fatalf("location = %+v", l)
	}
}
