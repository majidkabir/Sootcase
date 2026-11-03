package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
)

// splitRouter is a router whose routes split 60/40 between DK and DE when
// a calculation asks for country km, and that counts prefetches.
type splitRouter struct{ prefetches *int32 }

func (s splitRouter) Distance(ctx context.Context, m calc.Mode, from, to calc.Location) (float64, error) {
	r, err := s.Route(ctx, m, from, to)
	return r.Km, err
}

func (splitRouter) Route(ctx context.Context, m calc.Mode, from, to calc.Location) (calc.Route, error) {
	r := calc.Route{Km: 100}
	if calc.WantCountryKm(ctx) {
		r.CountryKm = map[string]float64{"DK": 60, "DE": 40}
	}
	return r, nil
}

func (s splitRouter) Prefetch(ctx context.Context, lanes []calc.Lane) {
	atomic.AddInt32(s.prefetches, 1)
}

func newSplitServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	ds, err := factors.Embedded("sample")
	if err != nil {
		t.Fatal(err)
	}
	var n int32
	s := &Server{Calc: calc.Calculator{Factors: ds, Distances: splitRouter{&n}, Places: places.Codes{}}, Factors: ds}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, &n
}

// country_km on a calculation, a batch (without the distance prefetch,
// which has no lines to split) and a report's results; a bad value is a
// 400; without it, no country km.
func TestCountryKm(t *testing.T) {
	srv, prefetches := newSplitServer(t)
	ship := `{"id":"s","weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"truck_40t","from":{"lat":55.68,"lon":12.57},"to":{"lat":53.55,"lon":9.99}}]}`
	post := func(path, body string) (int, string) {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post("/v1/calculate?country_km=true", ship); code != 200 || !strings.Contains(body, `"country_km":{"DE":40,"DK":60}`) {
		t.Errorf("calculate: %d %s", code, body)
	}
	if code, body := post("/v1/calculate", ship); code != 200 || strings.Contains(body, "country_km") {
		t.Errorf("calculate without the flag: %d %s", code, body)
	}
	if code, _ := post("/v1/calculate?country_km=yes", ship); code != http.StatusBadRequest {
		t.Errorf("country_km=yes: %d, want 400", code)
	}
	batch := `{"shipments":[` + ship + `,` + ship + `]}`
	if code, body := post("/v1/calculate/batch", batch); code != 200 || strings.Contains(body, "country_km") || atomic.LoadInt32(prefetches) != 1 {
		t.Errorf("batch without the flag: %d, prefetches %d", code, *prefetches)
	}
	if code, body := post("/v1/calculate/batch?country_km=true", batch); code != 200 || strings.Count(body, `"country_km"`) != 2 || atomic.LoadInt32(prefetches) != 1 {
		t.Errorf("batch with the flag: %d, prefetches %d (want no new one): %s", code, *prefetches, body)
	}
	if code, body := post("/v1/report?results=true&country_km=true", batch); code != 200 || strings.Count(body, `"country_km"`) != 2 {
		t.Errorf("report results: %d %s", code, body)
	}
}
