package ileap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// FuelLookup gives fuel factors by name (factors.Dataset does).
type FuelLookup interface {
	FuelFactor(name string) (calc.FuelFactor, error)
}

// Options are the details iLEAP asks for that a calculation doesn't
// have: who owns the data, when it was made, and the period it covers.
type Options struct {
	CompanyName string   // mandatory in iLEAP
	CompanyIDs  []string // optional, e.g. "urn:..." ids of the data owner
	CreatedAt   time.Time
	// PeriodStart and PeriodEnd bound the data's reference period
	// (mandatory in iLEAP); zero when not given. PeriodEnd is exclusive,
	// as in iLEAP: the moment after the period.
	PeriodStart, PeriodEnd time.Time
	// Fuels gives the emission factors of a default value's energy
	// carrier (GLEC Module 1, DESNZ fuels). Optional.
	Fuels FuelLookup
	// Country names the ISO 3166 alpha-2 country of a point, "" when it
	// can't. Optional; without it, locations need a UN/LOCODE.
	Country func(calc.Location) string
	// City names the city a point is in or near (ok false when none).
	// Optional; without it, or when it finds none, a location's own name
	// is used as its city.
	City func(calc.Location) (name string, km float64, ok bool)
	// Operator are TOCs and HOCs built from the operator's own data
	// (FromOperations), by id: legs and hubs calculated with them refer
	// to them instead of to a category made from the result.
	Operator map[string]any
}

func (o Options) stamp() (created, start, end string) {
	t := o.CreatedAt
	if t.IsZero() {
		t = time.Now()
	}
	created = t.UTC().Format(time.RFC3339)
	if !o.PeriodStart.IsZero() {
		start = o.PeriodStart.UTC().Format(time.RFC3339)
	}
	if !o.PeriodEnd.IsZero() {
		end = o.PeriodEnd.UTC().Format(time.RFC3339)
	}
	return
}

// builder collects the objects and gaps of one export.
type builder struct {
	opts  Options
	ex    Export
	tocs  map[string]int // id -> index in ex.TOCs
	hocs  map[string]int
	gaps  map[string]bool
	notes map[string]bool
}

func newBuilder(o Options) *builder {
	return &builder{opts: o, ex: Export{SpecVersion: SpecVersion}, tocs: map[string]int{}, hocs: map[string]int{},
		gaps: map[string]bool{}, notes: map[string]bool{}}
}

func (b *builder) gap(object, id, field, reason string) {
	k := object + "\x00" + id + "\x00" + field
	if !b.gaps[k] {
		b.gaps[k] = true
		b.ex.Gaps = append(b.ex.Gaps, Gap{Object: object, ID: id, Field: field, Reason: reason})
	}
}

func (b *builder) note(s string) {
	if !b.notes[s] {
		b.notes[s] = true
		b.ex.Notes = append(b.ex.Notes, s)
	}
}

// common fills the properties ShipmentFootprints, TOCs and HOCs share,
// and records the mandatory ones that are missing.
func (b *builder) common(object, id string) (created, start, end string) {
	created, start, end = b.opts.stamp()
	if b.opts.CompanyName == "" {
		b.gap(object, id, "companyName", "no company name given (the data owner)")
	}
	if start == "" || end == "" {
		b.gap(object, id, "referencePeriodStart/referencePeriodEnd", "no reference period given")
	}
	return
}

func (b *builder) finish() Export {
	sort.SliceStable(b.ex.Gaps, func(i, j int) bool { return b.ex.Gaps[i].Object < b.ex.Gaps[j].Object })
	b.ex.Conformant = len(b.ex.Gaps) == 0
	if b.ex.Conformant {
		b.ex.Conformant = len(Validate(b.ex)) == 0
	}
	return b.ex
}

