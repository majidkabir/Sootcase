// Package report aggregates shipment results into an emissions report with
// the basic content ISO 14083:2023 asks of a report at the level of
// transport or hub services (see GLEC Framework v3.2, Section 2 Chapter 1):
//
//   - the transport chains covered and the period;
//   - total GHG emissions, split into operational (TTW) and energy
//     provision (WTT) emissions, and the total emission intensity, stating
//     the type of distance used;
//   - totals and intensities per mode of transport and per hub operation;
//   - a breakdown by energy carrier;
//   - the share of primary, modelled and default data, the sources of
//     default values and the models and parameters used;
//   - omissions and deviations, and a reference to supporting information.
//
// A Builder takes results one at a time, so reports over millions of
// shipments need no more memory than one shipment.
package report

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// ISOStatement is the sentence ISO 14083 requires in a report that claims
// conformity. Sootcase adds it only when the reporter declares it.
const ISOStatement = "These calculation results have been established in accordance with ISO 14083:2023."

// MethodStatement is used when conformity is not declared.
const MethodStatement = "Calculated with Sootcase following the GLEC Framework v3.2 and ISO 14083:2023 methodology. " +
	"Conformity with ISO 14083 is not declared by this report."

// Meta describes the report: who, what and when. All fields are optional.
type Meta struct {
	Title        string `json:"title,omitempty"`
	Organisation string `json:"organisation,omitempty"`
	// PeriodFrom and PeriodTo are dates (YYYY-MM-DD) bounding the services
	// covered.
	PeriodFrom string `json:"period_from,omitempty"`
	PeriodTo   string `json:"period_to,omitempty"`
	// Reference says where supporting information can be found.
	Reference string `json:"reference,omitempty"`
	// DeclareISO adds the ISO 14083 conformity statement. Only the reporter
	// can make that claim: it covers data collection and boundaries that
	// Sootcase cannot see.
	DeclareISO bool `json:"declare_iso,omitempty"`
}

