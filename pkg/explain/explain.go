// Package explain writes out how a shipment's emissions were calculated,
// step by step, with the numbers used and where each step comes from in
// the GLEC Framework v3.2 and ISO 14083:2023, so that someone who knows
// these can check every figure by hand.
//
// The ISO 14083 clause numbers are those the GLEC Framework v3.2 itself
// lists in its "ISO References" boxes; page numbers are the GLEC PDF's.
package explain

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Sheet is a shipment's calculation, step by step.
type Sheet struct {
	ShipmentID string   `json:"shipment_id,omitempty"`
	Summary    string   `json:"summary"`
	Steps      []Step   `json:"steps"`
	References []string `json:"references"`
	Note       string   `json:"note"`
}

// Step is one part of the calculation.
type Step struct {
	Group   string   `json:"group"`             // "Cargo", "Leg 1 (road)", "Hub h1", "Total"
	Title   string   `json:"title"`             // what this step establishes
	Text    string   `json:"text"`              // how, in words
	Formula string   `json:"formula,omitempty"` // the arithmetic, with the numbers used
	Basis   []string `json:"basis,omitempty"`   // where it comes from
}

// References, as cited in steps.
const (
	GLEC = "GLEC Framework v3.2 (Smart Freight Centre, 2025)"
	ISO  = "ISO 14083:2023, Greenhouse gases: quantification and reporting of emissions arising from transport chain operations"
)

// Basis strings.
const (
	bChain     = "ISO 14083 3.1.25 transport chain, 3.1.26 transport chain element (GLEC p.16)"
	bMass      = "ISO 14083 5.4.2 freight mass (GLEC p.21)"
	bTEU       = "GLEC pp.21, 41, 51: converting TEU to tonnes when the mass isn't known (10 t average, 6 t light, 14.5 t heavy)"
	bDistance  = "ISO 14083 3.1.27 transport distance, 3.3.4 (GLEC pp.21–22): shortest feasible distance (SFD) or great-circle distance (GCD)"
	bChainDist = "ISO 14083 3.3.4 (GLEC p.22): a transport chain's distance runs from where the shipper hands the goods over to the consignee"
	bDAF       = "GLEC p.22: distance adjustment factor (DAF) between distance types"
	bDAFSea    = "GLEC p.51 (sea): DAF 1.15 on the shortest feasible distance (Clean Cargo)"
	bDAFAir    = "GLEC p.38 (air): great-circle distance + 95 km"
	bActivity  = "GLEC Section 1 Ch.2, pp.21–22 (Figure 2): transport activity = mass × distance, in tonne-km"
	bDefault   = "ISO 14083 7.2.5 selection of a value from a database of default values (GLEC p.29)"
	bModel     = "ISO 14083 7.2.4 calculation with a model (GLEC p.29)"
	bPrimary   = "ISO 14083 7.2.3 calculation with primary data (GLEC p.29)"
	bDataCats  = "ISO 14083 3.3.3 data categories: primary, modelled, default (GLEC p.25)"
	bLoad      = "GLEC Module 2, p.97: load factors and empty running"
	bFuelEx    = "GLEC Module 4, p.116: worked example, fuel used × fuel emission factor"
	bElec      = "ISO 14083 Annex J, GHG emission factors, including electricity (GLEC p.19)"
	bTCE       = "ISO 14083 clause 10, calculation of GHG emissions for a transport TCE (GLEC p.27)"
	bHubTCE    = "ISO 14083 clause 11, calculation of GHG emissions for a hub TCE (GLEC p.27); 9.3, 9.5 (GLEC pp.33–34)"
	bWTW       = "ISO 14083 5.2 system boundaries (GLEC p.19); GLEC p.10 (Figure 4) and p.57: well-to-wheel = tank-to-wheel (operational) + well-to-tank (energy provision)"
	bResults   = "ISO 14083 clause 12.1, results for one transport chain (GLEC p.27)"
)

