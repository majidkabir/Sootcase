package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The tests run the command itself: the test binary runs main when
// SOOTCASE_TEST_ARGS is set (arguments separated by a unit separator).
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv("SOOTCASE_TEST_ARGS"); ok {
		os.Args = append([]string{"sootcase"}, strings.Split(args, "\x1f")...)
		if args == "" {
			os.Args = os.Args[:1]
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// run runs sootcase with args and stdin, returning its output and exit code.
func run(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SOOTCASE_TEST_ARGS="+strings.Join(args, "\x1f"))
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

const (
	shipmentJSON = "../../examples/cph-rotterdam.json"
	shipmentsCSV = "../../examples/shipments.csv"
	operations   = "../../examples/operations.json"
)

func TestUsage(t *testing.T) {
	if _, e, code := run(t, ""); code != 2 || !strings.Contains(e, "glec-3.3") {
		t.Errorf("no command: exit %d, usage %q", code, e)
	}
	if _, e, code := run(t, "", "nonsense"); code != 2 || !strings.Contains(e, `unknown command "nonsense"`) {
		t.Errorf("unknown command: exit %d, %q", code, e)
	}
	if out, _, code := run(t, "", "version"); code != 0 || strings.TrimSpace(out) != "dev" {
		t.Errorf("version: exit %d, %q", code, out)
	}
}

func TestCalc(t *testing.T) {
	out, e, code := run(t, "", "calc", shipmentJSON)
	var r struct {
		WTWKg float64 `json:"wtw_kg"`
		Legs  []any   `json:"legs"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.WTWKg <= 0 || len(r.Legs) != 2 {
		t.Fatalf("calc: exit %d, %s %s", code, out, e)
	}
	// The same shipment from stdin.
	in, _ := os.ReadFile(shipmentJSON)
	if out2, _, code := run(t, string(in), "calc", "-"); code != 0 || out2 != out {
		t.Errorf("calc from stdin: exit %d, differs", code)
	}
	if _, e, code := run(t, "", "calc", "missing.json"); code != 1 || !strings.Contains(e, "error:") {
		t.Errorf("missing file: exit %d, %q", code, e)
	}
}

func TestBatchAndReport(t *testing.T) {
	out, e, code := run(t, "", "batch", shipmentsCSV)
	if code != 0 || !strings.HasPrefix(out, "shipment_id,") || strings.Count(out, "\n") != 5 || !strings.Contains(e, "4 shipments") {
		t.Errorf("batch: exit %d\n%s\n%s", code, out, e)
	}
	md, _, code := run(t, "", "report", shipmentsCSV)
	if code != 0 || !strings.HasPrefix(md, "# Freight GHG emissions report") {
		t.Errorf("report: exit %d, %.80q", code, md)
	}
	js, _, code := run(t, "", "report", "-format", "json", shipmentsCSV)
	var rep struct {
		Title string `json:"title"`
	}
	if code != 0 || json.Unmarshal([]byte(js), &rep) != nil || rep.Title == "" {
		t.Errorf("report JSON: exit %d, %.80q", code, js)
	}
}

func TestScenarioAndRecommend(t *testing.T) {
	if out, e, code := run(t, "", "scenario", shipmentsCSV, "testdata/plan.json"); code != 0 || !strings.Contains(out, "Scenario") {
		t.Errorf("scenario: exit %d\n%.200s\n%s", code, out, e)
	}
	if out, e, code := run(t, "", "recommend", shipmentsCSV); code != 0 || !strings.Contains(out, "Scenario") {
		t.Errorf("recommend: exit %d\n%.200s\n%s", code, out, e)
	}
}

func TestStreamFactorsTOC(t *testing.T) {
	in := `{"id":"a","weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"hgv_artic","distance_km":500}]}` + "\n" +
		`{"id":"b","weight_kg":1000,"legs":[{"mode":"rail","vehicle_type":"freight_train","distance_km":500}]}` + "\n"
	out, e, code := run(t, in, "stream")
	if code != 0 || strings.Count(out, "\n") != 2 || !strings.Contains(out, `"shipment_id":"b"`) {
		t.Errorf("stream: exit %d\n%s\n%s", code, out, e)
	}
	out, _, code = run(t, "", "factors")
	var ds struct {
		Name string `json:"name"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &ds) != nil || ds.Name != "desnz-2026+nl-iww-2026+eu-mrv-2024+glec-3.3" {
		t.Errorf("factors: exit %d, name %q", code, ds.Name)
	}
	// toc writes the layer as JSON and a summary on stderr.
	if out, e, code := run(t, "", "toc", operations); code != 0 || !strings.Contains(out, `"name": "acme-logistics"`) || !strings.Contains(e, "g CO2e/tkm") {
		t.Errorf("toc: exit %d\n%s\n%s", code, out, e)
	}
}

func TestHealthWithoutServer(t *testing.T) {
	if _, _, code := run(t, "", "health", "-addr", "127.0.0.1:1"); code == 0 {
		t.Error("health without a server should fail")
	}
}
