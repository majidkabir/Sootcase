package explain

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
)

var (
	cph = calc.Location{Name: "Copenhagen", Lat: 55.68, Lon: 12.57}
	ham = calc.Location{Name: "Hamburg", Lat: 53.55, Lon: 9.99}
	rtm = calc.Location{Name: "Rotterdam", Lat: 51.92, Lon: 4.48}
	nyc = calc.Location{Name: "New York", Lat: 40.68, Lon: -74.03}
)

func sheet(t *testing.T) (Sheet, calc.Result) {
	t.Helper()
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances(), NoRoadLegs: true, Places: places.Codes{}}
	s := calc.Shipment{ID: "S1", WeightKg: 12000, Legs: []calc.Leg{
		{ID: "truck", Mode: calc.Road, VehicleType: "hgv_artic_33t", From: cph, To: ham, DistanceKm: 332, DistanceType: calc.DistSFD, LoadTonnes: 15.6, EmptyTripFactor: 0.2},
		{ID: "ship", Mode: calc.Sea, VehicleType: "container_ship", From: rtm, To: nyc, DistanceKm: 6200, DistanceType: calc.DistSFD},
		{ID: "plane", Mode: calc.Air, VehicleType: "air_freight", From: nyc, To: cph},
		{ID: "fuel", Mode: calc.Road, VehicleType: "hgv", DistanceKm: 40, Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 14, VehicleTonneKm: 960}},
		{ID: "train", Mode: calc.Rail, VehicleType: "freight_train", DistanceKm: 300, EnergyKWhPerTkm: 0.04, Country: "DNK"},
	}}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return Shipment(s, r, ds), r
}

func TestSheet_EveryNumberIsThere(t *testing.T) {
	sh, r := sheet(t)
	if os.Getenv("SHOW") != "" {
		b, _ := json.MarshalIndent(sh, "", "  ")
		t.Log(string(b))
	}
	all := ""
	for _, s := range sh.Steps {
		all += s.Formula + "\n" + s.Text + "\n"
		if strings.Contains(s.Formula, "–") || strings.Contains(s.Formula, "NaN") {
			t.Errorf("%s / %s: %q", s.Group, s.Title, s.Formula)
		}
	}
	for _, l := range r.Legs {
		for _, v := range []float64{l.WTWKg, l.TonneKm} {
			for _, d := range []int{3, 1} {
				if strings.Contains(all, n(v, d)) {
					goto found
				}
			}
			t.Errorf("leg %s: %v not in the sheet", l.LegID, v)
		found:
		}
	}
	if !strings.Contains(all, n(r.WTWKg, 3)+" kg CO₂e") {
		t.Errorf("total %v not in the sheet", r.WTWKg)
	}
	// Every step names where it comes from, except leg totals and warnings.
	for _, s := range sh.Steps {
		if len(s.Basis) == 0 && s.Title != "Leg total" && s.Title != "Warning" {
			t.Errorf("%s / %s has no basis", s.Group, s.Title)
		}
	}
}

// The formulas' arithmetic holds with the numbers as printed (to rounding).
func TestSheet_FormulasAddUp(t *testing.T) {
	sh, _ := sheet(t)
	num := `(-?[\d,]+(?:\.\d+)?)`
	product := regexp.MustCompile(num + ` t × ` + num + ` km × ` + num + ` g/tkm ÷ 1,000 = ` + num + ` kg`)
	loadEq := regexp.MustCompile(`\(` + num + ` \+ ` + num + ` × ` + num + `\) ÷ \(` + num + ` t\) = ` + num + ` g`)
	activity := regexp.MustCompile(num + ` t × ` + num + ` km = ` + num + ` tkm`)
	byTkm := regexp.MustCompile(num + ` tkm × ` + num + ` g/tkm ÷ 1,000 = ` + num + ` kg`)
	f := func(s string) float64 { v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); return v }
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.002*math.Max(1, math.Abs(b)) }
	checked := 0
	for _, s := range sh.Steps {
		for _, m := range product.FindAllStringSubmatch(s.Formula, -1) {
			checked++
			if got := f(m[1]) * f(m[2]) * f(m[3]) / 1000; !near(got, f(m[4])) {
				t.Errorf("%s: %s → %v", s.Group, m[0], got)
			}
		}
		for _, m := range loadEq.FindAllStringSubmatch(s.Formula, -1) {
			checked++
			if got := (f(m[1]) + f(m[2])*f(m[3])) / f(m[4]); !near(got, f(m[5])) {
				t.Errorf("%s: %s → %v", s.Group, m[0], got)
			}
		}
		for _, m := range byTkm.FindAllStringSubmatch(s.Formula, -1) { // e.g. the load model's, after its distance adjustment
			checked++
			if got := f(m[1]) * f(m[2]) / 1000; !near(got, f(m[3])) {
				t.Errorf("%s: %s → %v", s.Group, m[0], got)
			}
		}
		for _, m := range activity.FindAllStringSubmatch(s.Formula, -1) {
			checked++
			if got := f(m[1]) * f(m[2]); !near(got, f(m[3])) {
				t.Errorf("%s: %s → %v", s.Group, m[0], got)
			}
		}
	}
	if checked < 8 {
		t.Errorf("only %d formulas checked", checked)
	}
}

