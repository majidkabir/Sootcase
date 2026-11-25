package distance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// fakeTableOSRM answers tables with distance = 1000 m x (row+1) x (col+1);
// with exclude=ferry, lane (0,1) is 5 km longer, as if it took a ferry.
// Routes answer 42 km with a 10 km ferry step.
func fakeTableOSRM(t *testing.T, tables, routes *int32, excludeOK bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/route/") {
			atomic.AddInt32(routes, 1)
			_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":42000,"legs":[{"steps":[{"distance":32000,"mode":"driving"},{"distance":10000,"mode":"ferry"}]}]}]}`))
			return
		}
		atomic.AddInt32(tables, 1)
		// OSRM separates list values with ';', which url.ParseQuery rejects.
		q := map[string]string{}
		for _, kv := range strings.Split(r.URL.RawQuery, "&") {
			k, v, _ := strings.Cut(kv, "=")
			q[k] = v
		}
		ex := q["exclude"]
		if ex != "" && !excludeOK {
			_, _ = w.Write([]byte(`{"code":"InvalidValue","message":"Exclude flag combination is not supported."}`))
			return
		}
		ns := len(strings.Split(q["sources"], ";"))
		nd := len(strings.Split(q["destinations"], ";"))
		d := make([][]float64, ns)
		for i := range d {
			d[i] = make([]float64, nd)
			for j := range d[i] {
				d[i][j] = 1000 * float64((i+1)*(j+1))
				if ex == "ferry" && i == 0 && j == 1 {
					d[i][j] += 5000
				}
			}
		}
		snaps := func(n int) []map[string]float64 {
			s := make([]map[string]float64, n)
			for i := range s {
				s[i] = map[string]float64{"distance": 10}
			}
			return s
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "Ok", "distances": d, "sources": snaps(ns), "destinations": snaps(nd)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// batch is a batch's context: it takes the distances Prefetch fetched.
var batch = calc.WithTableRoutes(context.Background())

func TestOSRM_PrefetchFerryCheck(t *testing.T) {
	var tables, routes int32
	srv := fakeTableOSRM(t, &tables, &routes, true)
	o, _ := NewOSRM(srv.URL)
	o.PartModes = RoadParts
	a := calc.Location{Lat: 55, Lon: 12}
	b := calc.Location{Lat: 56, Lon: 10}
	c := calc.Location{Lat: 54, Lon: 9}
	lanes := []calc.Lane{{Mode: calc.Road, From: a, To: b}, {Mode: calc.Road, From: a, To: c}, {Mode: calc.Road, From: b, To: c}}
	ByMode{Providers: map[calc.Mode]calc.DistanceProvider{calc.Road: o}}.Prefetch(context.Background(), lanes)
	if tables != 2 {
		t.Fatalf("tables = %d, want 2 (with and without ferries)", tables)
	}
	// Sources a,b; destinations b,c. a->b is (0,0), a->c (0,1), b->c (1,1).
	r, err := o.Route(batch, calc.Road, a, b)
	if err != nil || r.Km != 1 || r.Parts != nil || routes != 0 {
		t.Errorf("a->b = %+v %v, routes %d", r, err, routes)
	}
	if r, _ := o.Route(batch, calc.Road, b, c); r.Km != 4 || routes != 0 {
		t.Errorf("b->c = %+v, routes %d", r, routes)
	}
	// a->c differs without ferries: it was left for a full route with steps.
	r, err = o.Route(batch, calc.Road, a, c)
	if err != nil || routes != 1 || r.Parts[calc.PartFerry] != 10 {
		t.Errorf("a->c = %+v %v, routes %d", r, err, routes)
	}
	// A single calculation doesn't take a table's km: it routes, for the
	// leg's summary and driving time.
	if _, err := o.Route(context.Background(), calc.Road, a, b); err != nil || routes != 2 {
		t.Errorf("single a->b: %v, routes %d, want a route", err, routes)
	}
}

func TestOSRM_PrefetchNoExclude(t *testing.T) {
	var tables, routes int32
	srv := fakeTableOSRM(t, &tables, &routes, false)
	o, _ := NewOSRM(srv.URL)
	o.PartModes = RoadParts
	a, b := calc.Location{Lat: 55, Lon: 12}, calc.Location{Lat: 56, Lon: 10}
	o.Prefetch(context.Background(), []calc.Lane{{Mode: calc.Road, From: a, To: b}})
	// The profile can't exclude ferries, so nothing is cached and the route
	// is fetched with steps.
	if _, err := o.Route(batch, calc.Road, a, b); err != nil || routes != 1 {
		t.Errorf("routes = %d, %v", routes, err)
	}

	// Without part modes (e.g. waterways), tables alone fill the cache.
	tables, routes = 0, 0
	w, _ := NewOSRM(srv.URL)
	w.Prefetch(context.Background(), []calc.Lane{{Mode: calc.InlandWaterway, From: a, To: b}})
	if d, _ := w.Distance(batch, calc.InlandWaterway, a, b); d != 1 || tables != 1 || routes != 0 {
		t.Errorf("d = %v, tables %d, routes %d", d, tables, routes)
	}

	// Rail needs steps for electrification: no prefetch at all.
	tables = 0
	rl, _ := NewOSRM(srv.URL)
	rl.PartModes = RailParts
	rl.Prefetch(context.Background(), []calc.Lane{{Mode: calc.Rail, From: a, To: b}})
	if tables != 0 {
		t.Errorf("rail prefetched %d tables", tables)
	}
}

func TestOSRM_PrefetchSkipsScatteredLanes(t *testing.T) {
	var tables, routes int32
	srv := fakeTableOSRM(t, &tables, &routes, true)
	o, _ := NewOSRM(srv.URL)
	pt := func(i int) calc.Location { return calc.Location{Lat: 50 + float64(i)/10, Lon: 5 + float64(i)/10} }
	// 10 unrelated lanes: a 10 x 10 table for 10 lanes is too wasteful.
	var scattered []calc.Lane
	for i := 0; i < 10; i++ {
		scattered = append(scattered, calc.Lane{Mode: calc.Road, From: pt(i), To: pt(100 + i)})
	}
	o.Prefetch(context.Background(), scattered)
	if tables != 0 {
		t.Fatalf("%d tables for scattered lanes", tables)
	}
	// 10 lanes from one origin: one row, worth a table.
	var fan []calc.Lane
	for i := 0; i < 10; i++ {
		fan = append(fan, calc.Lane{Mode: calc.Road, From: pt(0), To: pt(200 + i)})
	}
	o.Prefetch(context.Background(), fan)
	if tables != 1 {
		t.Fatalf("%d tables for lanes from one origin", tables)
	}
}
