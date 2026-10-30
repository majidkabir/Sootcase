// Package recommend looks for route changes that lower a shipment's
// emissions: road freight to rail, inland waterway or short sea, air freight
// to sea or rail, another sea port, and fuller loads. Each alternative is a
// changed copy of the shipment calculated with the same method as the
// shipment itself (GLEC Framework v3.3, ISO 14083), so the difference comes
// only from the route. Results are scenarios, not reported emissions.
//
// The rules an alternative must meet are stated defaults with sources, and
// the user can change them:
//
//   - road legs longer than min_road_km (default 300 km, EU Transport White
//     Paper 2011: road freight over 300 km should shift to rail or waterborne
//     transport) are tried on rail, inland waterway and short sea;
//   - the rail, waterway or sea part must be longer than min_main_km in a
//     straight line (default 100 km, Council Directive 92/106/EEC, Article 1);
//   - road to and from a waterway or sea port must stay within
//     max_port_road_km of it in a straight line (default 150 km, same
//     Article); road to and from rail goes to the nearest rail terminal on
//     the network (same Article: "the nearest suitable rail loading station").
//
// New rail, waterway and sea legs run between rail terminals or ports: an
// end that isn't at one moves to the nearest, with a road leg to it, even
// when it is on the network (same Article: combined transport is road to
// and from the nearest suitable station or port; calc.Leg.AtTerminals).
//
// Extra transshipments: each terminal handling an alternative adds (a change
// of mode at a rail, waterway or container-ship terminal, and the ends of
// the new legs where the shipment starts or ends, as the cargo was on a
// truck there) adds GLEC Framework v3.3's default value for intermodal
// terminals (Module 2, Table 3, p. 97, per container), for the shipment's
// containers: its TEU, or its tonnes ÷ GLEC's tonnes per TEU (Section 1
// Chapter 4, logistics hubs). There is no GLEC default for airports or
// Ro-Ro and bulk terminals, so those are not counted.
//
// What it doesn't know, and says so (Limitations): transit times, costs,
// and whether a service runs on the lane.
package recommend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
)

// Kinds of route change.
const (
	Rail      = "road_to_rail"
	Waterway  = "road_to_waterway"
	ShortSea  = "road_to_short_sea"
	AirToSea  = "air_to_sea"
	AirToRail = "air_to_rail"
	OtherPort = "other_port"
	Load      = "fuller_loads"
)

// Kinds lists every kind of route change, in the order they are tried.
func Kinds() []string {
	return []string{Rail, Waterway, ShortSea, AirToSea, AirToRail, OtherPort, Load}
}

// Defaults and their sources.
const (
	DefaultMinRoadKm     = 300.0
	DefaultMinMainKm     = 100.0
	DefaultMaxPortRoadKm = 150.0
	DefaultPortRadiusKm  = 800.0
	DefaultMaxPorts      = 4

	TerminalNearest  = "nearest"
	TerminalBestTrip = "best_trip"

	Label = "Scenario: alternatives recalculated with the same method (GLEC Framework v3.3, ISO 14083), not reported emissions."

	SourceWhitePaper = "Road legs over 300 km: EU Transport White Paper 2011 (Council document 11255/11, II.8): \"30% of road freight over " +
		"300 km should shift to other modes such as rail or waterborne transport by 2030\""
	SourceMainKm = "Rail, waterway or sea part over 100 km: Council Directive 92/106/EEC (combined transport), Article 1: \"rail or inland " +
		"waterway or maritime services where this section exceeds 100 km as the crow flies\""
	SourcePortRoad = "Road to and from a waterway or sea port within 150 km of it: Council Directive 92/106/EEC, Article 1: \"within a radius " +
		"not exceeding 150 km as the crow flies from the inland waterway port or seaport of loading or unloading\""
	SourceTerminal = "Road to and from the nearest rail terminal on the rail network that can be reached by road: Council Directive " +
		"92/106/EEC, Article 1 (\"the nearest suitable rail loading station\"). Whether a terminal handles containers or trailers can't " +
		"be checked: the place list (UN/LOCODE) marks rail terminals, not their equipment"
	SourceBestTrip = "Road to and from the rail terminal giving the best trip among the nearest ones (terminal: best_trip)"
	SourcePorts    = "Other sea ports: the large cargo sea ports in Sootcase's place list (UN/LOCODE codes) within the search radius of " +
		"the port used now, with the onward leg in the same mode as now"
	SourceAir = "Air freight moved as one leg from where the cargo starts to where it ends (with the road legs next to the flight), " +
		"by sea or rail; ports and terminals are chosen as for any shipment (the calculator's access legs)"
)