// Report is the finished report. Emissions are kg CO2e, intensities g CO2e
// per tonne-km (transport) or per tonne (hubs).
type Report struct {
	Title        string    `json:"title"`
	Organisation string    `json:"organisation,omitempty"`
	Period       Period    `json:"period"`
	Level        string    `json:"level"`
	Statement    string    `json:"statement"`
	Reference    string    `json:"reference"`
	Datasets     []string  `json:"datasets"`
	Generated    time.Time `json:"generated"`

	Coverage Coverage `json:"coverage"`
	Total    Total    `json:"total"`
	Modes    []Mode   `json:"modes"`
	Hubs     []Hub    `json:"hubs,omitempty"`
	Energy   []Energy `json:"energy_carriers"`
	Data     Data     `json:"data_quality"`
	DAFs     []DAF    `json:"distance_adjustments,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// Period covered by the report.
type Period struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Coverage identifies the transport chains covered.
type Coverage struct {
	Shipments  int      `json:"shipments"`
	Calculated int      `json:"calculated"`
	Failed     int      `json:"failed"`
	FailedIDs  []string `json:"failed_ids,omitempty"` // first MaxListed
	// Failures says why the first MaxListed failed shipments failed, and
	// FailureReasons counts all failures by reason.
	Failures       []Failure     `json:"failures,omitempty"`
	FailureReasons []ReasonCount `json:"failure_reasons,omitempty"`
	// RetryableFailed counts the failures that weren't the input's fault (a
	// routing service didn't answer, after retries): run them again.
	RetryableFailed int     `json:"retryable_failed,omitempty"`
	Legs            int     `json:"legs"`
	HubStops        int     `json:"hub_stops"`
	WeightT         float64 `json:"weight_t"`
	// AddedRoadLegs are road legs the calculator added (included in Legs
	// and in all totals): where legs didn't join up, or to and from the port
	// or terminal a leg off its network was moved to.
	AddedRoadLegs  int     `json:"added_road_legs,omitempty"`
	AddedRoadKm    float64 `json:"added_road_km,omitempty"`
	AddedShipments int     `json:"added_road_leg_shipments,omitempty"`
	// Warned counts the shipments with warnings, and Warnings the warnings
	// by kind (calc.Result.Warnings).
	Warned   int           `json:"warned_shipments,omitempty"`
	Warnings []ReasonCount `json:"warnings,omitempty"`
}

// MaxListed caps the failed shipment ids listed in a report.
const MaxListed = 100

// Failure is one shipment that couldn't be calculated.
type Failure struct {
	ShipmentID string `json:"shipment_id"`
	Error      string `json:"error"`
	Retryable  bool   `json:"retryable,omitempty"`
}

// ReasonCount is how many shipments failed for one reason.
type ReasonCount struct {
	Reason    string `json:"reason"`
	Shipments int    `json:"shipments"`
	Retryable bool   `json:"retryable,omitempty"` // failures only: see Coverage.RetryableFailed
}

// reason turns an error into a reason shared by similar failures: numbers
// (distances, leg numbers, coordinates) become "#".
func reason(e string) string {
	var b strings.Builder
	num := false
	for _, r := range e {
		if (r >= '0' && r <= '9') || (num && (r == '.' || r == ',')) {
			if !num {
				b.WriteByte('#')
				num = true
			}
			continue
		}
		num = false
		b.WriteRune(r)
	}
	return b.String()
}

// warningKind turns a warning into its kind: without the leg ids, places
// and numbers.
func warningKind(w string) string {
	switch {
	case strings.Contains(w, "isn't counted (adding road legs is off)"):
		return "legs don't join up; the distance between them isn't counted (adding road legs is off)"
	case strings.Contains(w, "counted as sea distance"):
		return "a sea leg starts or ends inland; that part is counted as sea distance"
	case strings.Contains(w, "couldn't be calculated, so it isn't counted"):
		return "a road leg to add couldn't be calculated, so it isn't counted"
	}
	return reason(w)
}

// Total is the whole covered transport chain.
type Total struct {
	WTWKg          float64 `json:"wtw_kg"`
	TransportWTWKg float64 `json:"transport_wtw_kg"`
	// TTWKg (operational) and WTTKg (energy provision) cover transport;
	// hubs are reported WTW only, see Notes.
	TTWKg      float64 `json:"ttw_kg"`
	WTTKg      float64 `json:"wtt_kg"`
	HubWTWKg   float64 `json:"hub_wtw_kg"`
	TonneKm    float64 `json:"tonne_km"`
	IntensityG float64 `json:"intensity_g_per_tkm"` // transport WTW
	// BiogenicCO2Kg is reported separately and not included above (GLEC
	// Framework v3.2 p. 64); only measured fuel with a biogenic factor.
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
	// DistanceTypes is the share of tonne-km per distance type (sfd, gcd,
	// actual, or "unspecified").
	DistanceTypes map[string]float64 `json:"distance_types"`
}

// Mode is one mode of transport.
type Mode struct {
	Mode          calc.Mode          `json:"mode"`
	Legs          int                `json:"legs"`
	TonneKm       float64            `json:"tonne_km"`
	TTWKg         float64            `json:"ttw_kg"`
	WTTKg         float64            `json:"wtt_kg"`
	WTWKg         float64            `json:"wtw_kg"`
	IntensityG    float64            `json:"intensity_g_per_tkm"`
	DistanceTypes map[string]float64 `json:"distance_types"`
}

// Hub is one hub operation type.
type Hub struct {
	HubType     string  `json:"hub_type"`
	Stops       int     `json:"stops"`
	Tonnes      float64 `json:"tonnes"`
	WTWKg       float64 `json:"wtw_kg"`
	IntensityGT float64 `json:"intensity_g_per_t"`
}

// Energy is one energy carrier. Legs calculated from a published
// intensity or load model don't state their carrier.
type Energy struct {
	Carrier string  `json:"carrier"`
	TTWKg   float64 `json:"ttw_kg"`
	WTTKg   float64 `json:"wtt_kg"`
	WTWKg   float64 `json:"wtw_kg"`
	Share   float64 `json:"share"` // of transport WTW
}

// Unspecified labels a missing energy carrier or distance type.
const Unspecified = "unspecified"

// Data is the data quality section.
type Data struct {
	// Shares of total WTW (transport and hubs) by data type.
	Primary  float64 `json:"primary_share"`
	Modelled float64 `json:"modelled_share"`
	Default  float64 `json:"default_share"`
	// Sources of default values, by WTW.
	DefaultSources []Source `json:"default_sources,omitempty"`
	Models         []Model  `json:"models,omitempty"`
}

// Source is a default data source.
type Source struct {
	Source string  `json:"source"`
	WTWKg  float64 `json:"wtw_kg"`
	Share  float64 `json:"share"` // of total WTW
}

// Model is a model used, with its parameters.
type Model struct {
	Model      string            `json:"model"`
	Legs       int               `json:"legs"`
	WTWKg      float64           `json:"wtw_kg"`
	Parameters map[string]string `json:"parameters"`
}

// DAF is a distance adjustment factor applied.
type DAF struct {
	Source string  `json:"source"`
	Legs   int     `json:"legs"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// Builder accumulates results. It is not safe for concurrent use.
type Builder struct {
	meta      Meta
	now       func() time.Time
	cov       Coverage
	tot       Total
	modes     map[calc.Mode]*Mode
	modeDist  map[calc.Mode]map[string]float64
	dist      map[string]float64
	hubs      map[string]*Hub
	energy    map[string]*Energy
	byType    map[string]float64
	sources   map[string]float64
	models    map[string]*modelAcc
	reasons   map[string]int  // failures by reason
	retryable map[string]bool // reasons that are temporary
	warnings  map[string]int  // warnings by kind
	dafs      map[string]*DAF
	datasets  map[string]bool
}

type modelAcc struct {
	legs              int
	wtw, tkm          float64
	lfTkm, etfTkm     float64 // tonne-km weighted sums
	kwhTkm            float64
	countries         map[string]bool
	vehicles          map[string]bool
	lfMin, lfMax      float64
	etfMin, etfMax    float64
	kwhMin, kwhMax    float64
	firstLF, firstKWh bool
}

// New starts a report.
func New(m Meta) *Builder {
	return &Builder{meta: m, now: time.Now,
		modes: map[calc.Mode]*Mode{}, modeDist: map[calc.Mode]map[string]float64{}, dist: map[string]float64{},
		hubs: map[string]*Hub{}, energy: map[string]*Energy{}, byType: map[string]float64{},
		sources: map[string]float64{}, models: map[string]*modelAcc{}, dafs: map[string]*DAF{},
		datasets: map[string]bool{}}
}

// Add records one shipment's result, or its error.
func (b *Builder) Add(id string, r calc.Result, err error) {
	b.cov.Shipments++
	if err != nil {
		b.cov.Failed++
		retry := calc.Temporary(err)
		if retry {
			b.cov.RetryableFailed++
		}
		if len(b.cov.FailedIDs) < MaxListed {
			b.cov.FailedIDs = append(b.cov.FailedIDs, id)
			b.cov.Failures = append(b.cov.Failures, Failure{ShipmentID: id, Error: err.Error(), Retryable: retry})
		}
		if b.reasons == nil {
			b.reasons = map[string]int{}
		}
		b.reasons[reason(err.Error())]++
		if retry {
			if b.retryable == nil {
				b.retryable = map[string]bool{}
			}
			b.retryable[reason(err.Error())] = true
		}
		return
	}
	b.cov.Calculated++
	b.cov.WeightT += r.WeightKg / 1000
	added := false
	for _, l := range r.Legs {
		if l.Added != "" {
			b.cov.AddedRoadLegs++
			b.cov.AddedRoadKm += l.DistanceKm
			added = true
		}
	}
	if added {
		b.cov.AddedShipments++
	}
	if len(r.Warnings) > 0 {
		b.cov.Warned++
		if b.warnings == nil {
			b.warnings = map[string]int{}
		}
		for _, w := range r.Warnings {
			b.warnings[warningKind(w)]++
		}
	}
	if r.Dataset != "" {
		b.datasets[r.Dataset] = true
	}
	for _, l := range r.Legs {
		b.leg(l)
	}
	for _, h := range r.Hubs {
		b.cov.HubStops++
		hb := b.hubs[h.HubType]
		if hb == nil {
			hb = &Hub{HubType: h.HubType}
			b.hubs[h.HubType] = hb
		}
		hb.Stops++
		hb.Tonnes += r.WeightKg / 1000
		hb.WTWKg += h.WTWKg
		b.tot.HubWTWKg += h.WTWKg
		b.byType[h.DataType] += h.WTWKg
		if h.DataType == calc.DataDefault {
			b.sources[h.Source] += h.WTWKg
		}
	}
}

// leg adds a leg: the parts calculated separately (a ferry crossing, the
// electrified track) under their own mode, energy and data type, and the
// rest under the leg's.
func (b *Builder) leg(l calc.LegResult) {
	b.cov.Legs++
	main := l
	main.Parts = nil
	for _, p := range l.Parts {
		main.TonneKm -= p.TonneKm
		main.TTWKg -= p.TTWKg
		main.WTTKg -= p.WTTKg
		main.WTWKg -= p.WTWKg
		b.element(calc.LegResult{Mode: p.Mode, VehicleType: l.VehicleType, DistanceType: l.DistanceType,
			TonneKm: p.TonneKm, TTWKg: p.TTWKg, WTTKg: p.WTTKg, WTWKg: p.WTWKg, Method: p.Method,
			DataType: p.DataType, Factor: p.Factor, DAF: p.DAF, DAFSource: p.DAFSource,
			EnergyKWhPerTkm: p.EnergyKWhPerTkm, Country: p.Country})
	}
	b.element(main)
}

// element adds one leg, or one part of a leg.
func (b *Builder) element(l calc.LegResult) {
	m := b.modes[l.Mode]
	if m == nil {
		m = &Mode{Mode: l.Mode}
		b.modes[l.Mode] = m
		b.modeDist[l.Mode] = map[string]float64{}
	}
	m.Legs++
	m.TonneKm += l.TonneKm
	m.TTWKg += l.TTWKg
	m.WTTKg += l.WTTKg
	m.WTWKg += l.WTWKg
	dt := l.DistanceType
	if dt == "" {
		dt = Unspecified
	}
	b.modeDist[l.Mode][dt] += l.TonneKm
	b.dist[dt] += l.TonneKm

	b.tot.TonneKm += l.TonneKm
	b.tot.TTWKg += l.TTWKg
	b.tot.WTTKg += l.WTTKg
	b.tot.TransportWTWKg += l.WTWKg
	b.tot.BiogenicCO2Kg += l.BiogenicCO2Kg

	carrier := Unspecified
	switch {
	case l.FuelFactor != nil:
		carrier = l.FuelFactor.Fuel
	case l.Country != "":
		carrier = "electricity/" + l.Country
	}
	e := b.energy[carrier]
	if e == nil {
		e = &Energy{Carrier: carrier}
		b.energy[carrier] = e
	}
	e.TTWKg += l.TTWKg
	e.WTTKg += l.WTTKg
	e.WTWKg += l.WTWKg

	b.byType[l.DataType] += l.WTWKg
	if l.DataType == calc.DataDefault && l.Factor != nil {
		b.sources[sourceOf(l.Factor.Source, l.Factor.Year)] += l.WTWKg
	}
	if l.Method == calc.MethodLoadModel || l.Method == calc.MethodEnergy {
		b.model(l)
	}
	if l.DAF != 0 {
		d := b.dafs[l.DAFSource]
		if d == nil {
			d = &DAF{Source: l.DAFSource, Min: l.DAF, Max: l.DAF}
			b.dafs[l.DAFSource] = d
		}
		d.Legs++
		d.Min, d.Max = min(d.Min, l.DAF), max(d.Max, l.DAF)
	}
}

func sourceOf(src string, year int) string {
	if year > 0 && !strings.Contains(src, strconv.Itoa(year)) {
		return src + " (" + strconv.Itoa(year) + ")"
	}
	return src
}

func (b *Builder) model(l calc.LegResult) {
	a := b.models[l.Method]
	if a == nil {
		a = &modelAcc{countries: map[string]bool{}, vehicles: map[string]bool{}}
		b.models[l.Method] = a
	}
	a.legs++
	a.wtw += l.WTWKg
	a.tkm += l.TonneKm
	if l.VehicleType != "" {
		a.vehicles[l.VehicleType] = true
	}
	switch l.Method {
	case calc.MethodLoadModel:
		a.lfTkm += l.LoadFactor * l.TonneKm
		a.etfTkm += l.EmptyTripFactor * l.TonneKm
		if !a.firstLF {
			a.firstLF = true
			a.lfMin, a.lfMax, a.etfMin, a.etfMax = l.LoadFactor, l.LoadFactor, l.EmptyTripFactor, l.EmptyTripFactor
		}
		a.lfMin, a.lfMax = min(a.lfMin, l.LoadFactor), max(a.lfMax, l.LoadFactor)
		a.etfMin, a.etfMax = min(a.etfMin, l.EmptyTripFactor), max(a.etfMax, l.EmptyTripFactor)
	case calc.MethodEnergy:
		a.kwhTkm += l.EnergyKWhPerTkm * l.TonneKm
		a.countries[l.Country] = true
		if !a.firstKWh {
			a.firstKWh = true
			a.kwhMin, a.kwhMax = l.EnergyKWhPerTkm, l.EnergyKWhPerTkm
		}
		a.kwhMin, a.kwhMax = min(a.kwhMin, l.EnergyKWhPerTkm), max(a.kwhMax, l.EnergyKWhPerTkm)
	}
}

// Report returns the report for everything added so far.
func (b *Builder) Report() Report {
	b.cov.FailureReasons = nil
	for k, n := range b.reasons {
		b.cov.FailureReasons = append(b.cov.FailureReasons, ReasonCount{Reason: k, Shipments: n, Retryable: b.retryable[k]})
	}
	sort.Slice(b.cov.FailureReasons, func(i, j int) bool {
		x, y := b.cov.FailureReasons[i], b.cov.FailureReasons[j]
		if x.Shipments != y.Shipments {
			return x.Shipments > y.Shipments
		}
		return x.Reason < y.Reason
	})
	if len(b.cov.FailureReasons) > 20 {
		b.cov.FailureReasons = b.cov.FailureReasons[:20]
	}
	b.cov.Warnings = nil
	for k, n := range b.warnings {
		b.cov.Warnings = append(b.cov.Warnings, ReasonCount{Reason: k, Shipments: n})
	}
	sort.Slice(b.cov.Warnings, func(i, j int) bool {
		x, y := b.cov.Warnings[i], b.cov.Warnings[j]
		if x.Shipments != y.Shipments {
			return x.Shipments > y.Shipments
		}
		return x.Reason < y.Reason
	})
	r := Report{
		Title:        b.meta.Title,
		Organisation: b.meta.Organisation,
		Period:       Period{From: b.meta.PeriodFrom, To: b.meta.PeriodTo},
		Level:        "transport_or_hub_services",
		Statement:    MethodStatement,
		Reference:    b.meta.Reference,
		Generated:    b.now().UTC().Truncate(time.Second),
		Coverage:     b.cov,
		Total:        b.tot,
	}
	if r.Title == "" {
		r.Title = "Freight GHG emissions report"
	}
	if b.meta.DeclareISO {
		r.Statement = ISOStatement
	}
	if r.Reference == "" {
		r.Reference = "Per-shipment results with factors, sources and distances: Sootcase batch output for the same input. Method: https://github.com/majidkabir/sootcase"
	}
	r.Datasets = sortedKeys(b.datasets)

	t := &r.Total
	t.WTWKg = t.TransportWTWKg + t.HubWTWKg
	t.IntensityG = div(t.TransportWTWKg*1000, t.TonneKm)
	t.DistanceTypes = shares(b.dist, t.TonneKm)

	for _, m := range b.modes {
		m.IntensityG = div(m.WTWKg*1000, m.TonneKm)
		m.DistanceTypes = shares(b.modeDist[m.Mode], m.TonneKm)
		r.Modes = append(r.Modes, *m)
	}
	sort.Slice(r.Modes, func(i, j int) bool { return r.Modes[i].WTWKg > r.Modes[j].WTWKg })

	for _, h := range b.hubs {
		h.IntensityGT = div(h.WTWKg*1000, h.Tonnes)
		r.Hubs = append(r.Hubs, *h)
	}
	sort.Slice(r.Hubs, func(i, j int) bool { return r.Hubs[i].WTWKg > r.Hubs[j].WTWKg })

	for _, e := range b.energy {
		e.Share = div(e.WTWKg, t.TransportWTWKg)
		r.Energy = append(r.Energy, *e)
	}
	sort.Slice(r.Energy, func(i, j int) bool { return r.Energy[i].WTWKg > r.Energy[j].WTWKg })

	r.Data = Data{
		Primary:  div(b.byType[calc.DataPrimary], t.WTWKg),
		Modelled: div(b.byType[calc.DataModelled], t.WTWKg),
		Default:  div(b.byType[calc.DataDefault], t.WTWKg),
	}
	for s, kg := range b.sources {
		r.Data.DefaultSources = append(r.Data.DefaultSources, Source{Source: s, WTWKg: kg, Share: div(kg, t.WTWKg)})
	}
	sort.Slice(r.Data.DefaultSources, func(i, j int) bool {
		return r.Data.DefaultSources[i].WTWKg > r.Data.DefaultSources[j].WTWKg
	})
	for _, name := range sortedKeys(boolKeys(b.models)) {
		r.Data.Models = append(r.Data.Models, b.modelReport(name))
	}

	for _, d := range b.dafs {
		r.DAFs = append(r.DAFs, *d)
	}
	sort.Slice(r.DAFs, func(i, j int) bool { return r.DAFs[i].Legs > r.DAFs[j].Legs })

	r.Notes = b.notes(r)
	return r
}

func (b *Builder) modelReport(name string) Model {
	a := b.models[name]
	m := Model{Legs: a.legs, WTWKg: a.wtw, Parameters: map[string]string{}}
	vehicles := strings.Join(sortedKeys(a.vehicles), ", ")
	switch name {
	case calc.MethodLoadModel:
		m.Model = "Activity-based load model: per vehicle-km emissions interpolated between 0%, 50% and 100% laden, " +
			"payload derived from the dataset (see the dataset's load_models)"
		m.Parameters["vehicle_classes"] = vehicles
		m.Parameters["load_factor"] = rangeAvg(a.lfMin, a.lfMax, div(a.lfTkm, a.tkm))
		m.Parameters["empty_trip_factor"] = rangeAvg(a.etfMin, a.etfMax, div(a.etfTkm, a.tkm))
		m.Parameters["load_factor_source"] = "given per leg (primary)"
		m.Parameters["vehicle_and_energy_profile"] = "from the dataset (default)"
	case calc.MethodEnergy:
		m.Model = "Energy-based model: electricity per tonne-km times the country's grid intensity"
		m.Parameters["vehicle_classes"] = vehicles
		m.Parameters["energy_kwh_per_tkm"] = rangeAvg(a.kwhMin, a.kwhMax, div(a.kwhTkm, a.tkm))
		m.Parameters["countries"] = strings.Join(sortedKeys(a.countries), ", ")
		m.Parameters["grid_intensity_source"] = "dataset grid table (default)"
	default:
		m.Model = name
	}
	return m
}

func (b *Builder) notes(r Report) []string {
	var n []string
	if r.Coverage.Failed > 0 {
		s := strconv.Itoa(r.Coverage.Failed) + " of " + strconv.Itoa(r.Coverage.Shipments) +
			" shipments could not be calculated and are excluded from all totals"
		if len(r.Coverage.FailedIDs) > 0 && len(r.Coverage.FailedIDs) <= 10 {
			s += " (" + strings.Join(r.Coverage.FailedIDs, ", ") + ")"
		}
		var why []string
		for i, c := range r.Coverage.FailureReasons {
			if i == 5 {
				break
			}
			why = append(why, strconv.Itoa(c.Shipments)+" × "+c.Reason)
		}
		if len(why) > 0 {
			s += ". Reasons: " + strings.Join(why, "; ")
		}
		n = append(n, s+".")
		if k := r.Coverage.RetryableFailed; k > 0 {
			n = append(n, fmt.Sprintf("%s for a temporary reason (a routing service didn't answer, also when tried again), "+
				"not because of the data: calculating them again later should work.", countOf(k, "shipment failed", "shipments failed")))
		}
	}
	if c := r.Coverage; c.AddedRoadLegs > 0 {
		n = append(n, fmt.Sprintf("Road legs added by the calculator: %d (%s km) on %s, where the legs given didn't join up, "+
			"or a sea, rail or waterway leg started or ended off its network and was moved to a nearby port or terminal. "+
			"They are included in all totals.", c.AddedRoadLegs, strconv.FormatFloat(math.Round(c.AddedRoadKm), 'f', 0, 64),
			countOf(c.AddedShipments, "shipment", "shipments")))
	}
	if c := r.Coverage; c.Warned > 0 {
		var what []string
		for i, w := range c.Warnings {
			if i == 5 {
				break
			}
			what = append(what, strconv.Itoa(w.Shipments)+" × "+w.Reason)
		}
		n = append(n, fmt.Sprintf("%s warnings that may make the results too low: %s.",
			countOf(c.Warned, "shipment has", "shipments have"), strings.Join(what, "; ")))
	}
	if r.Total.HubWTWKg > 0 {
		n = append(n, "Hub emissions are reported well-to-wheel only: the hub factors used carry no split "+
			"into operational and energy provision emissions. Operational (TTW) and energy provision (WTT) "+
			"totals therefore cover transport only.")
	}
	if u := r.Total.DistanceTypes[Unspecified]; u > 0 {
		n = append(n, "For "+pct(u)+" of tonne-km the type of distance was not stated "+
			"(distances given without distance_type).")
	}
	if len(r.Total.DistanceTypes) > 1 {
		n = append(n, "Tonne-km combine more than one type of distance; see the shares per mode.")
	}
	if len(r.DAFs) > 0 {
		n = append(n, "Where a leg's distance type differed from the basis of its emission intensity, "+
			"a distance adjustment factor was applied; these are listed with their sources.")
	}
	if r.Data.Default > 0 {
		n = append(n, "Default emission intensities are used where no primary data (measured fuel or operation "+
			"category values) was supplied for a leg or hub; their sources are listed.")
	}
	return n
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func shares(m map[string]float64, total float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = div(v, total)
	}
	return out
}

func div(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolKeys[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + "%" }

// rangeAvg formats a parameter's range and tonne-km weighted average.
func rangeAvg(lo, hi, avg float64) string {
	if lo == hi {
		return fmt.Sprintf("%.3g", lo)
	}
	return fmt.Sprintf("%.3g to %.3g (tonne-km weighted average %.3g)", lo, hi, avg)
}