// The vehicle type's meaning for the leg's distance is told with the
// intensity, not the distance.
func TestSheet_FactorNote(t *testing.T) {
	sh, r := sheet(t)
	if r.Legs[1].FactorNote == "" || r.Legs[1].Note != "" {
		t.Fatalf("ship leg: factor note %q, note %q", r.Legs[1].FactorNote, r.Legs[1].Note)
	}
	for _, s := range sh.Steps {
		if !strings.HasPrefix(s.Group, "Leg 2") {
			continue
		}
		has := strings.Contains(s.Text, `"container_ship" from North Europe (NL) to North America EC/Gulf (US): trade lane North Europe to-from North America EC/Gulf`)
		if (s.Title == "Emission intensity") != has {
			t.Errorf("%s: %q", s.Title, s.Text)
		}
	}
}

// Legs without ids (or with the same id) each show their own input: two
// unnamed fuel legs, 100 l and 50 l.
func TestSheet_LegsWithoutIDs(t *testing.T) {
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds, Distances: ds.Distances(), NoRoadLegs: true, Places: places.Codes{}}
	s := calc.Shipment{ID: "S", WeightKg: 10000, Legs: []calc.Leg{
		{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 300, Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 100}},
		{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 150, Fuel: &calc.FuelUse{Fuel: "diesel", Amount: 50}},
	}}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	var amounts []string
	for _, st := range Shipment(s, r, ds).Steps {
		if m := regexp.MustCompile(`TTW = ([\d.,]+) l`).FindStringSubmatch(st.Formula); m != nil {
			amounts = append(amounts, m[1])
		}
	}
	if len(amounts) != 2 || amounts[0] != "100.000" || amounts[1] != "50.000" {
		t.Errorf("fuel amounts on the sheet %v, want 100.000 then 50.000", amounts)
	}
}

// A container ship value per TEU-km on cargo given in TEU: the sheet's
// product uses the TEU's tonnes the value was applied to, and adds up.
func TestSheet_PerTEU(t *testing.T) {
	ds2, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	c := calc.Calculator{Factors: ds2, Distances: ds2.Distances(), NoRoadLegs: true, Places: places.Codes{}}
	s := calc.Shipment{ID: "T", TEU: 2, TEUNetTonnes: 7, Legs: []calc.Leg{{Mode: calc.Sea, VehicleType: "container_ship", From: rtm, To: nyc, DistanceKm: 6200, DistanceType: calc.DistSFD}}}
	r, err := c.Calculate(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	num := `([\d,]+(?:\.\d+)?)`
	product := regexp.MustCompile(num + ` t × ` + num + ` km × ` + num + ` g/tkm ÷ 1,000 = ` + num + ` kg`)
	f := func(s string) float64 { v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); return v }
	checked := 0
	for _, st := range Shipment(s, r, ds2).Steps {
		for _, m := range product.FindAllStringSubmatch(st.Formula, -1) {
			checked++
			if got := f(m[1]) * f(m[2]) * f(m[3]) / 1000; math.Abs(got-f(m[4])) > 0.002*math.Max(1, f(m[4])) {
				t.Errorf("%s → %v", m[0], got)
			}
			if f(m[1]) != 20 {
				t.Errorf("%s: want the 2 TEU's 20 t, not the cargo's 14 t", m[0])
			}
		}
	}
	if checked == 0 {
		t.Error("no product on the sheet")
	}
}
