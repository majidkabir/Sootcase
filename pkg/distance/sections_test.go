package distance

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/borders"
	"github.com/majidkabir/sootcase/pkg/calc"
)

// checkSections checks sections the way the API promises them: end to end
// from 0 to km, regions end to end over their section, and each
// country's land sections adding up to its CountryKm.
func checkSections(t *testing.T, r calc.Route) {
	t.Helper()
	at, byCountry := 0.0, map[string]float64{}
	for _, s := range r.CountrySections {
		if s.FromKm != at || s.ToKm <= s.FromKm {
			t.Errorf("section %+v after %v km", s, at)
		}
		if s.Ferry != (s.Country == "") {
			t.Errorf("section %+v: ferry and country", s)
		}
		rat := s.FromKm
		for _, g := range s.Regions {
			if g.FromKm != rat || g.ToKm <= g.FromKm || !strings.HasPrefix(g.Region, s.Country+"-") {
				t.Errorf("region %+v after %v km in %s", g, rat, s.Country)
			}
			rat = g.ToKm
		}
		if len(s.Regions) > 0 && rat != s.ToKm {
			t.Errorf("regions of %s end at %v, section at %v", s.Country, rat, s.ToKm)
		}
		if !s.Ferry {
			byCountry[s.Country] += s.ToKm - s.FromKm
		}
		at = s.ToKm
	}
	if at != r.Km {
		t.Errorf("sections end at %v, route %v km", at, r.Km)
	}
	if len(byCountry) != len(r.CountryKm) {
		t.Errorf("sections in %v, country_km %v", byCountry, r.CountryKm)
	}
	for c, v := range r.CountryKm {
		if math.Abs(byCountry[c]-v) > 0.01 {
			t.Errorf("%s: sections %v km, country_km %v", c, byCountry[c], v)
		}
	}
}

// line is a GeoJSON line through pts with points every ~1 km, as OSRM's
// step geometries are dense.
func line(pts ...[2]float64) string {
	var out []string
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		n := int(math.Ceil(math.Hypot(a[0]-b[0], a[1]-b[1]) * 100))
		for k := 0; k < n; k++ {
			f := float64(k) / float64(n)
			out = append(out, fmt.Sprintf("[%.5f,%.5f]", a[0]+f*(b[0]-a[0]), a[1]+f*(b[1]-a[1])))
		}
	}
	p := pts[len(pts)-1]
	out = append(out, fmt.Sprintf("[%.5f,%.5f]", p[0], p[1]))
	return `{"coordinates":[` + strings.Join(out, ",") + `]}`
}

func countries(r calc.Route) string {
	var out []string
	for _, s := range r.CountrySections {
		c := s.Country
		if s.Ferry {
			c = "ferry"
		}
		out = append(out, c)
	}
	return strings.Join(out, " ")
}

