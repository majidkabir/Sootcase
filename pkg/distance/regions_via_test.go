package distance

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// geoOSRM is a fake OSRM server whose roads run in straight lines inside
// its box: every point snaps to itself (or to the box's edge, for points
// outside it), and distances are great circles. snapKm, if set, says how
// far a point is from the network.
func geoOSRM(t *testing.T, b box, calls *int32, snapKm func(calc.Location) float64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		parts := strings.Split(r.URL.Path, "/")
		var ps []calc.Location
		for _, c := range strings.Split(parts[len(parts)-1], ";") {
			lon, _ := strconv.ParseFloat(strings.Split(c, ",")[0], 64)
			lat, _ := strconv.ParseFloat(strings.Split(c, ",")[1], 64)
			p := calc.Location{Lon: math.Max(b.minLon, math.Min(b.maxLon, lon)), Lat: math.Max(b.minLat, math.Min(b.maxLat, lat))}
			ps = append(ps, p)
		}
		snap := func(p calc.Location) float64 {
			if snapKm == nil {
				return 0
			}
			return snapKm(p) * 1000
		}
		type wp struct {
			Distance float64    `json:"distance"`
			Location [2]float64 `json:"location"`
		}
		w.Header().Set("Content-Type", "application/json")
		if parts[1] == "route" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":      "Ok",
				"routes":    []map[string]any{{"distance": Haversine(ps[0], ps[1]) * 1000, "geometry": map[string]any{"coordinates": [][2]float64{{ps[0].Lon, ps[0].Lat}, {ps[1].Lon, ps[1].Lat}}}}},
				"waypoints": []wp{{Distance: snap(ps[0])}, {Distance: snap(ps[1])}},
			})
			return
		}
		idx := func(name string) []int {
			var out []int
			var v string
			for _, kv := range strings.Split(r.URL.RawQuery, "&") { // Query() drops values with ';'
				if k, x, _ := strings.Cut(kv, "="); k == name {
					v = x
				}
			}
			for _, s := range strings.Split(v, ";") {
				i, _ := strconv.Atoi(s)
				out = append(out, i)
			}
			return out
		}
		src, dst := idx("sources"), idx("destinations")
		var dist [][]*float64
		var sw, dw []wp
		for _, i := range src {
			var row []*float64
			for _, j := range dst {
				d := Haversine(ps[i], ps[j]) * 1000
				row = append(row, &d)
			}
			dist = append(dist, row)
			sw = append(sw, wp{snap(ps[i]), [2]float64{ps[i].Lon, ps[i].Lat}})
		}
		for _, j := range dst {
			dw = append(dw, wp{snap(ps[j]), [2]float64{ps[j].Lon, ps[j].Lat}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "Ok", "distances": dist, "sources": sw, "destinations": dw})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func viaRegions(t *testing.T, calls *[3]int32, snapKm func(calc.Location) float64) Regions {
	t.Helper()
	europe := box{-25, 27, 70, 72}
	asia := box{25, -12, 180, 82}
	americas := box{-170, -57, -25, 84}
	e := geoOSRM(t, europe, &calls[0], snapKm)
	a := geoOSRM(t, asia, &calls[1], snapKm)
	m := geoOSRM(t, americas, &calls[2], snapKm)
	rs, err := ParseRegions("europe="+e.URL+"@-25,27,70,72;asia="+a.URL+"@25,-12,180,82;americas="+m.URL+"@-170,-57,-25,84",
		func(o *OSRM) { o.MaxSnapKm = 50 })
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestRegions_Via(t *testing.T) {
	viaCache.Lock()
	viaCache.m = nil
	viaCache.Unlock()
	var calls [3]int32
	rs := viaRegions(t, &calls, nil)
	shanghai := calc.Location{Lat: 31.2304, Lon: 121.4737}
	ctx := calc.WithPath(context.Background())

	r, err := rs.Route(ctx, calc.Road, shanghai, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	// The straight line between them crosses the overlap, so the best
	// gateway is nearly on it.
	direct := Haversine(shanghai, hamburg)
	if r.Km < direct-0.01 || r.Km > direct*1.001 {
		t.Errorf("via route %.1f km, direct %.1f km", r.Km, direct)
	}
	if len(r.Path) != 4 || r.Path[1] != r.Path[2] {
		t.Errorf("path: %v", r.Path)
	}
	g := r.Path[1]
	if g[0] < 25 || g[0] > 70 || g[1] < 27 || g[1] > 72 {
		t.Errorf("gateway %v is outside the overlap", g)
	}
	if calls[0] == 0 || calls[1] == 0 || calls[2] != 0 {
		t.Errorf("calls %v", calls)
	}

	// Both directions, and the gateway is remembered.
	before := calls
	if r2, err := rs.Route(context.Background(), calc.Road, shanghai, hamburg); err != nil || math.Abs(r2.Km-r.Km) > 1e-9 {
		t.Errorf("again: %v %v", r2.Km, err)
	}
	if calls[0]-before[0] != 1 || calls[1]-before[1] != 1 {
		t.Errorf("cached gateway should cost one route per region, got %v -> %v", before, calls)
	}
	if back, err := rs.Route(context.Background(), calc.Road, hamburg, shanghai); err != nil || math.Abs(back.Km-r.Km) > direct*0.001 {
		t.Errorf("back: %v %v", back.Km, err)
	}

	// Regions that only touch (Americas and Europe at 25° W) aren't joined.
	newYork := calc.Location{Lat: 40.71, Lon: -74.0}
	if _, err := rs.Route(ctx, calc.Road, newYork, hamburg); err == nil || !strings.Contains(err.Error(), "no routing region") {
		t.Errorf("New York: %v", err)
	}
}

func TestRegions_ViaOffNetwork(t *testing.T) {
	viaCache.Lock()
	viaCache.m = nil
	viaCache.Unlock()
	var calls [3]int32
	shanghai := calc.Location{Lat: 31.2304, Lon: 121.4737}
	rs := viaRegions(t, &calls, func(p calc.Location) float64 {
		if Haversine(p, shanghai) < 1 {
			return 80
		}
		return 0
	})
	_, err := rs.Route(context.Background(), calc.Road, shanghai, hamburg)
	var off *calc.OffNetworkError
	if !errors.As(err, &off) || !off.FromOff() || off.ToOff() {
		t.Errorf("want origin off the network, got %v", err)
	}
}

func TestEdgePoints(t *testing.T) {
	ps := edgePoints(box{25, 27, 70, 72}, 0.5)
	if len(ps) != 4*86 {
		t.Errorf("%d points", len(ps))
	}
	for _, p := range ps {
		if p.Lon < 26 || p.Lon > 69 || p.Lat < 28 || p.Lat > 71 {
			t.Fatalf("%v is not on the inset edge", p)
		}
	}
	if _, ok := overlap(Region{MinLon: -170, MinLat: -57, MaxLon: -25, MaxLat: 84}, Region{MinLon: -25, MinLat: 27, MaxLon: 70, MaxLat: 72}); ok {
		t.Error("touching regions overlap")
	}
}

// A leg across regions keeps the warnings about its own stops, not those
// about the point where the two halves meet.
func TestEndWarnings(t *testing.T) {
	w := func(end string) calc.Warning {
		return calc.Warning{Code: calc.WarnTruckStop, Text: "the " + end + " point is on a road this vehicle may not use"}
	}
	r1 := []calc.Warning{w("start"), w("end")}
	r2 := []calc.Warning{w("start"), w("end"), {Code: "other", Text: "x"}}
	got := append(endWarnings(r1, "start"), endWarnings(r2, "end")...)
	if len(got) != 3 || got[0] != w("start") || got[1] != w("end") || got[2].Code != "other" {
		t.Errorf("warnings %v", got)
	}
}
