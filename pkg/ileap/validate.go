package ileap

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Allowed values (iLEAP 6.3, 6.4, 6.7).
var (
	modes     = []string{"Road", "Rail", "Air", "Sea", "InlandWaterway"}
	hubTypes  = []string{"Transshipment", "StorageAndTransshipment", "Warehouse", "LiquidBulkTerminal", "MaritimeContainerTerminal"}
	temps     = []string{TempAmbient, TempRefrigerated, TempMixed}
	carriersT = []string{Diesel, HVO, Petrol, CNG, LNG, LPG, HFO, MGO, Aviation, Hydrogen, Methanol, Electric}
	feeds     = []string{"Fossil", "Natural gas", FeedstockGrid, "Renewable electricity", "Cooking oil"}
	ecUnits   = []string{"l", "kg", "kWh", "MJ"}
	packaging = []string{"Box", "Pallet", "Container-TEU", "Container-FEU", "Container"}
)

// Validate checks an export against the iLEAP data model: mandatory
// properties, value formats and allowed values, and that every TCE's
// tocId or hocId is in the export. It returns the problems found.
func Validate(ex Export) []string {
	var p []string
	bad := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	tocs, hocs := map[string]bool{}, map[string]bool{}
	for _, t := range ex.TOCs {
		where := "toc " + t.TOCID
		checkCommon(bad, where, t.SpecVersion, t.CompanyName, t.CreatedAt, t.Status, t.ReferencePeriodStart, t.ReferencePeriodEnd)
		if t.TOCID == "" || tocs[t.TOCID] {
			bad("%s: tocId missing or used twice", where)
		}
		tocs[t.TOCID] = true
		if !slices.Contains(modes, t.Mode) {
			bad("%s: mode %q", where, t.Mode)
		}
		if t.TemperatureControl != "" && !slices.Contains(temps, t.TemperatureControl) {
			bad("%s: temperatureControl %q", where, t.TemperatureControl)
		}
		if t.AirShippingOption != "" && t.AirShippingOption != "belly freight" && t.AirShippingOption != "freighter" {
			bad("%s: airShippingOption %q", where, t.AirShippingOption)
		}
		if t.FlightLength != "" && t.FlightLength != "short-haul" && t.FlightLength != "long-haul" {
			bad("%s: flightLength %q", where, t.FlightLength)
		}
		if t.LoadFactor != "" {
			if x, ok := num(t.LoadFactor); !ok || x <= 0 || x > 1 {
				bad("%s: loadFactor %q", where, t.LoadFactor)
			}
		}
		if t.EmptyDistanceFactor != "" {
			if x, ok := num(t.EmptyDistanceFactor); !ok || x < 0 || x >= 1 {
				bad("%s: emptyDistanceFactor %q", where, t.EmptyDistanceFactor)
			}
		}
		checkCarriers(bad, where, t.EnergyCarriers)
		checkDec(bad, where+" co2eIntensityWTW", t.CO2eIntensityWTW, true)
		checkDec(bad, where+" co2eIntensityTTW", t.CO2eIntensityTTW, true)
		if t.TransportActivityUnit != UnitTkm && t.TransportActivityUnit != UnitTEUkm {
			bad("%s: transportActivityUnit %q", where, t.TransportActivityUnit)
		}
	}
	for _, h := range ex.HOCs {
		where := "hoc " + h.HOCID
		checkCommon(bad, where, h.SpecVersion, h.CompanyName, h.CreatedAt, h.Status, h.ReferencePeriodStart, h.ReferencePeriodEnd)
		if h.HOCID == "" || hocs[h.HOCID] {
			bad("%s: hocId missing or used twice", where)
		}
		hocs[h.HOCID] = true
		if !slices.Contains(hubTypes, h.HubType) {
			bad("%s: hubType %q", where, h.HubType)
		}
		if h.TemperatureControl != "" && !slices.Contains(temps, h.TemperatureControl) {
			bad("%s: temperatureControl %q", where, h.TemperatureControl)
		}
		checkCarriers(bad, where, h.EnergyCarriers)
		checkDec(bad, where+" co2eIntensityWTW", h.CO2eIntensityWTW, true)
		checkDec(bad, where+" co2eIntensityTTW", h.CO2eIntensityTTW, true)
		if h.HubActivityUnit != UnitTonnes && h.HubActivityUnit != UnitTEU {
			bad("%s: hubActivityUnit %q", where, h.HubActivityUnit)
		}
	}
	for _, s := range ex.ShipmentFootprints {
		where := "shipment " + s.ShipmentID
		checkCommon(bad, where, s.SpecVersion, s.CompanyName, s.CreatedAt, s.Status, s.ReferencePeriodStart, s.ReferencePeriodEnd)
		if s.ShipmentID == "" {
			bad("%s: shipmentId missing", where)
		}
		checkDec(bad, where+" mass", s.Mass, true)
		if len(s.TCEs) == 0 {
			bad("%s: no tces", where)
		}
		ids := map[string]bool{}
		for _, t := range s.TCEs {
			if t.TCEID == "" || ids[t.TCEID] {
				bad("%s: tceId %q missing or used twice", where, t.TCEID)
			}
			ids[t.TCEID] = true
		}
		for _, t := range s.TCEs {
			w := where + " tce " + t.TCEID
			if (t.TOCID == "") == (t.HOCID == "") {
				bad("%s: needs exactly one of tocId and hocId", w)
			} else if t.TOCID != "" && !tocs[t.TOCID] {
				bad("%s: tocId %s is not in the export", w, t.TOCID)
			} else if t.HOCID != "" && !hocs[t.HOCID] {
				bad("%s: hocId %s is not in the export", w, t.HOCID)
			}
			if t.ShipmentID != s.ShipmentID {
				bad("%s: shipmentId %q differs from the footprint's", w, t.ShipmentID)
			}
			for _, pr := range deref(t.PrevTCEIDs) {
				if !ids[pr] {
					bad("%s: prevTceIds %s is not in the shipment", w, pr)
				}
			}
			checkDec(bad, w+" mass", t.Mass, true)
			checkDec(bad, w+" transportActivity", t.TransportActivity, true)
			checkDec(bad, w+" co2eWTW", t.CO2eWTW, true)
			checkDec(bad, w+" co2eTTW", t.CO2eTTW, true)
			if t.Distance.Actual == "" && t.Distance.GCD == "" && t.Distance.SFD == "" {
				bad("%s: distance has no actual, gcd or sfd", w)
			}
			for _, d := range []Decimal{t.Distance.Actual, t.Distance.GCD, t.Distance.SFD} {
				checkDec(bad, w+" distance", d, false)
			}
			if t.PackagingOrTrEq != "" && !slices.Contains(packaging, t.PackagingOrTrEq) {
				bad("%s: packagingOrTrEqType %q", w, t.PackagingOrTrEq)
			}
			for _, l := range []*Location{t.Origin, t.Destination} {
				if l != nil && (l.City == "" || len(l.Country) != 2) {
					bad("%s: a location needs a city and an ISO 3166 alpha-2 country", w)
				}
				if l != nil && (l.Lat == "") != (l.Lng == "") {
					bad("%s: a location needs both lat and lng, or neither", w)
				}
			}
		}
	}
	return p
}