// FromResults turns calculated shipments into ShipmentFootprints, with
// one TCE per leg and per hub, and the TOCs and HOCs they refer to.
// A TOC made from a result is the intensity the leg was calculated with
// (its WTW and TTW over its tonne-km), so every TCE equals its transport
// activity times its TOC's intensity.
func FromResults(results []calc.Result, opts Options) Export {
	b := newBuilder(opts)
	for _, r := range results {
		b.shipment(r)
		if len(r.Hubs) > 0 {
			b.note("Hub TCEs have distance 0 and transport activity 0: iLEAP makes both mandatory for every TCE, and a hub has neither.")
		}
	}
	return b.finish()
}

func (b *builder) shipment(r calc.Result) {
	created, start, end := b.common("shipment", r.ShipmentID)
	sf := ShipmentFootprint{
		SpecVersion: SpecVersion, CompanyName: b.opts.CompanyName, CompanyIDs: b.opts.CompanyIDs,
		CreatedAt: created, Status: StatusActive, ReferencePeriodStart: start, ReferencePeriodEnd: end,
		Mass: dec(r.WeightKg), ShipmentID: r.ShipmentID,
	}
	if r.ShipmentID == "" {
		b.gap("shipment", "", "shipmentId", "the shipment has no id")
	}
	sources := map[SourceRef]bool{}
	// Legs are in transport order; hub stops aren't placed between them,
	// so the order is only given when there are no hubs.
	// A leg with parts (a ferry on a road route, electrified rail track)
	// becomes a TCE per part; where on the leg a part lies isn't known, so
	// then the order isn't given either.
	ordered := len(r.Hubs) == 0
	var legs []calc.LegResult
	for _, l := range r.Legs {
		main, parts := split(l)
		legs = append(append(legs, main), parts...)
		if len(parts) > 0 {
			ordered = false
		}
	}
	var prev []string
	used := map[string]int{}
	for _, l := range legs {
		id := uniqueID(r.ShipmentID+"/"+l.LegID, used)
		teu := b.containerTEU(r, l)
		tocID, srcs := b.legTOC(l, teu)
		for _, s := range srcs {
			sources[s] = true
		}
		t := TCE{
			TCEID: id, TOCID: tocID, ShipmentID: r.ShipmentID, Mass: dec(r.WeightKg),
			TransportActivity: dec(l.TonneKm), CO2eWTW: dec(l.WTWKg), CO2eTTW: dec(l.TTWKg),
			Distance: b.distance(id, l), Origin: b.location(l.From), Destination: b.location(l.To),
		}
		if teu > 0 {
			t.PackagingOrTrEq, t.PackagingAmount = "Container-TEU", dec(teu)
		}
		if ordered {
			t.PrevTCEIDs = append([]string{}, prev...)
			prev = []string{id}
		}
		sf.TCEs = append(sf.TCEs, t)
	}
	for _, h := range r.Hubs {
		id := uniqueID(r.ShipmentID+"/hub/"+h.HubID, used)
		hocID, srcs := b.hubHOC(r, h)
		for _, s := range srcs {
			sources[s] = true
		}
		t := TCE{
			TCEID: id, HOCID: hocID, ShipmentID: r.ShipmentID, Mass: dec(r.WeightKg),
			Distance: GLECDistance{Actual: "0"}, TransportActivity: "0", CO2eWTW: dec(h.WTWKg),
		}
		if h.Containers > 0 {
			t.PackagingOrTrEq, t.PackagingAmount = "Container-TEU", dec(h.Containers)
		}
		if h.TTWKg != nil {
			t.CO2eTTW = dec(*h.TTWKg)
		} else {
			b.gap("tce", id, "co2eTTW", "the hub's emission intensity ("+h.HubType+", "+sourceName(h.Source)+") is given as WTW only, with no TTW/WTT split")
		}
		sf.TCEs = append(sf.TCEs, t)
	}
	sf.SecondaryEmissionFactorSources = sortedSources(sources)
	if len(sf.TCEs) == 0 {
		b.gap("shipment", r.ShipmentID, "tces", "the shipment has no legs or hubs")
	}
	b.ex.ShipmentFootprints = append(b.ex.ShipmentFootprints, sf)
}

func uniqueID(id string, used map[string]int) string {
	used[id]++
	if n := used[id]; n > 1 {
		return fmt.Sprintf("%s#%d", id, n)
	}
	return id
}