// Settings say which route changes to try and the rules they follow.
// Pointers are optional values with defaults.
type Settings struct {
	// Kinds to try; empty means all (fuller_loads only when a load is given).
	Kinds []string `json:"kinds,omitempty"`
	// MinRoadKm: road legs longer than this are tried on rail, waterway and
	// short sea.
	MinRoadKm *float64 `json:"min_road_km,omitempty"`
	// MinMainKm: the rail, waterway or sea part must be longer than this in
	// a straight line.
	MinMainKm *float64 `json:"min_main_km,omitempty"`
	// MaxPortRoadKm: road to and from a waterway or sea port must stay
	// within this of it in a straight line (road to waterway and short sea).
	MaxPortRoadKm *float64 `json:"max_port_road_km,omitempty"`
	// Terminal: how rail terminals are chosen, "nearest" (default) or
	// "best_trip".
	Terminal string `json:"terminal,omitempty"`
	// Vehicle types of the new legs.
	RailVehicle     string `json:"rail_vehicle,omitempty"`      // default freight_train
	WaterwayVehicle string `json:"waterway_vehicle,omitempty"`  // default container_barge
	ShortSeaVehicle string `json:"short_sea_vehicle,omitempty"` // default roro
	SeaVehicle      string `json:"sea_vehicle,omitempty"`       // default container_ship (air to sea)
	// Other ports: how far from the port used now to look, and how many.
	PortRadiusKm *float64 `json:"port_radius_km,omitempty"`
	MaxPorts     *int     `json:"max_ports,omitempty"`
	// Fuller loads: the load on the shipment's road vehicles (load_tonnes or
	// load_factor, as in the calculator's load model), empty km per loaded
	// km, an optional other vehicle type, and where the values come from.
	LoadTonnes      *float64 `json:"load_tonnes,omitempty"`
	LoadFactor      *float64 `json:"load_factor,omitempty"`
	EmptyTripFactor *float64 `json:"empty_trip_factor,omitempty"`
	LoadVehicle     string   `json:"load_vehicle,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// ErrInvalidSettings reports settings that can't be used.
var ErrInvalidSettings = errors.New("invalid recommendation settings")

func ptr[T any](v T) *T { return &v }

func val[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// Validate checks the settings and fills in defaults. skipped says why a
// kind asked for implicitly won't be tried.
func (s Settings) Validate() (Settings, map[string]string, error) {
	bad := func(msg string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(msg, a...))
	}
	skipped := map[string]string{}
	known := map[string]bool{}
	for _, k := range Kinds() {
		known[k] = true
	}
	hasLoad := s.LoadTonnes != nil || s.LoadFactor != nil
	if len(s.Kinds) == 0 {
		for _, k := range Kinds() {
			if k == Load && !hasLoad {
				skipped[Load] = "give load_tonnes or load_factor to compare fuller loads"
				continue
			}
			s.Kinds = append(s.Kinds, k)
		}
	} else {
		seen := map[string]bool{}
		var ks []string
		for _, k := range s.Kinds {
			if !known[k] {
				return s, nil, bad("unknown kind %q; use %s", k, strings.Join(Kinds(), ", "))
			}
			if !seen[k] {
				seen[k] = true
				ks = append(ks, k)
			}
		}
		s.Kinds = ks
		if seen[Load] && !hasLoad {
			return s, nil, bad("fuller_loads needs load_tonnes or load_factor")
		}
	}
	s.MinRoadKm = ptr(val(s.MinRoadKm, DefaultMinRoadKm))
	s.MinMainKm = ptr(val(s.MinMainKm, DefaultMinMainKm))
	s.MaxPortRoadKm = ptr(val(s.MaxPortRoadKm, DefaultMaxPortRoadKm))
	s.PortRadiusKm = ptr(val(s.PortRadiusKm, DefaultPortRadiusKm))
	s.MaxPorts = ptr(val(s.MaxPorts, DefaultMaxPorts))
	for name, v := range map[string]float64{"min_road_km": *s.MinRoadKm, "min_main_km": *s.MinMainKm, "max_port_road_km": *s.MaxPortRoadKm} {
		if !(v >= 0) || v > 20000 {
			return s, nil, bad("%s must be between 0 and 20000", name)
		}
	}
	if !(*s.PortRadiusKm > 0) || *s.PortRadiusKm > 3000 {
		return s, nil, bad("port_radius_km must be > 0 and <= 3000")
	}
	if *s.MaxPorts < 1 || *s.MaxPorts > 10 {
		return s, nil, bad("max_ports must be 1 to 10")
	}
	switch s.Terminal {
	case "":
		s.Terminal = TerminalNearest
	case TerminalNearest, TerminalBestTrip:
	default:
		return s, nil, bad("terminal must be %s or %s", TerminalNearest, TerminalBestTrip)
	}
	if s.RailVehicle == "" {
		s.RailVehicle = "freight_train"
	}
	if s.WaterwayVehicle == "" {
		s.WaterwayVehicle = "container_barge"
	}
	if s.ShortSeaVehicle == "" {
		s.ShortSeaVehicle = "roro"
	}
	if s.SeaVehicle == "" {
		s.SeaVehicle = "container_ship"
	}
	if hasLoad {
		if s.LoadTonnes != nil && s.LoadFactor != nil {
			return s, nil, bad("give load_tonnes or load_factor, not both")
		}
		if s.LoadTonnes != nil && !(*s.LoadTonnes > 0) {
			return s, nil, bad("load_tonnes must be > 0")
		}
		if s.LoadFactor != nil && (!(*s.LoadFactor > 0) || *s.LoadFactor > 1) {
			return s, nil, bad("load_factor must be > 0 and <= 1")
		}
		if s.EmptyTripFactor != nil && (!(*s.EmptyTripFactor >= 0) || *s.EmptyTripFactor > 1) {
			return s, nil, bad("empty_trip_factor must be 0 to 1")
		}
	} else if s.EmptyTripFactor != nil || s.LoadVehicle != "" {
		return s, nil, bad("empty_trip_factor and load_vehicle need load_tonnes or load_factor")
	}
	return s, skipped, nil
}

func (s Settings) has(kind string) bool {
	for _, k := range s.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Totals are emissions in kg CO2e.
type Totals struct {
	TTWKg float64 `json:"ttw_kg"`
	WTTKg float64 `json:"wtt_kg"`
	WTWKg float64 `json:"wtw_kg"`
	// BiogenicCO2Kg is reported separately, not part of the totals (GLEC
	// Framework v3.2 p. 64).
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
}

func totals(r calc.Result) Totals { return Totals{r.TTWKg, r.WTTKg, r.WTWKg, r.BiogenicCO2Kg} }

func (t Totals) scale(k float64) Totals {
	return Totals{t.TTWKg * k, t.WTTKg * k, t.WTWKg * k, t.BiogenicCO2Kg * k}
}

func (t *Totals) add(o Totals) {
	t.TTWKg += o.TTWKg
	t.WTTKg += o.WTTKg
	t.WTWKg += o.WTWKg
	t.BiogenicCO2Kg += o.BiogenicCO2Kg
}

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
	Kind    string `json:"kind"`
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
}

// KindSummary totals one kind of route change over the shipments.
type KindSummary struct {
	Kind string `json:"kind"`
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
	Label       string            `json:"label"`
	Settings    Settings          `json:"settings"`
	Skipped     map[string]string `json:"skipped,omitempty"`
	Now         Totals            `json:"now"`
	Best        Totals            `json:"best"` // every shipment on its best alternative (or as now)
	SavedKg     float64           `json:"saved_kg"`
	SavedPct    float64           `json:"saved_pct"`
	Kinds       []KindSummary     `json:"kinds"`
	Lanes       []Lane            `json:"lanes"`
	Sources     []string          `json:"sources"`
	Limitations []string          `json:"limitations"`
	Shipments   []ShipmentResult  `json:"shipments"`
	Failed      int               `json:"failed"`
}

// Limitations are what the recommendations don't take into account.
func Limitations() []string {
	return []string{
		"No transit times or costs: there is no standard source for them, so alternatives are compared on emissions and distance only.",
		"Whether a rail, barge or ferry service actually runs on the lane, how often, and with free capacity is not known: an alternative is possible on the networks, not a booked service.",
		"Terminals and ports come from UN/LOCODE, which marks rail terminals and ports but not whether they handle containers or trailers.",
		"New rail, waterway and sea legs run between rail terminals or ports (from UN/LOCODE), with road to and from the nearest one. Each terminal handling this adds (truck to train, barge or container ship and back) counts GLEC v3.3's intermodal terminal value (Table 3: 18.2 kg CO2e per container, 21.4 temperature-controlled). Containers = the shipment's TEU, or its tonnes ÷ GLEC's tonnes per TEU (10; light 6, heavy 14.5); Table 3 gives no container size, so one container = one TEU. Airports, Ro-Ro and bulk terminals have no GLEC value and are not counted.",
		"New legs use fleet-average default factors (GLEC Framework v3.3 default values; UK DESNZ 2026 for ferries, Ro-Ro and non-container ships), not a carrier's own data.",
		"Combining shipments needs shipping dates, which the shipments file doesn't have: fuller loads use the load you give for every road vehicle.",
		"Rail and waterway distances are shortest paths on OpenStreetMap; a waterway alternative needs both ends near a navigable river or canal.",
	}
}

// Run finds alternatives for each shipment, with up to workers shipments
// at once. Shipments with the same legs (and no measured fuel) are
// calculated once and scaled by weight: emissions are proportional to the
// tonnes carried.
func Run(ctx context.Context, c calc.Calculator, shipments []calc.Shipment, set Settings, workers int) (Result, error) {
	set, skipped, err := set.Validate()
	if err != nil {
		return Result{}, err
	}
	for _, k := range []struct {
		kind    string
		mode    calc.Mode
		vehicle string
	}{{Rail, calc.Rail, set.RailVehicle}, {AirToRail, calc.Rail, set.RailVehicle}, {Waterway, calc.InlandWaterway, set.WaterwayVehicle},
		{ShortSea, calc.Sea, set.ShortSeaVehicle}, {AirToSea, calc.Sea, set.SeaVehicle}} {
		if set.has(k.kind) {
			if _, err := c.Factors.LegFactor(k.mode, k.vehicle); err != nil {
				return Result{}, fmt.Errorf("%w: %s vehicle %q: %v", ErrInvalidSettings, k.kind, k.vehicle, err)
			}
		}
	}
	if set.has(Load) && set.LoadVehicle != "" {
		if _, err := c.Factors.LegFactor(calc.Road, set.LoadVehicle); err != nil {
			return Result{}, fmt.Errorf("%w: load_vehicle %q: %v", ErrInvalidSettings, set.LoadVehicle, err)
		}
	}
	// Sea and waterway alternatives need their networks: a straight line
	// would cross land.
	if dm, ok := c.Distances.(calc.DistanceMethoder); ok {
		var kinds []string
		for _, k := range set.Kinds {
			m := calc.Mode("")
			switch k {
			case Waterway:
				m = calc.InlandWaterway
			case ShortSea, AirToSea, OtherPort:
				m = calc.Sea
			}
			if m != "" && strings.HasPrefix(dm.DistanceMethod(m), "great_circle") {
				skipped[k] = fmt.Sprintf("no %s network loaded: distances would be straight lines across land", network(m))
				continue
			}
			kinds = append(kinds, k)
		}
		set.Kinds = kinds
	}
	res := Result{Label: Label, Settings: set, Skipped: skipped, Sources: sources(c.Factors, set), Limitations: Limitations()}

	// Group shipments that only differ in weight.
	type group struct {
		first int
		all   []int
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, s := range shipments {
		key, ok := groupKey(s)
		if g := byKey[key]; ok && g != nil {
			g.all = append(g.all, i)
			continue
		}
		g := &group{first: i, all: []int{i}}
		groups = append(groups, g)
		if ok {
			byKey[key] = g
		}
	}

	out := make([]ShipmentResult, len(shipments))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan *group)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, max(1, len(groups))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				first := analyse(ctx, c, shipments[g.first], set)
				for _, i := range g.all {
					out[i] = rescale(first, shipments[i])
				}
			}
		}()
	}
	for _, g := range groups {
		if ctx.Err() != nil {
			break
		}
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	res.Shipments = out
	summarise(&res)
	return res, nil
}

// groupKey is a shipment's legs and cargo basis without the weight; ok is
// false when emissions aren't proportional to weight (measured fuel) or the
// cargo isn't given by weight.
func groupKey(s calc.Shipment) (string, bool) {
	if s.WeightKg <= 0 || s.TEU != 0 || s.TEUNetTonnes != 0 || s.ContainerLoad != "" {
		return "", false
	}
	for _, l := range s.Legs {
		if l.Fuel != nil {
			return "", false
		}
	}
	b, err := json.Marshal(struct {
		Legs []calc.Leg
		Hubs []calc.HubStop
		Add  *bool
	}{s.Legs, s.Hubs, s.AddRoadLegs})
	if err != nil {
		return "", false
	}
	return string(b), true
}

// rescale turns a group's first result into another member's.
func rescale(r ShipmentResult, s calc.Shipment) ShipmentResult {
	out := r
	out.ShipmentID = s.ID
	if r.WeightKg <= 0 || s.WeightKg == r.WeightKg || r.Error != "" {
		out.WeightKg = s.WeightKg
		return out
	}
	k := s.WeightKg / r.WeightKg
	out.WeightKg = s.WeightKg
	out.Now = r.Now.scale(k)
	out.Options = make([]Option, len(r.Options))
	for i, o := range r.Options {
		o.Route = o.Route.scale(k)
		o.SavedKg *= k
		out.Options[i] = o
	}
	return out
}

func (r Route) scale(k float64) Route {
	out := r
	out.Totals = r.Totals.scale(k)
	out.Steps = make([]Step, len(r.Steps))
	for i, s := range r.Steps {
		s.WTWKg *= k
		out.Steps[i] = s
	}
	if r.Terminals != nil {
		out.Terminals = make([]calc.HubResult, len(r.Terminals))
		for i, h := range r.Terminals {
			h.WTWKg *= k
			h.Containers *= k
			out.Terminals[i] = h
		}
	}
	return out
}

// sources lists what the settings are based on.
func sources(f calc.FactorProvider, set Settings) []string {
	var s []string
	road := set.has(Rail) || set.has(Waterway) || set.has(ShortSea)
	if road && *set.MinRoadKm == DefaultMinRoadKm {
		s = append(s, SourceWhitePaper)
	}
	if *set.MinMainKm == DefaultMinMainKm {
		s = append(s, SourceMainKm)
	}
	if (set.has(Waterway) || set.has(ShortSea)) && *set.MaxPortRoadKm == DefaultMaxPortRoadKm {
		s = append(s, SourcePortRoad)
	}
	if set.has(Rail) || set.has(AirToRail) {
		if set.Terminal == TerminalBestTrip {
			s = append(s, SourceBestTrip)
		} else {
			s = append(s, SourceTerminal)
		}
	}
	if set.has(AirToSea) || set.has(AirToRail) {
		s = append(s, SourceAir)
	}
	if set.has(OtherPort) {
		s = append(s, SourcePorts)
	}
	seen := map[string]bool{}
	for _, v := range []struct {
		kind    string
		mode    calc.Mode
		vehicle string
		what    string
	}{{Rail, calc.Rail, set.RailVehicle, "Rail"}, {AirToRail, calc.Rail, set.RailVehicle, "Rail"},
		{Waterway, calc.InlandWaterway, set.WaterwayVehicle, "Inland waterway"}, {ShortSea, calc.Sea, set.ShortSeaVehicle, "Short sea"},
		{AirToSea, calc.Sea, set.SeaVehicle, "Sea"}} {
		if !set.has(v.kind) || seen[v.what+v.vehicle] {
			continue
		}
		seen[v.what+v.vehicle] = true
		if lf, err := f.LegFactor(v.mode, v.vehicle); err == nil {
			s = append(s, fmt.Sprintf("%s (%s): %s", v.what, v.vehicle, lf.Source))
		}
	}
	if set.has(Load) {
		l := "Fuller loads: the calculator's load model for each road vehicle"
		if set.Source != "" {
			l += "; load given by the user: " + set.Source
		} else {
			l += "; load given by the user"
		}
		s = append(s, l)
	}
	return s
}

// analyser holds what one shipment's alternatives are built from.
type analyser struct {
	ctx  context.Context
	c    calc.Calculator
	set  Settings
	s    calc.Shipment // with leg ids
	base calc.Result
	now  Route
	own  map[string]calc.LegResult // the shipment's own legs as calculated
	sr   *ShipmentResult
	// nowTerminals counts the terminal changes of mode now; containers is
	// the shipment's containers for terminal handlings (0: not counted,
	// for the reason in noTerminals) and containerRule how it was set.
	nowTerminals  int
	containers    float64
	containerRule string
	noTerminals   string
}

// IntermodalTerminal is the hub type added for an extra change of mode.
const IntermodalTerminal = "intermodal_terminal"

// unitised says whether a leg's cargo goes through an intermodal terminal
// when the mode changes: road and rail always; waterway and sea only on
// container vessels (GLEC has no default for Ro-Ro or bulk terminals, and
// excludes self-driving cargo at Ro-Ro terminals: Section 1 Chapter 4,
// logistics hubs). Air: no GLEC default for airports.
func unitised(l calc.LegResult) bool {
	switch l.Mode {
	case calc.Road, calc.Rail:
		return true
	case calc.InlandWaterway, calc.Sea:
		vt := l.VehicleType
		if l.Factor != nil {
			vt += " " + l.Factor.VehicleType
		}
		return strings.Contains(strings.ToLower(vt), "container")
	}
	return false
}

// terminals counts the changes of mode at an intermodal terminal, and the
// ends of the new legs (main) where the shipment starts or ends: the cargo
// was on a truck there before, so it is handled at the terminal from or to
// the truck even when the road to it is too short to count (GapKm).
func terminals(r calc.Result, main map[string]bool) int {
	n := 0
	for i := 1; i < len(r.Legs); i++ {
		a, b := r.Legs[i-1], r.Legs[i]
		if a.Mode != b.Mode && unitised(a) && unitised(b) {
			n++
		}
	}
	if k := len(r.Legs); k > 0 {
		if l := r.Legs[0]; main[l.LegID] && l.Mode != calc.Road && unitised(l) {
			n++
		}
		if l := r.Legs[k-1]; main[l.LegID] && l.Mode != calc.Road && unitised(l) {
			n++
		}
	}
	return n
}

// explicit is r's route as legs to calculate again (map, download): the
// shipment's legs (in) with their ends as calculated, and the added road
// legs.
func explicit(in []calc.Leg, r calc.Result) []calc.Leg {
	byID := map[string]calc.Leg{}
	for _, l := range in {
		byID[l.ID] = l
	}
	out := make([]calc.Leg, 0, len(r.Legs))
	for _, lr := range r.Legs {
		l, ok := byID[lr.LegID]
		if !ok || lr.Added != "" {
			l = calc.Leg{ID: lr.LegID, Mode: lr.Mode, VehicleType: lr.VehicleType}
		}
		if lr.From != nil {
			l.From = *lr.From
		}
		if lr.To != nil {
			l.To = *lr.To
		}
		l.AtTerminals = false
		out = append(out, l)
	}
	return out
}

// setContainers works out the shipment's containers for terminal
// handlings: its TEU, or its tonnes ÷ GLEC's tonnes per TEU for its cargo
// type (light, heavy) or the average.
func (a *analyser) setContainers() {
	if _, err := a.c.Factors.HubFactor(IntermodalTerminal); err != nil {
		a.noTerminals = "the factor set has no intermodal terminal value"
		return
	}
	if a.s.TEU > 0 {
		a.containers, a.containerRule = a.s.TEU, "the shipment's TEU"
		return
	}
	clp, ok := a.c.Factors.(calc.ContainerLoadProvider)
	if !ok {
		a.noTerminals = "the factor set has no tonnes per TEU"
		return
	}
	profile := calc.DefaultContainerLoad
	if ct := a.s.CargoType; ct == "light" || ct == "heavy" {
		profile = ct
	}
	perTEU, err := clp.ContainerLoad(profile)
	if err != nil || perTEU <= 0 {
		a.noTerminals = "the factor set has no tonnes per TEU"
		return
	}
	a.containers = a.base.WeightKg / 1000 / perTEU
	a.containerRule = fmt.Sprintf("TEU = tonnes ÷ %g, GLEC %s", perTEU, profile)
}

// addTerminals adds to r the terminal handlings it has beyond now, and
// says so in a few words ("" if none).
func (a *analyser) addTerminals(cc calc.Calculator, r *calc.Result, main map[string]bool) ([]calc.HubResult, string, error) {
	extra := terminals(*r, main) - a.nowTerminals
	if extra <= 0 {
		return nil, "", nil
	}
	if a.containers <= 0 {
		return nil, fmt.Sprintf("%d extra terminal handlings not counted: %s", extra, a.noTerminals), nil
	}
	var hs []calc.HubResult
	kg := 0.0
	for k := range extra {
		h, err := cc.Hub(calc.HubStop{ID: fmt.Sprintf("added-terminal-%d", k+1), HubType: IntermodalTerminal, Containers: a.containers},
			a.s.CargoType, r.WeightKg/1000)
		if err != nil {
			return nil, "", err
		}
		hs = append(hs, h)
		kg += h.WTWKg
	}
	r.Hubs = append(r.Hubs, hs...)
	r.WTWKg += kg
	what := "terminal handlings"
	if extra == 1 {
		what = "terminal handling"
	}
	// No weight-dependent numbers here: results of shipments with the same
	// legs are scaled by weight (rescale); the numbers are in Terminals.
	return hs, fmt.Sprintf("%d extra %s (GLEC intermodal terminal, per TEU; %s)", extra, what, a.containerRule), nil
}

func analyse(ctx context.Context, c calc.Calculator, s calc.Shipment, set Settings) ShipmentResult {
	sr := ShipmentResult{ShipmentID: s.ID, WeightKg: s.WeightKg}
	s.Legs = append([]calc.Leg(nil), s.Legs...)
	for i := range s.Legs {
		if s.Legs[i].ID == "" {
			s.Legs[i].ID = fmt.Sprintf("leg-%d", i+1)
		}
	}
	base, err := c.Calculate(ctx, s)
	if err != nil {
		sr.Error = err.Error()
		return sr
	}
	sr.WeightKg = base.WeightKg
	a := &analyser{ctx: ctx, c: c, set: set, s: s, base: base, own: map[string]calc.LegResult{}, sr: &sr}
	for _, l := range base.Legs {
		if l.Added == "" {
			a.own[l.LegID] = l
		}
	}
	a.now = route(c, base)
	a.nowTerminals = terminals(base, nil)
	a.setContainers()
	sr.Now = a.now
	sr.Legs = s.Legs
	sr.Lane = a.lane()

	if set.has(Rail) {
		a.roadShift(Rail, calc.Rail, set.RailVehicle)
	}
	if set.has(Waterway) {
		a.roadShift(Waterway, calc.InlandWaterway, set.WaterwayVehicle)
	}
	if set.has(ShortSea) {
		a.roadShift(ShortSea, calc.Sea, set.ShortSeaVehicle)
	}
	if set.has(AirToSea) {
		a.airShift(AirToSea, calc.Sea, set.SeaVehicle)
	}
	if set.has(AirToRail) {
		a.airShift(AirToRail, calc.Rail, set.RailVehicle)
	}
	if set.has(OtherPort) {
		a.otherPorts()
	}
	if set.has(Load) {
		a.loads()
	}
	sort.SliceStable(sr.Options, func(i, j int) bool { return sr.Options[i].SavedKg > sr.Options[j].SavedKg })
	return sr
}

func (a *analyser) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	for _, m := range a.sr.Notes {
		if m == n {
			return
		}
	}
	a.sr.Notes = append(a.sr.Notes, n)
}

// loc is where a leg end was calculated, or as given.
func (a *analyser) loc(l calc.Leg, from bool) (calc.Location, bool) {
	r, ok := a.own[l.ID]
	if ok && from && r.From != nil {
		return *r.From, true
	}
	if ok && !from && r.To != nil {
		return *r.To, true
	}
	p := l.To
	if from {
		p = l.From
	}
	if p.Lat == 0 && p.Lon == 0 && p.Code == "" {
		return calc.Location{}, false
	}
	return p, true
}

func (a *analyser) lane() string {
	if len(a.s.Legs) == 0 {
		return ""
	}
	from, ok1 := a.loc(a.s.Legs[0], true)
	to, ok2 := a.loc(a.s.Legs[len(a.s.Legs)-1], false)
	if !ok1 || !ok2 {
		return ""
	}
	return a.name(from) + " → " + a.name(to)
}

// name labels a place: its name, else the nearest town within 30 km, else
// its coordinates.
func (a *analyser) name(p calc.Location) string {
	return placeName(a.c, p)
}

func placeName(c calc.Calculator, p calc.Location) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Code != "" && p.Lat == 0 && p.Lon == 0 {
		return p.Code
	}
	if t, ok := c.Places.(interface {
		Town(calc.Location) (string, float64, bool)
	}); ok {
		if n, km, ok := t.Town(p); ok {
			if km > 5 {
				return "near " + n
			}
			return n
		}
	} else if f, ok := c.Places.(calc.PlaceFinder); ok {
		if near := f.Near(p, []string{"city"}, 30, 1); len(near) > 0 {
			n := near[0].Name
			if i := strings.Index(n, " ("); i > 0 {
				n = n[:i]
			}
			if distance.Haversine(p, near[0]) > 3 {
				return "near " + n
			}
			return n
		}
	}
	return fmt.Sprintf("%.3f, %.3f", p.Lat, p.Lon)
}

func route(c calc.Calculator, r calc.Result) Route {
	rt := Route{Totals: totals(r)}
	var last calc.Mode
	for i, l := range r.Legs {
		st := Step{Mode: l.Mode, VehicleType: l.VehicleType, DistanceKm: l.DistanceKm, WTWKg: l.WTWKg, Added: l.Added != ""}
		if l.From != nil {
			st.From = placeName(c, *l.From)
		}
		if l.To != nil {
			st.To = placeName(c, *l.To)
		}
		rt.Steps = append(rt.Steps, st)
		rt.DistanceKm += l.DistanceKm
		if i > 0 && l.Mode != last {
			rt.Transfers++
		}
		if i == 0 || l.Mode != last {
			rt.Modes = append(rt.Modes, string(l.Mode))
		}
		last = l.Mode
	}
	return rt
}

// try calculates an alternative and adds it when it is lower than now.
// main are the ids of the new legs in mode m, which must be longer than
// min_main_km in a straight line; portRoad limits the added road legs.
func (a *analyser) try(kind string, s calc.Shipment, cc calc.Calculator, m calc.Mode, main map[string]bool, portRoad bool, summary func(calc.Result) string) bool {
	s.AddRoadLegs = ptr(true)
	r, err := cc.Calculate(a.ctx, s)
	if err != nil {
		a.note("%s: not possible: %v", kindName(kind), shortErr(err))
		return false
	}
	if len(r.Warnings) > len(a.base.Warnings) {
		a.note("%s: not possible: %s", kindName(kind), r.Warnings[len(r.Warnings)-1])
		return false
	}
	for _, l := range r.Legs {
		if !main[l.LegID] || l.Mode != m || l.From == nil || l.To == nil {
			continue
		}
		if km := distance.Haversine(*l.From, *l.To); km <= *a.set.MinMainKm {
			a.note("%s: not possible: the %s part would be %.0f km in a straight line, not more than %.0f km", kindName(kind), network(m), km, *a.set.MinMainKm)
			return false
		}
	}
	if portRoad {
		for _, l := range r.Legs {
			if l.Added != calc.AddedAccess || l.From == nil || l.To == nil {
				continue
			}
			if km := distance.Haversine(*l.From, *l.To); km > *a.set.MaxPortRoadKm {
				a.note("%s: not possible: the nearest %s port is %.0f km away in a straight line, more than %.0f km", kindName(kind), network(m), km, *a.set.MaxPortRoadKm)
				return false
			}
		}
	}
	// The new legs' ends where the shipment starts or ends were on a truck
	// before, except for another port: the sea leg was a sea leg already.
	edges := main
	if kind == OtherPort {
		edges = nil
	}
	hubs, hubText, err := a.addTerminals(cc, &r, edges)
	if err != nil {
		a.note("%s: not possible: %v", kindName(kind), shortErr(err))
		return false
	}
	saved := a.base.WTWKg - r.WTWKg
	if !(saved > 1e-9) {
		a.note("%s: possible, but not lower than now", kindName(kind))
		return false
	}
	rt := route(cc, r)
	rt.Terminals = hubs
	o := Option{Kind: kind, Route: rt, SavedKg: saved, Legs: explicit(s.Legs, r), Summary: summary(r)}
	if hubText != "" {
		o.Summary += "; " + hubText
	}
	if a.base.WTWKg > 0 {
		o.SavedPct = saved / a.base.WTWKg * 100
	}
	if a.now.DistanceKm > 0 {
		o.ExtraKmPct = (rt.DistanceKm - a.now.DistanceKm) / a.now.DistanceKm * 100
	}
	a.sr.Options = append(a.sr.Options, o)
	return true
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func network(m calc.Mode) string {
	switch m {
	case calc.InlandWaterway:
		return "waterway"
	default:
		return string(m)
	}
}

func kindName(k string) string {
	switch k {
	case Rail:
		return "Road to rail"
	case Waterway:
		return "Road to inland waterway"
	case ShortSea:
		return "Road to short sea"
	case AirToSea:
		return "Air to sea"
	case AirToRail:
		return "Air to rail"
	case OtherPort:
		return "Other port"
	case Load:
		return "Fuller loads"
	}
	return k
}

// describe summarises a route: the main legs in mode m with their ends.
func (a *analyser) describe(r calc.Result, m calc.Mode, main map[string]bool) string {
	var parts []string
	for _, l := range r.Legs {
		if main[l.LegID] && l.Mode == m && l.From != nil && l.To != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f km, %s → %s", network(m), l.DistanceKm, placeName(a.c, *l.From), placeName(a.c, *l.To)))
		}
	}
	road := 0.0
	for _, l := range r.Legs {
		if l.Mode == calc.Road {
			road += l.DistanceKm
		}
	}
	s := strings.Join(parts, "; ")
	if road > 0 {
		s += fmt.Sprintf("; road %.0f km in all", road)
	}
	return s
}

// roadShift moves the shipment's long road legs to mode m.
func (a *analyser) roadShift(kind string, m calc.Mode, vehicle string) {
	var cands []int
	for j, l := range a.s.Legs {
		r, ok := a.own[l.ID]
		if l.Mode != calc.Road || !ok || l.Fuel != nil || r.DistanceKm <= *a.set.MinRoadKm {
			continue
		}
		if _, ok := a.loc(l, true); !ok {
			continue
		}
		if _, ok := a.loc(l, false); !ok {
			continue
		}
		cands = append(cands, j)
	}
	if len(cands) == 0 {
		if kind == Rail {
			a.note("No road leg over %.0f km to move to another mode", *a.set.MinRoadKm)
		}
		return
	}
	build := func(js []int) (calc.Shipment, calc.Calculator, map[string]bool) {
		s := a.s
		s.Legs = append([]calc.Leg(nil), a.s.Legs...)
		cc := a.c
		cc.NearestTerminal = a.set.Terminal != TerminalBestTrip
		main := map[string]bool{}
		for _, j := range js {
			l := a.s.Legs[j]
			if cc.RoadVehicle == "" {
				cc.RoadVehicle = l.VehicleType
			}
			from, _ := a.loc(l, true)
			to, _ := a.loc(l, false)
			s.Legs[j] = calc.Leg{ID: l.ID, Mode: m, VehicleType: vehicle, From: from, To: to, AtTerminals: true}
			main[l.ID] = true
		}
		return s, cc, main
	}
	portRoad := m == calc.InlandWaterway || m == calc.Sea
	s, cc, main := build(cands)
	sum := func(r calc.Result) string { return a.describe(r, m, main) }
	if a.try(kind, s, cc, m, main, portRoad, sum) || len(cands) == 1 {
		return
	}
	// Legs one at a time; then the ones that work together.
	var good []int
	n := len(a.sr.Options)
	for _, j := range cands {
		s1, cc1, main1 := build([]int{j})
		if a.try(kind, s1, cc1, m, main1, portRoad, func(r calc.Result) string { return a.describe(r, m, main1) }) {
			good = append(good, j)
		}
	}
	if len(good) > 1 {
		a.sr.Options = a.sr.Options[:n]
		s, cc, main = build(good)
		a.try(kind, s, cc, m, main, portRoad, func(r calc.Result) string { return a.describe(r, m, main) })
	}
}

// airShift moves each air leg, with the road legs next to it, to one leg
// in mode m from where the cargo starts to where it ends.
func (a *analyser) airShift(kind string, m calc.Mode, vehicle string) {
	legs := a.s.Legs
	var out []calc.Leg
	main := map[string]bool{}
	cc := a.c
	cc.NearestTerminal = a.set.Terminal != TerminalBestTrip
	for j := 0; j < len(legs); j++ {
		l := legs[j]
		if l.Mode != calc.Air {
			out = append(out, l)
			continue
		}
		start, end := j, j
		// The road leg before, unless it was already taken into a span.
		if j > 0 && len(out) > 0 && out[len(out)-1].ID == legs[j-1].ID && legs[j-1].Mode == calc.Road && legs[j-1].Fuel == nil {
			start = j - 1
			out = out[:len(out)-1]
		}
		if j+1 < len(legs) && legs[j+1].Mode == calc.Road && legs[j+1].Fuel == nil {
			end = j + 1
		}
		from, ok1 := a.loc(legs[start], true)
		to, ok2 := a.loc(legs[end], false)
		if !ok1 || !ok2 {
			a.note("%s: not possible: leg %s has no places to route from", kindName(kind), l.ID)
			return
		}
		if cc.RoadVehicle == "" {
			for k := start; k <= end; k++ {
				if legs[k].Mode == calc.Road {
					cc.RoadVehicle = legs[k].VehicleType
					break
				}
			}
		}
		out = append(out, calc.Leg{ID: l.ID, Mode: m, VehicleType: vehicle, From: from, To: to, AtTerminals: true})
		main[l.ID] = true
		j = end
	}
	if len(main) == 0 {
		return
	}
	s := a.s
	s.Legs = out
	a.try(kind, s, cc, m, main, false, func(r calc.Result) string { return a.describe(r, m, main) })
}

// otherPorts tries the large sea ports near each end of the shipment's sea
// legs, with the leg on the land side of that end going to the new port.
func (a *analyser) otherPorts() {
	f, ok := a.c.Places.(calc.PlaceFinder)
	if !ok {
		return
	}
	found := false
	for j, l := range a.s.Legs {
		if l.Mode != calc.Sea {
			continue
		}
		for _, end := range []struct {
			fromSide bool
			land     int
		}{{true, j - 1}, {false, j + 1}} {
			if end.land < 0 || end.land >= len(a.s.Legs) {
				continue
			}
			ll := a.s.Legs[end.land]
			if (ll.Mode != calc.Road && ll.Mode != calc.Rail) || ll.Fuel != nil {
				continue
			}
			port, ok := a.loc(l, end.fromSide)
			if !ok {
				continue
			}
			found = true
			var cands []calc.Location
			for _, q := range f.Near(port, []string{calc.KindSeaPort}, *a.set.PortRadiusKm, *a.set.MaxPorts+3) {
				if distance.Haversine(q, port) > 20 && len(cands) < *a.set.MaxPorts {
					cands = append(cands, q)
				}
			}
			for _, q := range cands {
				s := a.s
				s.Legs = append([]calc.Leg(nil), a.s.Legs...)
				sea, land := s.Legs[j], s.Legs[end.land]
				sea.DistanceKm, sea.DistanceType = 0, ""
				land.DistanceKm, land.DistanceType = 0, ""
				if end.fromSide {
					sea.From, land.To = q, q
				} else {
					sea.To, land.From = q, q
				}
				// The sea leg's other end as calculated, so only this end changes.
				if other, ok := a.loc(l, !end.fromSide); ok {
					if end.fromSide {
						sea.To = other
					} else {
						sea.From = other
					}
				}
				s.Legs[j], s.Legs[end.land] = sea, land
				main := map[string]bool{sea.ID: true}
				name := placeName(a.c, q)
				a.try(OtherPort, s, a.c, calc.Sea, main, false, func(r calc.Result) string {
					return "via " + name + "; " + a.describe(r, calc.Sea, main)
				})
			}
		}
	}
	if !found && hasSea(a.s.Legs) {
		a.note("Other port: no sea leg with a road or rail leg on its land side")
	}
}

// loads gives the shipment's road legs the load the user set.
func (a *analyser) loads() {
	s := a.s
	s.Legs = append([]calc.Leg(nil), a.s.Legs...)
	n := 0
	for j := range s.Legs {
		l := &s.Legs[j]
		if l.Mode != calc.Road || l.Fuel != nil || l.EnergyKWhPerTkm != 0 {
			continue
		}
		if a.set.LoadVehicle != "" {
			l.VehicleType = a.set.LoadVehicle
		}
		l.LoadTonnes, l.LoadFactor = val(a.set.LoadTonnes, 0), val(a.set.LoadFactor, 0)
		l.EmptyTripFactor = val(a.set.EmptyTripFactor, 0)
		n++
	}
	if n == 0 {
		a.note("Fuller loads: no road leg on a fleet-average factor")
		return
	}
	load := ""
	if a.set.LoadTonnes != nil {
		load = fmt.Sprintf("%g t on the vehicle", *a.set.LoadTonnes)
	} else {
		load = fmt.Sprintf("load factor %g", *a.set.LoadFactor)
	}
	load += fmt.Sprintf(", empty trip factor %g", val(a.set.EmptyTripFactor, 0))
	if a.set.LoadVehicle != "" {
		load += ", " + a.set.LoadVehicle
	}
	s.AddRoadLegs = a.s.AddRoadLegs
	r, err := a.c.Calculate(a.ctx, s)
	if err != nil {
		a.note("Fuller loads: not possible: %v", shortErr(err))
		return
	}
	saved := a.base.WTWKg - r.WTWKg
	if !(saved > 1e-9) {
		a.note("Fuller loads: %s is not lower than now", load)
		return
	}
	rt := route(a.c, r)
	o := Option{Kind: Load, Route: rt, SavedKg: saved, Legs: s.Legs, Summary: fmt.Sprintf("%d road legs at %s", n, load)}
	if n == 1 {
		o.Summary = "1 road leg at " + load
	}
	if a.base.WTWKg > 0 {
		o.SavedPct = saved / a.base.WTWKg * 100
	}
	if a.now.DistanceKm > 0 {
		o.ExtraKmPct = (rt.DistanceKm - a.now.DistanceKm) / a.now.DistanceKm * 100
	}
	a.sr.Options = append(a.sr.Options, o)
}

// summarise fills in the totals, kinds and lanes.
func summarise(res *Result) {
	kinds := map[string]*KindSummary{}
	for _, k := range res.Settings.Kinds {
		kinds[k] = &KindSummary{Kind: k}
	}
	type laneAcc struct {
		Lane
		kinds map[string]int
	}
	lanes := map[string]*laneAcc{}
	var order []string
	for _, sr := range res.Shipments {
		if sr.Error != "" {
			res.Failed++
			continue
		}
		res.Now.add(sr.Now.Totals)
		best := sr.Now.Totals
		bestKind := ""
		if len(sr.Options) > 0 {
			best, bestKind = sr.Options[0].Totals, sr.Options[0].Kind
		}
		res.Best.add(best)
		seen := map[string]bool{}
		for _, o := range sr.Options {
			if k := kinds[o.Kind]; k != nil && !seen[o.Kind] {
				seen[o.Kind] = true
				k.Shipments++
				k.SavedKg += o.SavedKg
			}
		}
		if k := kinds[bestKind]; k != nil {
			k.BestFor++
		}
		key := sr.Lane
		if key == "" {
			key = sr.ShipmentID
		}
		la := lanes[key]
		if la == nil {
			la = &laneAcc{Lane: Lane{Lane: key}, kinds: map[string]int{}}
			lanes[key] = la
			order = append(order, key)
		}
		la.Shipments++
		la.NowKg += sr.Now.WTWKg
		la.BestKg += best.WTWKg
		if bestKind != "" {
			la.kinds[bestKind]++
		}
	}
	res.SavedKg = res.Now.WTWKg - res.Best.WTWKg
	if res.Now.WTWKg > 0 {
		res.SavedPct = res.SavedKg / res.Now.WTWKg * 100
	}
	for _, k := range res.Settings.Kinds {
		res.Kinds = append(res.Kinds, *kinds[k])
	}
	for _, key := range order {
		la := lanes[key]
		la.SavedKg = la.NowKg - la.BestKg
		bk, bn := "", 0
		for _, k := range Kinds() {
			if la.kinds[k] > bn {
				bk, bn = k, la.kinds[k]
			}
		}
		la.BestKind = bk
		res.Lanes = append(res.Lanes, la.Lane)
	}
	sort.SliceStable(res.Lanes, func(i, j int) bool { return res.Lanes[i].SavedKg > res.Lanes[j].SavedKg })
	if math.IsNaN(res.SavedPct) {
		res.SavedPct = 0
	}
}

func hasSea(legs []calc.Leg) bool {
	for _, l := range legs {
		if l.Mode == calc.Sea {
			return true
		}
	}
	return false
}