// Shipment explains the result r of shipment s, calculated with factors f
// (for the details of load models and DAFs; may be nil).
func Shipment(s calc.Shipment, r calc.Result, f calc.FactorProvider) Sheet {
	sh := Sheet{ShipmentID: r.ShipmentID, References: []string{GLEC, ISO}}
	t := r.WeightKg / 1000
	sh.Summary = fmt.Sprintf("%s kg CO₂e well-to-wheel for %s t over %s tonne-km (%s legs%s), dataset %s.",
		n(r.WTWKg, 1), n(t, 3), n(r.TonneKm, 1), strconv.Itoa(len(r.Legs)), hubsText(len(r.Hubs)), orDash(r.Dataset))

	// Cargo.
	cargo := Step{Group: "Cargo", Title: "Mass of the goods", Basis: []string{bMass}}
	if r.WeightBasis == "teu" {
		perTEU := 0.0
		if s.TEU > 0 {
			perTEU = r.WeightKg / 1000 / s.TEU
		}
		cargo.Text = "Given in containers: TEU times net cargo tonnes per TEU" + loadProfile(s, r) + ". Container tare is not added."
		cargo.Formula = fmt.Sprintf("mass = %s TEU × %s t/TEU = %s t", n(s.TEU, 2), n(perTEU, 3), n(t, 3))
		cargo.Basis = append(cargo.Basis, bTEU)
	} else {
		cargo.Text = "Given as the cargo's weight."
		cargo.Formula = fmt.Sprintf("mass = %s kg = %s t", n(r.WeightKg, 1), n(t, 3))
	}
	sh.Steps = append(sh.Steps, cargo)

	given := map[string]calc.Leg{}
	for _, l := range s.Legs {
		given[l.ID] = l
	}
	for i, l := range r.Legs {
		in := calc.Leg{}
		if l.Added == "" {
			in = given[l.LegID]
		}
		sh.Steps = append(sh.Steps, legSteps(i, l, in, t, f)...)
	}
	for _, h := range r.Hubs {
		sh.Steps = append(sh.Steps, Step{
			Group: "Hub " + h.HubID, Title: "Hub emissions (" + h.HubType + ")",
			Text: fmt.Sprintf("Mass handled times the hub operation's intensity per tonne, well-to-wheel only (%s data). Source: %s.",
				h.DataType, h.Source),
			Formula: fmt.Sprintf("WTW = %s t × %s g/t ÷ 1,000 = %s kg", n(t, 3), n(div(h.WTWKg*1e6, t*1000), 2), n(h.WTWKg, 3)),
			Basis:   []string{bHubTCE, bDataCats},
		})
	}

	// Total.
	var parts []string
	for _, l := range r.Legs {
		parts = append(parts, n(l.WTWKg, 3))
	}
	for _, h := range r.Hubs {
		parts = append(parts, n(h.WTWKg, 3))
	}
	total := Step{Group: "Total", Title: "Emissions of the transport chain",
		Text: "The sum over all transport chain elements (legs and hubs). Intensity is transport only: legs' WTW over their tonne-km.",
		Formula: fmt.Sprintf("WTW = %s = %s kg CO₂e\nTTW = %s kg, WTT = %s kg\nintensity = %s kg × 1,000 ÷ %s tkm = %s g CO₂e/tkm",
			strings.Join(parts, " + "), n(r.WTWKg, 3), n(r.TTWKg, 3), n(r.WTTKg, 3),
			n(transportWTW(r), 3), n(r.TonneKm, 1), n(r.IntensityG, 2)),
		Basis: []string{bResults, bWTW},
	}
	sh.Steps = append(sh.Steps, total)
	sh.Steps = append(sh.Steps, Step{Group: "Total", Title: "Data quality",
		Text: fmt.Sprintf("%s%% of the well-to-wheel emissions come from primary data (measured fuel or the operator's own intensities); "+
			"the rest from modelled data (load model, electricity use) or default values (published average intensities), as marked on each leg.",
			n(r.PrimaryDataShare*100, 1)),
		Basis: []string{bDataCats}})
	for _, w := range r.Warnings {
		sh.Steps = append(sh.Steps, Step{Group: "Total", Title: "Warning", Text: w})
	}
	sh.Note = "Sootcase calculates with full precision; the numbers here are rounded for reading, so redoing a step by hand " +
		"can differ in the last digit. Factors and their sources are those of the dataset named above."
	return sh
}