// distance is the leg's distance by its GLEC type: the type given with
// it, or else the type of the factor it was calculated with (Sootcase
// applies no adjustment then, so that is the type it stands for).
func (b *builder) distance(tceID string, l calc.LegResult) GLECDistance {
	t := l.DistanceType
	if t == "" && l.Factor != nil {
		t = l.Factor.DistanceBasis
	}
	km := dec(l.DistanceKm)
	switch t {
	case calc.DistActual:
		return GLECDistance{Actual: km}
	case calc.DistGCD:
		return GLECDistance{GCD: km}
	case calc.DistSFD:
		return GLECDistance{SFD: km}
	}
	b.gap("tce", tceID, "distance", fmt.Sprintf("the leg's distance (%s km) has no distance type (actual, gcd or sfd)", km))
	return GLECDistance{}
}

var locode = regexp.MustCompile(`^[A-Z]{2}[A-Z2-9]{3}$`)

// location is an iLEAP Location when the place has a name and a known
// country; nil otherwise (origin and destination are optional).
func (b *builder) location(p *calc.Location) *Location {
	if p == nil {
		return nil
	}
	city := strings.TrimSpace(p.Name)
	if b.opts.City != nil {
		if c, _, ok := b.opts.City(*p); ok && c != "" {
			city = c
		}
	}
	if city == "" {
		return nil
	}
	l := Location{City: city, Lat: dec(p.Lat), Lng: dec(p.Lon)}
	if code := strings.ToUpper(strings.ReplaceAll(p.Code, " ", "")); locode.MatchString(code) {
		l.LOCODE, l.Country = code, code[:2]
	}
	if l.Country == "" && b.opts.Country != nil {
		l.Country = strings.ToUpper(b.opts.Country(*p))
	}
	if len(l.Country) != 2 {
		return nil
	}
	return &l
}

// split separates a leg's parts (iLEAP has a TCE per mode and factor):
// the leg without its parts, and each part as a leg of its own. A leg's
// totals include its parts.
func split(l calc.LegResult) (calc.LegResult, []calc.LegResult) {
	if len(l.Parts) == 0 {
		return l, nil
	}
	main := l
	main.Parts = nil
	var out []calc.LegResult
	for _, p := range l.Parts {
		main.DistanceKm -= p.DistanceKm
		main.TonneKm -= p.TonneKm
		main.TTWKg -= p.TTWKg
		main.WTTKg -= p.WTTKg
		main.WTWKg -= p.WTWKg
		vt := l.VehicleType
		if p.Factor != nil {
			vt = p.Factor.VehicleType
		}
		out = append(out, calc.LegResult{LegID: l.LegID + "/" + p.Kind, Mode: p.Mode, VehicleType: vt,
			DistanceKm: p.DistanceKm, DistanceType: l.DistanceType, TonneKm: p.TonneKm,
			TTWKg: p.TTWKg, WTTKg: p.WTTKg, WTWKg: p.WTWKg, Method: p.Method, DataType: p.DataType,
			Factor: p.Factor, DAF: p.DAF, DAFSource: p.DAFSource, EnergyKWhPerTkm: p.EnergyKWhPerTkm, Country: p.Country})
	}
	return main, out
}

// containerTEU is the TEU a sea or inland waterway leg carries in
// containers, 0 when it isn't containerised (bulk, tankers, general cargo,
// Ro-Ro): the shipment's TEU when it was given in TEU; else, on a
// container value, its tonnes / 10 t per TEU, the average GLEC uses when
// only one of the two is known (GLEC v3.3, p. 21; the GLEC container
// values are per TEU-km / 10 t).
func (b *builder) containerTEU(r calc.Result, l calc.LegResult) float64 {
	if l.Mode != calc.Sea && l.Mode != calc.InlandWaterway {
		return 0
	}
	if r.TEU > 0 {
		return r.TEU
	}
	if !strings.Contains(strings.ToLower(factorID(l)), "container") {
		return 0
	}
	b.note("Container legs' TOCs are per TEU-km, as iLEAP asks for sea and inland waterways; for shipments given in kg, the TEU is the tonnes / 10 t per TEU (GLEC v3.3's average, p. 21), shown on the TCE as packagingOrTrEqAmount.")
	return r.WeightKg / 1000 / 10
}

