package factors

import (
	"fmt"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Region is one rule placing a point in a named region of a set: the point's
// country is in Countries and its coordinates are within the optional
// bounds. Rules are tried in order; the first match names the region.
type Region struct {
	Name      string   `json:"name"`
	Label     string   `json:"label,omitempty"`
	Set       string   `json:"set"`
	Countries []string `json:"countries"`
	LatMin    *float64 `json:"lat_min,omitempty"`
	LatMax    *float64 `json:"lat_max,omitempty"`
	LonMin    *float64 `json:"lon_min,omitempty"`
	LonMax    *float64 `json:"lon_max,omitempty"`
}

func (r Region) has(w calc.Where) bool {
	if w.Country == "" {
		return false
	}
	in := false
	for _, c := range r.Countries {
		if strings.EqualFold(c, w.Country) {
			in = true
			break
		}
	}
	if !in {
		return false
	}
	if r.LatMin != nil && w.Lat < *r.LatMin || r.LatMax != nil && w.Lat > *r.LatMax ||
		r.LonMin != nil && w.Lon < *r.LonMin || r.LonMax != nil && w.Lon > *r.LonMax {
		return false
	}
	return true
}

// Lane picks a vehicle type by the zones of a leg's two ends: one end in A
// and the other in B, either way round. Any matches any two known zones.
type Lane struct {
	A           []string `json:"a,omitempty"`
	B           []string `json:"b,omitempty"`
	Any         bool     `json:"any,omitempty"`
	VehicleType string   `json:"vehicle_type"`
	Label       string   `json:"label,omitempty"`
}

func (l Lane) matches(za, zb string) bool {
	if l.Any {
		return true
	}
	return contains(l.A, za) && contains(l.B, zb) || contains(l.A, zb) && contains(l.B, za)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

type placeLane struct {
	Lane
	f calc.Factor
}

// placeAlias is an alias whose meaning depends on where the leg is.
type placeAlias struct {
	alias, set, note string
	regions          map[string]calc.Factor
	lanes            []placeLane
}

func (d *Dataset) indexPlaceAlias(a VehicleAlias) error {
	k := legKey(a.Mode, a.Alias)
	if a.LoadModel != "" {
		if d.aliasLM == nil {
			d.aliasLM = map[string]string{}
		}
		d.aliasLM[k] = a.LoadModel
	}
	if len(a.Regions) == 0 && len(a.Lanes) == 0 {
		return nil
	}
	if a.RegionSet == "" {
		return fmt.Errorf("vehicle alias %s: regions and lanes need a region_set", k)
	}
	known := map[string]bool{}
	for _, r := range d.Regions {
		if r.Set == a.RegionSet {
			known[r.Name] = true
		}
	}
	p := placeAlias{alias: a.Alias, set: a.RegionSet, note: a.Note, regions: map[string]calc.Factor{}}
	for region, vt := range a.Regions {
		if !known[region] {
			return fmt.Errorf("vehicle alias %s: no region %q in set %q", k, region, a.RegionSet)
		}
		f, ok := d.legIdx[legKey(a.Mode, vt)]
		if !ok {
			return fmt.Errorf("vehicle alias %s: region %s points to unknown %s", k, region, vt)
		}
		p.regions[region] = f
	}
	for _, l := range a.Lanes {
		for _, z := range append(append([]string{}, l.A...), l.B...) {
			if !known[z] {
				return fmt.Errorf("vehicle alias %s: no zone %q in set %q", k, z, a.RegionSet)
			}
		}
		f, ok := d.legIdx[legKey(a.Mode, l.VehicleType)]
		if !ok {
			return fmt.Errorf("vehicle alias %s: lane points to unknown %s", k, l.VehicleType)
		}
		p.lanes = append(p.lanes, placeLane{Lane: l, f: f})
	}
	if d.placeIdx == nil {
		d.placeIdx = map[string]placeAlias{}
	}
	d.placeIdx[k] = p
	return nil
}

// region names the region of w in set, and its label; "" if none.
func (d *Dataset) region(set string, w calc.Where) (name, label string) {
	for _, r := range d.Regions {
		if r.Set == set && r.has(w) {
			l := r.Label
			if l == "" {
				l = r.Name
			}
			return r.Name, l
		}
	}
	return "", ""
}

// LegFactorAt implements calc.PlaceFactorProvider: aliases with regions or
// lanes choose their vehicle type by where the leg's ends are; others work
// as in LegFactorFor.
func (d *Dataset) LegFactorAt(m calc.Mode, vt string, km float64, from, to calc.Where) (calc.Factor, string, error) {
	p, ok := d.placeIdx[legKey(m, vt)]
	if !ok || from.Country == "" && to.Country == "" {
		return d.LegFactorFor(m, vt, km)
	}
	ra, la := d.region(p.set, from)
	rb, lb := d.region(p.set, to)
	where := func() string {
		if la != "" && la == lb {
			cs := strings.ToUpper(from.Country)
			if !strings.EqualFold(from.Country, to.Country) {
				cs += ", " + strings.ToUpper(to.Country)
			}
			return "in " + la + " (" + cs + ")"
		}
		return "from " + placeName(from, la) + " to " + placeName(to, lb)
	}
	withNote := func(s string) string {
		if p.note != "" {
			return s + " " + p.note
		}
		return s
	}
	if len(p.lanes) > 0 && ra != "" && rb != "" {
		for _, l := range p.lanes {
			if l.matches(ra, rb) {
				return l.f, withNote(fmt.Sprintf("%q %s: trade lane %s, %s.", p.alias, where(), l.Label, l.f.VehicleType)), nil
			}
		}
	}
	if ra != "" && ra == rb {
		if f, ok := p.regions[ra]; ok {
			return f, withNote(fmt.Sprintf("%q %s: %s.", p.alias, where(), f.VehicleType)), nil
		}
	}
	f, note, err := d.LegFactorFor(m, vt, km)
	if err != nil {
		return f, note, err
	}
	if note == "" {
		why := ""
		switch {
		case len(p.lanes) > 0 && (ra == "" || rb == ""):
			why = " (a port's zone is unknown)"
		case len(p.regions) > 0 && ra != rb:
			why = " (the ends are in different regions)"
		}
		note = fmt.Sprintf("%q %s%s: %s.", p.alias, where(), why, f.VehicleType)
	}
	return f, withNote(note), nil
}

func placeName(w calc.Where, label string) string {
	switch {
	case label != "" && w.Country != "":
		return label + " (" + strings.ToUpper(w.Country) + ")"
	case w.Country != "":
		return strings.ToUpper(w.Country)
	case label != "":
		return label
	}
	return "an unknown country"
}
