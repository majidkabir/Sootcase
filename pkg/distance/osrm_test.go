package distance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// fakeOSRM answers /route/v1/... like an OSRM server and counts calls.
func fakeOSRM(t *testing.T, body string, status int, calls *int32, gotPath *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		*gotPath = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOSRM_Distance(t *testing.T) {
	var calls int32
	var path string
	srv := fakeOSRM(t, `{"code":"Ok","routes":[{"distance":312456.7,"duration":12000}]}`, 200, &calls, &path)
	o, err := NewOSRM(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	d, err := o.Distance(context.Background(), calc.Road, copenhagen, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	if d != 312.4567 {
		t.Errorf("distance = %v km, want 312.4567", d)
	}
	// OSRM takes lon,lat order.
	if want := "/route/v1/driving/12.56830,55.67610;9.99370,53.55110"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	// Second identical call is served from cache.
	if _, err := o.Distance(context.Background(), calc.Road, copenhagen, hamburg); err != nil || calls != 1 {
		t.Errorf("calls = %d, err = %v; want cached", calls, err)
	}
	if o.DistanceMethod(calc.Road) != "osrm" {
		t.Error("method name")
	}
}

func TestOSRM_Errors(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
		want   string
	}{
		"no route": {`{"code":"NoRoute","message":"Impossible route between points"}`, 400, "NoRoute"},
		"empty":    {`{"code":"Ok","routes":[]}`, 200, "no route"},
		"not json": {`<html>bad gateway</html>`, 502, "HTTP 502"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int32
			var path string
			srv := fakeOSRM(t, tc.body, tc.status, &calls, &path)
			o, _ := NewOSRM(srv.URL)
			_, err := o.Distance(context.Background(), calc.Road, copenhagen, hamburg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	for _, bad := range []string{"", "localhost:5000", "ftp://x", "http://"} {
		if _, err := NewOSRM(bad); err == nil {
			t.Errorf("NewOSRM(%q): want error", bad)
		}
	}
}

func TestByMode(t *testing.T) {
	var calls int32
	var path string
	srv := fakeOSRM(t, `{"code":"Ok","routes":[{"distance":100000}]}`, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	gc, _ := NewGreatCircle([]Adjustment{{Mode: calc.Air, AddKm: 50}})
	b := ByMode{Providers: map[calc.Mode]calc.DistanceProvider{calc.Road: o}, Fallback: gc}

	c := calc.Calculator{Factors: fakeFactors{}, Distances: b}
	// Not a chain of legs, so no road legs between them.
	res, err := c.Calculate(calc.WithoutRoadLegs(context.Background()), calc.Shipment{WeightKg: 1000, Legs: []calc.Leg{
		{ID: "road", Mode: calc.Road, VehicleType: "x", From: copenhagen, To: hamburg},
		{ID: "rail", Mode: calc.Rail, VehicleType: "x", From: copenhagen, To: hamburg},
		{ID: "air", Mode: calc.Air, VehicleType: "x", From: copenhagen, To: hamburg},
		{ID: "given", Mode: calc.Road, VehicleType: "x", DistanceKm: 7},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if b.DistanceType(calc.Road) != calc.DistSFD || b.DistanceType(calc.Rail) != calc.DistGCD || b.DistanceType(calc.Air) != "" {
		t.Errorf("distance types: road %q rail %q air %q", b.DistanceType(calc.Road), b.DistanceType(calc.Rail), b.DistanceType(calc.Air))
	}
	want := map[string]struct {
		km     float64
		method string
	}{
		"road":  {100, "osrm"},
		"rail":  {Haversine(copenhagen, hamburg), "great_circle"},
		"air":   {Haversine(copenhagen, hamburg) + 50, "great_circle_adjusted"},
		"given": {7, calc.DistanceGiven},
	}
	for _, l := range res.Legs {
		w := want[l.LegID]
		if l.DistanceKm != w.km || l.DistanceMethod != w.method {
			t.Errorf("%s: %v km via %q, want %v via %q", l.LegID, l.DistanceKm, l.DistanceMethod, w.km, w.method)
		}
	}
}

// fakeFactors returns a 1 g/tkm factor for anything.
type fakeFactors struct{}

func (fakeFactors) LegFactor(m calc.Mode, vt string) (calc.Factor, error) {
	return calc.Factor{Mode: m, VehicleType: vt, TTW: 1}, nil
}
func (fakeFactors) HubFactor(string) (calc.HubFactor, error)   { return calc.HubFactor{}, nil }
func (fakeFactors) FuelFactor(string) (calc.FuelFactor, error) { return calc.FuelFactor{}, nil }

func TestOSRM_Waterway(t *testing.T) {
	var calls int32
	var path string
	srv := fakeOSRM(t, `{"code":"Ok","routes":[{"distance":220000}],"waypoints":[{"distance":800},{"distance":15500}]}`, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	o.Method, o.MaxSnapKm = "osrm_waterway", 10
	_, err := o.Distance(context.Background(), calc.InlandWaterway, copenhagen, hamburg)
	var off *calc.OffNetworkError
	if !errors.As(err, &off) || off.FromOff() || !off.ToOff() || !strings.Contains(err.Error(), "destination is 15.5 km from the waterway network") {
		t.Errorf("want snap error, got %v", err)
	}
	o.MaxSnapKm = 20
	if d, err := o.Distance(context.Background(), calc.InlandWaterway, copenhagen, hamburg); err != nil || d != 220 {
		t.Errorf("d = %v, %v", d, err)
	}
	if o.DistanceMethod(calc.InlandWaterway) != "osrm_waterway" || o.DistanceType(calc.InlandWaterway) != calc.DistSFD {
		t.Error("method or type")
	}
}

func TestOSRM_RouteParts(t *testing.T) {
	var calls int32
	var path string
	// Steps as OSRM sends them with geometries=polyline: each step's
	// geometry is an encoded string.
	body := `{"code":"Ok","routes":[{"distance":120000,"legs":[
		{"steps":[{"distance":50000,"mode":"driving","geometry":"_p~iF~ps|U_ulLnnqC"},{"distance":19000,"mode":"ferry","geometry":"_mqNvxq@"},
			{"distance":0,"mode":"ferry","geometry":"??"}]},
		{"steps":[{"distance":1500,"mode":"ferry","geometry":"??"},{"distance":49500,"mode":"driving","geometry":"_p~iF~ps|U"}]}]}]}`
	srv := fakeOSRM(t, body, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	o.PartModes = RoadParts
	r, err := o.Route(context.Background(), calc.Road, copenhagen, hamburg)
	if err != nil || r.Km != 120 || len(r.Parts) != 1 || r.Parts[calc.PartFerry] != 20.5 {
		t.Errorf("route = %+v, %v", r, err)
	}
	// Distance and Route share the cache.
	if d, _ := o.Distance(context.Background(), calc.Road, copenhagen, hamburg); d != 120 || calls != 1 {
		t.Errorf("d = %v, calls = %d", d, calls)
	}
	// Through ByMode, and for a provider without parts.
	b := ByMode{Providers: map[calc.Mode]calc.DistanceProvider{calc.Road: o}, Fallback: GreatCircle{}}
	if r, _ := b.Route(context.Background(), calc.Road, copenhagen, hamburg); r.Parts[calc.PartFerry] != 20.5 {
		t.Errorf("by mode %+v", r)
	}
	if r, err := b.Route(context.Background(), calc.Air, copenhagen, hamburg); err != nil || r.Parts != nil || r.Km < 280 {
		t.Errorf("fallback %+v %v", r, err)
	}
}

func TestOSRM_TemporaryOrNot(t *testing.T) {
	var calls int32
	var path string
	for _, c := range []struct {
		name, body string
		status     int
		temporary  bool
	}{
		{"no route", `{"code":"NoRoute","message":"Impossible route"}`, 400, false},
		{"no segment", `{"code":"NoSegment","message":"Could not find a matching segment"}`, 400, false},
		{"bad gateway", `<html>bad gateway</html>`, 502, true},
		{"busy", `{"code":"TooBusy"}`, 503, true},
		{"rate limited", ``, 429, true},
		{"cut off", `{"code":"Ok","routes":[{"dist`, 200, true},
		{"bad request page", `<html>no</html>`, 400, false},
	} {
		srv := fakeOSRM(t, c.body, c.status, &calls, &path)
		o, _ := NewOSRM(srv.URL)
		_, err := o.Distance(context.Background(), calc.Road, copenhagen, hamburg)
		if err == nil || calc.Temporary(err) != c.temporary {
			t.Errorf("%s: error %v, temporary %v, want %v", c.name, err, calc.Temporary(err), c.temporary)
		}
		srv.Close()
	}
	// Nothing listening.
	o, _ := NewOSRM("http://127.0.0.1:1")
	if _, err := o.Distance(context.Background(), calc.Road, copenhagen, hamburg); !calc.Temporary(err) {
		t.Errorf("unreachable: %v, want temporary", err)
	}
	// A cancelled request isn't the server's fault, nor worth retrying.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := o.Distance(ctx, calc.Road, copenhagen, hamburg); calc.Temporary(err) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// No more than MaxInFlight requests reach the server at a time; the rest
// wait their turn instead of timing out.
func TestOSRM_MaxInFlight(t *testing.T) {
	var now, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&now, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&now, -1)
		_, _ = io.WriteString(w, `{"code":"Ok","routes":[{"distance":1000}]}`)
	}))
	defer srv.Close()
	o, _ := NewOSRM(srv.URL)
	o.MaxInFlight = 3
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			to := calc.Location{Lat: 50 + float64(i)/100, Lon: 10}
			if _, err := o.Distance(context.Background(), calc.Road, copenhagen, to); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if peak > 3 || peak < 2 {
		t.Errorf("peak %d requests at a time, want at most 3", peak)
	}
}

// splitByLon puts a line in "DK" if its first point is
// east of 11°E, else in "DE", with a third of each line in "" (sea).
type splitByLon struct{}

func (splitByLon) SplitKm(line [][2]float64) map[string]float64 {
	if len(line) < 2 {
		return nil
	}
	c := "DE"
	if line[0][0] > 11 {
		c = "DK"
	}
	return map[string]float64{c: 2, "": 1}
}

func TestOSRM_CountryKm(t *testing.T) {
	var calls int32
	var query string
	body := `{"code":"Ok","routes":[{"distance":130000,"legs":[{"steps":[
		{"distance":60000,"mode":"driving","geometry":{"coordinates":[[12.5,55.6],[11.9,54.7]]}},
		{"distance":19000,"mode":"ferry","geometry":{"coordinates":[[11.3,54.6],[11.2,54.5]]}},
		{"distance":51000,"mode":"driving","geometry":{"coordinates":[[10.9,54.4],[10.0,53.5]]}},
		{"distance":0,"mode":"driving","geometry":{"coordinates":[[10.0,53.5]]}}]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	o, err := NewOSRM(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	o.PartModes, o.Countries = RoadParts, splitByLon{}

	// Not asked for: no split.
	r, err := o.Route(context.Background(), calc.Road, copenhagen, hamburg)
	if err != nil || r.CountryKm != nil {
		t.Fatalf("plain route: %+v, %v; want no CountryKm", r, err)
	}
	ctx := calc.WithCountryKm(context.Background())
	r, err = o.Route(ctx, calc.Road, copenhagen, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "steps=true") || !strings.Contains(query, "geometries=geojson") {
		t.Errorf("query %q: want steps and GeoJSON geometry", query)
	}
	// 111 km on land: each driving step 2/3 in its country, 1/3 unknown.
	want := map[string]float64{"DK": 40, "DE": 34, calc.CountryUnknown: 37}
	for k, v := range want {
		if d := r.CountryKm[k] - v; d > 1e-9 || d < -1e-9 {
			t.Errorf("CountryKm[%s] = %v, want %v (all: %v)", k, r.CountryKm[k], v, r.CountryKm)
		}
	}
	if len(r.CountryKm) != len(want) || r.Parts[calc.PartFerry] != 19 {
		t.Errorf("got %v and parts %v", r.CountryKm, r.Parts)
	}
	// The split route is cached apart from the plain one.
	if _, err := o.Route(ctx, calc.Road, copenhagen, hamburg); err != nil || calls != 2 {
		t.Errorf("calls = %d, err = %v; want the split route cached", calls, err)
	}
}
