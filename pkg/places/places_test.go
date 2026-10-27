package places

import (
	"github.com/majidkabir/sootcase/pkg/calc"
	"testing"
	"time"
)

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"København": "kobenhavn", "Düsseldorf": "dusseldorf", "Straße": "strasse", "Łódź": "lodz",
		"  Frankfurt am Main ": "frankfurt am main", "St. Petersburg": "st petersburg", "Ærøskøbing": "aeroskobing",
	} {
		if got := fold(in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func index(t *testing.T) *Index {
	t.Helper()
	start := time.Now()
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if ix.Len() < 50000 {
		t.Fatalf("only %d places", ix.Len())
	}
	t.Logf("%d places loaded in %v", ix.Len(), time.Since(start))
	return ix
}

func TestLookup(t *testing.T) {
	ix := index(t)
	for code, want := range map[string]string{"NLRTM": "locode:NLRTM", "nl rtm": "locode:NLRTM", "CPH": "iata:CPH", "iata:CPH": "iata:CPH"} {
		p, ok := ix.Lookup(code)
		if !ok || p.ID != want {
			t.Errorf("Lookup(%q) = %q %v, want %q", code, p.ID, ok, want)
		}
	}
	p, _ := ix.Lookup("DEHAM")
	if p.Lat < 53 || p.Lat > 54 || p.Lon < 9.5 || p.Lon > 10.5 || p.CountryName != "Germany" {
		t.Errorf("Hamburg: %+v", p)
	}
	if _, ok := ix.Lookup("XXZZZ"); ok {
		t.Error("unknown code found")
	}
}

func TestSearch(t *testing.T) {
	ix := index(t)
	first := func(q string, kinds ...string) Place {
		t.Helper()
		r := ix.Search(q, kinds, 5)
		if len(r) == 0 {
			t.Fatalf("%q: nothing found", q)
		}
		return r[0]
	}
	if p := first("rotterdam", Port); p.ID != "locode:NLRTM" {
		t.Errorf("rotterdam port: %s %s", p.ID, p.Name)
	}
	if p := first("CPH"); p.ID != "iata:CPH" {
		t.Errorf("CPH: %s", p.ID)
	}
	if p := first("nlrtm"); p.ID != "locode:NLRTM" {
		t.Errorf("nlrtm: %s", p.ID)
	}
	if p := first("kobenhavn", Port); p.ID != "locode:DKCPH" {
		t.Errorf("kobenhavn: %s", p.ID)
	}
	if p := first("Duisb", Port); p.ID != "locode:DEDUI" {
		t.Errorf("Duisb: %s", p.ID)
	}
	if p := first("copenhagen", Airport); p.ID != "iata:CPH" {
		t.Errorf("copenhagen airport: %s", p.ID)
	}
	for _, p := range ix.Search("hamburg", []string{Airport}, 10) {
		if !anyKind(p.Kinds, map[string]bool{Airport: true}) {
			t.Errorf("kind filter let %s through", p.ID)
		}
	}
	if r := ix.Search("", nil, 5); len(r) != 0 {
		t.Error("empty query matched")
	}
}

func BenchmarkSearch(b *testing.B) {
	ix, _ := Default()
	for b.Loop() {
		ix.Search("hamb", nil, 10)
	}
}

func TestNear(t *testing.T) {
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	munich := calc.Location{Lat: 48.14, Lon: 11.58}
	got := ix.Near(munich, []string{Rail}, 30, 5)
	if len(got) == 0 || len(got) > 5 {
		t.Fatalf("rail places near Munich: %v", got)
	}
	last := 0.0
	for _, l := range got {
		d := haversine(munich.Lat, munich.Lon, l.Lat, l.Lon)
		if d > 30 || d < last {
			t.Errorf("%s at %.1f km: not within 30 km, nearest first", l.Name, d)
		}
		last = d
	}
	ports := ix.Near(munich, []string{Port, Rail}, 1000, 5000)
	names := map[string]bool{}
	for _, l := range ports {
		names[l.Code] = true
	}
	for _, c := range []string{"DEHAM", "DEBRV"} {
		if !names[c] {
			t.Errorf("no %s among %d ports with rail within 1000 km of Munich", c, len(ports))
		}
	}
	if len(ix.Near(munich, []string{Port}, 5, 3)) != 0 {
		t.Errorf("Munich isn't a port")
	}
}

func TestSeaports(t *testing.T) {
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for c := range seaports {
		p, ok := ix.Lookup(c)
		if !ok {
			t.Errorf("sea port %s isn't in the place list", c)
			continue
		}
		if !p.allKinds([]string{Port, SeaPort}) {
			t.Errorf("sea port %s (%s) isn't a port: %v", c, p.Name, p.Kinds)
		}
	}
}

func TestCountry(t *testing.T) {
	ix, _ := Default()
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{{28.61, 77.21, "IN"}, {48.14, 11.58, "DE"}, {51.92, 4.48, "NL"}, {0, -30, ""}} {
		if got := ix.Country(calc.Location{Lat: c.lat, Lon: c.lon}); got != c.want {
			t.Errorf("%v, %v: %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
}

func TestTown(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{41.3874, 2.1686, "Barcelona"}, // in Eixample: the city, not the district
		{48.8566, 2.3522, "Paris"},
		{45.764, 4.8357, "Lyon"},
		{53.5511, 9.9937, "Hamburg"},
	} {
		got, _, ok := Codes{}.Town(calc.Location{Lat: c.lat, Lon: c.lon})
		if !ok || got != c.want {
			t.Errorf("%v,%v: %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
	if _, _, ok := (Codes{}).Town(calc.Location{Lat: 0, Lon: -30}); ok {
		t.Error("mid-Atlantic should have no town")
	}
}