func legSteps(i int, l calc.LegResult, in calc.Leg, t float64, f calc.FactorProvider) []Step {
	group := fmt.Sprintf("Leg %d (%s)", i+1, modeName(l.Mode))
	if l.Added != "" {
		group = fmt.Sprintf("Leg %d (road, added)", i+1)
	}
	var steps []Step

	// Distance.
	d := Step{Group: group, Title: "Distance", Basis: []string{bDistance}}
	d.Text = distanceText(l)
	if l.Note != "" {
		d.Text += " " + l.Note
		d.Basis = append(d.Basis, bChainDist, bChain)
	}
	d.Formula = fmt.Sprintf("distance = %s km", n(l.DistanceKm, 1))
	if l.From != nil && l.To != nil {
		d.Formula = fmt.Sprintf("%s → %s: %s km", place(*l.From), place(*l.To), n(l.DistanceKm, 1))
	}
	steps = append(steps, d)

	// Transport activity.
	steps = append(steps, Step{Group: group, Title: "Transport activity",
		Text:    "The mass carried times the distance.",
		Formula: fmt.Sprintf("activity = %s t × %s km = %s tkm", n(t, 3), n(l.DistanceKm, 1), n(l.TonneKm, 1)),
		Basis:   []string{bActivity}})

	// The part of the leg calculated with its own method (the rest of the
	// leg when parts were split off).
	mainKm, mainTTW, mainWTW := l.DistanceKm, l.TTWKg, l.WTWKg
	for _, p := range l.Parts {
		mainKm -= p.DistanceKm
		mainTTW -= p.TTWKg
		mainWTW -= p.WTWKg
	}
	mainTkm := t * mainKm
	if len(l.Parts) > 0 {
		var names []string
		for _, p := range l.Parts {
			names = append(names, fmt.Sprintf("%s km %s", n(p.DistanceKm, 1), partName(p.Kind)))
		}
		steps = append(steps, Step{Group: group, Title: "Parts calculated separately",
			Text: "The route includes " + strings.Join(names, " and ") + ", calculated with their own factors below; " +
				"the rest of the leg is calculated with the leg's method.",
			Formula: fmt.Sprintf("rest of the leg = %s km − %s km = %s km", n(l.DistanceKm, 1), n(l.DistanceKm-mainKm, 1), n(mainKm, 1))})
	}

	steps = append(steps, intensitySteps(group, l, in, t, mainKm, mainTkm, mainTTW, mainWTW, f)...)

	for _, p := range l.Parts {
		pf := p.Factor
		st := Step{Group: group, Title: "Emissions of the " + partName(p.Kind) + " part", Basis: []string{bTCE, bWTW}}
		switch {
		case p.Method == calc.MethodEnergy && pf != nil:
			st.Text = fmt.Sprintf("Electrified track by the energy method: %s.", pf.Source)
			st.Formula = fmt.Sprintf("WTT = %s tkm × %s g/tkm ÷ 1,000 = %s kg (TTW = 0)", n(p.TonneKm, 1), n(pf.WTT, 3), n(p.WTWKg, 3))
			st.Basis = append(st.Basis, bModel, bElec)
		case pf != nil:
			st.Text = fmt.Sprintf("Default intensity for %s: %s.", pf.VehicleType, pf.Source)
			adj := p.DistanceKm
			if p.DAF > 0 {
				adj = p.DistanceKm * p.DAF
				st.Text += fmt.Sprintf(" The distance is adjusted by a DAF of %s (%s).", n(p.DAF, 4), p.DAFSource)
			}
			at := t * adj
			st.Formula = fmt.Sprintf("TTW = %s tkm × %s g/tkm ÷ 1,000 = %s kg\nWTT = %s tkm × %s g/tkm ÷ 1,000 = %s kg\nWTW = %s kg",
				n(at, 1), n(pf.TTW, 3), n(p.TTWKg, 3), n(at, 1), n(pf.WTT, 3), n(p.WTTKg, 3), n(p.WTWKg, 3))
			st.Basis = append(st.Basis, bDefault)
		}
		steps = append(steps, st)
	}
	if len(l.Parts) > 0 {
		steps = append(steps, Step{Group: group, Title: "Leg total",
			Formula: fmt.Sprintf("WTW = %s kg (rest) + %s = %s kg", n(mainWTW, 3), partsSum(l.Parts), n(l.WTWKg, 3))})
	}
	return steps
}

