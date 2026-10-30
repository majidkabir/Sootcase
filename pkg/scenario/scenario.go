// Package scenario recalculates shipments with changed inputs, to compare
// "what if" cases with how they are: road freight moved to rail, electric
// trucks, other fuels, other loads. It uses the same calculation (GLEC
// Framework v3.2, ISO 14083) for both, so the difference comes only from the
// changes. Results are scenarios, not reported emissions.
//
// Every value a change uses is either given by the user (with its source)
// or a stated default with its source:
//
//   - road_to_rail: road legs longer than min_road_km (default 300 km, EU
//     Transport White Paper 2011, Council document 11255/11, II.8: "30% of
//     road freight over 300 km should shift to other modes such as rail or
//     waterborne transport by 2030") move to rail for share of the cargo
//     (default 0.3, same source). The rail part must be longer than
//     min_rail_km in a straight line (default 100 km, Council Directive
//     92/106/EEC on combined transport, Article 1), with road legs to and
//     from the nearest rail terminal on the network that can be reached by
//     road (the Directive's "nearest suitable rail station"; terminal
//     "best_trip" uses the calculator's usual choice instead), also when the
//     leg's end is on the rail network but not at a terminal. Each terminal
//     handling this adds counts GLEC v3.3's intermodal terminal value per
//     container (calc.TerminalHandlings, calc.Calculator.Containers).
//   - electrify: matching road legs run on electricity: no tank-to-wheel
//     emissions, and kwh_per_tkm × the country's grid factor upstream (as
//     the calculator's energy method). kwh_per_tkm, country and source are
//     the user's.
//   - fuel_switch: matching diesel road legs use another fuel. The leg's
//     energy use is derived from its diesel emissions and the diesel fuel
//     factor per kWh (net CV), times energy_ratio (default 1: the same energy
//     per tonne-km), and multiplied by the new fuel's factor per GJ (net CV).
//     A modelled calculation (ISO 14083 7.2.4).
//   - load: the shipment's own matching road legs get load_tonnes or
//     load_factor and empty_trip_factor (the calculator's load model).
//
// A share s of a change applies it to s of the cargo on each matching leg:
// emissions are (1 − s) × before + s × after.
package scenario

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
)

// Change types.
const (
	RoadToRail  = "road_to_rail"
	Electrify   = "electrify"
	FuelSwitch  = "fuel_switch"
	LoadChange  = "load"
	scenarioTag = "Scenario: a what-if recalculation with the same method, not reported emissions."
)

// Defaults for road_to_rail and their sources.
const (
	DefaultMinRoadKm = 300.0
	DefaultRailShare = 0.30
	DefaultMinRailKm = 100.0

	SourceWhitePaper = "EU Transport White Paper 2011 (Council document 11255/11, II.8): \"30% of road freight over 300 km " +
		"should shift to other modes such as rail or waterborne transport by 2030\""
	SourceDirective = "Council Directive 92/106/EEC (combined transport), Article 1: the rail section exceeds 100 km as the crow flies; " +
		"road to and from the nearest suitable rail station"
	SourceTerminals = "Road legs to and from the nearest rail terminal on the rail network that can be reached by road. " +
		"\"Suitable\" in the Directive's sense can't be checked further: the place list (UN/LOCODE) marks rail terminals, " +
		"not which ones handle containers or trailers"
	SourceBestTrip = "Road legs to and from the rail terminal giving the best trip among the nearest ones (terminal: best_trip)"

	TerminalNearest  = "nearest"
	TerminalBestTrip = "best_trip"
	SourceEnergy     = "Energy use derived from the leg's diesel emissions and the diesel (average biofuel blend) factor per kWh net CV; " +
		"modelled calculation (ISO 14083 7.2.4)"
	SourceBiogenic = "Biofuel factors exclude biogenic CO2, as the GLEC Framework v3.3 asks (p. 64: CO2e excluding biogenic CO2, " +
		"reported separately): it is given separately as biogenic_co2_kg, from DESNZ's \"outside of scopes\" factor for the fuel. " +
		"The biofuel share of the average diesel blend in fleet-average factors isn't counted (the vehicle factors don't give it)"
)

