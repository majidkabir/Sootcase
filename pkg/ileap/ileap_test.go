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
		Fuels: ds, Country: places.Codes{}.Country,
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
	if sea := sf.TCEs[1]; sea.Distance.SFD != "560" {
		t.Errorf("sea distance %+v (the factor's basis)", sea.Distance)
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