// intensitySteps explains the leg's (or its main part's) intensity and
// emissions.
func intensitySteps(group string, l calc.LegResult, in calc.Leg, t, km, tkm, ttwKg, wtwKg float64, f calc.FactorProvider) []Step {
	fac := l.Factor
	wttKg := wtwKg - ttwKg
	emissions := Step{Group: group, Title: "Emissions", Basis: []string{bTCE, bWTW},
		Text: "Transport activity times intensity, for the vehicle itself (tank-to-wheel: operational emissions) and for producing " +
			"its fuel or electricity (well-to-tank: energy provision emissions). Well-to-wheel is their sum."}
	switch l.Method {
	case calc.MethodFuel:
		ff := l.FuelFactor
		if ff == nil {
			return nil
		}
		share := l.AllocationShare
		amount := div(wtwKg, ff.WTW()*share)
		if in.Fuel != nil {
			amount = in.Fuel.Amount
		}
		s := Step{Group: group, Title: "Measured fuel",
			Text: fmt.Sprintf("Fuel actually used on the trip (primary data), times the fuel's emission factor: %s kg CO₂e per %s tank-to-wheel and %s well-to-tank (%s).",
				n(ff.TTW, 4), unit(ff.Unit), n(ff.WTT, 4), ff.Source),
			Basis: []string{bPrimary, bFuelEx, bDataCats}}
		if share < 1 {
			s.Text += fmt.Sprintf(" The trip carried other goods too: this shipment's share is its tonne-km over the trip's (%s).", n(share*100, 2)+"%")
			s.Formula = fmt.Sprintf("share = %s tkm ÷ %s tkm = %s", n(tkm, 1), n(div(tkm, share), 1), n(share, 4))
		}
		emissions.Text = "Fuel used times its emission factor, for burning it (tank-to-wheel) and producing it (well-to-tank), " +
			"times this shipment's share of the trip."
		emissions.Formula = fmt.Sprintf("TTW = %s %s × %s kg/%s × %s = %s kg\nWTT = %s %s × %s kg/%s × %s = %s kg\nWTW = %s kg",
			n(amount, 3), ff.Unit, n(ff.TTW, 4), unit(ff.Unit), n(share, 4), n(ttwKg, 3),
			n(amount, 3), ff.Unit, n(ff.WTT, 4), unit(ff.Unit), n(share, 4), n(wttKg, 3), n(wtwKg, 3))
		if l.BiogenicCO2Kg > 0 {
			emissions.Text += fmt.Sprintf(" Biogenic CO₂ from burning the fuel's biological part, %s kg per %s, is reported separately and "+
				"not included (GLEC p.64).", n(ff.BiogenicCO2, 4), unit(ff.Unit))
			emissions.Formula += fmt.Sprintf("\nbiogenic CO₂ (separate) = %s %s × %s kg/%s × %s = %s kg",
				n(amount, 3), ff.Unit, n(ff.BiogenicCO2, 4), unit(ff.Unit), n(share, 4), n(l.BiogenicCO2Kg, 3))
		}
		return []Step{s, emissions}

	case calc.MethodEnergy:
		if fac == nil {
			return nil
		}
		grid := div(fac.WTT, l.EnergyKWhPerTkm)
		s := Step{Group: group, Title: "Electricity",
			Text: fmt.Sprintf("Electric vehicle: nothing is emitted at the vehicle (TTW = 0); generating the electricity is the energy provision (WTT). "+
				"Electricity per tonne-km as given, times the grid intensity of %s: %s.", l.Country, fac.Source),
			Formula: fmt.Sprintf("intensity = %s kWh/tkm × %s g/kWh = %s g CO₂e/tkm", n(l.EnergyKWhPerTkm, 4), n(grid, 1), n(fac.WTT, 3)),
			Basis:   []string{bModel, bElec, bDataCats}}
		emissions.Formula = fmt.Sprintf("WTT = %s tkm × %s g/tkm ÷ 1,000 = %s kg\nTTW = 0, WTW = %s kg", n(tkm, 1), n(fac.WTT, 3), n(wttKg, 3), n(wtwKg, 3))
		return []Step{s, emissions}

	case calc.MethodLoadModel:
		if fac == nil {
			return nil
		}
		s := Step{Group: group, Title: "Intensity from the load model",
			Text: fmt.Sprintf("The vehicle's emissions per vehicle-km depend on its load; they are interpolated between the dataset's values "+
				"empty, half and fully laden, and spread over the tonnes carried, with empty running added. Load factor %s of the model's "+
				"%s t payload%s, %s empty km per loaded km. Source: %s.",
				n(l.LoadFactor, 4), n(l.PayloadT, 2), loadTonnesText(l), n(l.EmptyTripFactor, 3), fac.Source),
			Basis: []string{bModel, bLoad, bDataCats}}
		if lmp, ok := f.(calc.LoadModelProvider); ok && f != nil {
			if m, err := lmp.LoadModel(l.Mode, l.VehicleType); err == nil {
				et, ew := m.At(l.LoadFactor)
				zt, zw := m.At(0)
				load := l.PayloadT * l.LoadFactor
				s.Formula = fmt.Sprintf("per vehicle-km at load %s: TTW %s g, WTT %s g; empty: TTW %s g, WTT %s g\n"+
					"TTW = (%s + %s × %s) ÷ (%s t) = %s g CO₂e/tkm\nWTT = (%s + %s × %s) ÷ (%s t) = %s g CO₂e/tkm",
					n(l.LoadFactor, 4), n(et, 2), n(ew, 2), n(zt, 2), n(zw, 2),
					n(et, 2), n(l.EmptyTripFactor, 3), n(zt, 2), n(load, 3), n(fac.TTW, 3),
					n(ew, 2), n(l.EmptyTripFactor, 3), n(zw, 2), n(load, 3), n(fac.WTT, 3))
			}
		}
		emissions.Formula = fmt.Sprintf("TTW = %s tkm × %s g/tkm ÷ 1,000 = %s kg\nWTT = %s tkm × %s g/tkm ÷ 1,000 = %s kg\nWTW = %s kg",
			n(tkm, 1), n(fac.TTW, 3), n(ttwKg, 3), n(tkm, 1), n(fac.WTT, 3), n(wttKg, 3), n(wtwKg, 3))
		return []Step{s, emissions}

	default: // intensity
		if fac == nil {
			return nil
		}
		basis := []string{bDefault, bDataCats}
		kind := "Default value"
		if l.DataType == calc.DataPrimary {
			basis, kind = []string{bPrimary, bDataCats}, "The operator's own intensity (primary data)"
		}
		s := Step{Group: group, Title: "Emission intensity",
			Text: strings.TrimSpace(fmt.Sprintf("%s %s for %s: %s. %s", l.FactorNote, kind, fac.VehicleType, fac.Source, basisText(fac.DistanceBasis))),
			Formula: fmt.Sprintf("intensity = %s g TTW + %s g WTT = %s g CO₂e/tkm (well-to-wheel)",
				n(fac.TTW, 3), n(fac.WTT, 3), n(fac.WTW(), 3)),
			Basis: basis}
		steps := []Step{s}
		adjKm := km
		if l.DAF > 0 {
			adjKm = km * l.DAF
			ds := Step{Group: group, Title: "Distance adjustment",
				Text: fmt.Sprintf("The distance is %s but the intensity is per %s tonne-km, so the distance is adjusted: %s.",
					distBasis(l.DistanceType), distBasis(fac.DistanceBasis), l.DAFSource),
				Basis: []string{bDAF}}
			switch l.Mode {
			case calc.Sea:
				ds.Basis = append(ds.Basis, bDAFSea)
			case calc.Air:
				ds.Basis = append(ds.Basis, bDAFAir)
			}
			rule := dafRule(f, l)
			ds.Formula = fmt.Sprintf("adjusted distance = %s km %s = %s km", n(km, 1), rule, n(adjKm, 1))
			steps = append(steps, ds)
		}
		emissions.Formula = fmt.Sprintf("TTW = %s t × %s km × %s g/tkm ÷ 1,000 = %s kg\nWTT = %s t × %s km × %s g/tkm ÷ 1,000 = %s kg\nWTW = %s kg",
			n(t, 3), n(adjKm, 1), n(fac.TTW, 3), n(ttwKg, 3), n(t, 3), n(adjKm, 1), n(fac.WTT, 3), n(wttKg, 3), n(wtwKg, 3))
		return append(steps, emissions)
	}
}

