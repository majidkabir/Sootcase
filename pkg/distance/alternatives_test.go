package distance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// step is a test route step: mode, name, ref and km.
type step struct {
	mode, name, ref string
	km              float64
}

// testRoute builds an OSRM route: its steps, and its nodes (the km are
// spread evenly over the node pairs).
func testRoute(weight float64, steps []step, nodes []int64) map[string]any {
	var km float64
	var st []map[string]any
	for _, s := range steps {
		km += s.km
		st = append(st, map[string]any{"distance": s.km * 1000, "mode": s.mode, "name": s.name, "ref": s.ref})
	}
	dist := make([]float64, len(nodes)-1)
	for i := range dist {
		dist[i] = km * 1000 / float64(len(dist))
	}
	return map[string]any{"distance": km * 1000, "duration": weight, "weight": weight,
		"legs": []any{map[string]any{"steps": st, "annotation": map[string]any{"nodes": nodes, "distance": dist}}}}
}

func seq(from, n int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = from + int64(i)
	}
	return out
}

// altServer answers with routes, or land when the request excludes
// ferries; it records the queries.
func altServer(t *testing.T, routes []map[string]any, land map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		body := map[string]any{"code": "Ok", "routes": routes}
		if r.URL.Query().Get("exclude") == "ferry" {
			if land == nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"NoRoute","message":"Impossible route between points"}`))
				return
			}
			body["routes"] = []any{land}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func TestOSRM_Alternatives(t *testing.T) {
	// Primary over a ferry (Gedser–Rostock); OSRM's own alternatives: a
	// near-copy of it (90% shared), one over another ferry, one far too
	// long, one land-free but different. The land-only route comes from
	// the request without ferries.
	primary := testRoute(100, []step{{"driving", "", "E 47", 150}, {"ferry", "Gedser - Rostock", "", 30}, {"ferry", "", "", 18}, {"driving", "", "A 19", 600}, {"driving", "", "", 5}}, seq(0, 101))
	nearCopy := testRoute(101, []step{{"driving", "", "E 47", 160}, {"ferry", "Gedser - Rostock", "", 48}, {"driving", "", "A 19", 590}}, append(seq(0, 91), seq(500, 10)...))
	otherFerry := testRoute(110, []step{{"driving", "", "E 47", 200}, {"ferry", "Rødby (DK) – Puttgarden (D)", "", 19}, {"driving", "", "A 1;E 22", 700}}, seq(1000, 50))
	tooLong := testRoute(150, []step{{"driving", "", "E 45", 1300}}, seq(2000, 40))
	land := testRoute(120, []step{{"driving", "", "E 20", 300}, {"driving", "", "A 7", 800}}, seq(3000, 60))
	srv, queries := altServer(t, []map[string]any{primary, nearCopy, otherFerry, tooLong}, land)
	o, _ := NewOSRM(srv.URL)
	o.PartModes = RoadParts
	ctx := calc.WithRouteOptions(context.Background(), calc.RouteOptions{Alternatives: 4})

	r, err := o.Route(ctx, calc.Road, copenhagen, hamburg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Km != 803 || r.Parts[calc.PartFerry] != 48 || r.DurationH != 100.0/3600 {
		t.Errorf("primary = %v km, parts %v, %v h", r.Km, r.Parts, r.DurationH)
	}
	want := &calc.RouteSummary{Roads: []string{"E 47", "A 19"}, Ferries: []calc.FerryCrossing{{Name: "Gedser - Rostock", From: "Gedser", To: "Rostock", DistanceKm: 48}}}
	if !reflect.DeepEqual(r.Summary, want) {
		t.Errorf("summary = %+v", r.Summary)
	}
	// The near-copy and the too-long route are left out; the land route
	// and the other ferry stay, by OSRM's cost.
	if len(r.Alternatives) != 2 {
		t.Fatalf("alternatives = %+v", r.Alternatives)
	}
	if a := r.Alternatives[0]; a.Km != 919 || a.Summary.Ferries[0].From != "Rødby (DK)" || a.Summary.Ferries[0].To != "Puttgarden (D)" ||
		!reflect.DeepEqual(a.Summary.Roads, []string{"E 47", "A 1"}) {
		t.Errorf("first alternative = %+v %+v", a, a.Summary)
	}
	if a := r.Alternatives[1]; a.Km != 1100 || len(a.Summary.Ferries) != 0 || a.Parts[calc.PartFerry] != 0 {
		t.Errorf("land alternative = %+v %+v", a, a.Summary)
	}
	if len(*queries) != 2 || !strings.Contains((*queries)[0], "alternatives=4") || !strings.Contains((*queries)[0], "annotations=nodes,distance") ||
		!strings.Contains((*queries)[1], "exclude=ferry") {
		t.Errorf("queries = %q", *queries)
	}

	// With room for one: the land route first (ferry against land), even
	// though the other ferry is cheaper.
	ctx2 := calc.WithRouteOptions(context.Background(), calc.RouteOptions{Alternatives: 2})
	r, err = o.Route(ctx2, calc.Road, copenhagen, hamburg)
	if err != nil || len(r.Alternatives) != 1 || r.Alternatives[0].Km != 1100 {
		t.Errorf("one alternative = %+v %v", r.Alternatives, err)
	}
	// The same request gives the same routes in the same order.
	again, _ := o.Route(ctx, calc.Road, copenhagen, hamburg)
	first, _ := o.Route(ctx, calc.Road, copenhagen, hamburg)
	if !reflect.DeepEqual(again, first) {
		t.Error("not repeatable")
	}

	// Without options: one plain request, no summary, no alternatives.
	*queries = nil
	r, err = o.Route(context.Background(), calc.Road, copenhagen, hamburg)
	if err != nil || r.Summary != nil || r.Alternatives != nil || r.DurationH != 0 || len(*queries) != 1 ||
		!strings.Contains((*queries)[0], "alternatives=false") || strings.Contains((*queries)[0], "annotations") {
		t.Errorf("plain = %+v %v %q", r, err, *queries)
	}
	// Not for other modes.
	*queries, o.cache = nil, nil
	if r, _ := o.Route(ctx, calc.InlandWaterway, copenhagen, hamburg); r.Alternatives != nil || !strings.Contains((*queries)[0], "alternatives=false") {
		t.Errorf("waterway = %+v %q", r, *queries)
	}
}

func TestOSRM_AvoidFerries(t *testing.T) {
	primary := testRoute(100, []step{{"driving", "", "E 47", 150}, {"ferry", "Gedser - Rostock", "", 48}, {"driving", "", "A 19", 600}}, seq(0, 50))
	land := testRoute(120, []step{{"driving", "", "E 20", 300}, {"driving", "", "A 7", 800}}, seq(3000, 60))
	srv, queries := altServer(t, []map[string]any{primary}, land)
	o, _ := NewOSRM(srv.URL)
	o.PartModes = RoadParts
	ctx := calc.WithRouteOptions(context.Background(), calc.RouteOptions{AvoidFerries: true})
	r, err := o.Route(ctx, calc.Road, copenhagen, hamburg)
	if err != nil || r.Km != 1100 || r.Parts[calc.PartFerry] != 0 || r.Summary != nil || len(*queries) != 1 {
		t.Errorf("land only = %+v %v %q", r, err, *queries)
	}
	// With alternatives, the primary is land-only and no land route is
	// fetched again.
	*queries = nil
	ctx = calc.WithRouteOptions(context.Background(), calc.RouteOptions{AvoidFerries: true, Alternatives: 3})
	if r, err := o.Route(ctx, calc.Road, copenhagen, hamburg); err != nil || r.Km != 1100 || len(r.Summary.Ferries) != 0 || len(*queries) != 1 {
		t.Errorf("land only with alternatives = %+v %v %q", r, err, *queries)
	}

	// No land route: refused, saying so.
	srv2, _ := altServer(t, []map[string]any{primary}, nil)
	o2, _ := NewOSRM(srv2.URL)
	o2.PartModes = RoadParts
	if _, err := o2.Route(calc.WithRouteOptions(context.Background(), calc.RouteOptions{AvoidFerries: true}), calc.Road, copenhagen, hamburg); err == nil ||
		!strings.Contains(err.Error(), "no land route") {
		t.Errorf("no land route: %v", err)
	}
	// Asking for alternatives where there is no land route: the ferry
	// route stays alone, without error.
	r, err = o2.Route(calc.WithRouteOptions(context.Background(), calc.RouteOptions{Alternatives: 3}), calc.Road, copenhagen, hamburg)
	if err != nil || len(r.Alternatives) != 0 || r.Parts[calc.PartFerry] != 48 {
		t.Errorf("no land alternative = %+v %v", r, err)
	}
}

// A land-only primary gets a ferry route among its alternatives when OSRM
// finds one, ahead of cheaper land routes.
func TestPickAlternatives_FerryForLand(t *testing.T) {
	o := &OSRM{PartModes: RoadParts}
	q := routeQuery{summary: true, nodes: true}
	parse := func(m map[string]any) osrmRoute {
		b, _ := json.Marshal(m)
		var raw rawRoute
		_ = json.Unmarshal(b, &raw)
		return o.build(raw, q)
	}
	primary := parse(testRoute(100, []step{{"driving", "", "A 7", 500}}, seq(0, 50)))
	landA := parse(testRoute(105, []step{{"driving", "", "A 1", 520}}, seq(100, 50)))
	landB := parse(testRoute(106, []step{{"driving", "", "A 2", 530}}, seq(200, 50)))
	ferry := parse(testRoute(130, []step{{"driving", "", "E 6", 300}, {"ferry", "Göteborg - Kiel", "", 250}}, seq(300, 50)))
	got := pickAlternatives(primary, []osrmRoute{landA, landB, ferry}, 2)
	if len(got) != 2 || got[0].Km != 520 || got[1].Km != 550 {
		t.Errorf("picked %v, %v", got[0].Km, got)
	}
	if got := pickAlternatives(primary, nil, 4); len(got) != 0 {
		t.Errorf("none: %v", got)
	}
}

func TestPorts(t *testing.T) {
	for name, want := range map[string][2]string{
		"Rødby (DK) - Puttgarden (D)": {"Rødby (DK)", "Puttgarden (D)"},
		"Rostock (D) – Gedser (S)":    {"Rostock (D)", "Gedser (S)"},
		"Øresundslinjen":              {"", ""},
		"A - B - C":                   {"", ""},
	} {
		if a, b := ports(name); a != want[0] || b != want[1] {
			t.Errorf("%q: %q %q", name, a, b)
		}
	}
}