// legTOC returns the id of the TOC a leg was calculated with, adding it
// to the export, and the secondary sources it rests on. With teu > 0 the
// TOC is per TEU-km (container legs on sea and inland waterways), so the
// leg's emissions = TEU x distance x intensity; else per tonne-km.
func (b *builder) legTOC(l calc.LegResult, teu float64) (string, []SourceRef) {
	if l.Factor != nil && l.Factor.DataType != "" && l.Factor.DataType != calc.DataDefault {
		if t, ok := b.opts.Operator[l.Factor.VehicleType].(TOC); ok {
			b.addTOC(t)
			return t.TOCID, t.SecondaryEmissionFactorSources
		}
	}
	if l.TonneKm <= 0 {
		id := "sootcase-toc-" + hash(string(l.Mode), l.LegID, "no-activity")
		b.gap("toc", id, "co2eIntensityWTW", "the leg has no transport activity, so no intensity")
		return id, nil
	}
	activity, unitName := l.TonneKm, UnitTkm
	if teu > 0 && l.DistanceKm > 0 {
		activity, unitName = teu*l.DistanceKm, UnitTEUkm
	}
	wtw, ttw := dec(l.WTWKg/activity), dec(l.TTWKg/activity)
	key, desc, comment := l.Method, "", ""
	var srcs []SourceRef
	var ec []EnergyCarrier
	var why string
	switch {
	case l.Method == calc.MethodEnergy && l.EnergyKWhPerTkm > 0 && l.Factor != nil:
		// The energy method: kWh per tonne-km x the country's grid; the
		// vehicle itself emits nothing.
		key += "|" + l.Factor.Source
		desc = fmt.Sprintf("%s, electric (%s kWh/tkm, %s grid)", l.Mode, dec(l.EnergyKWhPerTkm), l.Country)
		comment = "Emission intensity: " + l.Factor.Source
		ec = []EnergyCarrier{{EnergyCarrier: Electric, RelativeShare: "1",
			Feedstocks:        []Feedstock{{Feedstock: FeedstockGrid, RegionProvenance: l.Country}},
			EnergyConsumption: dec(l.EnergyKWhPerTkm), EnergyConsumptionUnit: "kWh",
			EmissionFactorWTW: dec(l.Factor.WTT / l.EnergyKWhPerTkm / 1000), EmissionFactorTTW: "0"}}
		if teu > 0 {
			ec[0].EnergyConsumption = "" // per tonne-km; the TOC is per TEU-km
		}
	case l.FuelFactor != nil:
		key += "|" + l.FuelFactor.Fuel + "|" + l.FuelFactor.Source
		desc = fmt.Sprintf("%s, measured fuel (%s)", l.Mode, l.FuelFactor.Fuel)
		comment = "Intensity of this shipment's share of the measured fuel. Fuel factor: " + l.FuelFactor.Source
		if s, ok := secondary(l.FuelFactor.Source); ok {
			srcs = append(srcs, s)
		}
		c, reason := carrierFromFuel(*l.FuelFactor, 0)
		if reason != "" {
			why = reason
		} else {
			c.RelativeShare = "1"
			ec = []EnergyCarrier{c}
		}
	case l.Factor != nil:
		f := l.Factor
		key += "|" + f.VehicleType + "|" + f.Source
		desc = fmt.Sprintf("%s, %s (%s)", l.Mode, f.VehicleType, dataTypeLabel(f.DataType))
		comment = "Emission intensity: " + f.Source
		if l.DAF > 0 && l.DAF != 1 {
			comment += fmt.Sprintf("; distance adjustment x %s (%s)", dec(l.DAF), l.DAFSource)
		}
		if s, ok := secondary(f.Source); ok {
			srcs = append(srcs, s)
		}
		if f.DataType == "" || f.DataType == calc.DataDefault {
			var fuelSrc string
			ec, why, fuelSrc = b.defaultCarrier(*f)
			if fuelSrc != "" {
				comment += "; energy carrier emission factors: " + fuelSrc
				if s, ok := secondary(fuelSrc); ok && !slices.Contains(srcs, s) {
					srcs = append(srcs, s)
				}
			}
		} else {
			why = "the leg used the operator's own intensity (" + f.VehicleType + "), which doesn't say which energy carriers it is for; give the operator's data (operations) to export its TOC"
		}
	default:
		key += "|" + l.VehicleType
		desc = fmt.Sprintf("%s, %s", l.Mode, l.VehicleType)
		why = "the result doesn't name the leg's emission factor"
	}
	if l.LoadFactor > 0 || l.EmptyTripFactor > 0 || l.AllocationShare > 0 {
		key += fmt.Sprintf("|%g|%g|%g", l.LoadFactor, l.EmptyTripFactor, l.AllocationShare)
	}
	id := "sootcase-toc-" + hash(string(l.Mode), key, string(wtw), string(ttw), unitName)
	if _, ok := b.tocs[id]; ok {
		return id, srcs
	}
	created, start, end := b.common("toc", id)
	t := TOC{
		SpecVersion: SpecVersion, CompanyName: b.opts.CompanyName, CompanyIDs: b.opts.CompanyIDs,
		CreatedAt: created, Status: StatusActive, ReferencePeriodStart: start, ReferencePeriodEnd: end,
		SecondaryEmissionFactorSources: srcs, Comment: comment,
		TOCID: id, Description: desc, Mode: mode(l.Mode), EnergyCarriers: ec,
		CO2eIntensityWTW: wtw, CO2eIntensityTTW: ttw, TransportActivityUnit: unitName,
		TemperatureControl: temperature(l.VehicleType + " " + factorID(l)),
	}
	t.AirShippingOption, t.FlightLength = air(factorID(l))
	if l.LoadFactor > 0 && l.LoadFactor <= 1 {
		t.LoadFactor = dec(l.LoadFactor)
	}
	if l.EmptyTripFactor > 0 {
		// Sootcase's empty trip factor is empty km per loaded km; iLEAP's
		// emptyDistanceFactor is empty km over all km.
		t.EmptyDistanceFactor = dec(l.EmptyTripFactor / (1 + l.EmptyTripFactor))
	}
	if len(ec) == 0 {
		b.gap("toc", id, "energyCarriers", why)
	} else if ec[0].EmissionFactorWTW == "" {
		b.gap("toc", id, "energyCarriers.emissionFactorWTW/TTW", why)
	}
	b.addTOC(t)
	return id, srcs
}

