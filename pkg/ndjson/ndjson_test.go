package ndjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase/pkg/batch"
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/factors"
)

func readAll(t *testing.T, in string, maxLine int) []csvio.Item {
	t.Helper()
	r := NewReader(strings.NewReader(in), maxLine)
	var out []csvio.Item
	for {
		it, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, it)
	}
}

func TestReader(t *testing.T) {
	in := "{\"id\":\"a\",\"weight_kg\":1,\"legs\":[{\"mode\":\"road\",\"vehicle_type\":\"hgv\",\"distance_km\":10}]}\r\n" +
		"\n   \n" +
		"{\"id\":\"b\",\"weight_kg\":1,\"legz\":[]}\n" + // unknown field
		"{\"id\":\"c\",\n" + // broken JSON
		"{\"id\":\"d\"} {\"id\":\"e\"}\n" + // two values
		"{\"id\":\"g\",\"weight_kg\":1}}\n" + // a trailing brace
		"{\"id\":\"f\",\"weight_kg\":2}" // last line without newline
	items := readAll(t, in, 0)
	want := []struct {
		id   string
		line int
		bad  bool
	}{{"a", 1, false}, {"b", 4, true}, {"c", 5, true}, {"d", 6, true}, {"g", 7, true}, {"f", 8, false}}
	if len(items) != len(want) {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	for i, w := range want {
		it := items[i]
		if it.Shipment.ID != w.id || it.Line != w.line || (it.Err != nil) != w.bad {
			t.Errorf("item %d = id %q line %d err %v; want %+v", i, it.Shipment.ID, it.Line, it.Err, w)
		}
	}
	if items[0].Shipment.WeightKg != 1 || len(items[0].Shipment.Legs) != 1 {
		t.Errorf("a parsed as %+v", items[0].Shipment)
	}
}

func TestReader_LongLines(t *testing.T) {
	long := `{"id":"` + strings.Repeat("x", 500) + `"}`
	in := `{"id":"ok1"}` + "\n" + long + "\n" + `{"id":"ok2"}` + "\n" + long
	items := readAll(t, in, 100)
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	for i, bad := range []bool{false, true, false, true} {
		if (items[i].Err != nil) != bad || (bad && !strings.Contains(items[i].Err.Error(), "longer than 100 bytes")) {
			t.Errorf("item %d: err %v", i, items[i].Err)
		}
	}
	if items[2].Shipment.ID != "ok2" || items[2].Line != 3 {
		t.Errorf("reader didn't resync after a long line: %+v", items[2])
	}
}

// End to end through batch.Process: ordered output, one line per input.
func TestStream(t *testing.T) {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		t.Fatal(err)
	}
	var in strings.Builder
	for i := 0; i < 200; i++ {
		if i%50 == 7 {
			in.WriteString("not json\n")
			continue
		}
		in.WriteString(`{"id":"s` + string(rune('a'+i%26)) + `","weight_kg":1000,"legs":[{"mode":"road","vehicle_type":"hgv","distance_km":100}]}` + "\n")
	}
	var out bytes.Buffer
	w := NewWriter(&out)
	stats, err := batch.Process(context.Background(), calc.Calculator{Factors: ds}, NewReader(strings.NewReader(in.String()), 0), 4, w.Write)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Shipments != 200 || stats.Failed != 4 {
		t.Errorf("stats %+v", stats)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 200 {
		t.Fatalf("%d output lines", len(lines))
	}
	for i, l := range lines {
		var o Out
		if err := json.Unmarshal([]byte(l), &o); err != nil {
			t.Fatal(err)
		}
		if o.Line != i+1 {
			t.Fatalf("line %d reported as %d (out of order)", i+1, o.Line)
		}
		if (i%50 == 7) != (o.Error != "") || (o.Error == "") == (o.Result == nil) {
			t.Errorf("line %d: %+v", i+1, o)
		}
	}
}
