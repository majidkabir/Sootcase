// Package desnz converts the UK government's GHG conversion factors
// ("flat file" format, published yearly by DESNZ) into a Sootcase dataset.
//
// It imports, in kg CO2e:
//   - "Freighting goods" rows per tonne.km   -> leg factors (TTW)
//   - "WTT- delivery vehs & freight" rows    -> the matching leg WTT
//   - "Fuels" and "Bioenergy" rows           -> fuel factors (TTW)
//   - "WTT- fuels" / "WTT- bioenergy" rows   -> the matching fuel WTT
//   - "Outside of scopes" rows (kg CO2 per unit) -> biogenic CO2 of the
//     matching bioenergy fuel, kept outside its totals
//
// Rows are matched to their WTT counterpart by Level 2/3/4, Column Text and
// unit. Vehicle types and fuel ids are slugs of those labels, so the dataset
// stays complete without a hand-maintained mapping.
//
// The data is Crown copyright, published under the Open Government Licence
// v3.0, which requires the attribution set in the dataset note.
package desnz

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Attribution is the statement the Open Government Licence v3.0 requires.
const Attribution = "Contains public sector information licensed under the Open Government Licence v3.0. " +
	"Source: UK Department for Energy Security and Net Zero (DESNZ), Greenhouse gas reporting: conversion factors."

// Result is an import outcome.
type Result struct {
	Dataset  factors.Dataset
	Warnings []string
	// Blank counts imported-category rows with no published value
	// (e.g. per tonne.km for 0% laden vehicles), which are skipped.
	Blank int
}

type row struct {
	level1, level2, level3, level4, text, uom string
	value                                     float64
	line                                      int
}

// Parse converts the rows of a flat-file sheet. It finds the header row by
// its column names, so title rows above it are ignored.
func Parse(rows [][]string) (Result, error) {
	hdr, cols, year, err := findHeader(rows)
	if err != nil {
		return Result{}, err
	}
	var res Result
	warn := func(format string, a ...any) { res.Warnings = append(res.Warnings, fmt.Sprintf(format, a...)) }

	var freight, freightWTT, fuel, fuelWTT, freightKm, freightWTTKm, biogenic []row
	for i := hdr + 1; i < len(rows); i++ {
		get := func(c string) string {
			idx := cols[c]
			if idx < 0 || idx >= len(rows[i]) {
				return ""
			}
			return strings.TrimSpace(rows[i][idx])
		}
		l1, uom := norm(get("level 1")), norm(get("uom"))
		ghg := norm(get("ghg/unit"))
		if l1 == "outsideofscopes" && strings.Contains(ghg, "ofco2") {
			// Biogenic CO2 of bioenergy ("kg CO2e of CO2 per unit"):
			// kept beside the fuel factor, outside its totals.
			if v, err := strconv.ParseFloat(get("factor"), 64); err == nil {
				biogenic = append(biogenic, row{level2: get("level 2"), level3: get("level 3"), level4: get("level 4"),
					text: get("column text"), uom: get("uom"), value: v, line: i + 1})
			}
			continue
		}
		if ghg != "kgco2e" {
			continue
		}
		var dst *[]row
		switch {
		case l1 == "freightinggoods" && uom == "tonne.km":
			dst = &freight
		case strings.HasPrefix(l1, "wtt") && strings.Contains(l1, "freight") && uom == "tonne.km":
			dst = &freightWTT
		case l1 == "freightinggoods" && uom == "km":
			dst = &freightKm
		case strings.HasPrefix(l1, "wtt") && strings.Contains(l1, "freight") && uom == "km":
			dst = &freightWTTKm
		case l1 == "fuels", l1 == "bioenergy":
			dst = &fuel
		case strings.HasPrefix(l1, "wtt") && (strings.Contains(l1, "fuel") || strings.Contains(l1, "bioenergy")):
			dst = &fuelWTT
		default:
			continue
		}
		raw := get("factor")
		if raw == "" {
			res.Blank++
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			warn("line %d: factor %q is not a number, skipped", i+1, raw)
			continue
		}
		*dst = append(*dst, row{
			level1: get("level 1"), level2: get("level 2"), level3: get("level 3"),
			level4: get("level 4"), text: get("column text"), uom: get("uom"),
			value: v, line: i + 1,
		})
	}
	if len(freight) == 0 {
		return Result{}, fmt.Errorf("no \"Freighting goods\" tonne.km rows in kg CO2e found below header on line %d", hdr+1)
	}

	ds := &res.Dataset
	ds.Name = fmt.Sprintf("desnz-%d", year)
	ds.Note = fmt.Sprintf("UK DESNZ GHG conversion factors %d. %s", year, Attribution)

	wtt := index(freightWTT, warn, "freight WTT")
	seen := map[string]bool{}
	for _, r := range freight {
		mode, ok := modeOf(r.level2)
		if !ok {
			warn("line %d: no transport mode for %q, skipped", r.line, r.level2)
			continue
		}
		vt := slugPath(r.level2, r.level3, r.level4, r.text)
		if seen[string(mode)+"/"+vt] {
			warn("line %d: duplicate freight factor %s, skipped", r.line, vt)
			continue
		}
		seen[string(mode)+"/"+vt] = true
		f := calc.Factor{
			Mode: mode, VehicleType: vt, TTW: grams(r.value), Year: year,
			Source: fmt.Sprintf("DESNZ %d: %s", year, labelPath(r.level1, r.level2, r.level3, r.level4, r.text)),
		}
		if w, ok := wtt[key(r)]; ok {
			f.WTT = grams(w.value)
			delete(wtt, key(r))
		} else {
			warn("line %d: no WTT factor for %s, WTT set to 0", r.line, vt)
		}
		if strings.Contains(vt, "battery_electric") {
			// DESNZ's battery electric factor is electricity generation and
			// T&D losses (its Table 15): no emissions at the vehicle. In ISO
			// 14083 terms all of it is energy provision (WTT).
			f.WTT, f.TTW = clean(f.WTT+f.TTW), 0
			f.Source += BEVNote
		}
		ds.Legs = append(ds.Legs, f)
	}
	for _, r := range sortedRest(wtt) {
		warn("line %d: freight WTT row without matching freight row: %s", r.line, labelPath(r.level2, r.level3, r.level4, r.text))
	}

	fwtt := index(fuelWTT, warn, "fuel WTT")
	bio := map[string]row{}
	for _, r := range biogenic {
		bio[key(r)] = r
		if norm(r.level2) == "forecourtfuelscontainingbiofuel" {
			// The biogenic share of diesel and petrol as sold, listed under
			// another Level 2 than the fuel itself.
			bio["blend|"+norm(r.level3)+"|"+norm(r.uom)] = r
		}
	}
	seen = map[string]bool{}
	for _, r := range fuel {
		id := slugPath(r.level2, r.level3, r.level4, r.text, r.uom)
		if seen[id] {
			warn("line %d: duplicate fuel factor %s, skipped", r.line, id)
			continue
		}
		seen[id] = true
		f := calc.FuelFactor{
			Fuel: id, Unit: r.uom, TTW: clean(r.value), Year: year,
			Source: fmt.Sprintf("DESNZ %d: %s (%s)", year, labelPath(r.level1, r.level2, r.level3, r.level4, r.text), r.uom),
		}
		if w, ok := fwtt[key(r)]; ok {
			f.WTT = clean(w.value)
			delete(fwtt, key(r))
		} else {
			warn("line %d: no WTT factor for fuel %s, WTT set to 0", r.line, id)
		}
		if b, ok := bio[key(r)]; ok && norm(r.level1) == "bioenergy" {
			f.BiogenicCO2 = clean(b.value)
		} else if b, ok := bio["blend|"+norm(r.level3)+"|"+norm(r.uom)]; ok && norm(r.level1) == "fuels" {
			f.BiogenicCO2 = clean(b.value)
		}
		ds.Fuels = append(ds.Fuels, f)
	}
	// Unmatched fuel WTT rows are normal (WTT covers fuels with no direct
	// emissions factor in some units), so they are not reported.
	addAliases(ds, warn)
	ds.LoadModels = loadModels(freight, freightKm, freightWTTKm, year, warn)
	return res, nil
}

