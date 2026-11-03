package borders

import (
	"bytes"
	"math"
	"testing"
)

func index(t testing.TB) *Index {
	t.Helper()
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestCountry(t *testing.T) {
	ix := index(t)
	for _, tc := range []struct {
		name     string
		lon, lat float64
		want     string
	}{
		{"Copenhagen", 12.55, 55.68, "DK"},
		{"Copenhagen harbour (water at 1:10m)", 12.5683, 55.6761, ""},
		{"Hamburg", 9.9937, 53.5511, "DE"},
		{"Malmö", 13.0038, 55.6050, "SE"},
		{"Padborg (DK, 1.5 km from the border)", 9.3640, 54.8260, "DK"},
		{"Flensburg (DE)", 9.4370, 54.7937, "DE"},
		{"Paris", 2.3522, 48.8566, "FR"},
		{"Oslo", 10.7522, 59.9139, "NO"},
		{"Pristina", 21.1655, 42.6629, "XK"},
		{"Vienna", 16.3738, 48.2082, "AT"},
		{"Brussels", 4.3517, 50.8503, "BE"},
		{"North Sea", 3.0, 56.0, ""},
	} {
		if got := ix.Country(tc.lon, tc.lat); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSplitKmAcrossBorder(t *testing.T) {
	ix := index(t)
	// Padborg (DK) to Flensburg (DE): a short line over the border.
	line := [][2]float64{{9.3640, 54.8260}, {9.3900, 54.8150}, {9.4100, 54.8050}, {9.4370, 54.7937}}
	got := ix.SplitKm(line)
	if got["DK"] <= 0 || got["DE"] <= 0 || len(got) != 2 {
		t.Fatalf("got %v, want DK and DE parts", got)
	}
	var total float64
	for k := 0; k+1 < len(line); k++ {
		total += haversineKm(line[k], line[k+1])
	}
	if math.Abs(got["DK"]+got["DE"]-total) > 1e-9 {
		t.Errorf("parts %v don't add up to %v km", got, total)
	}
}

func TestSplitKmBridgeAndSea(t *testing.T) {
	ix := index(t)
	// The Øresund bridge's midpoint is over water, a few km from both
	// coasts: it counts for the nearer one, not for "".
	bridge := [][2]float64{{12.75, 55.57}, {12.80, 55.58}}
	for k := range ix.SplitKm(bridge) {
		if k != "DK" && k != "SE" {
			t.Errorf("Øresund: got country %q, want DK or SE", k)
		}
	}
	// A point the 1:10m coastline leaves in the water counts for the
	// country next to it.
	if got := ix.SplitKm([][2]float64{{12.565, 55.676}, {12.5716, 55.6762}}); got["DK"] <= 0 || len(got) != 1 {
		t.Errorf("Copenhagen harbour: got %v, want only DK", got)
	}
	// Far out at sea: no country.
	sea := [][2]float64{{-30, 45}, {-29.9, 45}}
	if got := ix.SplitKm(sea); got[""] <= 0 || len(got) != 1 {
		t.Errorf("Atlantic: got %v, want only \"\"", got)
	}
}

func TestEncodeDecode(t *testing.T) {
	cs := []Country{{Code: "AA", Polygons: []Polygon{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}}},
		{Code: "BBB", Polygons: []Polygon{{{{-1.5, -1}, {-1, -1}, {-1, -0.5}, {-1.5, -1}}, {{-1.2, -0.9}, {-1.1, -0.9}, {-1.1, -0.8}, {-1.2, -0.9}}}}}}
	var buf bytes.Buffer
	if err := Encode(&buf, cs); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Code != "BBB" || len(got[1].Polygons[0]) != 2 || got[1].Polygons[0][1][2] != [2]float64{-1.1, -0.8} {
		t.Fatalf("round trip: got %+v", got)
	}
	ix := NewIndex(got)
	if c := ix.Country(0.9, 0.5); c != "AA" {
		t.Errorf("inside AA: got %q", c)
	}
	if c := ix.Country(-1.15, -0.87); c != "" {
		t.Errorf("in BBB's hole: got %q, want \"\"", c)
	}
	if _, err := Decode(buf.Bytes()[:buf.Len()/2]); err == nil {
		t.Error("truncated file: want an error")
	}
}

func BenchmarkSplitKm(b *testing.B) {
	ix := index(b)
	// A 1,000-point line over land, from Hamburg (DE) to Vienna (AT)
	// through the Czech Republic.
	line := make([][2]float64, 1000)
	for k := range line {
		f := float64(k) / float64(len(line)-1)
		line[k] = [2]float64{9.9937 + f*(16.3738-9.9937), 53.5511 + f*(48.2082-53.5511)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.SplitKm(line)
	}
}

// Belgium's regions: points in each, and lines that cross them.
func TestRegions(t *testing.T) {
	ix := index(t)
	for _, tc := range []struct {
		name     string
		lon, lat float64
		want     string
	}{
		{"Brussels, Grand-Place", 4.3525, 50.8467, "BE-BRU"},
		{"Uccle (Brussels)", 4.3333, 50.8000, "BE-BRU"},
		{"Drogenbos (Flanders, beside Brussels)", 4.3150, 50.7870, "BE-VLG"},
		{"Zaventem, Brussels Airport (Flanders)", 4.4844, 50.9010, "BE-VLG"},
		{"Antwerp", 4.4025, 51.2194, "BE-VLG"},
		{"Namur", 4.8719, 50.4674, "BE-WAL"},
		{"Liège", 5.5797, 50.6326, "BE-WAL"},
		{"Waterloo (Walloon Brabant)", 4.3990, 50.7150, "BE-WAL"},
		{"Voeren (Flemish exclave)", 5.7700, 50.7550, "BE-VLG"},
		{"Comines (Walloon exclave)", 2.9990, 50.7680, "BE-WAL"},
	} {
		if r, _ := ix.regions.find(tc.lon, tc.lat, -1); r < 0 || ix.regions.countries[r].code != tc.want {
			got := ""
			if r >= 0 {
				got = ix.regions.countries[r].code
			}
			t.Errorf("%s: region %q, want %q", tc.name, got, tc.want)
		}
	}

	// Antwerp to Brussels to Namur, and on into France: the regions add up
	// to Belgium's km, and France has none.
	// Points every ~100 m, as route geometry is: each short segment counts
	// for the region of its midpoint.
	line := densify([][2]float64{{4.4025, 51.2194}, {4.3525, 50.8467}, {4.8719, 50.4674}, {4.0300, 50.2600}, {3.9700, 50.1000}}, 0.001)
	countries, regions := ix.SplitKmRegions(line)
	if countries["BE"] <= 0 || countries["FR"] <= 0 {
		t.Fatalf("countries %v, want BE and FR", countries)
	}
	var sum float64
	for code, km := range regions {
		if code != "BE-VLG" && code != "BE-WAL" && code != "BE-BRU" {
			t.Errorf("region %q (%v km): want only Belgian regions", code, km)
		}
		sum += km
	}
	if regions["BE-VLG"] <= 0 || regions["BE-WAL"] <= 0 || regions["BE-BRU"] <= 0 || math.Abs(sum-countries["BE"]) > 1e-9 {
		t.Errorf("regions %v (sum %v), want all three adding up to BE %v", regions, sum, countries["BE"])
	}
	if c := ix.SplitKm(line); math.Abs(c["BE"]-countries["BE"]) > 1e-9 || math.Abs(c["FR"]-countries["FR"]) > 1e-9 {
		t.Errorf("SplitKm %v, SplitKmRegions %v: want the same countries", c, countries)
	}
	// A line outside regioned countries has no regions.
	if _, r := ix.SplitKmRegions([][2]float64{{12.55, 55.68}, {9.9937, 53.5511}}); len(r) != 0 {
		t.Errorf("Copenhagen-Hamburg regions %v, want none", r)
	}
}

// densify adds points along each segment, step degrees apart.
func densify(line [][2]float64, step float64) [][2]float64 {
	out := [][2]float64{line[0]}
	for k := 0; k+1 < len(line); k++ {
		a, b := line[k], line[k+1]
		n := int(math.Ceil(math.Hypot(b[0]-a[0], b[1]-a[1]) / step))
		for i := 1; i <= n; i++ {
			f := float64(i) / float64(n)
			out = append(out, [2]float64{a[0] + f*(b[0]-a[0]), a[1] + f*(b[1]-a[1])})
		}
	}
	return out
}

// Areas Natural Earth gives no ISO code: those ISO counts in a country
// get that country's code; Kosovo is XK.
func TestCodesWithoutISO(t *testing.T) {
	ix := index(t)
	for _, tc := range []struct {
		name     string
		lon, lat float64
		want     string
	}{
		{"Northern Cyprus (Kyrenia)", 33.3190, 35.3360, "CY"},
		{"Nicosia, south", 33.3600, 35.1600, "CY"},
		{"Pristina", 21.1655, 42.6629, "XK"},
		{"Hargeisa (Somaliland)", 44.0650, 9.5600, "SO"},
	} {
		if got := ix.Country(tc.lon, tc.lat); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The nearest-border search by latitude band finds what a search of
// every edge finds.
func TestNearestMatchesFullSearch(t *testing.T) {
	ix := index(t)
	full := func(lon, lat, maxKm float64) int {
		cosLat := math.Max(math.Cos(lat*math.Pi/180), 0.01)
		best, bestD := -1, math.Inf(1)
		for ci, c := range ix.countries {
			for _, p := range c.polys {
				for _, r := range p.rings {
					for k := 0; k+1 < len(r.pts); k++ {
						if d := segDistKm(lon, lat, cosLat, r.pts[k], r.pts[k+1]); d < bestD {
							best, bestD = ci, d
						}
					}
				}
			}
		}
		if bestD > maxKm {
			return -1
		}
		return best
	}
	// Harbours, fjords, straits and open sea.
	for _, p := range [][2]float64{{5.32, 60.39}, {12.335, 45.437}, {28.98, 41.02}, {-123.12, 49.29}, {30.31, 59.94},
		{12.5683, 55.6761}, {12.75, 55.57}, {-5.6, 36.0}, {1.5, 51.0}, {-30, 40}} {
		if got, want := ix.nearest(p[0], p[1], NearestKm), full(p[0], p[1], NearestKm); got != want {
			t.Errorf("%v: nearest %d, full search %d", p, got, want)
		}
	}
}

// A borders file with a point out of range is refused.
func TestDecodeOutOfRange(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, []Country{{Code: "AA", Polygons: []Polygon{{{{0, 0}, {20000, 0}, {1, 1}, {0, 0}}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(buf.Bytes()); err == nil {
		t.Error("a longitude of 20,000 degrees was accepted")
	}
}

// Km in a country with regions but in none of them count for
// "<country>-unknown".
func TestUnknownRegion(t *testing.T) {
	sq := func(x0, y0, x1, y1 float64) Polygon {
		return Polygon{{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}}
	}
	ix := NewIndex([]Country{{Code: "AA", Polygons: []Polygon{sq(0, 0, 10, 10)}}}).
		WithRegions([]Country{{Code: "AA-X", Polygons: []Polygon{sq(0, 0, 1, 1)}}})
	_, regions := ix.SplitKmRegions([][2]float64{{0.4, 0.5}, {0.6, 0.5}, {8, 8}, {8.2, 8}})
	if regions["AA-X"] <= 0 || regions["AA-"+UnknownRegion] <= 0 || len(regions) != 2 {
		t.Errorf("regions %v, want AA-X and AA-unknown", regions)
	}
}