func checkCommon(bad func(string, ...any), where, spec, company, created, status, start, end string) {
	if spec != SpecVersion {
		bad("%s: specVersion %q", where, spec)
	}
	if company == "" {
		bad("%s: companyName missing", where)
	}
	if status != StatusActive && status != "Deprecated" {
		bad("%s: status %q", where, status)
	}
	for _, f := range []struct{ name, v string }{{"createdAt", created}, {"referencePeriodStart", start}, {"referencePeriodEnd", end}} {
		if _, err := time.Parse(time.RFC3339, f.v); err != nil {
			bad("%s: %s %q is not an ISO 8601 UTC timestamp", where, f.name, f.v)
		}
	}
	a, _ := time.Parse(time.RFC3339, start)
	b, _ := time.Parse(time.RFC3339, end)
	if start != "" && end != "" && !b.After(a) {
		bad("%s: the reference period ends before it starts", where)
	}
}

func checkDec(bad func(string, ...any), where string, d Decimal, mandatory bool) {
	if d == "" {
		if mandatory {
			bad("%s: missing", where)
		}
		return
	}
	if x, ok := num(d); !ok || x < 0 {
		bad("%s: %q is not a decimal >= 0", where, d)
	}
}

func checkCarriers(bad func(string, ...any), where string, ecs []EnergyCarrier) {
	if len(ecs) == 0 {
		bad("%s: energyCarriers missing", where)
		return
	}
	sum := 0.0
	for _, e := range ecs {
		w := where + " energy carrier " + e.EnergyCarrier
		if !slices.Contains(carriersT, e.EnergyCarrier) {
			bad("%s: energyCarrier %q", w, e.EnergyCarrier)
		}
		x, ok := num(e.RelativeShare)
		if !ok || x <= 0 || x > 1 {
			bad("%s: relativeShare %q", w, e.RelativeShare)
		}
		sum += x
		checkDec(bad, w+" emissionFactorWTW", e.EmissionFactorWTW, true)
		checkDec(bad, w+" emissionFactorTTW", e.EmissionFactorTTW, true)
		if e.EnergyConsumptionUnit == "" {
			bad("%s: energyConsumptionUnit missing (the unit of the emission factors)", w)
		}
		if e.EnergyConsumptionUnit != "" && !slices.Contains(ecUnits, e.EnergyConsumptionUnit) {
			bad("%s: energyConsumptionUnit %q", w, e.EnergyConsumptionUnit)
		}
		checkDec(bad, w+" energyConsumption", e.EnergyConsumption, false)
		fs := 0.0
		for _, f := range e.Feedstocks {
			if !slices.Contains(feeds, f.Feedstock) {
				bad("%s: feedstock %q", w, f.Feedstock)
			}
			if f.FeedstockShare != "" {
				y, ok := num(f.FeedstockShare)
				if !ok || y > 1 {
					bad("%s: feedstockShare %q", w, f.FeedstockShare)
				}
				fs += y
			}
		}
		if fs > 1+1e-9 {
			bad("%s: feedstock shares add up to more than 1", w)
		}
	}
	if math.Abs(sum-1) > 1e-6 {
		bad("%s: energy carrier shares add up to %s, not 1", where, dec(sum))
	}
}

func deref(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
