package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/places"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ds, err := factors.Embedded("sample")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Calc: calc.Calculator{Factors: ds, Distances: distance.GreatCircle{}, Places: places.Codes{}}, Factors: ds}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp, out
}

func TestCalculate(t *testing.T) {
	srv := newTestServer(t)
	// sample truck_40t: 60 + 15 g/tkm. 10 t x 500 km -> 375 kg WTW
	resp, out := post(t, srv.URL+"/v1/calculate",
		`{"id":"s1","weight_kg":10000,"legs":[{"id":"l1","mode":"road","vehicle_type":"truck_40t","distance_km":500}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	if out["wtw_kg"].(float64) != 375 {
		t.Errorf("wtw_kg = %v, want 375", out["wtw_kg"])
	}
}

func TestCalculate_Errors(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		name, body string
		status     int
	}{
		{"bad json", `{`, http.StatusBadRequest},
		{"unknown field", `{"weight_kg":1,"legz":[]}`, http.StatusBadRequest},
		{"zero weight", `{"weight_kg":0,"legs":[{"mode":"road"}]}`, http.StatusUnprocessableEntity},
		{"unknown vehicle", `{"weight_kg":1,"legs":[{"mode":"road","vehicle_type":"x","distance_km":1}]}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := post(t, srv.URL+"/v1/calculate", tc.body)
			if resp.StatusCode != tc.status || out["error"] == "" {
				t.Errorf("status %d body %v, want %d with error", resp.StatusCode, out, tc.status)
			}
		})
	}
}