// dafRule writes the DAF as the dataset defines it ("× 1.15", "+ 95 km"),
// or as the ratio applied.
func dafRule(f calc.FactorProvider, l calc.LegResult) string {
	if p, ok := f.(calc.DAFProvider); ok && f != nil && l.Factor != nil {
		if d, ok := p.DAF(l.Mode, l.DistanceType, l.Factor.DistanceBasis); ok {
			var r []string
			if d.Multiplier != 0 && d.Multiplier != 1 {
				r = append(r, "× "+n(d.Multiplier, 4))
			}
			if d.AddKm != 0 {
				r = append(r, fmt.Sprintf("%+g km", d.AddKm))
			}
			if len(r) > 0 {
				return strings.Join(r, " ")
			}
		}
	}
	return "× " + n(l.DAF, 4)
}

func distanceText(l calc.LegResult) string {
	typ := map[string]string{calc.DistSFD: "a shortest feasible distance (SFD)", calc.DistGCD: "a great-circle distance (GCD)",
		calc.DistActual: "the actual distance"}[l.DistanceType]
	switch l.DistanceMethod {
	case calc.DistanceGiven:
		if typ == "" {
			return "Given with the shipment; its type (SFD, GCD or actual) was not stated."
		}
		return "Given with the shipment, as " + typ + "."
	case "osrm":
		return "Routed on the road network (OpenStreetMap, with OSRM): " + orType(typ) + "."
	case "osrm_rail":
		return "Routed on the rail network (OpenStreetMap railways, with OSRM): " + orType(typ) + "."
	case "osrm_waterway":
		return "Routed on navigable inland waterways (OpenStreetMap, with OSRM): " + orType(typ) + "."
	case "sea_network":
		return "Routed on the maritime network (Eurostat SeaRoute) from port to port: " + orType(typ) + "."
	case "great_circle":
		return "The great-circle distance between the two points: this mode isn't routed."
	case "great_circle_adjusted":
		return "The great-circle distance between the two points with the dataset's adjustment for this mode (for flights, GLEC p.38: + 95 km)."
	}
	return "From " + l.DistanceMethod + "."
}

