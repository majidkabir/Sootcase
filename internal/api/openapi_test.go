package api

import (
	"bufio"
	"bytes"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/ileap"
	"github.com/majidkabir/sootcase/pkg/places"
	"github.com/majidkabir/sootcase/pkg/recommend"
	"github.com/majidkabir/sootcase/pkg/report"
	"github.com/majidkabir/sootcase/pkg/scenario"
)

// specProperties reads the property names of each schema in openapi.yaml
// (components.schemas.<Name>.properties), by indentation.
func specProperties(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	schema, inSchemas, inProps := "", false, false
	name := regexp.MustCompile(`^    ([A-Za-z]+):\s*$`)
	prop := regexp.MustCompile(`^        ([a-z0-9_]+):`)
	sc := bufio.NewScanner(bytes.NewReader(openAPISpec))
	for sc.Scan() {
		l := sc.Text()
		switch {
		case l == "  schemas:":
			inSchemas = true
		case inSchemas && name.MatchString(l):
			schema, inProps = name.FindStringSubmatch(l)[1], false
			out[schema] = nil
		case inSchemas && strings.HasPrefix(l, "      ") && !strings.HasPrefix(l, "        "):
			inProps = strings.TrimSpace(l) == "properties:"
		case inSchemas && inProps && prop.MatchString(l):
			out[schema] = append(out[schema], prop.FindStringSubmatch(l)[1])
		}
	}
	return out
}

// jsonFields returns the JSON names of a struct's fields, with embedded
// structs flattened.
func jsonFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if f.Anonymous && tag == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			out = append(out, jsonFields(ft)...)
			continue
		}
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		out = append(out, tag)
	}
	return out
}

// The API description lists every field of the JSON the API reads and
// writes, and no field the code doesn't have.
func TestOpenAPIMatchesTypes(t *testing.T) {
	spec := specProperties(t)
	types := map[string]any{
		"Location": calc.Location{}, "Place": places.Place{}, "FuelUse": calc.FuelUse{}, "OwnFactor": calc.OwnFactor{},
		"Leg": calc.Leg{}, "HubStop": calc.HubStop{}, "Shipment": calc.Shipment{}, "Factor": calc.Factor{},
		"FuelFactor": calc.FuelFactor{}, "HubFactor": calc.HubFactor{}, "LegResult": calc.LegResult{}, "LegPart": calc.LegPart{},
		"Result": calc.Result{}, "Dataset": factors.Dataset{}, "Report": report.Report{}, "RecommendSettings": recommend.Settings{},
		"RecommendRoute": recommend.Route{}, "ScenarioChange": scenario.Change{}, "ScenarioPlan": scenario.Plan{},
		"ScenarioTotals": scenario.Totals{}, "ScenarioResult": scenario.Result{},
		"ILEAPResponse": ILEAPResponse{}, "ILEAPGap": ileap.Gap{},
	}
	for name, v := range types {
		props, ok := spec[name]
		if !ok {
			t.Errorf("openapi.yaml has no schema %s", name)
			continue
		}
		code := jsonFields(reflect.TypeOf(v))
		inSpec, inCode := map[string]bool{}, map[string]bool{}
		for _, p := range props {
			inSpec[p] = true
		}
		for _, f := range code {
			inCode[f] = true
		}
		var missing, extra []string
		for _, f := range code {
			if !inSpec[f] {
				missing = append(missing, f)
			}
		}
		for _, p := range props {
			if !inCode[p] {
				extra = append(extra, p)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s: in the code, not in openapi.yaml: %s", name, strings.Join(missing, ", "))
		}
		if len(extra) > 0 {
			t.Errorf("%s: in openapi.yaml, not in the code: %s", name, strings.Join(extra, ", "))
		}
	}
}