func (b *builder) addTOC(t TOC) {
	if _, ok := b.tocs[t.TOCID]; ok {
		return
	}
	b.tocs[t.TOCID] = len(b.ex.TOCs)
	b.ex.TOCs = append(b.ex.TOCs, t)
}

func (b *builder) addHOC(h HOC) {
	if _, ok := b.hocs[h.HOCID]; ok {
		return
	}
	b.hocs[h.HOCID] = len(b.ex.HOCs)
	b.ex.HOCs = append(b.ex.HOCs, h)
}

// defaultCarrier is the energy carrier of a default (or modelled) value,
// when its id names one, with the emission factors of that fuel from the
// same source family when the dataset has them; why says what's missing.
func (b *builder) defaultCarrier(f calc.Factor) ([]EnergyCarrier, string, string) {
	c := factorCarrier(f)
	if c == "" {
		return nil, fmt.Sprintf("the emission factor %s doesn't say which energy carrier it is for", f.VehicleType), ""
	}
	ec := EnergyCarrier{EnergyCarrier: c, RelativeShare: "1"}
	name := ""
	switch {
	case strings.HasPrefix(f.VehicleType, "glec/"):
		name = glecFuel(c, f.VehicleType, f.Mode)
	case strings.HasPrefix(baseSource(f.Source), "DESNZ"):
		name = desnzFuel(c)
	}
	if name == "" || b.opts.Fuels == nil {
		return []EnergyCarrier{ec}, fmt.Sprintf("no %s fuel factor to go with %s", c, f.VehicleType), ""
	}
	ff, err := b.opts.Fuels.FuelFactor(name)
	if err != nil {
		return []EnergyCarrier{ec}, fmt.Sprintf("fuel factor %s: %v", name, err), ""
	}
	full, reason := carrierFromFuel(ff, 0)
	if reason != "" {
		return []EnergyCarrier{ec}, reason, ""
	}
	full.EnergyCarrier, full.RelativeShare = c, "1"
	return []EnergyCarrier{full}, "", ff.Source
}

