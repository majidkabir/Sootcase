package calc

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Air pollutants from fuel burnt in the vehicle, by the GLEC Framework v3.3
// Section 3 Module 6 (Air Pollutants Methodology for the Logistics Sector,
// pp. 171-178): Tier 1, emissions = fuel burnt (kg) × an emission factor per
// kg of that fuel (Equations 3, 7, 11). Fuel production, tyre and brake wear
// are outside its scope (Figure 1), so electric vehicles emit none here.
//
// The fuel burnt is the leg's own measured fuel, or, for a default value,
// its TTW divided by the TTW per kg of the fuel behind the value (Module 6
// Table 2: fuel intensity = emission intensity ÷ the fuel's GHG factor).
// Only what a value's id or text says is used: a value that doesn't say
// which fuel it is for (container trade lanes, own intensities, ...) gets
// no figure, with the reason.

// AirPollutants are exhaust air pollutants in kg. A field is left out
// (nil) when the method gives no factor for it for that fuel.
type AirPollutants struct {
	NOxKg  *float64 `json:"nox_kg,omitempty"`
	SO2Kg  *float64 `json:"so2_kg,omitempty"`
	PM10Kg *float64 `json:"pm10_kg,omitempty"`
	PM25Kg *float64 `json:"pm2_5_kg,omitempty"`
	BCKg   *float64 `json:"bc_kg,omitempty"`
	COKg   *float64 `json:"co_kg,omitempty"`
}