func orType(t string) string {
	if t == "" {
		return "type not stated"
	}
	return t
}

func distBasis(b string) string {
	switch b {
	case calc.DistSFD:
		return "shortest feasible distance"
	case calc.DistGCD:
		return "great-circle distance"
	case calc.DistActual:
		return "actual distance"
	}
	return "unstated distance"
}

func basisText(b string) string {
	if b == "" {
		return "The source doesn't say which distance type its tonne-km are based on, so no distance adjustment is made."
	}
	return "Per " + distBasis(b) + " tonne-km."
}

func unit(u string) string { return strings.TrimSuffix(u, "s") }

func loadProfile(s calc.Shipment, r calc.Result) string {
	switch {
	case s.ContainerLoad != "":
		return fmt.Sprintf(" (the dataset's %q container load profile)", s.ContainerLoad)
	case r.ContainerLoad != "":
		return fmt.Sprintf(" (not given, so the dataset's %q profile: GLEC's average when the mass is known only in TEU)", r.ContainerLoad)
	}
	return ""
}

func loadTonnesText(l calc.LegResult) string {
	if l.LoadTonnes > 0 {
		return fmt.Sprintf(" (%s t on the vehicle, as given)", n(l.LoadTonnes, 3))
	}
	return ""
}

func partName(k string) string {
	switch k {
	case calc.PartFerry:
		return "ferry"
	case calc.PartElectrified:
		return "electrified track"
	}
	return k
}

func partsSum(ps []calc.LegPart) string {
	var s []string
	for _, p := range ps {
		s = append(s, n(p.WTWKg, 3)+" kg ("+partName(p.Kind)+")")
	}
	return strings.Join(s, " + ")
}

func modeName(m calc.Mode) string {
	if m == calc.InlandWaterway {
		return "inland waterway"
	}
	return string(m)
}

func place(l calc.Location) string {
	if l.Name != "" {
		return l.Name
	}
	if l.Code != "" {
		return l.Code
	}
	return fmt.Sprintf("%.4f, %.4f", l.Lat, l.Lon)
}

func hubsText(h int) string {
	if h == 0 {
		return ""
	}
	return fmt.Sprintf(", %d hubs", h)
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func transportWTW(r calc.Result) float64 {
	v := r.WTWKg
	for _, h := range r.Hubs {
		v -= h.WTWKg
	}
	return v
}

func div(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// n formats v with dec decimals and thousands separators.
func n(v float64, dec int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "–"
	}
	s := strconv.FormatFloat(v, 'f', dec, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}
