package recommend

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
)

// Shipments with the same leg and load_tonnes are grouped only when they
// fit the load: one heavier than it is refused on its own, whatever the
// order, and a lighter one gets its own result.
func TestGroupOverLoad(t *testing.T) {
	c := setup(t)
	leg := road("a", hamburg, munich, 0)
	leg.LoadTonnes = 20
	heavy := calc.Shipment{ID: "heavy", WeightKg: 25000, Legs: []calc.Leg{leg}}
	light := calc.Shipment{ID: "light", WeightKg: 5000, Legs: []calc.Leg{leg}}
	for _, order := range [][]calc.Shipment{{light, heavy}, {heavy, light}} {
		res, err := Run(context.Background(), c, order, Settings{Kinds: []string{LargerTruck}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, sr := range res.Shipments {
			if (sr.ShipmentID == "heavy") != (sr.Error != "") {
				t.Errorf("order %s first: %s error %q", order[0].ID, sr.ShipmentID, sr.Error)
			}
		}
	}
}

// Scaling a grouped result scales a split hub's TTW and WTT too, on
// copies: the first shipment's result stays as it was.
func TestScaleHubSplit(t *testing.T) {
	ttw, wtt := 10.0, 26.4
	in := Route{Terminals: []calc.HubResult{{WTWKg: 36.4, Containers: 2, TTWKg: &ttw, WTTKg: &wtt}}}
	h := in.scale(1.5).Terminals[0]
	if math.Abs(*h.TTWKg+*h.WTTKg-h.WTWKg) > 1e-9 || ttw != 10 || wtt != 26.4 {
		t.Errorf("scaled wtw %v ttw %v wtt %v; original ttw %v wtt %v", h.WTWKg, *h.TTWKg, *h.WTTKg, ttw, wtt)
	}
}

// A shipment's own cargo_type comes back with its result, so the page's
// routes CSV can calculate it again the same way.
func TestCargoTypeReturned(t *testing.T) {
	c := setup(t)
	s := calc.Shipment{ID: "r", WeightKg: 5000, CargoType: "temperature_controlled", Legs: []calc.Leg{road("a", hamburg, munich, 0)}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{LargerTruck}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Shipments[0].CargoType; got != s.CargoType {
		t.Errorf("cargo_type %q, want %q", got, s.CargoType)
	}
}

// Legs that share an id are each judged on their own: the same result
// as with their own ids.
func TestRepeatedLegIDs(t *testing.T) {
	c := setup(t)
	run := func(a, b string) ShipmentResult {
		t.Helper()
		s := calc.Shipment{ID: "dup", WeightKg: 20000, Legs: []calc.Leg{road(a, kiel, hamburg, 100), road(b, hamburg, munich, 780)}}
		res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Rail}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		return res.Shipments[0]
	}
	same, own := run("x", "x"), run("a", "b")
	if same.Lane != own.Lane || len(same.Options) != len(own.Options) {
		t.Fatalf("repeated ids: lane %q, %d options; own ids: %q, %d", same.Lane, len(same.Options), own.Lane, len(own.Options))
	}
	for i := range same.Options {
		if math.Abs(same.Options[i].WTWKg-own.Options[i].WTWKg) > 1e-6 {
			t.Errorf("option %d: %v kg, with own ids %v", i, same.Options[i].WTWKg, own.Options[i].WTWKg)
		}
	}
}

// Fuller loads leave a leg the load doesn't fit (a van: no load model) as
// it is, with a note, and still give the option for the others.
func TestFullerLoadsSkipLegs(t *testing.T) {
	c := setup(t)
	artic := road("a", hamburg, munich, 0)
	artic.VehicleType = "hgv_artic_33t"
	van := road("v", munich, hamburg, 0)
	van.VehicleType = "van"
	s := calc.Shipment{ID: "s", WeightKg: 1000, Legs: []calc.Leg{artic, van}}
	res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Load}, LoadTonnes: ptr(24.0), Source: "own plan"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sr := res.Shipments[0]
	if len(sr.Options) != 1 || sr.Options[0].Kind != Load {
		t.Errorf("options %+v, notes %v", sr.Options, sr.Notes)
	}
}

// A shipment given in TEU is judged by its calculated weight, as one given
// in kg: the rigid leg a load factor can't carry is left as it is.
func TestFullerLoadsTEU(t *testing.T) {
	c := setup(t)
	artic := road("a", hamburg, munich, 0)
	artic.VehicleType = "hgv_artic_33t"
	rigid := road("v", munich, hamburg, 0)
	rigid.VehicleType = "hgv_rigid"
	for _, s := range []calc.Shipment{{ID: "kg", WeightKg: 10000, Legs: []calc.Leg{artic, rigid}}, {ID: "teu", TEU: 1, Legs: []calc.Leg{artic, rigid}}} {
		res, err := Run(context.Background(), c, []calc.Shipment{s}, Settings{Kinds: []string{Load}, LoadFactor: ptr(0.9), Source: "own plan"}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if sr := res.Shipments[0]; len(sr.Options) != 1 {
			t.Errorf("%s: %d options, notes %v", s.ID, len(sr.Options), sr.Notes)
		}
	}
}

// A shipment given in TEU keeps its calculated weight and numbers (they
// were scaled by its input weight, 0).
func TestTEUNumbers(t *testing.T) {
	c := setup(t)
	teu := calc.Shipment{ID: "teu", TEU: 1, Legs: []calc.Leg{road("a", hamburg, munich, 0)}}
	kg := calc.Shipment{ID: "kg", WeightKg: 10000, Legs: []calc.Leg{road("a", hamburg, munich, 0)}}
	res, err := Run(context.Background(), c, []calc.Shipment{teu, kg}, Settings{Kinds: []string{Rail}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Shipments[0], res.Shipments[1]
	if a.WeightKg != 10000 || !(a.Now.Totals.WTWKg > 0) || math.Abs(a.Now.Totals.WTWKg-b.Now.Totals.WTWKg) > 1e-6 ||
		len(a.Options) != len(b.Options) || len(a.Options) == 0 || math.Abs(a.Options[0].SavedKg-b.Options[0].SavedKg) > 1e-6 {
		t.Errorf("TEU: weight %v, now %v, options %d; by weight: now %v, options %d", a.WeightKg, a.Now.Totals.WTWKg, len(a.Options), b.Now.Totals.WTWKg, len(b.Options))
	}
}

// A shipment whose own weight can't be calculated never passes its error
// to a lighter one with the same legs, nor gets a scaled result that
// overflows: either order, the light one is fine and the heavy one refused.
func TestGroup_HugeWeight(t *testing.T) {
	c := setup(t)
	leg := road("r", hamburg, munich, 1000000)
	leg.VehicleType = "hgv_rigid"
	light := calc.Shipment{ID: "a", WeightKg: 1, Legs: []calc.Leg{leg}}
	heavy := calc.Shipment{ID: "b", WeightKg: 1.7e308, Legs: []calc.Leg{leg}}
	for _, order := range [][]calc.Shipment{{light, heavy}, {heavy, light}} {
		res, err := Run(context.Background(), c, order, Settings{Kinds: []string{LargerTruck}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, sr := range res.Shipments {
			if (sr.ShipmentID == "a") != (sr.Error == "") || !finite(sr) {
				t.Errorf("order %s first: %s error %q, finite %v", order[0].ID, sr.ShipmentID, sr.Error, finite(sr))
			}
		}
	}
}

// Shipments heavier than the fuller-loads load aren't grouped: each note
// names its own weight.
func TestOverLoad_OwnNote(t *testing.T) {
	c := setup(t)
	leg := road("r", hamburg, munich, 780)
	leg.VehicleType = "hgv_artic_33t"
	lt := 20.0
	res, err := Run(context.Background(), c, []calc.Shipment{{ID: "a", WeightKg: 25000, Legs: []calc.Leg{leg}}, {ID: "b", WeightKg: 30000, Legs: []calc.Leg{leg}}},
		Settings{Kinds: []string{Load}, LoadTonnes: &lt}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, sr := range res.Shipments {
		notes := strings.Join(sr.Notes, "|")
		own := map[string]string{"a": "25 t", "b": "30 t"}[sr.ShipmentID]
		if !strings.Contains(notes, own) || strings.Contains(notes, "no road leg on a fleet-average factor") {
			t.Errorf("%s: %s", sr.ShipmentID, notes)
		}
	}
}

// seaNet is a router whose sea network only reaches places near a sea
// port (as SeaRoute snaps), for the real place list.
type seaNet struct{ pc *places.Index }

func (n seaNet) Distance(_ context.Context, m calc.Mode, a, b calc.Location) (float64, error) {
	if m == calc.Air {
		return distance.Haversine(a, b), nil
	}
	return 1.2*distance.Haversine(a, b) + 1, nil
}
func (seaNet) DistanceType(m calc.Mode) string {
	if m == calc.Air {
		return calc.DistGCD
	}
	return calc.DistSFD
}
func (n seaNet) SnapKm(m calc.Mode, p calc.Location, maxKm float64) (float64, bool) {
	if m != calc.Sea {
		return 0, true
	}
	if near := n.pc.Near(p, []string{calc.KindSeaPort}, 300, 1); len(near) > 0 {
		return distance.Haversine(p, near[0]), true
	}
	return 300, true
}

// A sea leg whose ends are both nearest one port has no sea part: it is
// refused, not counted as sea across land, and no option builds one.
func TestSeaLegEndsAtOnePort(t *testing.T) {
	pc, err := places.Default()
	if err != nil {
		t.Fatal(err)
	}
	ds, _ := factors.Open("")
	c := calc.Calculator{Factors: ds, Distances: seaNet{pc}, Places: pc}
	basel, antwerp := calc.Location{Lat: 47.5596, Lon: 7.5886}, calc.Location{Lat: 51.2194, Lon: 4.4025}
	_, err = c.Calculate(context.Background(), calc.Shipment{ID: "s", WeightKg: 10000, Legs: []calc.Leg{{Mode: calc.Sea, VehicleType: "container_ship", From: basel, To: antwerp}}})
	if !errors.Is(err, calc.ErrNoDistance) || !strings.Contains(err.Error(), "same port") {
		t.Errorf("sea leg Basel to Antwerp: %v", err)
	}
	leg := road("r", basel, antwerp, 0)
	leg.VehicleType = "hgv"
	res, err := Run(context.Background(), c, []calc.Shipment{{ID: "s", WeightKg: 12000, Legs: []calc.Leg{leg}}}, Settings{Kinds: []string{ShortSea}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o := option(res.Shipments[0], ShortSea); o != nil {
		t.Errorf("short sea from Basel: %s", o.Summary)
	}
}