// LegAirPollutants are a leg's air pollutants: from its fuel burnt
// (FuelKg, Fuel names the factors' fuel and vehicle category), or none
// burnt (electric), and NotKnown says what of the leg has no figure.
type LegAirPollutants struct {
	AirPollutants
	FuelKg   float64  `json:"fuel_kg,omitempty"`
	Fuel     string   `json:"fuel,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	NotKnown string   `json:"not_known,omitempty"`
	// missing are the pollutants some part of the leg has no figure for.
	missing map[string]bool
}

// AirPollutantTotals are a shipment's air pollutants: each one's sum over
// the legs that have it. Partial names the pollutants some leg (or part of
// one) has no figure for, so their sums are too low; NotKnown says which
// legs and why. Hubs are outside Module 6's scope.
type AirPollutantTotals struct {
	AirPollutants
	Method   string   `json:"method"`
	Partial  []string `json:"partial,omitempty"`
	NotKnown []string `json:"not_known,omitempty"`
}

// AirPollutantsMethod names the method on every result.
const AirPollutantsMethod = "GLEC Framework v3.3 Section 3 Module 6, Tier 1: fuel burnt in the vehicle × emission factor per kg of fuel " +
	"(exhaust only: no fuel production, tyre or brake wear)"

// pollutantNames are AirPollutants' JSON names, in order.
var pollutantNames = []string{"nox_kg", "so2_kg", "pm10_kg", "pm2_5_kg", "bc_kg", "co_kg"}

// airFactor is a fuel's Tier 1 factors in kg of pollutant per kg of fuel;
// a negative value is one the source gives none for.
type airFactor struct {
	name                         string
	nox, so2, pm10, pm25, bc, co float64
	source                       string
}

const (
	srcGLECM6 = "GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 3 Module 6"
	srcSFCAP  = "Smart Freight Centre and Stockholm Environment Institute, Air Pollutant Emissions Methodology for the Logistics Sector (October 2025)"
	srcEMEP   = "EMEP/EEA air pollutant emission inventory guidebook 2023 (European Environment Agency, CC BY)"
)

// SO2 from fuel sulphur: 2 × sulphur mass fraction (SO2 is twice
// sulphur's mass): SFC/SEI Equation 10 with its Table 6 (from EMEP/EEA)
// for road fuels of 2009 and later, EMEP/EEA 2023 1.A.3.c equation 2 with
// its typical diesel for rail.
const (
	roadDieselS = 3e-6    // 3 ppm
	roadPetrolS = 5e-6    // 5 ppm
	railDieselS = 0.00005 // 0.005 % by mass
)

var airFactors = map[string]airFactor{
	"road/hdv/diesel": {"heavy-duty vehicle, diesel", 0.02595, 2 * roadDieselS, 0.00055, 0.00055, 0.0002915, 0.0061,
		srcGLECM6 + ", Table 8 (p. 176), from EMEP/EEA (2019); SO2: " + srcSFCAP + ", Equation 10 and Table 6 (diesel 3 ppm sulphur, 2009 and later)"},
	"road/lcv/diesel": {"light commercial vehicle, diesel", 0.01348, 2 * roadDieselS, 0.00122, 0.00122, 0.000671, 0.00681,
		srcSFCAP + ", Table 4, from EMEP/EEA (2019); SO2: Equation 10 and Table 6 (diesel 3 ppm sulphur, 2009 and later)"},
	"road/lcv/petrol": {"light commercial vehicle, petrol", 0.00593, 2 * roadPetrolS, 0.00002, 0.00002, 0.000001, 0.1187,
		srcSFCAP + ", Table 4, from EMEP/EEA (2019); SO2: Equation 10 and Table 6 (petrol 5 ppm sulphur, 2009 and later)"},
	"rail/diesel": {"locomotive, diesel (gas oil)", 0.0524, 2 * railDieselS, 0.00144, 0.00137, 0.0008905, 0.0107,
		srcGLECM6 + ", Table 6 (p. 175), for PM10, PM2.5 and BC (<PM2.5), from EMEP/EEA (2019); NOx and CO: " + srcEMEP +
			", 1.A.3.c Railways, Table 3-1; SO2: its equation 2 with its typical diesel, 0.005 % sulphur"},
	"ship/hfo": {"ship, bunker fuel oil", 0.0691, 0.0192, 0.0052, -1, 0.00009, 0.00367,
		srcSFCAP + ", Table 14, from EMEP/EEA (2023); not differentiated by bunker fuel type (HSFO, VLSFO, ULSFO); no PM2.5 factor"},
	"ship/mgo": {"ship, marine diesel oil / marine gas oil", 0.0722, 0.00182, 0.00107, -1, 0.00004, 0.00384,
		srcGLECM6 + ", Table 10 (p. 177), from EMEP/EEA (2019); no PM2.5 factor"},
	"ship/lng": {"ship, LNG", 0.00492, 0, 0.00000124, 0.00000106, 0.0000000249, 0.0138,
		srcSFCAP + ", Table 14, from EMEP/EEA (2023)"},
}

// airPollutantsOf sets the air pollutants of a result's legs (and their
// alternatives) and its totals.
func (c Calculator) airPollutantsOf(res *Result) {
	t := &AirPollutantTotals{Method: AirPollutantsMethod}
	partial := map[string]bool{}
	for i := range res.Legs {
		l := &res.Legs[i]
		l.AirPollutants = c.legAirPollutants(*l)
		for k := range l.Alternatives {
			l.Alternatives[k].AirPollutants = c.legAirPollutants(l.Alternatives[k])
		}
		a := l.AirPollutants
		name := l.LegID
		if name == "" {
			name = fmt.Sprintf("leg %d", i+1)
		}
		if a.NotKnown != "" {
			t.NotKnown = append(t.NotKnown, name+": "+a.NotKnown)
		}
		for _, p := range pollutantNames {
			v := a.get(p)
			if v == nil || a.missing[p] {
				partial[p] = true
			}
			if v != nil {
				t.add(p, *v)
			}
		}
	}
	t.Partial = PollutantsIn(partial)
	res.AirPollutants = t
}

func (a *AirPollutants) ptr(p string) **float64 {
	switch p {
	case "nox_kg":
		return &a.NOxKg
	case "so2_kg":
		return &a.SO2Kg
	case "pm10_kg":
		return &a.PM10Kg
	case "pm2_5_kg":
		return &a.PM25Kg
	case "bc_kg":
		return &a.BCKg
	}
	return &a.COKg
}

func (a *AirPollutants) get(p string) *float64 { return *a.ptr(p) }

// Add adds b's pollutants to a (each one that b has).
func (a *AirPollutants) Add(b AirPollutants) {
	for _, p := range pollutantNames {
		if v := b.get(p); v != nil {
			a.add(p, *v)
		}
	}
}

// PollutantsIn are the pollutant names in set, in their usual order.
func PollutantsIn(set map[string]bool) []string {
	var out []string
	for _, p := range pollutantNames {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

func (a *AirPollutants) add(p string, v float64) {
	q := a.ptr(p)
	if *q == nil {
		*q = new(float64)
	}
	**q += v
}

// portion is a part of a leg with one way of calculating: the leg's main
// part or one of its LegParts.
type portion struct {
	what     string // "", "the ferry crossing", "the electrified track"
	mode     Mode
	method   string
	factor   *Factor
	fuel     *FuelFactor
	vehicle  string
	ttw      float64
	electric bool
}

// legAirPollutants are a leg's air pollutants, from its main part and its
// parts (ferry crossings, electrified track).
func (c Calculator) legAirPollutants(l LegResult) *LegAirPollutants {
	main := portion{mode: l.Mode, method: l.Method, factor: l.Factor, fuel: l.FuelFactor, vehicle: l.VehicleType, ttw: l.TTWKg}
	main.electric = ElectricLeg(l)
	ps := []portion{}
	for _, p := range l.Parts {
		main.ttw -= p.TTWKg
		what := "the ferry crossing"
		if p.Kind != PartFerry {
			what = "the electrified track"
		}
		ps = append(ps, portion{what: what, mode: p.Mode, method: p.Method, factor: p.Factor, ttw: p.TTWKg,
			electric: p.Kind == PartElectrified || p.Method == MethodEnergy})
	}
	ps = append([]portion{main}, ps...)
	out := &LegAirPollutants{missing: map[string]bool{}}
	var notKnown, fuels []string
	srcs := map[string]bool{}
	for _, p := range ps {
		if p.ttw < 1e-12 && p.ttw > -1e-12 {
			p.ttw = 0
		}
		if p.electric || p.ttw == 0 && electricPortion(p) {
			for _, n := range pollutantNames {
				out.AirPollutants.add(n, 0) // nothing burnt in the vehicle
			}
			fuels = append(fuels, "none burnt (electric)")
			continue
		}
		key, kg, why := c.fuelBurnt(p)
		if why != "" {
			if p.what != "" {
				why = p.what + ": " + why
			}
			notKnown = append(notKnown, why)
			for _, n := range pollutantNames {
				out.missing[n] = true
			}
			continue
		}
		f := airFactors[key]
		out.FuelKg += kg
		fuels = append(fuels, f.name)
		srcs[f.source] = true
		for i, v := range []float64{f.nox, f.so2, f.pm10, f.pm25, f.bc, f.co} {
			if v >= 0 {
				out.AirPollutants.add(pollutantNames[i], kg*v)
			} else {
				out.missing[pollutantNames[i]] = true
			}
		}
		if f.pm25 < 0 {
			notKnown = append(notKnown, "no PM2.5 factor for "+f.name)
		}
	}
	out.Fuel = strings.Join(dedupe(fuels), "; ")
	for s := range srcs {
		out.Sources = append(out.Sources, s)
	}
	sort.Strings(out.Sources)
	out.NotKnown = strings.Join(notKnown, "; ")
	return out
}

func dedupe(s []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// electricPortion says whether a portion with no TTW is on an electric
// value (no fuel burnt), as the GHG scopes read it.
func electricPortion(p portion) bool {
	if p.method == MethodEnergy {
		return true
	}
	if p.fuel != nil {
		return ElectricFuel(*p.fuel)
	}
	return p.factor != nil && electricFactor(p.factor.VehicleType)
}

// fuelBurnt is the air factor key and kg of fuel burnt for a portion, or
// why there is none.
func (c Calculator) fuelBurnt(p portion) (string, float64, string) {
	switch {
	case p.mode == Air:
		return "", 0, "GLEC Module 6 and the EMEP/EEA guidebook give no Tier 1 factor per kg of jet fuel (only for aviation gasoline)"
	case p.fuel != nil:
		return c.measuredFuel(p)
	case p.factor == nil:
		return "", 0, "no emission factor to read the fuel from"
	}
	f := *p.factor
	if f.VehicleType == "own" || f.DataType != "" && f.DataType != DataDefault && !strings.HasPrefix(f.Source, "load model (") {
		return "", 0, "an own or operator intensity doesn't say which fuel was burnt"
	}
	carrier := FactorCarrier(f)
	mixed := false
	if carrier == "" && f.Mode == Rail && strings.HasPrefix(f.VehicleType, "glec/rail/") && p.ttw > 0 {
		// GLEC rail values of unknown or mixed traction: their TTW is all
		// from diesel trains (electric trains burn nothing).
		carrier, mixed = CarrierDiesel, true
	}
	if carrier == "" && strings.HasPrefix(f.Source, "load model (") {
		carrier = FuelCarrier(f.VehicleType) // DESNZ's load models are diesel lorries
	}
	if carrier == "" {
		return "", 0, fmt.Sprintf("the emission factor %s doesn't say which fuel it is for", f.VehicleType)
	}
	key, why := airKey(p.mode, carrier, p.vehicle, f.VehicleType)
	if why != "" {
		return "", 0, why
	}
	name := ""
	switch {
	case strings.HasPrefix(f.VehicleType, "glec/"):
		name = GLECFuelFor(carrier, f.VehicleType, f.Mode)
	case strings.Contains(f.Source, "DESNZ"):
		name = DESNZFuelFor(carrier)
	}
	if name == "" {
		return "", 0, fmt.Sprintf("no %s fuel factor to go with %s", carrier, f.VehicleType)
	}
	perKg, ff, err := c.ttwPerKg(name)
	if err != nil {
		return "", 0, err.Error()
	}
	if !mixed {
		// The fuel factor must be the one behind the value (as for iLEAP's
		// energy carriers): their TTW shares of WTW agree within rounding.
		if a, b := shareOf(f.TTW, f.WTW()), shareOf(ff.TTW, ff.WTW()); math.Abs(a-b) > 0.03 {
			return "", 0, fmt.Sprintf("%s's TTW is %.1f%% of its WTW, but %s's is %.1f%%, so that isn't the fuel behind it", f.VehicleType, 100*a, name, 100*b)
		}
	}
	return key, p.ttw / perKg, ""
}

func shareOf(ttw, wtw float64) float64 {
	if wtw <= 0 {
		return 0
	}
	return ttw / wtw
}

// measuredFuel is a measured-fuel portion's fuel burnt.
func (c Calculator) measuredFuel(p portion) (string, float64, string) {
	ff := *p.fuel
	carrier := FuelCarrier(ff.Fuel)
	if carrier == "" {
		return "", 0, fmt.Sprintf("fuel %q isn't one the air pollutant factors are for", ff.Fuel)
	}
	key, why := airKey(p.mode, carrier, p.vehicle, "")
	if why != "" {
		return "", 0, why
	}
	if !(ff.TTW > 0) {
		return "", 0, fmt.Sprintf("fuel %q has no TTW factor to read the amount burnt from", ff.Fuel)
	}
	amount := p.ttw / ff.TTW // in the factor's unit (the shipment's share)
	switch strings.ToLower(strings.TrimSpace(ff.Unit)) {
	case "kg":
		return key, amount, ""
	case "t", "tonne", "tonnes":
		return key, amount * 1000, ""
	case "l", "litre", "litres", "liter", "liters":
		// GLEC Section 3 Module 1's densities (Module 6, Table 2): litres
		// of this fuel to kg, from its per-litre and per-kg factors.
		d, err := c.density(carrier)
		if err != nil {
			return "", 0, err.Error()
		}
		return key, amount * d, ""
	}
	return "", 0, fmt.Sprintf("fuel %q is per %s; the factors are per kg of fuel", ff.Fuel, ff.Unit)
}

// airKey is the factor key for a fuel on a mode and vehicle, or why there
// is none.
func airKey(mode Mode, carrier, vehicle, factorID string) (string, string) {
	switch mode {
	case Road:
		cat := "hdv"
		id := strings.ToLower(vehicle + " " + factorID)
		switch {
		case strings.Contains(id, "van"):
			cat = "lcv"
		case vehicle == "" && factorID == "":
			return "", "the vehicle (van or truck) isn't given, and the factors differ by it"
		}
		switch carrier {
		case CarrierDiesel:
			return "road/" + cat + "/diesel", ""
		case CarrierPetrol:
			if cat == "lcv" {
				return "road/lcv/petrol", ""
			}
		}
		return "", fmt.Sprintf("Module 6 gives no road factor for %s %s", map[string]string{"hdv": "trucks on", "lcv": "vans on"}[cat], strings.ToLower(carrier))
	case Rail:
		if carrier == CarrierDiesel {
			return "rail/diesel", ""
		}
	case Sea, InlandWaterway:
		switch carrier {
		case CarrierHFO:
			return "ship/hfo", ""
		case CarrierMGO, CarrierDiesel:
			return "ship/mgo", "" // gas oil/diesel (Module 6 Table 10, for sea and inland waterways alike)
		case CarrierLNG:
			return "ship/lng", ""
		}
	}
	return "", fmt.Sprintf("Module 6 gives no %s factor for %s", mode, strings.ToLower(carrier))
}

// ttwPerKg is a fuel's TTW per kg, from its own factor or its per-kg (or
// per-tonne) sibling: GLEC "…/l" → "…/kg", DESNZ "…/litres" → "…/tonnes".
func (c Calculator) ttwPerKg(id string) (float64, FuelFactor, error) {
	ff, err := c.Factors.FuelFactor(id)
	if err != nil {
		return 0, FuelFactor{}, err
	}
	if per, ok := perKg(ff); ok {
		return per, ff, nil
	}
	for _, s := range [][2]string{{"/l", "/kg"}, {"/litres", "/tonnes"}} {
		if base, ok := strings.CutSuffix(id, s[0]); ok {
			if sib, err := c.Factors.FuelFactor(base + s[1]); err == nil {
				if per, ok := perKg(sib); ok {
					return per, ff, nil
				}
			}
		}
	}
	return 0, FuelFactor{}, fmt.Errorf("no per-kg factor for fuel %s", id)
}

func perKg(ff FuelFactor) (float64, bool) {
	switch strings.ToLower(ff.Unit) {
	case "kg":
		return ff.TTW, ff.TTW > 0
	case "t", "tonne", "tonnes":
		return ff.TTW / 1000, ff.TTW > 0
	}
	return 0, false
}

// density is GLEC Module 1's kg per litre of a fuel (European values):
// its TTW per litre ÷ per kg.
func (c Calculator) density(carrier string) (float64, error) {
	name := map[string]string{CarrierDiesel: "glec/fuel/eu/diesel", CarrierPetrol: "glec/fuel/eu/gasoline"}[carrier]
	if name == "" {
		return 0, fmt.Errorf("no GLEC density for %s given in litres", strings.ToLower(carrier))
	}
	l, err1 := c.Factors.FuelFactor(name + "/l")
	kg, err2 := c.Factors.FuelFactor(name + "/kg")
	if err1 != nil || err2 != nil || !(kg.TTW > 0) {
		return 0, fmt.Errorf("no GLEC density for %s given in litres", strings.ToLower(carrier))
	}
	return l.TTW / kg.TTW, nil
}