func TestCalculateBatch(t *testing.T) {
	srv := newTestServer(t)
	resp, out := post(t, srv.URL+"/v1/calculate/batch", `{"shipments":[
		{"id":"ok","weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"truck_40t","distance_km":100}]},
		{"id":"bad","weight_kg":0,"legs":[{"mode":"road"}]}
	]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	results := out["results"].([]any)
	if len(results) != 2 || out["failed"].(float64) != 1 {
		t.Fatalf("got %v", out)
	}
	if results[0].(map[string]any)["result"] == nil || results[1].(map[string]any)["error"] == nil {
		t.Errorf("got %v", results)
	}
}

func TestGetEndpoints(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/healthz", "/v1/factors", "/openapi.yaml"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/v1/calculate")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/calculate: status %d, want 405", resp.StatusCode)
	}
}

func TestReport(t *testing.T) {
	srv := newTestServer(t)
	body := `{"organisation":"Acme","period_from":"2026-01-01","period_to":"2026-03-31","shipments":[
		{"id":"ok","weight_kg":10000,"legs":[{"mode":"road","vehicle_type":"truck_40t","distance_km":500}]},
		{"id":"bad","weight_kg":0,"legs":[{"mode":"road"}]}]}`
	resp, out := post(t, srv.URL+"/v1/report", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	total := out["total"].(map[string]any)
	cov := out["coverage"].(map[string]any)
	if total["wtw_kg"].(float64) != 375 || cov["failed"].(float64) != 1 || out["organisation"] != "Acme" {
		t.Errorf("got total %v coverage %v", total, cov)
	}

	r, err := http.Post(srv.URL+"/v1/report?format=md", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var md strings.Builder
	_, _ = io.Copy(&md, r.Body)
	if r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/markdown") ||
		!strings.Contains(md.String(), "**375.0**") {
		t.Errorf("markdown %d %q:\n%s", r.StatusCode, r.Header.Get("Content-Type"), md.String())
	}

	for name, bad := range map[string]string{
		"bad date":  `{"period_from":"1 Jan","shipments":[]}`,
		"long text": `{"title":"` + strings.Repeat("x", maxMetaLen+1) + `","shipments":[]}`,
		"unknown":   `{"tittle":"x","shipments":[]}`,
	} {
		if resp, _ := post(t, srv.URL+"/v1/report", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}
}

func TestReport_CSV(t *testing.T) {
	srv := newTestServer(t)
	csv := "shipment_id,weight_kg,mode,vehicle_type,distance_km\nA,10000,road,truck_40t,500\nB,1000,road,truck_40t,abc\n"
	resp, err := http.Post(srv.URL+"/v1/report?organisation=Acme&period_from=2026-01-01", "text/csv; charset=utf-8", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	cov := out["coverage"].(map[string]any)
	if resp.StatusCode != 200 || out["total"].(map[string]any)["wtw_kg"].(float64) != 375 ||
		cov["shipments"].(float64) != 2 || cov["failed"].(float64) != 1 || out["organisation"] != "Acme" {
		t.Errorf("status %d: %v", resp.StatusCode, out)
	}
	for name, bad := range map[string]string{"no header": "", "no id": "weight_kg\n1\n"} {
		r, _ := http.Post(srv.URL+"/v1/report", "text/csv", strings.NewReader(bad))
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, r.StatusCode)
		}
	}
	r, _ := http.Post(srv.URL+"/v1/report?period_to=yesterday", "text/csv", strings.NewReader(csv))
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad date: status %d", r.StatusCode)
	}
}

func TestReport_WithResultsAndMarkdown(t *testing.T) {
	srv := newTestServer(t)
	csv := "shipment_id,weight_kg,mode,vehicle_type,distance_km\nB,1000,road,truck_40t,abc\nA,10000,road,truck_40t,500\nC,2000,road,truck_40t,100\n"
	resp, err := http.Post(srv.URL+"/v1/report?results=true&markdown=true", "text/csv", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out ReportResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || out.Coverage.Shipments != 3 || !strings.Contains(out.Markdown, "# ") || len(out.Results) != 3 {
		t.Fatalf("status %d, coverage %+v, markdown %d bytes, %d results", resp.StatusCode, out.Coverage, len(out.Markdown), len(out.Results))
	}
	// File order, the failed row included.
	for i, want := range []string{"B", "A", "C"} {
		if out.Results[i].ShipmentID != want {
			t.Errorf("result %d is %s, want %s", i, out.Results[i].ShipmentID, want)
		}
	}
	if out.Results[0].Error == "" || out.Results[1].Result == nil || out.Results[1].Result.WTWKg != 375 || len(out.Results[1].Result.Legs) != 1 {
		t.Errorf("results: %+v", out.Results)
	}
	// Without the flags, the report alone.
	r, _ := http.Post(srv.URL+"/v1/report", "text/csv", strings.NewReader(csv))
	var plain map[string]any
	_ = json.NewDecoder(r.Body).Decode(&plain)
	r.Body.Close()
	if _, ok := plain["results"]; ok {
		t.Error("results without ?results=true")
	}
}

func TestPlaces(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/v1/places?q=rotterdam&kind=port&limit=3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out PlacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(out.Places) == 0 || out.Places[0].Code != "NLRTM" || len(out.Places) > 3 {
		t.Fatalf("status %d: %+v", resp.StatusCode, out)
	}
	resp2, err := http.Get(srv.URL + "/v1/places?q=")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	b, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != 200 || !strings.Contains(string(b), `"places":[]`) {
		t.Fatalf("empty query: %d %s", resp2.StatusCode, b)
	}
}

func TestCalculate_LocationCode(t *testing.T) {
	srv := newTestServer(t)
	resp, out := post(t, srv.URL+"/v1/calculate",
		`{"weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"truck_40t","from":{"code":"DKCPH"},"to":{"code":"DEHAM"}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	resp, out = post(t, srv.URL+"/v1/calculate",
		`{"weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"truck_40t","from":{"code":"QQQQQ"},"to":{"code":"DEHAM"}}]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["error"]), "unknown location code") {
		t.Fatalf("unknown code: %d %v", resp.StatusCode, out)
	}
}

func TestCalculate_Path(t *testing.T) {
	srv := newTestServer(t)
	body := `{"weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"truck_40t","from":{"lat":55.68,"lon":12.57},"to":{"lat":53.55,"lon":9.99}},{"mode":"road","vehicle_type":"truck_40t","distance_km":40}]}`
	_, out := post(t, srv.URL+"/v1/calculate?path=true", body)
	legs := out["legs"].([]any)
	if p, ok := legs[0].(map[string]any)["path"].([]any); !ok || len(p) != 2 {
		t.Fatalf("leg 1 path: %v", legs[0].(map[string]any)["path"])
	}
	if _, ok := legs[1].(map[string]any)["path"]; ok {
		t.Error("a leg without coordinates got a path")
	}
	_, out = post(t, srv.URL+"/v1/calculate", body)
	if _, ok := out["legs"].([]any)[0].(map[string]any)["path"]; ok {
		t.Error("path without asking")
	}
	for _, d := range []string{"simplified", "detailed", "full"} {
		if _, out = post(t, srv.URL+"/v1/calculate?path="+d, body); out["legs"].([]any)[0].(map[string]any)["path"] == nil {
			t.Errorf("path=%s: no path", d)
		}
	}
	if resp, out := post(t, srv.URL+"/v1/calculate?path=huge", body); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("path=huge: %d %v", resp.StatusCode, out)
	}
	resp, err := http.Get(srv.URL + "/static/leaflet.js")
	if err != nil || resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("leaflet.js: %v %v", resp, err)
	}
	resp.Body.Close()
	if resp, _ := http.Get(srv.URL + "/static/LICENSE"); resp.StatusCode != 404 {
		t.Error("other files served from static")
	}
}

func TestCalculate_OwnFactors(t *testing.T) {
	srv := newTestServer(t)
	body := `{"weight_kg":10000,"legs":[{"mode":"road","distance_km":300,"own_factor":{"wtw_g_per_tkm":80,"ttw_g_per_tkm":64,"source":"Carrier X 2025"}},` +
		`{"mode":"road","vehicle_type":"truck_40t","distance_km":300,"fuel":{"fuel":"hvo","amount":100,"factor":{"unit":"l","wtw_kg_per_unit":0.5,"ttw_kg_per_unit":0.03,"source":"Supplier Y"}}}]}`
	resp, out := post(t, srv.URL+"/v1/calculate", body)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	legs := out["legs"].([]any)
	l0, l1 := legs[0].(map[string]any), legs[1].(map[string]any)
	if l0["data_type"] != "primary" || math.Abs(l0["wtw_kg"].(float64)-240) > 1e-9 || l1["data_type"] != "primary" || math.Abs(l1["wtw_kg"].(float64)-50) > 1e-9 {
		t.Errorf("legs: %v %v", l0, l1)
	}
	if out["primary_data_share"].(float64) != 1 {
		t.Errorf("primary share %v", out["primary_data_share"])
	}
	if resp, out := post(t, srv.URL+"/v1/calculate", `{"weight_kg":1000,"legs":[{"mode":"road","distance_km":10,"own_factor":{"wtw_g_per_tkm":80}}]}`); resp.StatusCode != 422 && resp.StatusCode != 400 {
		t.Errorf("no source: %d %v", resp.StatusCode, out)
	}
}