func findHeader(rows [][]string) (hdr int, cols map[string]int, year int, err error) {
	want := []string{"level 1", "level 2", "level 3", "level 4", "column text", "uom", "ghg/unit"}
	for i, r := range rows {
		if i > 100 {
			break
		}
		cols = map[string]int{"factor": -1}
		for _, w := range want {
			cols[w] = -1
		}
		for j, cell := range r {
			c := strings.ToLower(strings.Join(strings.Fields(cell), " "))
			if _, ok := cols[c]; ok {
				cols[c] = j
			}
			if strings.HasPrefix(c, "ghg conversion factor") {
				cols["factor"] = j
				if m := yearRe.FindString(c); m != "" {
					year, _ = strconv.Atoi(m)
				}
			}
		}
		if cols["level 1"] >= 0 && cols["uom"] >= 0 {
			for c, idx := range cols {
				if idx < 0 {
					return 0, nil, 0, fmt.Errorf("header on line %d has no %q column", i+1, c)
				}
			}
			if year == 0 {
				return 0, nil, 0, fmt.Errorf("header on line %d: no year in factor column name", i+1)
			}
			return i, cols, year, nil
		}
	}
	return 0, nil, 0, fmt.Errorf("no header row with \"Level 1\" and \"UOM\" columns found; is this the flat-file format?")
}

var yearRe = regexp.MustCompile(`(19|20)\d\d`)

func index(rows []row, warn func(string, ...any), what string) map[string]row {
	m := make(map[string]row, len(rows))
	for _, r := range rows {
		k := key(r)
		if _, dup := m[k]; dup {
			warn("line %d: duplicate %s row %s, skipped", r.line, what, k)
			continue
		}
		m[k] = r
	}
	return m
}

