package api

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// art10 matches the statement Regulation (EU) 2026/1030 Art. 10(1) keeps
// for certified tools ("calculated in accordance with Regulation (EU)
// 2026/1030") and wordings that read like it. Sootcase isn't certified, so
// nothing it gives out may say it.
var art10 = regexp.MustCompile(`(?is)(accordance|compliance|conformity|line|compliant|conform\w*|meets?|complies)\s+(with\s+)?(the\s+)?(Regulation\s*\(EU\)\s*)?(No\.?\s*)?2026/1030`)

func noArt10(t *testing.T, where, text string) {
	t.Helper()
	if m := art10.FindString(text); m != "" {
		t.Errorf("%s says %q: only tools certified under Regulation (EU) 2026/1030 may", where, m)
	}
}

// Every result, report, scenario and recommendation states its basis and
// that it isn't certified (R1, R2), and no output, page or document has
// the Art. 10 statement (R3).
func TestBasisAndNoArt10Statement(t *testing.T) {
	if art10.FindString("Greenhouse gas emissions calculated in accordance with Regulation (EU) 2026/1030") == "" ||
		art10.FindString("Sootcase output is compliant with Regulation (EU) 2026/1030") == "" ||
		art10.FindString(calc.Certification) != "" {
		t.Fatal("the pattern doesn't tell the Art. 10 statement from the certification line")
	}
	srv := scenarioServer(t)
	shipment := `{"id":"A","weight_kg":20000,"legs":[{"id":"r","mode":"road","vehicle_type":"hgv","from":{"lat":53.55,"lon":9.99},"to":{"lat":48.14,"lon":11.58},"distance_km":780,"distance_type":"sfd"}]}`
	csv := "shipment_id,weight_kg,leg_id,mode,vehicle_type,from_lat,from_lon,to_lat,to_lon,distance_km,distance_type\n" +
		"A,20000,r,road,hgv,53.55,9.99,48.14,11.58,780,sfd\n"
	for _, c := range []struct{ path, ctype, body string }{
		{"/v1/calculate?explain=true", "application/json", shipment},
		{"/v1/calculate/batch", "application/json", `{"shipments":[` + shipment + `]}`},
		{"/v1/report?results=true&markdown=true", "text/csv", csv},
		{"/v1/report?format=md", "text/csv", csv},
		{"/v1/scenario?plan=" + url.QueryEscape(`{"changes":[{"type":"road_to_rail"}]}`), "text/csv", csv},
		{"/v1/recommend?settings=" + url.QueryEscape(`{"kinds":["road_to_rail"]}`), "text/csv", csv},
		{"/v1/ileap", "text/csv", csv},
	} {
		resp, err := http.Post(srv.URL+c.path, c.ctype, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		out := string(b)
		noArt10(t, c.path, out)
		if resp.StatusCode != 200 {
			t.Errorf("%s: HTTP %d %.200s", c.path, resp.StatusCode, out)
			continue
		}
		basis := `"standard":"EN ISO 14083:2023","framework":"GLEC Framework v3.3","sootcase":"dev"`
		if strings.Contains(c.path, "format=md") {
			basis = "Basis: EN ISO 14083:2023, GLEC Framework v3.3, Sootcase dev."
		}
		if !strings.Contains(out, basis) || !strings.Contains(out, calc.Certification) {
			t.Errorf("%s: no basis or certification in %.300s", c.path, out)
		}
	}
	// The page, its script, the API description and the documents.
	files := []string{"openapi.yaml", "../../README.md", "../../NOTICE"}
	web, _ := filepath.Glob("web/*")
	docs, _ := filepath.Glob("../../docs/*.md")
	for _, f := range append(append(files, web...), docs...) {
		if st, err := os.Stat(f); err != nil || st.IsDir() {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		noArt10(t, f, string(b))
	}
}
