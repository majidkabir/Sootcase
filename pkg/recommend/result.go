package recommend

import "github.com/majidkabir/sootcase/pkg/calc"

// Totals are emissions in kg CO2e.
type Totals = calc.Emissions

// Step is one leg of a route as calculated.
type Step struct {
	Mode        calc.Mode `json:"mode"`
	VehicleType string    `json:"vehicle_type,omitempty"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	DistanceKm  float64   `json:"distance_km"`
	WTWKg       float64   `json:"wtw_kg"`
	// Added marks a road leg the calculator added to reach a port, terminal
	// or airport.
	Added bool `json:"added,omitempty"`
}

// Route is a shipment's route as calculated: now, or an alternative.
type Route struct {
	Totals
	DistanceKm float64 `json:"distance_km"`
	// Transfers counts changes of mode along the route.
	Transfers int      `json:"transfers"`
	Modes     []string `json:"modes"`
	Steps     []Step   `json:"steps"`
	// Terminals are the terminal handlings an alternative adds to now
	// (included in the totals).
	Terminals []calc.HubResult `json:"terminals,omitempty"`
}

// Option is one alternative for a shipment.
type Option struct {
	Kind string `json:"kind"`
	// Area is the kind's GLEC reduction area (Areas).
	Area    string `json:"area"`
	Summary string `json:"summary"`
	Route
	SavedKg  float64 `json:"saved_kg"`  // now − alternative, WTW
	SavedPct float64 `json:"saved_pct"` // of now
	// ExtraKmPct is how much longer the whole route is than now, in percent
	// (negative: shorter).
	ExtraKmPct float64 `json:"extra_km_pct"`
	// Legs are the alternative's legs as input to the calculator, to
	// calculate or map it again.
	Legs []calc.Leg `json:"legs"`
}

// ShipmentResult is the alternatives for one shipment, best first. Only
// alternatives with lower emissions than now are listed.
type ShipmentResult struct {
	ShipmentID string  `json:"shipment_id"`
	Lane       string  `json:"lane"`
	WeightKg   float64 `json:"weight_kg"`
	Now        Route   `json:"now"`
	// Legs are the shipment's own legs, to calculate or map it again.
	Legs    []calc.Leg `json:"legs,omitempty"`
	Options []Option   `json:"options"`
	// Notes say why route changes that were tried don't apply.
	Notes []string `json:"notes,omitempty"`
	Error string   `json:"error,omitempty"`
	// Retryable marks an error that is temporary (a routing service that
	// didn't answer): the same request can succeed later.
	Retryable bool `json:"retryable,omitempty"`
}

// KindSummary totals one kind of route change over the shipments.
type KindSummary struct {
	Kind string `json:"kind"`
	// Area is the kind's GLEC reduction area (Areas).
	Area string `json:"area"`
	// Shipments that have an alternative of this kind with lower emissions.
	Shipments int `json:"shipments"`
	// SavedKg is the saving if every such shipment took its best
	// alternative of this kind (kinds overlap: don't add them up).
	SavedKg float64 `json:"saved_kg"`
	// BestFor counts shipments where this kind gives the biggest saving.
	BestFor int `json:"best_for"`
}

// Lane totals the shipments between the same two places.
type Lane struct {
	Lane      string  `json:"lane"`
	Shipments int     `json:"shipments"`
	NowKg     float64 `json:"now_kg"`
	BestKg    float64 `json:"best_kg"`
	SavedKg   float64 `json:"saved_kg"`
	// BestKind is the kind of the best alternative for most of the lane's
	// shipments ("" when none has one).
	BestKind string `json:"best_kind,omitempty"`
}

// Result is the recommendations for all shipments.
type Result struct {
	Label    string            `json:"label"`
	Settings Settings          `json:"settings"`
	Skipped  map[string]string `json:"skipped,omitempty"`
	Now      Totals            `json:"now"`
	Best     Totals            `json:"best"` // every shipment on its best alternative (or as now)
	SavedKg  float64           `json:"saved_kg"`
	SavedPct float64           `json:"saved_pct"`
	Kinds    []KindSummary     `json:"kinds"`
	// Areas are GLEC's five reduction areas with the kinds tried in each.
	Areas       []Area           `json:"areas"`
	Lanes       []Lane           `json:"lanes"`
	Sources     []string         `json:"sources"`
	Limitations []string         `json:"limitations"`
	Shipments   []ShipmentResult `json:"shipments"`
	Failed      int              `json:"failed"`
}

// Limitations are what the recommendations don't take into account.
func Limitations() []string {
	return []string{
		"No transit times or costs: there is no standard source for them, so alternatives are compared on emissions and distance only.",
		"Whether a rail, barge or ferry service actually runs on the lane, how often, and with free capacity is not known: an alternative is possible on the networks, not a booked service.",
		"Terminals and ports come from UN/LOCODE, which marks rail terminals and ports but not whether they handle containers or trailers.",
		"New rail, waterway and sea legs run between rail terminals or ports (from UN/LOCODE), with road to and from the nearest one. Each terminal handling this adds (truck to train, barge or container ship and back) counts GLEC v3.3's intermodal terminal value (Table 3: 18.2 kg CO2e per container, 21.4 temperature-controlled). Containers = the shipment's TEU, or its tonnes ÷ GLEC's tonnes per TEU (10; light 6, heavy 14.5); Table 3 gives no container size, so one container = one TEU. Airports, Ro-Ro and bulk terminals have no GLEC value and are not counted.",
		"Vehicle changes (larger, electric and bio-LNG trucks) keep the route and use GLEC's default value for that vehicle at GLEC's average load, in the regions GLEC gives one. They don't check range and charging, fuel availability, or whether the cargo can be consolidated into a larger truck.",
		"New legs use fleet-average default factors (GLEC Framework v3.3 default values; UK DESNZ 2026 for ferries, Ro-Ro and non-container ships), not a carrier's own data.",
		"Combining shipments needs shipping dates, which the shipments file doesn't have: fuller loads use the load you give for every road vehicle.",
		"Rail and waterway distances are shortest paths on OpenStreetMap; a waterway alternative needs both ends near a navigable river or canal.",
	}
}
