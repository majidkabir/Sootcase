package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

func scenarioServer(t *testing.T) *httptest.Server {
	t.Helper()
	ds, err := factors.Open("")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&Server{Calc: calc.Calculator{Factors: ds, Distances: ds.Distances(), NoRoadLegs: true}, Factors: ds}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestScenario_JSONAndCSV(t *testing.T) {
	srv := scenarioServer(t)
	body := `{"plan":{"name":"rail","changes":[{"type":"road_to_rail"}]},"shipments":[
		{"id":"A","weight_kg":20000,"legs":[{"id":"r","mode":"road","vehicle_type":"hgv","from":{"lat":53.55,"lon":9.99},"to":{"lat":48.14,"lon":11.58},"distance_km":780,"distance_type":"sfd"}]}]}`
	resp, out := post(t, srv.URL+"/v1/scenario", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	if out["diff_kg"].(float64) >= 0 || !strings.Contains(out["label"].(string), "not reported") || len(out["changes"].([]any)) != 1 {
		t.Errorf("json: %v", out)
	}

	csv := "shipment_id,weight_kg,leg_id,mode,vehicle_type,from_lat,from_lon,to_lat,to_lon,distance_km,distance_type\n" +
		"A,20000,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\nB,abc,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\n"
	plan := url.QueryEscape(`{"changes":[{"type":"fuel_switch","fuel":"hvo"}]}`)
	r, err := http.Post(srv.URL+"/v1/scenario?plan="+plan, "text/csv", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if r.StatusCode != http.StatusOK || got["failed"].(float64) != 1 || got["diff_kg"].(float64) >= 0 {
		t.Errorf("csv: %d %v", r.StatusCode, got)
	}

	for name, b := range map[string]string{
		"bad plan":      `{"plan":{"changes":[{"type":"teleport"}]},"shipments":[]}`,
		"no changes":    `{"plan":{},"shipments":[]}`,
		"unknown field": `{"plan":{"changes":[{"type":"road_to_rail","colour":"red"}]},"shipments":[]}`,
	} {
		if resp, out := post(t, srv.URL+"/v1/scenario", b); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d %v", name, resp.StatusCode, out)
		}
	}
}
