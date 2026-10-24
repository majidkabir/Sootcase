package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRecommend_JSONAndCSV(t *testing.T) {
	srv := scenarioServer(t)
	body := `{"settings":{"kinds":["road_to_rail"]},"shipments":[
		{"id":"A","weight_kg":20000,"legs":[{"id":"r","mode":"road","vehicle_type":"hgv","from":{"lat":53.55,"lon":9.99},"to":{"lat":48.14,"lon":11.58},"distance_km":780,"distance_type":"sfd"}]}]}`
	resp, out := post(t, srv.URL+"/v1/recommend", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	ships := out["shipments"].([]any)
	if out["saved_kg"].(float64) <= 0 || !strings.Contains(out["label"].(string), "not reported") || len(ships) != 1 ||
		len(ships[0].(map[string]any)["options"].([]any)) != 1 || len(out["limitations"].([]any)) == 0 {
		t.Errorf("json: %v", out)
	}

	csv := "shipment_id,weight_kg,leg_id,mode,vehicle_type,from_lat,from_lon,to_lat,to_lon,distance_km,distance_type\n" +
		"A,20000,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\nB,abc,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\n"
	settings := url.QueryEscape(`{"kinds":["road_to_rail"],"min_road_km":500}`)
	r, err := http.Post(srv.URL+"/v1/recommend?settings="+settings, "text/csv", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if r.StatusCode != http.StatusOK || got["failed"].(float64) != 1 || got["saved_kg"].(float64) <= 0 {
		t.Errorf("csv: %d %v", r.StatusCode, got)
	}

	for name, b := range map[string]string{
		"bad kind":      `{"settings":{"kinds":["teleport"]},"shipments":[{"id":"A","weight_kg":1,"legs":[]}]}`,
		"no shipments":  `{"settings":{}}`,
		"unknown field": `{"settings":{"colour":"red"},"shipments":[]}`,
		"load no value": `{"settings":{"kinds":["fuller_loads"]},"shipments":[{"id":"A","weight_kg":1,"legs":[]}]}`,
	} {
		if resp, out := post(t, srv.URL+"/v1/recommend", b); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d %v", name, resp.StatusCode, out)
		}
	}
}