// DieselFuel is the fuel the diesel road factors are converted from.
const DieselFuel = "liquid_fuels/diesel_average_biofuel_blend/kwh_net_cv"

// fuelNames maps short fuel names to dataset fuel ids without the unit.
var fuelNames = map[string]string{
	"hvo":            "biofuel/biodiesel_hvo",
	"b100":           "biofuel/biodiesel_me",
	"biodiesel":      "biofuel/biodiesel_me",
	"biomethane":     "biofuel/biomethane_compressed",
	"biomethane_lng": "biofuel/biomethane_liquified",
	"cng":            "gaseous_fuels/cng",
	"lng":            "gaseous_fuels/lng",
}

// FuelNames lists the short fuel names fuel_switch accepts (besides full
// dataset fuel ids).
func FuelNames() []string {
	return []string{"hvo", "b100", "biodiesel", "biomethane", "biomethane_lng", "cng", "lng"}
}

// Change is one change to the shipments. Pointers are optional values that
// get defaults.
type Change struct {
	Type string `json:"type"`
	// VehicleTypes limits the change to road legs with these vehicle types
	// (as given, e.g. "hgv", or the dataset id); empty means all road legs.
	VehicleTypes []string `json:"vehicle_types,omitempty"`
	// Share of the cargo on each matching leg the change applies to (0..1].
	Share *float64 `json:"share,omitempty"`
	// Source is where the user's values come from.
	Source string `json:"source,omitempty"`

	// road_to_rail
	MinRoadKm   *float64 `json:"min_road_km,omitempty"`
	MinRailKm   *float64 `json:"min_rail_km,omitempty"`
	RailVehicle string   `json:"rail_vehicle,omitempty"` // default freight_train
	// Terminal is how road_to_rail picks the rail terminals: "nearest"
	// (default; Directive 92/106/EEC: the nearest suitable rail station) or
	// "best_trip" (the calculator's usual choice among the nearest).
	Terminal string `json:"terminal,omitempty"`

	// electrify
	KWhPerTkm float64 `json:"kwh_per_tkm,omitempty"`
	Country   string  `json:"country,omitempty"` // ISO 3166 alpha-3

	// fuel_switch
	Fuel        string   `json:"fuel,omitempty"`
	EnergyRatio *float64 `json:"energy_ratio,omitempty"`

	// load
	LoadTonnes      *float64 `json:"load_tonnes,omitempty"`
	LoadFactor      *float64 `json:"load_factor,omitempty"`
	EmptyTripFactor *float64 `json:"empty_trip_factor,omitempty"`
}

// Plan is a named set of changes, applied in order.
type Plan struct {
	Name    string   `json:"name,omitempty"`
	Changes []Change `json:"changes"`
}

// Totals are emissions in kg CO2e.
type Totals struct {
	TTWKg float64 `json:"ttw_kg"`
	WTTKg float64 `json:"wtt_kg"`
	WTWKg float64 `json:"wtw_kg"`
	// BiogenicCO2Kg is reported separately, not part of the totals (GLEC
	// Framework v3.2 p. 64): from biofuels a change switches to, and from
	// legs given as measured biofuel use.
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
}

func (t *Totals) add(o Totals, w float64) {
	t.TTWKg += w * o.TTWKg
	t.WTTKg += w * o.WTTKg
	t.WTWKg += w * o.WTWKg
	t.BiogenicCO2Kg += w * o.BiogenicCO2Kg
}