// factorCarrier is vehicleCarrier, plus what a GLEC value's own text
// says: GLEC's inland waterway rows (Table 2) and its US rail average are
// for diesel.
func factorCarrier(f calc.Factor) string {
	if c := vehicleCarrier(f.VehicleType, f.Mode); c != "" {
		return c
	}
	src := strings.ToLower(f.Source)
	switch {
	case !strings.HasPrefix(f.Source, "GLEC Framework") || strings.Contains(src, "electric"):
		return ""
	case f.Mode == calc.InlandWaterway && strings.HasPrefix(f.VehicleType, "glec/iww/") && strings.HasSuffix(src, ", diesel"):
		return Diesel
	case f.Mode == calc.Rail && strings.HasPrefix(f.VehicleType, "glec/rail/na/") && strings.Contains(src, "(diesel)"):
		return Diesel
	}
	return ""
}

// hubHOC returns the id of the HOC a hub stop was calculated with,
// adding it to the export.
func (b *builder) hubHOC(r calc.Result, h calc.HubResult) (string, []SourceRef) {
	if h.DataType != "" && h.DataType != calc.DataDefault {
		if o, ok := b.opts.Operator[h.HubType].(HOC); ok {
			b.addHOC(o)
			return o.HOCID, o.SecondaryEmissionFactorSources
		}
	}
	per, unitName := r.WeightKg/1000, UnitTonnes
	if h.Containers > 0 {
		per, unitName = h.Containers, UnitTEU
	}
	var srcs []SourceRef
	if s, ok := secondary(h.Source); ok {
		srcs = append(srcs, s)
	}
	wtw := dec(h.WTWKg / per)
	ttw := Decimal("")
	if h.TTWKg != nil {
		ttw = dec(*h.TTWKg / per)
	}
	id := "sootcase-hoc-" + hash(h.HubType, h.Source, string(wtw), string(ttw), unitName)
	if _, ok := b.hocs[id]; ok {
		return id, srcs
	}
	created, start, end := b.common("hoc", id)
	ht, temp := hubType(h.HubType)
	o := HOC{
		SpecVersion: SpecVersion, CompanyName: b.opts.CompanyName, CompanyIDs: b.opts.CompanyIDs,
		CreatedAt: created, Status: StatusActive, ReferencePeriodStart: start, ReferencePeriodEnd: end,
		SecondaryEmissionFactorSources: srcs, Comment: "Emission intensity: " + h.Source,
		HOCID: id, Description: fmt.Sprintf("%s (%s)", h.HubType, dataTypeLabel(h.DataType)),
		HubType: ht, TemperatureControl: temp,
		CO2eIntensityWTW: wtw, CO2eIntensityTTW: ttw, HubActivityUnit: unitName,
	}
	if ht == "" {
		b.gap("hoc", id, "hubType", "iLEAP has no hub type for "+h.HubType)
	}
	if ttw == "" {
		b.gap("hoc", id, "co2eIntensityTTW", "the hub's emission intensity is given as WTW only, with no TTW/WTT split")
	}
	b.gap("hoc", id, "energyCarriers", "the hub's emission intensity doesn't say which energy carriers it is for")
	b.addHOC(o)
	return id, srcs
}

