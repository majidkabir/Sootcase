package ileap

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/toc"
)

// FromOperations turns an operator's measured data (the TOCs and HOCs of
// "sootcase toc") into iLEAP TOCs and HOCs: intensities, energy carriers
// with their consumption per unit of activity and emission factors.
// Options.CompanyName and the period (from the operations, or else the
// options) fill what iLEAP asks about the data owner.
func FromOperations(ops toc.Operations, fuels calc.FactorProvider, opts Options) Export {
	if t, err := time.Parse(time.DateOnly, ops.PeriodStart); err == nil {
		opts.PeriodStart = t
	}
	if t, err := time.Parse(time.DateOnly, ops.PeriodEnd); err == nil {
		opts.PeriodEnd = t.AddDate(0, 0, 1) // iLEAP's end is exclusive
	}
	if opts.CompanyName == "" {
		opts.CompanyName = ops.Name
	}
	b := newBuilder(opts)
	for _, t := range ops.TOCs {
		b.addTOC(b.operatorTOC(ops, t, fuels))
	}
	for _, h := range ops.HOCs {
		b.addHOC(b.operatorHOC(ops, h, fuels))
	}
	return b.finish()
}

// Operator indexes an export's TOCs and HOCs by id, for Options.Operator.
func Operator(ex Export) map[string]any {
	m := map[string]any{}
	for _, t := range ex.TOCs {
		m[t.TOCID] = t
	}
	for _, h := range ex.HOCs {
		m[h.HOCID] = h
	}
	return m
}

func (b *builder) operatorTOC(ops toc.Operations, t toc.TOC, fuels calc.FactorProvider) TOC {
	created, start, end := b.common("toc", t.ID)
	out := TOC{
		SpecVersion: SpecVersion, CompanyName: b.opts.CompanyName, CompanyIDs: b.opts.CompanyIDs,
		CreatedAt: created, Status: StatusActive, ReferencePeriodStart: start, ReferencePeriodEnd: end,
		TOCID: t.ID, Description: t.Description, Mode: mode(t.Mode), TemperatureControl: t.TemperatureControl,
		TransportActivityUnit: UnitTkm,
	}
	if out.Mode == "" {
		b.gap("toc", t.ID, "mode", fmt.Sprintf("iLEAP has no transport mode %q", t.Mode))
	}
	ttw, wtt, err := toc.Totals(t.Fuels, t.Refrigerants, fuels)
	if err != nil {
		b.gap("toc", t.ID, "co2eIntensityWTW", err.Error())
		return out
	}
	out.CO2eIntensityWTW, out.CO2eIntensityTTW = dec((ttw+wtt)/t.TonneKm), dec(ttw/t.TonneKm)
	out.EnergyCarriers, out.SecondaryEmissionFactorSources = b.carriers("toc", t.ID, t.Fuels, t.TonneKm, fuels)
	out.Comment = operatorComment(ops, t.DataType, t.Refrigerants, fuels, t.TonneKm, "tonne-km")
	return out
}

func (b *builder) operatorHOC(ops toc.Operations, h toc.HOC, fuels calc.FactorProvider) HOC {
	created, start, end := b.common("hoc", h.ID)
	out := HOC{
		SpecVersion: SpecVersion, CompanyName: b.opts.CompanyName, CompanyIDs: b.opts.CompanyIDs,
		CreatedAt: created, Status: StatusActive, ReferencePeriodStart: start, ReferencePeriodEnd: end,
		HOCID: h.ID, Description: h.Description, TemperatureControl: h.TemperatureControl,
		HubType: map[string]string{
			"transshipment": "Transshipment", "storage_transshipment": "StorageAndTransshipment",
			"warehouse": "Warehouse", "liquid_bulk_terminal": "LiquidBulkTerminal",
			"maritime_container_terminal": "MaritimeContainerTerminal",
		}[h.HubType],
	}
	if out.HubType == "" {
		b.gap("hoc", h.ID, "hubType", "no hub_type given for the HOC")
	}
	activity, unitName, label := h.Tonnes, UnitTonnes, "tonnes"
	switch {
	case h.Containers > 0 && h.ContainersInTEU:
		activity, unitName, label = h.Containers, UnitTEU, "TEU"
	case h.Containers > 0:
		// Containers counted as boxes of any size; iLEAP's unit is the TEU.
		activity, unitName, label = h.Containers, "", "containers"
		b.gap("hoc", h.ID, "hubActivityUnit", "the HOC is per container, of no given size, and iLEAP's hub units are TEU and tonnes; set containers_in_teu when the containers are counted in TEU")
	}
	out.HubActivityUnit = unitName
	ttw, wtt, err := toc.Totals(h.Energy, h.Refrigerants, fuels)
	if err != nil {
		b.gap("hoc", h.ID, "co2eIntensityWTW", err.Error())
		return out
	}
	out.CO2eIntensityWTW, out.CO2eIntensityTTW = dec((ttw+wtt)/activity), dec(ttw/activity)
	out.EnergyCarriers, out.SecondaryEmissionFactorSources = b.carriers("hoc", h.ID, h.Energy, activity, fuels)
	out.Comment = operatorComment(ops, h.DataType, h.Refrigerants, fuels, activity, label)
	return out
}