// ShipmentResult compares one shipment.
type ShipmentResult struct {
	ShipmentID string   `json:"shipment_id"`
	Base       Totals   `json:"base"`
	Scenario   Totals   `json:"scenario"`
	DiffKg     float64  `json:"diff_kg"` // scenario − base, WTW
	Notes      []string `json:"notes,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// ChangeReport says what a change did and what it is based on.
type ChangeReport struct {
	Change
	ShipmentsAffected int      `json:"shipments_affected"`
	LegsAffected      int      `json:"legs_affected"`
	Sources           []string `json:"sources"`
}

// Result is the comparison for all shipments.
type Result struct {
	Plan      string           `json:"plan,omitempty"`
	Label     string           `json:"label"`
	Base      Totals           `json:"base"`
	Scenario  Totals           `json:"scenario"`
	DiffKg    float64          `json:"diff_kg"`
	DiffPct   float64          `json:"diff_pct"`
	Changes   []ChangeReport   `json:"changes"`
	Shipments []ShipmentResult `json:"shipments"`
	Failed    int              `json:"failed"`
}

// ErrInvalidPlan reports a plan that can't be applied.
var ErrInvalidPlan = errors.New("invalid scenario plan")

func val(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

// Validate checks the plan and fills in defaults, returning the changes as
// they will be applied.
func (p Plan) Validate() ([]Change, error) {
	if len(p.Changes) == 0 {
		return nil, fmt.Errorf("%w: no changes", ErrInvalidPlan)
	}
	out := make([]Change, len(p.Changes))
	for i, c := range p.Changes {
		bad := func(msg string, a ...any) error {
			return fmt.Errorf("%w: change %d (%s): %s", ErrInvalidPlan, i+1, c.Type, fmt.Sprintf(msg, a...))
		}
		share := 1.0
		switch c.Type {
		case RoadToRail:
			share = DefaultRailShare
			c.MinRoadKm = ptr(val(c.MinRoadKm, DefaultMinRoadKm))
			c.MinRailKm = ptr(val(c.MinRailKm, DefaultMinRailKm))
			if *c.MinRoadKm < 0 || *c.MinRailKm < 0 {
				return nil, bad("distances must be >= 0")
			}
			if c.RailVehicle == "" {
				c.RailVehicle = "freight_train"
			}
			switch c.Terminal {
			case "":
				c.Terminal = TerminalNearest
			case TerminalNearest, TerminalBestTrip:
			default:
				return nil, bad("terminal must be %s or %s", TerminalNearest, TerminalBestTrip)
			}
		case Electrify:
			if !(c.KWhPerTkm > 0) || c.KWhPerTkm > 10 {
				return nil, bad("give kwh_per_tkm (kWh per tonne-km, > 0)")
			}
			if strings.TrimSpace(c.Country) == "" {
				return nil, bad("give country (ISO 3166 alpha-3, e.g. DNK) for the grid factor")
			}
			if strings.TrimSpace(c.Source) == "" {
				return nil, bad("give source: where kwh_per_tkm comes from")
			}
		case FuelSwitch:
			if strings.TrimSpace(c.Fuel) == "" {
				return nil, bad("give fuel (%s, or a dataset fuel id)", strings.Join(FuelNames(), ", "))
			}
			c.EnergyRatio = ptr(val(c.EnergyRatio, 1))
			if !(*c.EnergyRatio > 0) || *c.EnergyRatio > 5 {
				return nil, bad("energy_ratio must be > 0")
			}
			if *c.EnergyRatio != 1 && strings.TrimSpace(c.Source) == "" {
				return nil, bad("give source: where energy_ratio comes from")
			}
		case LoadChange:
			if c.LoadTonnes == nil && c.LoadFactor == nil {
				return nil, bad("give load_tonnes or load_factor")
			}
			if c.LoadTonnes != nil && c.LoadFactor != nil {
				return nil, bad("give load_tonnes or load_factor, not both")
			}
		default:
			return nil, bad("unknown type; use %s, %s, %s or %s", RoadToRail, Electrify, FuelSwitch, LoadChange)
		}
		c.Share = ptr(val(c.Share, share))
		if !(*c.Share > 0) || *c.Share > 1 {
			return nil, bad("share must be > 0 and <= 1")
		}
		out[i] = c
	}
	return out, nil
}

func ptr(v float64) *float64 { return &v }

func (c Change) matches(vehicle string, factor *calc.Factor) bool {
	if len(c.VehicleTypes) == 0 {
		return true
	}
	for _, v := range c.VehicleTypes {
		if v == vehicle || (factor != nil && v == factor.VehicleType) {
			return true
		}
	}
	return false
}

// sources lists what a change is based on.
func (c Change) sources(f calc.FactorProvider) []string {
	var s []string
	if c.Source != "" {
		s = append(s, "Given: "+c.Source)
	}
	switch c.Type {
	case RoadToRail:
		if *c.Share == DefaultRailShare || *c.MinRoadKm == DefaultMinRoadKm {
			s = append(s, SourceWhitePaper)
		}
		if *c.MinRailKm == DefaultMinRailKm {
			s = append(s, SourceDirective)
		}
		if c.Terminal == TerminalBestTrip {
			s = append(s, SourceBestTrip)
		} else {
			s = append(s, SourceTerminals)
		}
		if rf, err := f.LegFactor(calc.Rail, c.RailVehicle); err == nil {
			s = append(s, "Rail: "+rf.Source)
		}
	case Electrify:
		if gp, ok := f.(calc.GridProvider); ok {
			if g, err := gp.Grid(c.Country); err == nil {
				s = append(s, fmt.Sprintf("Grid %s %d: %.0f g CO2e/kWh, %s", g.Country, g.Year, g.G, g.Source))
			}
		}
	case FuelSwitch:
		s = append(s, SourceEnergy)
		if d, err := f.FuelFactor(DieselFuel); err == nil {
			s = append(s, "Diesel: "+d.Source)
		}
		if nf, _, err := energyFactor(f, c.Fuel); err == nil {
			s = append(s, "New fuel: "+nf.Source)
			if strings.HasPrefix(nf.Fuel, "biofuel/") {
				s = append(s, SourceBiogenic)
			}
		}
		if *c.EnergyRatio == 1 {
			s = append(s, "energy_ratio 1: the same energy per tonne-km as the diesel vehicle (an assumption; set it from the vehicle maker's data)")
		}
	}
	return s
}

const kWhPerGJ = 1e9 / 3.6e6

// energyFactor returns a fuel's factor and its kg CO2e per GJ (net CV)
// multiplier: TTW and WTT per GJ are factor.TTW × m and factor.WTT × m.
func energyFactor(f calc.FactorProvider, name string) (calc.FuelFactor, float64, error) {
	base := name
	if id, ok := fuelNames[strings.ToLower(name)]; ok {
		base = id
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, "/gj"), "/kwh_net_cv")
	if ff, err := f.FuelFactor(base + "/gj"); err == nil {
		return ff, 1, nil
	}
	if ff, err := f.FuelFactor(base + "/kwh_net_cv"); err == nil {
		return ff, kWhPerGJ, nil
	}
	// A dataset alias or id with another unit: find its energy variant.
	if ff, err := f.FuelFactor(name); err == nil {
		i := strings.LastIndex(ff.Fuel, "/")
		if i > 0 && ff.Fuel[:i] != base {
			return energyFactor(f, ff.Fuel[:i])
		}
	}
	return calc.FuelFactor{}, 0, fmt.Errorf("%w: no energy (GJ or kWh net CV) factor for fuel %q", ErrInvalidPlan, name)
}

// Run compares the shipments as they are with the plan applied, one at a
// time. RunParallel uses workers.
func Run(ctx context.Context, c calc.Calculator, shipments []calc.Shipment, p Plan) (Result, error) {
	return RunParallel(ctx, c, shipments, p, 1)
}

// RunParallel is Run with up to workers shipments at once; results keep
// the input order.
func RunParallel(ctx context.Context, c calc.Calculator, shipments []calc.Shipment, p Plan, workers int) (Result, error) {
	changes, err := p.Validate()
	if err != nil {
		return Result{}, err
	}
	f := c.Factors
	// Resolve the factors the post-calculation changes need, once.
	var dieselPerGJ float64
	fuels := map[int][3]float64{} // change index -> TTW, WTT, biogenic CO2 kg per GJ
	grids := map[int]float64{}    // change index -> g per kWh
	for i, ch := range changes {
		switch ch.Type {
		case FuelSwitch:
			if dieselPerGJ == 0 {
				d, err := f.FuelFactor(DieselFuel)
				if err != nil {
					return Result{}, fmt.Errorf("%w: the dataset has no diesel factor per kWh (%s)", ErrInvalidPlan, DieselFuel)
				}
				dieselPerGJ = d.TTW * kWhPerGJ
			}
			nf, m, err := energyFactor(f, ch.Fuel)
			if err != nil {
				return Result{}, err
			}
			fuels[i] = [3]float64{nf.TTW * m, nf.WTT * m, nf.BiogenicCO2 * m}
		case Electrify:
			gp, ok := f.(calc.GridProvider)
			if !ok {
				return Result{}, fmt.Errorf("%w: the dataset has no electricity grid factors", ErrInvalidPlan)
			}
			g, err := gp.Grid(ch.Country)
			if err != nil {
				return Result{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
			}
			grids[i] = g.G
		case RoadToRail:
			if _, err := f.LegFactor(calc.Rail, ch.RailVehicle); err != nil {
				return Result{}, fmt.Errorf("%w: rail_vehicle: %v", ErrInvalidPlan, err)
			}
		}
	}

	res := Result{Plan: p.Name, Label: scenarioTag}
	reports := make([]ChangeReport, len(changes))
	for i, ch := range changes {
		reports[i] = ChangeReport{Change: ch, Sources: ch.sources(f)}
	}
	if workers < 1 {
		workers = 1
	}
	type out struct {
		sr   ShipmentResult
		reps []ChangeReport
	}
	outs := make([]out, len(shipments))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, max(1, len(shipments))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				reps := make([]ChangeReport, len(changes))
				outs[i] = out{runOne(ctx, c, shipments[i], changes, dieselPerGJ, fuels, grids, reps), reps}
			}
		}()
	}
	for i := range shipments {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	for _, o := range outs {
		sr := o.sr
		if sr.Error != "" {
			res.Failed++
		} else {
			res.Base.add(sr.Base, 1)
			res.Scenario.add(sr.Scenario, 1)
			for i, r := range o.reps {
				reports[i].LegsAffected += r.LegsAffected
				reports[i].ShipmentsAffected += r.ShipmentsAffected
			}
		}
		res.Shipments = append(res.Shipments, sr)
	}
	res.Changes = reports
	res.DiffKg = res.Scenario.WTWKg - res.Base.WTWKg
	if res.Base.WTWKg > 0 {
		res.DiffPct = res.DiffKg / res.Base.WTWKg * 100
	}
	return res, nil
}

func totals(r calc.Result) Totals { return Totals{r.TTWKg, r.WTTKg, r.WTWKg, r.BiogenicCO2Kg} }

// runOne compares one shipment.
func runOne(ctx context.Context, c calc.Calculator, s calc.Shipment, changes []Change, dieselPerGJ float64,
	fuels map[int][3]float64, grids map[int]float64, reports []ChangeReport) ShipmentResult {
	sr := ShipmentResult{ShipmentID: s.ID}
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
	sr.Base = totals(base)
	touched := make([]bool, len(changes))
	touch := func(i, legs int) {
		if legs > 0 {
			reports[i].LegsAffected += legs
			touched[i] = true
		}
	}

	// Input changes: loads on the shipment's own road legs.
	in := s
	in.Legs = append([]calc.Leg(nil), s.Legs...)
	byID := map[string]calc.LegResult{}
	for _, l := range base.Legs {
		if l.Added == "" {
			byID[l.LegID] = l
		}
	}
	for i, ch := range changes {
		if ch.Type != LoadChange || *ch.Share != 1 {
			continue
		}
		n := 0
		for j := range in.Legs {
			l := &in.Legs[j]
			if l.Mode != calc.Road || l.Fuel != nil || l.EnergyKWhPerTkm != 0 {
				continue
			}
			r := byID[l.ID]
			if !ch.matches(l.VehicleType, r.Factor) {
				continue
			}
			l.LoadTonnes, l.LoadFactor = val(ch.LoadTonnes, 0), val(ch.LoadFactor, 0)
			if ch.EmptyTripFactor != nil {
				l.EmptyTripFactor = *ch.EmptyTripFactor
			}
			n++
		}
		touch(i, n)
	}

	// Variants: the input as changed, and with road moved to rail.
	type variant struct {
		w float64
		s calc.Shipment
		c calc.Calculator
	}
	variants := []variant{{1, in, c}}
	for i, ch := range changes {
		if ch.Type != RoadToRail {
			continue
		}
		var next []variant
		for _, v := range variants {
			rail, n, notes, ok := toRail(ctx, v.c, v.s, ch)
			sr.Notes = append(sr.Notes, notes...)
			if !ok {
				next = append(next, v)
				continue
			}
			touch(i, n)
			next = append(next, variant{v.w * (1 - *ch.Share), v.s, v.c}, variant{v.w * *ch.Share, rail.s, rail.c})
		}
		variants = next
	}

	// Partial loads are a mixture too.
	for i, ch := range changes {
		if ch.Type != LoadChange || *ch.Share == 1 {
			continue
		}
		var next []variant
		for _, v := range variants {
			ls := v.s
			ls.Legs = append([]calc.Leg(nil), v.s.Legs...)
			n := 0
			for j := range ls.Legs {
				l := &ls.Legs[j]
				if l.Mode != calc.Road || l.Fuel != nil || l.EnergyKWhPerTkm != 0 || !ch.matches(l.VehicleType, byID[l.ID].Factor) {
					continue
				}
				l.LoadTonnes, l.LoadFactor = val(ch.LoadTonnes, 0), val(ch.LoadFactor, 0)
				if ch.EmptyTripFactor != nil {
					l.EmptyTripFactor = *ch.EmptyTripFactor
				}
				n++
			}
			if n == 0 {
				next = append(next, v)
				continue
			}
			touch(i, n)
			next = append(next, variant{v.w * (1 - *ch.Share), v.s, v.c}, variant{v.w * *ch.Share, ls, v.c})
		}
		variants = next
	}

	most := make([]int, len(changes)) // legs hit by post-calculation changes, the most in any variant
	for _, v := range variants {
		r, err := v.c.Calculate(ctx, v.s)
		if err != nil {
			sr.Error = "scenario: " + err.Error()
			return sr
		}
		t := totals(r)
		n := make([]int, len(changes))
		for _, l := range r.Legs {
			d, legs := legChanges(l, changes, dieselPerGJ, fuels, grids, touched)
			t.add(d, 1)
			for i, hit := range legs {
				if hit {
					n[i]++
				}
			}
		}
		for i := range n {
			most[i] = max(most[i], n[i])
		}
		sr.Scenario.add(t, v.w)
	}
	for i, n := range most {
		reports[i].LegsAffected += n
	}
	for i, t := range touched {
		if t {
			reports[i].ShipmentsAffected++
		}
	}
	sr.DiffKg = sr.Scenario.WTWKg - sr.Base.WTWKg
	return sr
}

// legChanges returns the change in a leg's emissions from the
// post-calculation changes (electrify, fuel_switch), and which changes hit.
func legChanges(l calc.LegResult, changes []Change, dieselPerGJ float64, fuels map[int][3]float64,
	grids map[int]float64, touched []bool) (Totals, []bool) {
	hit := make([]bool, len(changes))
	if l.Mode != calc.Road {
		return Totals{}, hit
	}
	if l.Method != calc.MethodIntensity && l.Method != calc.MethodLoadModel {
		return Totals{}, hit // measured fuel or electricity: the user's own data
	}
	// The road part of the leg (without a ferry crossing).
	ttw, wtt, tkm := l.TTWKg, l.WTTKg, l.TonneKm
	for _, p := range l.Parts {
		ttw, wtt, tkm = ttw-p.TTWKg, wtt-p.WTTKg, tkm-p.TonneKm
	}
	diesel := l.Factor != nil && strings.Contains(l.Factor.VehicleType, "diesel")
	left := 1.0 // share still on the vehicle's own fuel
	var d Totals
	for i, ch := range changes {
		if left <= 0 || !ch.matches(l.VehicleType, l.Factor) {
			continue
		}
		p := left * *ch.Share
		switch ch.Type {
		case Electrify:
			newWTT := tkm * ch.KWhPerTkm * grids[i] / 1000
			d.add(Totals{TTWKg: -ttw, WTTKg: newWTT - wtt, WTWKg: newWTT - ttw - wtt}, p)
		case FuelSwitch:
			if !diesel || dieselPerGJ <= 0 {
				continue
			}
			gj := ttw / dieselPerGJ * *ch.EnergyRatio
			nt, nw := gj*fuels[i][0], gj*fuels[i][1]
			d.add(Totals{TTWKg: nt - ttw, WTTKg: nw - wtt, WTWKg: nt + nw - ttw - wtt, BiogenicCO2Kg: gj * fuels[i][2]}, p)
		default:
			continue
		}
		left -= p
		hit[i] = true
		touched[i] = true
	}
	return d, hit
}

type railVariant struct {
	s calc.Shipment
	c calc.Calculator
}

// toRail returns the shipment with its long road legs as rail legs, the
// number moved, and notes. ok is false when no leg could move.
func toRail(ctx context.Context, c calc.Calculator, s calc.Shipment, ch Change) (railVariant, int, []string, bool) {
	before, err := c.Calculate(ctx, s)
	if err != nil {
		return railVariant{}, 0, nil, false
	}
	byID := map[string]calc.LegResult{}
	for _, l := range before.Legs {
		if l.Added == "" {
			byID[l.LegID] = l
		}
	}
	var cands []int
	for j, l := range s.Legs {
		r, ok := byID[l.ID]
		if l.Mode != calc.Road || !ok || r.DistanceKm <= *ch.MinRoadKm || !ch.matches(l.VehicleType, r.Factor) {
			continue
		}
		if l.From.Lat == 0 && l.From.Lon == 0 && l.From.Code == "" || l.To.Lat == 0 && l.To.Lon == 0 && l.To.Code == "" {
			continue // a given distance without places can't be routed by rail
		}
		cands = append(cands, j)
	}
	if len(cands) == 0 {
		return railVariant{}, 0, nil, false
	}
	var notes []string
	terminalNote := ""
	try := func(js []int) (railVariant, bool) {
		rs := s
		rs.Legs = append([]calc.Leg(nil), s.Legs...)
		cc := c
		cc.NearestTerminal = ch.Terminal != TerminalBestTrip
		ids := map[string]bool{}
		for _, j := range js {
			l := s.Legs[j]
			if cc.RoadVehicle == "" {
				cc.RoadVehicle = l.VehicleType
			}
			rs.Legs[j] = calc.Leg{ID: l.ID, Mode: calc.Rail, VehicleType: ch.RailVehicle, From: l.From, To: l.To, AtTerminals: true}
			ids[l.ID] = true
		}
		r, err := cc.Calculate(ctx, rs)
		if err != nil {
			return railVariant{}, false
		}
		for _, j := range js {
			for _, lr := range r.Legs {
				if lr.LegID != s.Legs[j].ID || lr.Mode != calc.Rail || lr.From == nil || lr.To == nil {
					continue
				}
				if km := distance.Haversine(*lr.From, *lr.To); km <= *ch.MinRailKm {
					if !hasNote(notes, s.Legs[j].ID) {
						notes = append(notes, fmt.Sprintf("leg %s stays on road: the rail part would be %.0f km in a straight line, not more than %.0f km",
							s.Legs[j].ID, km, *ch.MinRailKm))
					}
					return railVariant{}, false
				}
			}
		}
		// The terminal handlings rail adds (calc.TerminalHandlings), as hubs.
		if extra := calc.TerminalHandlings(r, ids) - calc.TerminalHandlings(before, nil); extra > 0 {
			n, _, err := cc.Containers(s, before.WeightKg)
			if err != nil {
				terminalNote = fmt.Sprintf("terminal handlings at the rail terminals not counted: %v", err)
			} else {
				rs.Hubs = append([]calc.HubStop(nil), s.Hubs...)
				for k := range extra {
					rs.Hubs = append(rs.Hubs, calc.HubStop{ID: fmt.Sprintf("added-terminal-%d", k+1), HubType: calc.IntermodalTerminal, Containers: n})
				}
			}
		}
		return railVariant{rs, cc}, true
	}
	if v, ok := try(cands); ok {
		if terminalNote != "" {
			notes = append(notes, terminalNote)
		}
		return v, len(cands), notes, true
	}
	// Some legs can't go by rail: keep the ones that can.
	var good []int
	for _, j := range cands {
		if _, ok := try([]int{j}); ok {
			good = append(good, j)
		} else if !hasNote(notes, s.Legs[j].ID) {
			notes = append(notes, fmt.Sprintf("leg %s stays on road: no rail route", s.Legs[j].ID))
		}
	}
	if len(good) == 0 {
		return railVariant{}, 0, notes, false
	}
	v, ok := try(good)
	if ok && terminalNote != "" {
		notes = append(notes, terminalNote)
	}
	return v, len(good), notes, ok
}

func hasNote(notes []string, id string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, "leg "+id+" ") {
			return true
		}
	}
	return false
}