// hubType maps a Sootcase (GLEC) hub type to iLEAP's HubType and
// temperature control. GLEC's per-tonne hub values have an ambient and a
// mixed (ambient and temperature-controlled) column.
func hubType(t string) (string, string) {
	base, temp := t, ""
	if strings.HasPrefix(t, "glec") || !strings.Contains(t, "/") {
		switch {
		case strings.HasSuffix(t, "_mixed"):
			base, temp = strings.TrimSuffix(t, "_mixed"), TempMixed
		case strings.HasSuffix(t, "_temperature_controlled"):
			base, temp = strings.TrimSuffix(t, "_temperature_controlled"), TempRefrigerated
		}
	}
	ht := map[string]string{
		"transshipment":         "Transshipment",
		"storage_transshipment": "StorageAndTransshipment",
		"warehouse":             "Warehouse",
		"liquid_bulk_terminal":  "LiquidBulkTerminal",
	}[base]
	if ht != "" && temp == "" {
		temp = TempAmbient
	}
	return ht, temp
}

// air reads iLEAP's airShippingOption and flightLength from a GLEC air
// factor id (glec/air/<belly|freighter|unknown>/<short_haul|long_haul>);
// GLEC's short and long haul split at 1,500 km, as iLEAP's do.
func air(id string) (option, length string) {
	p := strings.Split(id, "/")
	if len(p) != 4 || p[0] != "glec" || p[1] != "air" {
		return "", ""
	}
	option = map[string]string{"belly": "belly freight", "freighter": "freighter"}[p[2]]
	length = map[string]string{"short_haul": "short-haul", "long_haul": "long-haul"}[p[3]]
	return option, length
}

func mode(m calc.Mode) string {
	return map[calc.Mode]string{calc.Road: "Road", calc.Rail: "Rail", calc.Sea: "Sea", calc.Air: "Air",
		calc.InlandWaterway: "InlandWaterway"}[m]
}

// temperature is "refrigerated" when a vehicle or factor id says the
// freight is temperature-controlled, "" when it doesn't say.
func temperature(s string) string {
	s = strings.ToLower(s)
	if strings.Contains(s, "non_refrigerated") {
		return ""
	}
	for _, w := range []string{"reefer", "refrigerated", "temperature_controlled"} {
		if strings.Contains(s, w) {
			return TempRefrigerated
		}
	}
	return ""
}

func factorID(l calc.LegResult) string {
	if l.Factor != nil {
		return l.Factor.VehicleType
	}
	return ""
}

func dataTypeLabel(t string) string {
	switch t {
	case calc.DataPrimary:
		return "primary data"
	case calc.DataModelled:
		return "modelled data"
	}
	return "default value"
}

// secondary names the published source of a factor, from the start of
// its source text; ok is false for the operator's own data.
func secondary(src string) (SourceRef, bool) {
	src = baseSource(src)
	switch {
	case strings.HasPrefix(src, "GLEC Framework v3.3"):
		return SourceRef{Name: "GLEC Framework", Version: "3.3"}, true
	case strings.HasPrefix(src, "DESNZ 2026"):
		return SourceRef{Name: "UK Government GHG Conversion Factors for Company Reporting (DESNZ)", Version: "2026"}, true
	case strings.HasPrefix(src, "EU MRV 2024"):
		return SourceRef{Name: "EU MRV (EMSA THETIS-MRV)", Version: "2024"}, true
	case strings.HasPrefix(src, "www.co2emissiefactoren.nl 2026"):
		return SourceRef{Name: "CO2emissiefactoren.nl", Version: "2026"}, true
	}
	return SourceRef{}, false
}

// baseSource is the published source a factor's source text rests on: a
// load model's text names the dataset it is "from".
func baseSource(src string) string {
	if strings.HasPrefix(src, "load model (") {
		if i := strings.LastIndex(src, ") from "); i > 0 {
			return src[i+len(") from "):]
		}
	}
	return src
}

func sortedSources(m map[SourceRef]bool) []SourceRef {
	out := make([]SourceRef, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].Version < out[j].Name+out[j].Version })
	if len(out) == 0 {
		return nil
	}
	return out
}

func hash(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:6])
}

// sourceName is a short name of a factor's source, for messages.
func sourceName(src string) string {
	if s, ok := secondary(src); ok {
		return s.Name + " " + s.Version
	}
	if len(src) > 60 {
		return src[:60] + "..."
	}
	return src
}
