package toc

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

const ops = `{
  "name": "acme", "period": "2025", "year": 2025,
  "tocs": [{"id": "road_dk_ambient", "mode": "road", "tonne_km": 1000000,
            "fuels": [{"fuel": "diesel", "amount": 20000}, {"fuel": "hvo", "amount": 5000}]}],
  "hocs": [{"id": "cph_crossdock", "tonnes": 50000,
            "energy": [{"fuel": "electricity/DNK", "amount": 400000}]}]
}`

func TestBuild_AgainstDESNZ(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Parse(strings.NewReader(ops))
	if err != nil {
		t.Fatal(err)
	}
	layer, err := Build(o, ds)
	if err != nil {
		t.Fatal(err)
	}

	diesel, _ := ds.FuelFactor("diesel")
	hvo, _ := ds.FuelFactor("hvo")
	grid, _ := ds.Grid("DNK")
	wantTTW := (20000*diesel.TTW + 5000*hvo.TTW) * 1000 / 1e6
	wantWTT := (20000*diesel.WTT + 5000*hvo.WTT) * 1000 / 1e6
	leg := layer.Legs[0]
	if math.Abs(leg.TTW-wantTTW) > 1e-9 || math.Abs(leg.WTT-wantWTT) > 1e-9 || leg.DataType != calc.DataPrimary ||
		leg.VehicleType != "road_dk_ambient" || leg.Year != 2025 {
		t.Errorf("toc factor = %+v, want TTW %v WTT %v", leg, wantTTW, wantWTT)
	}
	hub := layer.Hubs[0]
	if want := 400000 * grid.G / 50000; math.Abs(hub.WTWPerTonneG-want) > 1e-9 || hub.DataType != calc.DataPrimary {
		t.Errorf("hoc factor = %+v, want %v g/t", hub, want)
	}

	// Layer on DESNZ and calculate a shipment through both.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(layer); err != nil {
		t.Fatal(err)
	}
	merged, err := layered(t, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: merged}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10_000,
		Legs: []calc.Leg{{ID: "toc", Mode: calc.Road, VehicleType: "road_dk_ambient", DistanceKm: 200},
			{ID: "avg", Mode: calc.Road, VehicleType: "hgv", DistanceKm: 50}},
		Hubs: []calc.HubStop{{ID: "h", HubType: "cph_crossdock"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Legs[0].DataType != calc.DataPrimary || r.Legs[1].DataType != calc.DataDefault || r.Hubs[0].DataType != calc.DataPrimary {
		t.Errorf("data types %q %q %q", r.Legs[0].DataType, r.Legs[1].DataType, r.Hubs[0].DataType)
	}
	if want := 2000 * (wantTTW + wantWTT) / 1000; math.Abs(r.Legs[0].WTWKg-want) > 1e-9 {
		t.Errorf("toc leg = %v kg, want %v", r.Legs[0].WTWKg, want)
	}
	// The HOC keeps its TTW/WTT split (ISO 14083 13.3.2), and so does the hub's result.
	el, _ := ds.FuelFactor("electricity/DNK")
	if hub.TTWPerTonneG == nil || hub.WTTPerTonneG == nil ||
		math.Abs(*hub.TTWPerTonneG+*hub.WTTPerTonneG-hub.WTWPerTonneG) > 1e-9 ||
		math.Abs(*hub.TTWPerTonneG-400000*el.TTW*1000/50000) > 1e-9 {
		t.Errorf("hoc split = %v + %v, want %v total", hub.TTWPerTonneG, hub.WTTPerTonneG, hub.WTWPerTonneG)
	}
	if h := r.Hubs[0]; h.TTWKg == nil || h.WTTKg == nil || math.Abs(*h.TTWKg+*h.WTTKg-h.WTWKg) > 1e-9 {
		t.Errorf("hub result split = %v + %v, want %v", h.TTWKg, h.WTTKg, h.WTWKg)
	}
	wantShare := (r.Legs[0].WTWKg + r.Hubs[0].WTWKg) / r.WTWKg
	if math.Abs(r.PrimaryDataShare-wantShare) > 1e-12 {
		t.Errorf("primary share %v, want %v", r.PrimaryDataShare, wantShare)
	}
}

func layered(t *testing.T, layer []byte) (*factors.Dataset, error) {
	t.Helper()
	p := t.TempDir() + "/layer.json"
	if err := writeFile(p, layer); err != nil {
		return nil, err
	}
	return factors.Open(factors.Default + "," + p)
}

func TestParse_Invalid(t *testing.T) {
	cases := map[string]string{
		"empty":                 `{"name":"x"}`,
		"unknown key":           `{"tocs":[{"id":"a","mode":"road","tonne_km":1,"fuels":[{"fuel":"diesel","amount":1}]}],"extra":1}`,
		"bad id":                `{"tocs":[{"id":"Bad ID","mode":"road","tonne_km":1,"fuels":[{"fuel":"diesel","amount":1}]}]}`,
		"dup id":                `{"tocs":[{"id":"a","mode":"road","tonne_km":1,"fuels":[{"fuel":"d","amount":1}]}],"hocs":[{"id":"a","tonnes":1,"energy":[{"fuel":"d","amount":1}]}]}`,
		"no mode":               `{"tocs":[{"id":"a","tonne_km":1,"fuels":[{"fuel":"diesel","amount":1}]}]}`,
		"zero tkm":              `{"tocs":[{"id":"a","mode":"road","tonne_km":0,"fuels":[{"fuel":"diesel","amount":1}]}]}`,
		"no fuels":              `{"tocs":[{"id":"a","mode":"road","tonne_km":1,"fuels":[]}]}`,
		"neg amount":            `{"tocs":[{"id":"a","mode":"road","tonne_km":1,"fuels":[{"fuel":"diesel","amount":-1}]}]}`,
		"zero tonnes":           `{"hocs":[{"id":"h","tonnes":0,"energy":[{"fuel":"diesel","amount":1}]}]}`,
		"tonnes and containers": `{"hocs":[{"id":"h","tonnes":1,"containers":1,"energy":[{"fuel":"diesel","amount":1}]}]}`,
		"neg containers":        `{"hocs":[{"id":"h","containers":-1,"energy":[{"fuel":"diesel","amount":1}]}]}`,
		"default data type":     `{"hocs":[{"id":"h","tonnes":1,"data_type":"default","energy":[{"fuel":"diesel","amount":1}]}]}`,
		"neg refrigerant":       `{"hocs":[{"id":"h","tonnes":1,"energy":[{"fuel":"diesel","amount":1}],"refrigerants":[{"refrigerant":"R-404A","kg":-1}]}]}`,
		"no refrigerant name":   `{"hocs":[{"id":"h","tonnes":1,"energy":[{"fuel":"diesel","amount":1}],"refrigerants":[{"refrigerant":" ","kg":1}]}]}`,
		"gwp without source":    `{"hocs":[{"id":"h","tonnes":1,"energy":[{"fuel":"diesel","amount":1}],"refrigerants":[{"refrigerant":"X","kg":1,"gwp":10}]}]}`,
		"source without gwp":    `{"hocs":[{"id":"h","tonnes":1,"energy":[{"fuel":"diesel","amount":1}],"refrigerants":[{"refrigerant":"X","kg":1,"gwp_source":"s"}]}]}`,
		"toc data type":         `{"tocs":[{"id":"a","mode":"road","tonne_km":1,"data_type":"measured","fuels":[{"fuel":"diesel","amount":1}]}]}`,
	}
	for name, in := range cases {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBuild_UnknownFuel(t *testing.T) {
	ds, _ := factors.Open(factors.Default)
	o, err := Parse(strings.NewReader(`{"tocs":[{"id":"a","mode":"road","tonne_km":1,"fuels":[{"fuel":"unobtainium","amount":1}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(o, ds); err == nil || !strings.Contains(err.Error(), `toc "a"`) {
		t.Errorf("want error naming the toc, got %v", err)
	}
}

func writeFile(p string, b []byte) error { return os.WriteFile(p, b, 0o600) }

func TestDistanceBasis(t *testing.T) {
	ds, _ := factors.Open(factors.Default)
	o, err := Parse(strings.NewReader(`{"tocs":[{"id":"sea_x","mode":"sea","tonne_km":1000,"distance_basis":"actual","fuels":[{"fuel":"marine_fuel_oil","amount":10}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	layer, err := Build(o, ds)
	if err != nil || layer.Legs[0].DistanceBasis != "actual" {
		t.Fatalf("basis not carried: %+v %v", layer.Legs, err)
	}
	if _, err := Parse(strings.NewReader(`{"tocs":[{"id":"a","mode":"sea","tonne_km":1,"distance_basis":"nm","fuels":[{"fuel":"x","amount":1}]}]}`)); err == nil {
		t.Error("bad basis: want error")
	}
}

// GLEC's hub defaults are WTW only: the result has no TTW/WTT split, rather
// than a zero one.
func TestHubDefault_WTWOnly(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 10_000,
		Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 50}},
		Hubs: []calc.HubStop{{ID: "w", HubType: "warehouse"}}})
	if err != nil {
		t.Fatal(err)
	}
	h := r.Hubs[0]
	if h.WTWKg <= 0 || h.TTWKg != nil || h.WTTKg != nil {
		t.Errorf("default hub = %+v, want WTW only", h)
	}
	b, _ := json.Marshal(h)
	if strings.Contains(string(b), "ttw_kg") || strings.Contains(string(b), "wtt_kg") {
		t.Errorf("default hub JSON has a split: %s", b)
	}
}

// Refrigerant losses add to TTW: from the dataset's GWP (by name or alias),
// or from the user's own GWP with its source; an unlisted refrigerant
// without one is refused.
func TestRefrigerants(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	build := func(refs string) (factors.Dataset, error) {
		o, err := Parse(strings.NewReader(`{"name":"a","period":"2025","hocs":[{"id":"cold","tonnes":1000,
		  "energy":[{"fuel":"electricity/DNK","amount":1000}],"refrigerants":` + refs + `}]}`))
		if err != nil {
			return factors.Dataset{}, err
		}
		return Build(o, ds)
	}
	plain, err := build(`[]`)
	if err != nil {
		t.Fatal(err)
	}
	base := *plain.Hubs[0].TTWPerTonneG
	for refs, gwp := range map[string]float64{
		`[{"refrigerant":"R-404A","kg":10}]`:                                          4728,
		`[{"refrigerant":"FX 100","kg":10}]`:                                          2396.7,
		`[{"refrigerant":"R-717","kg":10,"gwp":0,"gwp_source":"own: not a GHG"}]`:     0,
		`[{"refrigerant":"R-404A","kg":10,"gwp":3922,"gwp_source":"IPCC AR4 (own)"}]`: 3922,
	} {
		layer, err := build(refs)
		if err != nil {
			t.Fatalf("%s: %v", refs, err)
		}
		h := layer.Hubs[0]
		if want := base + 10*gwp*1000/1000; math.Abs(*h.TTWPerTonneG-want) > 1e-9 || math.Abs(h.WTWPerTonneG-want-*h.WTTPerTonneG) > 1e-9 {
			t.Errorf("%s: TTW %v g/t, want %v", refs, *h.TTWPerTonneG, want)
		}
		if !strings.Contains(h.Source, "kg") || !strings.Contains(h.Source, "lost") {
			t.Errorf("%s: source doesn't list the loss: %s", refs, h.Source)
		}
	}
	if _, err := build(`[{"refrigerant":"R-717","kg":1}]`); err == nil || !strings.Contains(err.Error(), "gwp") {
		t.Errorf("R-717 without gwp: want error asking for gwp, got %v", err)
	}
}

// A HOC per container handled (container terminals) gives a per-container
// hub factor with its TTW/WTT split; hub stops then give containers.
func TestHOCPerContainer(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Parse(strings.NewReader(`{"name":"t","period":"2025","hocs":[{"id":"aarhus_terminal","containers":20000,
	  "energy":[{"fuel":"diesel","amount":40000},{"fuel":"electricity/DNK","amount":500000}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	layer, err := Build(o, ds)
	if err != nil {
		t.Fatal(err)
	}
	h := layer.Hubs[0]
	diesel, _ := ds.FuelFactor("diesel")
	el, _ := ds.FuelFactor("electricity/DNK")
	wantTTW := (40000*diesel.TTW + 500000*el.TTW) * 1000 / 20000
	wantWTT := (40000*diesel.WTT + 500000*el.WTT) * 1000 / 20000
	if h.WTWPerTonneG != 0 || h.TTWPerTonneG != nil || h.TTWPerContainerG == nil ||
		math.Abs(*h.TTWPerContainerG-wantTTW) > 1e-9 || math.Abs(*h.WTTPerContainerG-wantWTT) > 1e-9 ||
		math.Abs(h.WTWPerContainerG-wantTTW-wantWTT) > 1e-9 || !strings.Contains(h.Source, "20000 containers") {
		t.Fatalf("hoc = %+v", h)
	}
	b, _ := json.Marshal(layer)
	merged, err := layered(t, b)
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: merged}
	r, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 20_000,
		Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 50}},
		Hubs: []calc.HubStop{{ID: "aar", HubType: "aarhus_terminal", Containers: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	hr := r.Hubs[0]
	if math.Abs(hr.WTWKg-2*(wantTTW+wantWTT)/1000) > 1e-9 || hr.TTWKg == nil || math.Abs(*hr.TTWKg-2*wantTTW/1000) > 1e-9 ||
		math.Abs(*hr.WTTKg-2*wantWTT/1000) > 1e-9 || hr.DataType != calc.DataPrimary {
		t.Errorf("hub result = %+v", hr)
	}
	if _, err := c.Calculate(context.Background(), calc.Shipment{WeightKg: 20_000,
		Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 50}},
		Hubs: []calc.HubStop{{ID: "aar", HubType: "aarhus_terminal"}}}); err == nil {
		t.Error("per-container HOC without containers: want error")
	}
}

// Data extrapolated from a sample is modelled data (ISO 14083): the factors
// say so, and it doesn't count toward the primary data share.
func TestModelledData(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Parse(strings.NewReader(`{"name":"m","period":"2025",
	  "tocs":[{"id":"sampled_road","mode":"road","tonne_km":1000000,"data_type":"modelled","fuels":[{"fuel":"diesel","amount":20000}]}],
	  "hocs":[{"id":"sampled_hub","tonnes":1000,"data_type":"modelled","energy":[{"fuel":"electricity/DNK","amount":1000}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	layer, err := Build(o, ds)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Legs[0].DataType != calc.DataModelled || layer.Hubs[0].DataType != calc.DataModelled {
		t.Fatalf("data types %q %q", layer.Legs[0].DataType, layer.Hubs[0].DataType)
	}
	b, _ := json.Marshal(layer)
	merged, err := layered(t, b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := calc.Calculator{Factors: merged}.Calculate(context.Background(), calc.Shipment{WeightKg: 1000,
		Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "sampled_road", DistanceKm: 100}},
		Hubs: []calc.HubStop{{ID: "h", HubType: "sampled_hub"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Legs[0].DataType != calc.DataModelled || r.Hubs[0].DataType != calc.DataModelled || r.PrimaryDataShare != 0 {
		t.Errorf("legs %q hubs %q primary share %v", r.Legs[0].DataType, r.Hubs[0].DataType, r.PrimaryDataShare)
	}
}
