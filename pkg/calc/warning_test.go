package calc

import (
	"encoding/json"
	"testing"
)

// Warnings are plain texts in JSON, as they were before they had codes.
func TestWarningJSON(t *testing.T) {
	b, err := json.Marshal(Result{Warnings: []Warning{{Code: WarnSeaInland, Text: "leg 1 starts inland"}}})
	if err != nil {
		t.Fatal(err)
	}
	var raw struct{ Warnings []string }
	if err := json.Unmarshal(b, &raw); err != nil || len(raw.Warnings) != 1 || raw.Warnings[0] != "leg 1 starts inland" {
		t.Fatalf("JSON %s: %v", b, err)
	}
	var back Result
	if err := json.Unmarshal(b, &back); err != nil || back.Warnings[0].Text != "leg 1 starts inland" {
		t.Fatalf("read back %+v: %v", back.Warnings, err)
	}
}
