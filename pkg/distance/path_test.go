package distance

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

func TestSeaNetwork_Path(t *testing.T) {
	n := loadTestNet(t)
	n.Avoid["suez"] = true
	at := func(lon, lat float64) calc.Location { return calc.Location{Lon: lon, Lat: lat} }
	r, err := n.Route(calc.WithPath(context.Background()), calc.Sea, at(0.1, 0), at(10, 0.1))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]float64{{0.1, 0}, {0, 0}, {0, 10}, {10, 10}, {10, 0}, {10, 0.1}}
	if len(r.Path) != len(want) {
		t.Fatalf("path %v", r.Path)
	}
	for i := range want {
		if r.Path[i] != want[i] {
			t.Fatalf("path %v, want %v", r.Path, want)
		}
	}
	d, _ := n.Distance(context.Background(), calc.Sea, at(0.1, 0), at(10, 0.1))
	if math.Abs(d-r.Km) > 1e-9 {
		t.Errorf("route %v km, distance %v km", r.Km, d)
	}
	if r, _ := n.Route(context.Background(), calc.Sea, at(0.1, 0), at(10, 0.1)); r.Path != nil {
		t.Error("path without asking")
	}
}

func TestOSRM_Path(t *testing.T) {
	var calls int32
	var path string
	srv := fakeOSRM(t, `{"code":"Ok","routes":[{"distance":1000,"geometry":{"type":"LineString","coordinates":[[12.5,55.6],[11,55],[10,53.5]]}}]}`, 200, &calls, &path)
	o, _ := NewOSRM(srv.URL)
	ctx := calc.WithPath(context.Background())
	r, err := o.Route(ctx, calc.Road, copenhagen, hamburg)
	if err != nil || len(r.Path) != 3 || r.Path[1] != [2]float64{11, 55} {
		t.Fatalf("%+v %v", r, err)
	}
	// Paths aren't cached, and a cached route doesn't stand in for one.
	if _, err := o.Route(context.Background(), calc.Road, copenhagen, hamburg); err != nil || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls %d", calls)
	}
	if r, _ := o.Route(ctx, calc.Road, copenhagen, hamburg); len(r.Path) != 3 || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("path route served from cache: calls %d", calls)
	}
}

// Path detail: simplified asks OSRM for its simplified line, detailed and
// full for the full one; detailed is thinned to calc.MaxDetailedPoints.
func TestOSRM_PathDetail(t *testing.T) {
	var coords strings.Builder
	coords.WriteString("[")
	for i := 0; i < 5000; i++ {
		if i > 0 {
			coords.WriteString(",")
		}
		fmt.Fprintf(&coords, "[%g,%g]", 10+float64(i)*0.0005, 55-float64(i)*0.0003)
	}
	coords.WriteString("]")
	var mu sync.Mutex
	var query string
	body := `{"code":"Ok","routes":[{"distance":1000,"geometry":{"type":"LineString","coordinates":` + coords.String() + `}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		query = r.URL.RawQuery
		mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	o, _ := NewOSRM(srv.URL)
	for _, c := range []struct {
		d        calc.PathDetail
		overview string
		points   int
	}{{calc.PathSimplified, "overview=simplified", 5000}, {calc.PathDetailed, "overview=full", calc.MaxDetailedPoints}, {calc.PathFull, "overview=full", 5000}} {
		r, err := o.Route(calc.WithPathDetail(context.Background(), c.d), calc.Road, copenhagen, hamburg)
		mu.Lock()
		q := query
		mu.Unlock()
		if err != nil || len(r.Path) != c.points || !strings.Contains(q, c.overview) || r.Km != 1 {
			t.Errorf("%s: %d points, %v, asked %q", c.d, len(r.Path), err, q)
		}
		if len(r.Path) > 0 && (r.Path[0] != [2]float64{10, 55} || r.Path[len(r.Path)-1][0] != 10+4999*0.0005) {
			t.Errorf("%s: ends %v %v", c.d, r.Path[0], r.Path[len(r.Path)-1])
		}
	}
}
