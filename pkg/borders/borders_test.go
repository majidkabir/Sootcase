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