// carriers makes the energy carriers of a category's energy uses. With
// more than one, iLEAP needs each one's share of the activity, which only
// the operator knows (activity_share).
func (b *builder) carriers(object, id string, uses []toc.EnergyUse, activity float64, fuels calc.FactorProvider) ([]EnergyCarrier, []SourceRef) {
	var out []EnergyCarrier
	srcs := map[SourceRef]bool{}
	shares, sum := true, 0.0
	for _, u := range uses {
		ff, err := fuels.FuelFactor(u.Fuel)
		if err != nil {
			b.gap(object, id, "energyCarriers", err.Error())
			return nil, nil
		}
		ec, why := carrierFromFuel(ff, u.Amount/activity)
		if why != "" {
			b.gap(object, id, "energyCarriers", why)
			return nil, nil
		}
		if s, ok := secondary(ff.Source); ok {
			srcs[s] = true
		}
		if u.ActivityShare == nil {
			shares = false
		} else {
			ec.RelativeShare = dec(*u.ActivityShare)
			sum += *u.ActivityShare
		}
		out = append(out, ec)
	}
	switch {
	case len(out) == 1:
		out[0].RelativeShare = "1"
	case !shares:
		b.gap(object, id, "energyCarriers.relativeShare",
			"more than one energy carrier: give each fuel's activity_share (its share of the category's activity)")
	case math.Abs(sum-1) > 1e-6:
		b.gap(object, id, "energyCarriers.relativeShare", fmt.Sprintf("the activity shares add up to %s, not 1", dec(sum)))
	}
	return out, sortedSources(srcs)
}

func operatorComment(ops toc.Operations, dataType string, refs []toc.RefrigerantUse, fuels calc.FactorProvider, activity float64, label string) string {
	parts := []string{fmt.Sprintf("Calculated by Sootcase from the operator's measured energy use over %s %s", dec(activity), label)}
	if ops.Period != "" {
		parts[0] += " (period " + ops.Period + ")"
	}
	if dataType == calc.DataModelled {
		parts = append(parts, "modelled: the period's data was extrapolated from a sample")
	}
	if len(refs) > 0 {
		if ttw, _, err := toc.Totals(nil, refs, fuels); err == nil {
			parts = append(parts, fmt.Sprintf("includes %s kg CO2e of refrigerant losses in the TTW value (ISO 14083 Annex I), which iLEAP has no separate property for", dec(ttw)))
		}
	}
	return strings.Join(parts, "; ") + "."
}

// Merge combines two exports: their objects (each TOC and HOC once, the
// first one kept), gaps and notes.
func Merge(a, b Export) Export {
	out := Export{SpecVersion: SpecVersion,
		ShipmentFootprints: append(append([]ShipmentFootprint{}, a.ShipmentFootprints...), b.ShipmentFootprints...)}
	seen := map[string]bool{}
	for _, t := range append(append([]TOC{}, a.TOCs...), b.TOCs...) {
		if !seen["t"+t.TOCID] {
			seen["t"+t.TOCID] = true
			out.TOCs = append(out.TOCs, t)
		}
	}
	for _, h := range append(append([]HOC{}, a.HOCs...), b.HOCs...) {
		if !seen["h"+h.HOCID] {
			seen["h"+h.HOCID] = true
			out.HOCs = append(out.HOCs, h)
		}
	}
	for _, g := range append(append([]Gap{}, a.Gaps...), b.Gaps...) {
		k := "g" + g.Object + "\x00" + g.ID + "\x00" + g.Field
		if !seen[k] {
			seen[k] = true
			out.Gaps = append(out.Gaps, g)
		}
	}
	for _, n := range append(append([]string{}, a.Notes...), b.Notes...) {
		if !seen["n"+n] {
			seen["n"+n] = true
			out.Notes = append(out.Notes, n)
		}
	}
	out.Conformant = len(out.Gaps) == 0 && len(Validate(out)) == 0
	return out
}