// Copenhagen to Hamburg over Rødby-Puttgarden with the real borders: DK,
// the ferry, DE (Schleswig-Holstein, then Hamburg).
func TestOSRM_CountrySections(t *testing.T) {
	ix, err := borders.Default()
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	var path string
	body := `{"code":"Ok","routes":[{"distance":296123.4,"legs":[{"steps":[
		{"distance":160100.2,"mode":"driving","geometry":` + line([2]float64{12.5683, 55.6761}, [2]float64{12.0, 55.3}, [2]float64{11.86, 54.77}, [2]float64{11.35, 54.655}) + `},
		{"distance":19000,"mode":"ferry","geometry":` + line([2]float64{11.35, 54.655}, [2]float64{11.226, 54.5}) + `},
		{"distance":117023.2,"mode":"driving","geometry":` + line([2]float64{11.226, 54.5}, [2]float64{10.686, 53.866}, [2]float64{9.9937, 53.5511}) + `}]}]}]}`
	srv := fakeOSRM(t, body, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	o.PartModes, o.Countries = RoadParts, ix
	r, err := o.Route(calc.WithCountryKm(context.Background()), calc.Road, copenhagen, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	if got := countries(r); got != "DK ferry DE" {
		t.Fatalf("sections %s, want DK ferry DE (%+v)", got, r.CountrySections)
	}
	checkSections(t, r)
	if f := r.CountrySections[1]; math.Abs(f.ToKm-f.FromKm-19) > 1e-9 {
		t.Errorf("ferry section %+v, want 19 km", f)
	}
	de := r.CountrySections[2].Regions
	if len(de) != 2 || de[0].Region != "DE-SH" || de[1].Region != "DE-HH" {
		t.Errorf("DE regions %+v, want DE-SH then DE-HH", de)
	}
	if len(r.RegionKm) != 0 {
		t.Errorf("RegionKm %v: Germany's states are not in region_km", r.RegionKm)
	}
	if r.CountrySections[0].Regions != nil {
		t.Errorf("DK has no regions: %+v", r.CountrySections[0])
	}
}

// Salzburg to Kufstein through Rosenheim: Austria twice.
func TestOSRM_CountrySectionsTwice(t *testing.T) {
	ix, _ := borders.Default()
	var calls int32
	var path string
	body := `{"code":"Ok","routes":[{"distance":130000,"legs":[{"steps":[
		{"distance":90000,"mode":"driving","geometry":` + line([2]float64{13.04, 47.80}, [2]float64{12.12, 47.856}) + `},
		{"distance":40000,"mode":"driving","geometry":` + line([2]float64{12.12, 47.856}, [2]float64{12.17, 47.583}) + `}]}]}]}`
	srv := fakeOSRM(t, body, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	o.PartModes, o.Countries = RoadParts, ix
	r, err := o.Route(calc.WithCountryKm(context.Background()), calc.Road, copenhagen, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	if got := countries(r); got != "AT DE AT" {
		t.Fatalf("sections %s, want AT DE AT (%+v)", got, r.CountrySections)
	}
	checkSections(t, r)
	if g := r.CountrySections[1].Regions; len(g) != 1 || g[0].Region != "DE-BY" {
		t.Errorf("DE regions %+v, want DE-BY", g)
	}
}

// A splitter that can't give stretches in order gives no sections; the
// rest is as before.
func TestOSRM_NoSectionsWithoutStretches(t *testing.T) {
	var calls int32
	var path string
	body := `{"code":"Ok","routes":[{"distance":90000,"legs":[{"steps":[
		{"distance":90000,"mode":"driving","geometry":{"coordinates":[[4.4,51.2],[4.35,50.85]]}}]}]}]}`
	srv := fakeOSRM(t, body, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	o.PartModes, o.Countries = RoadParts, splitRegions{}
	r, err := o.Route(calc.WithCountryKm(context.Background()), calc.Road, copenhagen, hamburg)
	if err != nil || r.CountryKm == nil || r.CountrySections != nil {
		t.Errorf("route %+v, %v: want CountryKm and no sections", r, err)
	}
}

func TestJoinSections(t *testing.T) {
	a := []calc.CountrySection{{Country: "DK", FromKm: 0, ToKm: 10},
		{Country: "DE", FromKm: 10, ToKm: 30, Regions: []calc.RegionSection{{Region: "DE-SH", FromKm: 10, ToKm: 30}}}}
	b := []calc.CountrySection{{Country: "DE", FromKm: 0, ToKm: 50, Regions: []calc.RegionSection{
		{Region: "DE-SH", FromKm: 0, ToKm: 20}, {Region: "DE-HH", FromKm: 20, ToKm: 50}}},
		{Country: "", Ferry: true, FromKm: 50, ToKm: 60.12345}}
	at := 30.0
	got := joinSections(a, b, at)
	r := calc.Route{Km: math.Round((at+b[1].ToKm)*1e4) / 1e4, CountrySections: got, CountryKm: map[string]float64{"DK": 10, "DE": 70}}
	checkSections(t, r)
	if countries(r) != "DK DE ferry" || len(got[1].Regions) != 2 || got[1].Regions[0].ToKm != 50 {
		t.Errorf("joined %+v", got)
	}
	if a[1].ToKm != 30 || len(a[1].Regions) != 1 || a[1].Regions[0].ToKm != 30 {
		t.Errorf("a changed: %+v", a)
	}
	// The last section ends at the route's km exactly, without float noise.
	a2 := []calc.CountrySection{{Country: "DE", FromKm: 0, ToKm: 987.6543}}
	b2 := []calc.CountrySection{{Country: "AT", FromKm: 0, ToKm: 500.4321}}
	if j := joinSections(a2, b2, 987.6543); j[1].ToKm != 1488.0864 {
		t.Errorf("last to_km %v, want 1488.0864", j[1].ToKm)
	}
	if joinSections(a, nil, 30) != nil {
		t.Error("a half without sections: want none")
	}
}

// Near a border the region and country polygons (different sources) don't
// agree to the km: a country's sections only ever hold its own regions,
// and region_km stays Belgian. Eupen → Kelmis → Aachen, and Burg-Reuland
// over the BE-LU-DE corner into Germany.
func TestOSRM_RegionsOfTheirCountry(t *testing.T) {
	ix, _ := borders.Default()
	for _, pts := range [][][2]float64{
		{{6.0361, 50.6293}, {6.0105, 50.7186}, {6.0839, 50.7753}},
		{{6.14, 50.20}, {6.098, 50.1495}, {6.25, 50.08}},
		{{6.14, 50.20}, {6.0975, 50.149}, {6.0985, 50.150}, {6.16, 50.15}},
	} {
		body := `{"code":"Ok","routes":[{"distance":20000,"legs":[{"steps":[
		{"distance":20000,"mode":"driving","geometry":` + line(pts...) + `}]}]}]}`
		var calls int32
		var path string
		srv := fakeOSRM(t, body, 200, &calls, &path)
		o, _ := NewOSRM(srv.URL)
		o.PartModes, o.Countries = RoadParts, ix
		r, err := o.Route(calc.WithCountryKm(context.Background()), calc.Road, copenhagen, hamburg)
		if err != nil {
			t.Fatal(err)
		}
		checkSections(t, r)
		for g := range r.RegionKm {
			if !strings.HasPrefix(g, "BE-") {
				t.Errorf("region_km %v: Belgium's only", r.RegionKm)
			}
		}
	}
}
