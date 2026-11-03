package calc

import "encoding/json"

// Warning codes say which kind a Warning is, so that reports can count
// them without reading their text.
const (
	WarnAirportFar     = "airport_far"      // an air leg ends away from an airport
	WarnSeaInland      = "sea_inland"       // a sea leg ends away from the sea network; counted as sea distance
	WarnGapNotCounted  = "gap_not_counted"  // legs don't join up and adding road legs is off
	WarnAddedLegFailed = "added_leg_failed" // a road leg to add couldn't be calculated
)

// Warning is something left as given that may make the result too low. In
// JSON it is its text; Code is for programs.
type Warning struct {
	Code string
	Text string
}

func (w Warning) String() string { return w.Text }

// MarshalJSON writes the warning as its text.
func (w Warning) MarshalJSON() ([]byte, error) { return json.Marshal(w.Text) }

// UnmarshalJSON reads a warning's text (without its code).
func (w *Warning) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &w.Text) }

// WarningTexts returns the warnings' texts.
func WarningTexts(ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Text
	}
	return out
}