func sortedRest(m map[string]row) []row {
	out := make([]row, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// BEVNote marks battery electric factors moved to WTT.
const BEVNote = "; electricity (generation, T&D and upstream) counted as energy provision (WTT), none at the vehicle (DESNZ 2026 methodology, Table 15)"

// key matches a row to its WTT counterpart: WTT sheets repeat the labels,
// sometimes with a "WTT-" prefix.
func key(r row) string {
	parts := []string{r.level2, r.level3, r.level4, r.text, r.uom}
	for i, p := range parts {
		p = norm(p)
		p = strings.TrimPrefix(p, "wtt-")
		parts[i] = p
	}
	return strings.Join(parts, "|")
}

func modeOf(level2 string) (calc.Mode, bool) {
	l := strings.ToLower(level2)
	switch {
	case strings.Contains(l, "hgv"), strings.Contains(l, "van"), strings.Contains(l, "lorr"):
		return calc.Road, true
	case strings.Contains(l, "rail"), strings.Contains(l, "train"):
		return calc.Rail, true
	case strings.Contains(l, "ship"), strings.Contains(l, "tanker"), strings.Contains(l, "vessel"):
		return calc.Sea, true
	case strings.Contains(l, "flight"), strings.Contains(l, "air"):
		return calc.Air, true
	}
	return "", false
}

// norm lowercases and removes whitespace, for label comparison.
func norm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "%", "pct")
	return strings.Trim(nonSlug.ReplaceAllString(s, "_"), "_")
}

func slugPath(parts ...string) string {
	var out []string
	for _, p := range parts {
		if s := slug(p); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

func labelPath(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " > ")
}

// grams converts kg/tkm to g/tkm without float noise.
func grams(kg float64) float64 { return clean(kg * 1000) }

// clean rounds to 12 significant digits, removing binary float noise
// (e.g. 105.30000000000001) without losing published precision.
func clean(v float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 12, 64), 64)
	return r
}

// loadModels builds per-class load models from the per-km rows at 0%, 50%
// and 100% laden. Payload capacity is implied by the 100% laden rows:
// (kg per vehicle-km) / (kg per tonne-km) = tonnes carried.
func loadModels(freight, perKm, wttKm []row, year int, warn func(string, ...any)) []calc.LoadModel {
	loads := map[string]float64{"0pct_laden": 0, "50pct_laden": 0.5, "100pct_laden": 1}
	type class struct {
		mode    calc.Mode
		id, lbl string
		ttw     map[float64]float64
		wtt     map[float64]float64
		tkm100  float64
		line    int
	}
	classes := map[string]*class{}
	var order []string
	get := func(r row) (*class, float64, bool) {
		lf, ok := loads[slug(r.text)]
		if !ok {
			return nil, 0, false
		}
		mode, ok := modeOf(r.level2)
		if !ok {
			return nil, 0, false
		}
		k := key(row{level2: r.level2, level3: r.level3, level4: r.level4})
		c := classes[k]
		if c == nil {
			c = &class{mode: mode, id: slugPath(r.level2, r.level3, r.level4),
				lbl: labelPath(r.level2, r.level3, r.level4), ttw: map[float64]float64{}, wtt: map[float64]float64{}, line: r.line}
			classes[k] = c
			order = append(order, k)
		}
		return c, lf, true
	}
	for _, r := range perKm {
		if c, lf, ok := get(r); ok {
			c.ttw[lf] = grams(r.value)
		}
	}
	for _, r := range wttKm {
		k := key(row{level2: r.level2, level3: r.level3, level4: r.level4})
		if c := classes[k]; c != nil {
			if lf, ok := loads[slug(r.text)]; ok {
				c.wtt[lf] = grams(r.value)
			}
		}
	}
	for _, r := range freight {
		if slug(r.text) == "100pct_laden" {
			if c := classes[key(row{level2: r.level2, level3: r.level3, level4: r.level4})]; c != nil {
				c.tkm100 = grams(r.value)
			}
		}
	}

	var out []calc.LoadModel
	for _, k := range order {
		c := classes[k]
		if len(c.ttw) != 3 || len(c.wtt) != 3 || c.tkm100 <= 0 {
			warn("line %d: load model %s incomplete (needs 0/50/100%% laden per km, WTT and 100%% per tonne.km), skipped", c.line, c.id)
			continue
		}
		m := calc.LoadModel{
			Mode: c.mode, VehicleType: c.id, PayloadT: clean(c.ttw[1] / c.tkm100), Year: year,
			DistanceBasis: calc.DistActual, // per km driven, like the road factors (aliases.go)
			Source:        fmt.Sprintf("DESNZ %d: Freighting goods > %s (per km at 0/50/100%% laden; payload from 100%% laden per tonne.km)", year, c.lbl),
		}
		for _, lf := range []float64{0, 0.5, 1} {
			m.Points = append(m.Points, calc.LoadPoint{Load: lf, TTW: c.ttw[lf], WTT: c.wtt[lf]})
		}
		if mid := (c.ttw[0] + c.ttw[1]) / 2; math.Abs(c.ttw[0.5]-mid)/mid > 0.02 {
			warn("load model %s: 50%% laden is %.1f%% off linear; interpolating through it", c.id, (c.ttw[0.5]/mid-1)*100)
		}
		out = append(out, m)
	}
	return out
}
