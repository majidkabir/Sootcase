package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestILEAP_JSONAndCSV(t *testing.T) {
	srv := scenarioServer(t)
	body := `{"company":"Acme","period_from":"2026-01-01","period_to":"2026-12-31","shipments":[
		{"id":"A","weight_kg":20000,"legs":[{"id":"r","mode":"road","vehicle_type":"hgv","from":{"name":"Hamburg","lat":53.55,"lon":9.99},"to":{"name":"Munich","lat":48.14,"lon":11.58},"distance_km":780,"distance_type":"sfd"}]}]}`
	resp, out := post(t, srv.URL+"/v1/ileap", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	if out["conformant"] != true || out["ileap_spec_version"] != "1.1.0" {
		t.Fatalf("json: %v", out)
	}
	sf := out["shipment_footprints"].([]any)[0].(map[string]any)
	tce := sf["tces"].([]any)[0].(map[string]any)
	if sf["companyName"] != "Acme" || sf["referencePeriodEnd"] != "2027-01-01T00:00:00Z" || tce["transportActivity"] != "15600" ||
		tce["origin"].(map[string]any)["country"] != "DE" {
		t.Errorf("footprint %v", sf)
	}

	csv := "shipment_id,weight_kg,leg_id,mode,vehicle_type,from_lat,from_lon,to_lat,to_lon,distance_km,distance_type\n" +
		"A,20000,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\nB,abc,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\n"
	r, err := http.Post(srv.URL+"/v1/ileap?company=Acme", "text/csv", strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if r.StatusCode != http.StatusOK || len(got["failed"].([]any)) != 1 || got["conformant"] != false {
		t.Fatalf("csv: %d %v", r.StatusCode, got)
	}
	// No period given: listed, not invented.
	if !strings.Contains(toJSON(got["gaps"]), "referencePeriodStart") {
		t.Errorf("gaps %v", got["gaps"])
	}

	for _, bad := range []string{`{"shipments":[]}`, `{"period_from":"2026-02-01","period_to":"2026-01-01","shipments":[{"id":"A","weight_kg":1,"legs":[]}]}`} {
		if resp, _ := post(t, srv.URL+"/v1/ileap", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", bad, resp.StatusCode)
		}
	}
}

func TestILEAP_Operations(t *testing.T) {
	srv := scenarioServer(t)
	body := `{"company":"Acme","operations":{"name":"acme","period":"2025","period_start":"2025-01-01","period_end":"2025-12-31",
		"tocs":[{"id":"trucks","mode":"road","tonne_km":1000000,"fuels":[{"fuel":"diesel","amount":20000}]}],
		"hocs":[{"id":"dc","hub_type":"warehouse","tonnes":10000,"energy":[{"fuel":"electricity/DNK","amount":50000}]}]}}`
	resp, out := post(t, srv.URL+"/v1/ileap/operations", body)
	if resp.StatusCode != http.StatusOK || out["conformant"] != true {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	toc := out["tocs"].([]any)[0].(map[string]any)
	if toc["tocId"] != "trucks" || toc["referencePeriodEnd"] != "2026-01-01T00:00:00Z" || toc["energyCarriers"].([]any)[0].(map[string]any)["energyConsumption"] != "0.02" {
		t.Errorf("toc %v", toc)
	}
	if resp, _ := post(t, srv.URL+"/v1/ileap/operations", `{"operations":{"name":"x","tocs":[]}}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty operations: %d", resp.StatusCode)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
