// Package factors provides emission factor datasets. Datasets are embedded
// in the binary (see Embedded and EmbeddedNames) or loaded from a JSON file
// with the same shape, e.g. one converted from GLEC or a licensed source.
package factors

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/distance"
)

// Default is the embedded dataset used when none is chosen: UK DESNZ for
// road, rail, sea, air and hubs, layered with Dutch inland waterway factors
// and a container ship factor from EU MRV data (which "container_ship"
// then means).
const Default = "desnz-2026,nl-iww-2026,eu-mrv-2024,glec-3.3"

//go:embed data/*.json data/grid/*.json
var embedded embed.FS

// DefaultGrid is the embedded electricity grid table attached to datasets
// that bring no grids of their own.
const DefaultGrid = "data/grid/ember-owid.json"

// Dataset is a named set of factors that implements calc.FactorProvider.
type Dataset struct {
	Name  string            `json:"name"`
	Note  string            `json:"note,omitempty"`
	Legs  []calc.Factor     `json:"legs"`
	Hubs  []calc.HubFactor  `json:"hubs"`
	Fuels []calc.FuelFactor `json:"fuels"`
	// DistanceAdjustments turn great-circle distances into estimated
	// actual distances per mode, matching the dataset's methodology.
	DistanceAdjustments []distance.Adjustment `json:"distance_adjustments,omitempty"`
	// VehicleAliases and FuelAliases map short names to full ids. Lookups
	// accept either; results always carry the full id.
	VehicleAliases []VehicleAlias    `json:"vehicle_aliases,omitempty"`
	FuelAliases    map[string]string `json:"fuel_aliases,omitempty"`
	// FuelRegions make a fuel alias mean another fuel when both ends of the
	// leg are in a region (e.g. GLEC's North American diesel).
	FuelRegions map[string]FuelRegion `json:"fuel_regions,omitempty"`
	// CargoTypes name the cargo types a shipment can give; CargoVariants
	// map a vehicle type to its factor for each cargo type, and
	// HubCargoVariants a hub type to another hub type.
	CargoTypes       []CargoType                  `json:"cargo_types,omitempty"`
	CargoVariants    map[string]map[string]string `json:"cargo_variants,omitempty"`
	HubCargoVariants map[string]map[string]string `json:"hub_cargo_variants,omitempty"`
	// ContainerLoads maps profile names (e.g. "average") to net cargo
	// tonnes per TEU, for shipments given in TEU.
	ContainerLoads map[string]float64 `json:"container_loads,omitempty"`
	// DAFs convert a leg's distance type into a factor's distance basis.
	DAFs []calc.DAF `json:"dafs,omitempty"`
	// LoadModels give load-dependent emissions per vehicle class.
	LoadModels []calc.LoadModel `json:"load_models,omitempty"`
	// Grids give electricity carbon intensity per country. Datasets without
	// them use the embedded default (Ember via Our World in Data).
	Grids    []calc.GridFactor `json:"grids,omitempty"`
	GridNote string            `json:"grid_note,omitempty"`
	// Regions place a leg's ends in the regions or zones that regional
	// aliases choose by (see VehicleAlias.Regions and Lanes).
	Regions []Region `json:"regions,omitempty"`

	legIdx   map[string]calc.Factor
	shortIdx map[string]shortAlias
	loadIdx  map[string]calc.LoadModel
	dafIdx   map[string]calc.DAF
	gridIdx  map[string]calc.GridFactor
	hubIdx   map[string]calc.HubFactor
	fuelIdx  map[string]calc.FuelFactor
	placeIdx map[string]placeAlias
	aliasLM  map[string]string
	fuelReg  map[string]map[string]calc.FuelFactor
	cargoIdx map[string]map[string]calc.Factor
}

// CargoType is a cargo type a shipment can give (Shipment.CargoType).
type CargoType struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// FuelRegion is a fuel alias's meaning per region of RegionSet.
type FuelRegion struct {
	RegionSet string            `json:"region_set"`
	Regions   map[string]string `json:"regions"`
}

// Embedded loads a dataset bundled in the binary, e.g. "sample".
func Embedded(name string) (*Dataset, error) {
	f, err := embedded.Open("data/" + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("embedded dataset %q: %w", name, err)
	}
	defer f.Close()
	return Parse(f)
}

// VehicleAlias is a short name for a vehicle type within one mode.
type VehicleAlias struct {
	Mode        calc.Mode `json:"mode"`
	Alias       string    `json:"alias"`
	VehicleType string    `json:"vehicle_type"`
	// ShortVehicleType, if set, is what the alias means on legs shorter
	// than ShortBelowKm (e.g. a short-sea ship for "container_ship").
	ShortVehicleType string  `json:"short_vehicle_type,omitempty"`
	ShortBelowKm     float64 `json:"short_below_km,omitempty"`
	// RegionSet names the set of Regions that Regions and Lanes use.
	// Regions maps a region to the vehicle type the alias means when both
	// ends of the leg are in it (e.g. GLEC's North American road values).
	RegionSet string            `json:"region_set,omitempty"`
	Regions   map[string]string `json:"regions,omitempty"`
	// Lanes choose the vehicle type by the zones of both ends (e.g. GLEC's
	// container trade lanes); the first matching lane wins.
	Lanes []Lane `json:"lanes,omitempty"`
	// Note explains the alias's choice; it is added to the leg's factor note.
	Note string `json:"note,omitempty"`
	// LoadModel is the load model class used when a load is given for
	// this alias (the alias's own factor has none).
	LoadModel string `json:"load_model,omitempty"`
	// OnlyRegions refuses legs whose ends aren't both in one of Regions
	// (e.g. GLEC's electric trucks, given for Europe, China and India).
	// UnavailableRegions refuses legs with both ends in one of them.
	OnlyRegions        bool     `json:"only_regions,omitempty"`
	UnavailableRegions []string `json:"unavailable_regions,omitempty"`
}

// EmbeddedNames lists the datasets bundled in the binary.
func EmbeddedNames() []string {
	entries, _ := fs.ReadDir(embedded, "data")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), path.Ext(e.Name())))
	}
	sort.Strings(names)
	return names
}

// Open loads an embedded dataset by name, or a JSON file by path.
// An empty name opens Default.
//
// A comma-separated list layers datasets, e.g. "desnz-2026,my-hubs.json":
// their contents are combined, and defining the same factor, fuel alias,
// grid country, or container profile with another value twice is an error.
// A vehicle or fuel alias or DAF defined again is the later dataset's.
func Open(nameOrPath string) (*Dataset, error) {
	if nameOrPath == "" {
		nameOrPath = Default
	}
	parts := strings.Split(nameOrPath, ",")
	if len(parts) == 1 {
		return openOne(nameOrPath)
	}
	var merged Dataset
	var names, notes []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		raw, err := readRaw(p)
		if err != nil {
			return nil, err
		}
		if err := mergeInto(&merged, raw); err != nil {
			return nil, fmt.Errorf("layering %s: %w", p, err)
		}
		names = append(names, raw.Name)
		if raw.Note != "" {
			notes = append(notes, raw.Note)
		}
	}
	merged.Name = strings.Join(names, "+")
	merged.Note = strings.Join(notes, " | ")
	b, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return Parse(strings.NewReader(string(b)))
}

func openOne(nameOrPath string) (*Dataset, error) {
	for _, n := range EmbeddedNames() {
		if n == nameOrPath {
			return Embedded(n)
		}
	}
	return Load(nameOrPath)
}

// readRaw decodes a dataset without indexing or defaults, for layering.
func readRaw(nameOrPath string) (Dataset, error) {
	var r io.ReadCloser
	var err error
	for _, n := range EmbeddedNames() {
		if n == nameOrPath {
			r, err = embedded.Open("data/" + n + ".json")
			break
		}
	}
	if r == nil && err == nil {
		r, err = os.Open(nameOrPath)
	}
	if err != nil {
		return Dataset{}, err
	}
	defer r.Close()
	var d Dataset
	if err := json.NewDecoder(r).Decode(&d); err != nil {
		return Dataset{}, fmt.Errorf("parse dataset %s: %w", nameOrPath, err)
	}
	return d, nil
}

// mergeInto appends src to dst. Duplicate lists entries are caught by
// Parse afterwards; map keys are checked here.
func mergeInto(dst *Dataset, src Dataset) error {
	dst.Legs = append(dst.Legs, src.Legs...)
	dst.Hubs = append(dst.Hubs, src.Hubs...)
	dst.Fuels = append(dst.Fuels, src.Fuels...)
	dst.LoadModels = append(dst.LoadModels, src.LoadModels...)
	for _, x := range src.DAFs {
		replaced := false
		for i, y := range dst.DAFs {
			if x.Mode == y.Mode && x.From == y.From && x.To == y.To {
				dst.DAFs[i], replaced = x, true
			}
		}
		if !replaced {
			dst.DAFs = append(dst.DAFs, x)
		}
	}
	dst.Regions = append(dst.Regions, src.Regions...)
	for _, ct := range src.CargoTypes {
		known := false
		for _, x := range dst.CargoTypes {
			known = known || x.Name == ct.Name
		}
		if !known {
			dst.CargoTypes = append(dst.CargoTypes, ct)
		}
	}
	for dstMap, srcMap := range map[*map[string]map[string]string]map[string]map[string]string{
		&dst.CargoVariants: src.CargoVariants, &dst.HubCargoVariants: src.HubCargoVariants} {
		for k, v := range srcMap {
			if *dstMap == nil {
				*dstMap = map[string]map[string]string{}
			}
			if (*dstMap)[k] == nil {
				(*dstMap)[k] = map[string]string{}
			}
			for c, t := range v {
				(*dstMap)[k][c] = t
			}
		}
	}
	for _, a := range src.VehicleAliases {
		replaced := false
		for i, b := range dst.VehicleAliases {
			if a.Mode == b.Mode && a.Alias == b.Alias {
				dst.VehicleAliases[i], replaced = a, true
			}
		}
		if !replaced {
			dst.VehicleAliases = append(dst.VehicleAliases, a)
		}
	}
	dst.Grids = append(dst.Grids, src.Grids...)
	if src.GridNote != "" {
		dst.GridNote = strings.TrimPrefix(dst.GridNote+" | "+src.GridNote, " | ")
	}
	for _, a := range src.DistanceAdjustments {
		for _, b := range dst.DistanceAdjustments {
			if a.Mode == b.Mode {
				return fmt.Errorf("distance adjustment for %s defined twice", a.Mode)
			}
		}
		dst.DistanceAdjustments = append(dst.DistanceAdjustments, a)
	}
	for k, v := range src.FuelAliases {
		if dst.FuelAliases == nil {
			dst.FuelAliases = map[string]string{}
		}
		dst.FuelAliases[k] = v // a later layer's alias wins, as for vehicle aliases
		delete(dst.FuelRegions, k)
	}
	for k, v := range src.FuelRegions {
		if dst.FuelRegions == nil {
			dst.FuelRegions = map[string]FuelRegion{}
		}
		dst.FuelRegions[k] = v
	}
	for k, v := range src.ContainerLoads {
		if old, dup := dst.ContainerLoads[k]; dup && old != v {
			return fmt.Errorf("container load %q defined twice with different values", k)
		}
		if dst.ContainerLoads == nil {
			dst.ContainerLoads = map[string]float64{}
		}
		dst.ContainerLoads[k] = v
	}
	return nil
}

// Load reads a dataset from a JSON file.
func Load(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse decodes and indexes a dataset, rejecting duplicates and negative values.
func Parse(r io.Reader) (*Dataset, error) {
	var d Dataset
	if err := json.NewDecoder(r).Decode(&d); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	d.legIdx = make(map[string]calc.Factor, len(d.Legs))
	for _, f := range d.Legs {
		if f.TTW < 0 || f.WTT < 0 {
			return nil, fmt.Errorf("negative factor for %s/%s", f.Mode, f.VehicleType)
		}
		switch f.DistanceBasis {
		case "", calc.DistSFD, calc.DistGCD, calc.DistActual:
		default:
			return nil, fmt.Errorf("factor %s/%s: distance_basis must be sfd, gcd or actual", f.Mode, f.VehicleType)
		}
		k := legKey(f.Mode, f.VehicleType)
		if _, dup := d.legIdx[k]; dup {
			return nil, fmt.Errorf("duplicate factor for %s", k)
		}
		d.legIdx[k] = f
	}
	d.hubIdx = make(map[string]calc.HubFactor, len(d.Hubs))
	for _, h := range d.Hubs {
		if h.WTWPerContainerG < 0 || h.WTWPerContainerG > 0 && h.WTWPerTonneG != 0 {
			return nil, fmt.Errorf("hub factor for %s: give wtw_g_per_tonne or wtw_g_per_container (>= 0), not both", h.HubType)
		}
		if h.WTWPerTonneG < 0 {
			return nil, fmt.Errorf("negative hub factor for %s", h.HubType)
		}
		if _, dup := d.hubIdx[h.HubType]; dup {
			return nil, fmt.Errorf("duplicate hub factor for %s", h.HubType)
		}
		d.hubIdx[h.HubType] = h
	}
	d.fuelIdx = make(map[string]calc.FuelFactor, len(d.Fuels))
	for _, f := range d.Fuels {
		if f.TTW < 0 || f.WTT < 0 {
			return nil, fmt.Errorf("negative fuel factor for %s", f.Fuel)
		}
		if _, dup := d.fuelIdx[f.Fuel]; dup {
			return nil, fmt.Errorf("duplicate fuel factor for %s", f.Fuel)
		}
		d.fuelIdx[f.Fuel] = f
	}
	if _, err := distance.NewGreatCircle(d.DistanceAdjustments); err != nil {
		return nil, err
	}
	for _, a := range d.VehicleAliases {
		k := legKey(a.Mode, a.Alias)
		target, ok := d.legIdx[legKey(a.Mode, a.VehicleType)]
		if !ok {
			return nil, fmt.Errorf("vehicle alias %s points to unknown %s", k, a.VehicleType)
		}
		if _, clash := d.legIdx[k]; clash {
			return nil, fmt.Errorf("vehicle alias %s clashes with an existing vehicle type", k)
		}
		d.legIdx[k] = target
		if err := d.indexPlaceAlias(a); err != nil {
			return nil, err
		}
		if a.ShortVehicleType != "" || a.ShortBelowKm != 0 {
			short, ok := d.legIdx[legKey(a.Mode, a.ShortVehicleType)]
			if !ok || a.ShortBelowKm <= 0 {
				return nil, fmt.Errorf("vehicle alias %s: short_vehicle_type must name a vehicle type and short_below_km be > 0", k)
			}
			if d.shortIdx == nil {
				d.shortIdx = map[string]shortAlias{}
			}
			d.shortIdx[k] = shortAlias{alias: a.Alias, belowKm: a.ShortBelowKm, short: short, long: target}
		}
	}
	d.dafIdx = make(map[string]calc.DAF, len(d.DAFs))
	for _, x := range d.DAFs {
		if err := x.Validate(); err != nil {
			return nil, err
		}
		k := dafKey(x.Mode, x.From, x.To)
		if _, dup := d.dafIdx[k]; dup {
			return nil, fmt.Errorf("duplicate DAF %s", k)
		}
		d.dafIdx[k] = x
	}
	d.loadIdx = make(map[string]calc.LoadModel, len(d.LoadModels))
	for _, m := range d.LoadModels {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		k := legKey(m.Mode, m.VehicleType)
		if _, dup := d.loadIdx[k]; dup {
			return nil, fmt.Errorf("duplicate load model for %s", k)
		}
		d.loadIdx[k] = m
	}
	if len(d.Grids) == 0 {
		g, note, err := defaultGrids()
		if err != nil {
			return nil, err
		}
		d.Grids, d.GridNote = g, note
	}
	d.gridIdx = make(map[string]calc.GridFactor, len(d.Grids))
	for _, g := range d.Grids {
		c := strings.ToUpper(g.Country)
		if len(c) != 3 || g.G < 0 {
			return nil, fmt.Errorf("grid factor %q: need ISO alpha-3 country and g_per_kwh >= 0", g.Country)
		}
		if _, dup := d.gridIdx[c]; dup {
			return nil, fmt.Errorf("duplicate grid factor for %s", c)
		}
		d.gridIdx[c] = g
	}
	for name, t := range d.ContainerLoads {
		if t <= 0 {
			return nil, fmt.Errorf("container load %q must be > 0 tonnes per TEU", name)
		}
	}
	for alias, id := range d.FuelAliases {
		target, ok := d.fuelIdx[id]
		if !ok {
			return nil, fmt.Errorf("fuel alias %s points to unknown %s", alias, id)
		}
		if _, clash := d.fuelIdx[alias]; clash {
			return nil, fmt.Errorf("fuel alias %s clashes with an existing fuel", alias)
		}
		d.fuelIdx[alias] = target
	}
	cargo := map[string]bool{}
	for _, ct := range d.CargoTypes {
		cargo[ct.Name] = true
	}
	for vt, m := range d.CargoVariants {
		var mode calc.Mode
		for _, f := range d.Legs {
			if f.VehicleType == vt {
				mode = f.Mode
			}
		}
		if mode == "" {
			return nil, fmt.Errorf("cargo_variants: unknown vehicle type %s", vt)
		}
		for c, t := range m {
			f, ok := d.legIdx[legKey(mode, t)]
			if !ok || !cargo[c] {
				return nil, fmt.Errorf("cargo_variants %s: cargo type %q or vehicle type %s unknown", vt, c, t)
			}
			if d.cargoIdx == nil {
				d.cargoIdx = map[string]map[string]calc.Factor{}
			}
			k := legKey(mode, vt)
			if d.cargoIdx[k] == nil {
				d.cargoIdx[k] = map[string]calc.Factor{}
			}
			d.cargoIdx[k][c] = f
		}
	}
	for ht, m := range d.HubCargoVariants {
		for c, t := range m {
			if _, ok := d.hubIdx[t]; !ok || !cargo[c] {
				return nil, fmt.Errorf("hub_cargo_variants %s: cargo type %q or hub type %s unknown", ht, c, t)
			}
		}
	}
	for alias, fr := range d.FuelRegions {
		if _, ok := d.fuelIdx[alias]; !ok {
			return nil, fmt.Errorf("fuel_regions %s: no fuel or fuel alias of that name", alias)
		}
		known := map[string]bool{}
		for _, r := range d.Regions {
			if r.Set == fr.RegionSet {
				known[r.Name] = true
			}
		}
		m := map[string]calc.FuelFactor{}
		for region, id := range fr.Regions {
			f, ok := d.fuelIdx[id]
			if !ok || !known[region] {
				return nil, fmt.Errorf("fuel_regions %s: region %q in set %q or fuel %s unknown", alias, region, fr.RegionSet, id)
			}
			m[region] = f
		}
		if d.fuelReg == nil {
			d.fuelReg = map[string]map[string]calc.FuelFactor{}
		}
		d.fuelReg[alias] = m
	}
	return &d, nil
}

func legKey(m calc.Mode, vt string) string { return string(m) + "/" + vt }

// shortAlias is an alias whose meaning depends on the leg's distance.
type shortAlias struct {
	alias       string
	belowKm     float64
	short, long calc.Factor
}

// LegFactorFor implements calc.DistanceFactorProvider: aliases with a
// short_vehicle_type mean it on legs shorter than short_below_km.
func (d *Dataset) LegFactorFor(m calc.Mode, vt string, km float64) (calc.Factor, string, error) {
	a, ok := d.shortIdx[legKey(m, vt)]
	if !ok {
		f, err := d.LegFactor(m, vt)
		return f, "", err
	}
	if km < a.belowKm {
		return a.short, fmt.Sprintf("%q on a %.0f km leg means %s (legs under %.0f km).", a.alias, km, a.short.VehicleType, a.belowKm), nil
	}
	return a.long, fmt.Sprintf("%q on a %.0f km leg means %s (legs of %.0f km or more).", a.alias, km, a.long.VehicleType, a.belowKm), nil
}

// LegFactor implements calc.FactorProvider.
func (d *Dataset) LegFactor(m calc.Mode, vt string) (calc.Factor, error) {
	if f, ok := d.legIdx[legKey(m, vt)]; ok {
		return f, nil
	}
	for _, f := range d.Legs {
		if f.Mode == m {
			return calc.Factor{}, fmt.Errorf("no factor for %s/%s in dataset %q", m, vt, d.Name)
		}
	}
	return calc.Factor{}, fmt.Errorf("dataset %q has no %s factors; add a layer with your own (-factors %s,mine.json) "+
		"or build one from fuel data with sootcase toc", d.Name, m, d.Name)
}

// HubFactor implements calc.FactorProvider.
func (d *Dataset) HubFactor(t string) (calc.HubFactor, error) {
	if h, ok := d.hubIdx[t]; ok {
		return h, nil
	}
	return calc.HubFactor{}, fmt.Errorf("no hub factor for %s in dataset %q", t, d.Name)
}

// FuelFactor implements calc.FactorProvider. "electricity/<ISO3>" is
// derived from the grid table, per kWh, all upstream (WTT).
func (d *Dataset) FuelFactor(fuel string) (calc.FuelFactor, error) {
	if f, ok := d.fuelIdx[fuel]; ok {
		return f, nil
	}
	if c, ok := strings.CutPrefix(fuel, "electricity/"); ok {
		g, err := d.Grid(c)
		if err != nil {
			return calc.FuelFactor{}, err
		}
		return calc.FuelFactor{Fuel: fuel, Unit: "kWh", TTW: 0, WTT: g.G / 1000, Year: g.Year,
			Source: fmt.Sprintf("%s grid %g g/kWh (%d, %s)", g.Country, g.G, g.Year, g.Source)}, nil
	}
	return calc.FuelFactor{}, fmt.Errorf("no fuel factor for %s in dataset %q", fuel, d.Name)
}

// LoadModel implements calc.LoadModelProvider. vt can be the model's class
// id, or any vehicle type or alias of that class (e.g. "hgv", or a DESNZ id
// ending in "/average_laden"): the load suffix is dropped to find the class.
func (d *Dataset) LoadModel(m calc.Mode, vt string) (calc.LoadModel, error) {
	if lm, ok := d.loadIdx[legKey(m, vt)]; ok {
		return lm, nil
	}
	if class, ok := d.aliasLM[legKey(m, vt)]; ok {
		if lm, ok := d.loadIdx[legKey(m, class)]; ok {
			return lm, nil
		}
	}
	if f, ok := d.legIdx[legKey(m, vt)]; ok {
		if i := strings.LastIndex(f.VehicleType, "/"); i > 0 && strings.HasSuffix(f.VehicleType, "laden") {
			if lm, ok := d.loadIdx[legKey(m, f.VehicleType[:i])]; ok {
				return lm, nil
			}
		}
	}
	return calc.LoadModel{}, fmt.Errorf("no load model for %s/%s in dataset %q", m, vt, d.Name)
}

func dafKey(m calc.Mode, from, to string) string { return string(m) + " " + from + "->" + to }

// DAF implements calc.DAFProvider.
func (d *Dataset) DAF(m calc.Mode, from, to string) (calc.DAF, bool) {
	x, ok := d.dafIdx[dafKey(m, from, to)]
	return x, ok
}

// DatasetName implements calc.DatasetNamer.
func (d *Dataset) DatasetName() string { return d.Name }

// Grid implements calc.GridProvider. country is ISO 3166-1 alpha-3.
func (d *Dataset) Grid(country string) (calc.GridFactor, error) {
	if g, ok := d.gridIdx[strings.ToUpper(strings.TrimSpace(country))]; ok {
		return g, nil
	}
	return calc.GridFactor{}, fmt.Errorf("no electricity grid factor for country %q (use ISO 3166 alpha-3, e.g. DNK)", country)
}

var (
	gridOnce  sync.Once
	gridCache []calc.GridFactor
	gridNote  string
	gridErr   error
)

func defaultGrids() ([]calc.GridFactor, string, error) {
	gridOnce.Do(func() {
		b, err := embedded.ReadFile(DefaultGrid)
		if err != nil {
			gridErr = err
			return
		}
		var f struct {
			Note  string            `json:"note"`
			Grids []calc.GridFactor `json:"grids"`
		}
		gridErr = json.Unmarshal(b, &f)
		gridCache, gridNote = f.Grids, f.Note
	})
	return gridCache, gridNote, gridErr
}

// ContainerLoad implements calc.ContainerLoadProvider.
func (d *Dataset) ContainerLoad(name string) (float64, error) {
	if t, ok := d.ContainerLoads[name]; ok {
		return t, nil
	}
	return 0, fmt.Errorf("no container load profile %q in dataset %q", name, d.Name)
}

// Distances returns a great-circle provider using the dataset's adjustments.
func (d *Dataset) Distances() distance.GreatCircle {
	g, _ := distance.NewGreatCircle(d.DistanceAdjustments) // validated in Parse
	return g
}

var (
	_ calc.FactorProvider        = (*Dataset)(nil)
	_ calc.ContainerLoadProvider = (*Dataset)(nil)
	_ calc.LoadModelProvider     = (*Dataset)(nil)
	_ calc.GridProvider          = (*Dataset)(nil)
	_ calc.DatasetNamer          = (*Dataset)(nil)
	_ calc.DAFProvider           = (*Dataset)(nil)
)

// CargoFactor implements calc.CargoProvider.
func (d *Dataset) CargoFactor(m calc.Mode, f calc.Factor, cargo string) (calc.Factor, string, error) {
	if err := d.knownCargo(cargo); err != nil || cargo == "average" {
		return f, "", err
	}
	if v, ok := d.cargoIdx[legKey(m, f.VehicleType)][cargo]; ok {
		return v, fmt.Sprintf("Cargo type %q: %s.", cargo, v.VehicleType), nil
	}
	return f, fmt.Sprintf("Cargo type %q: the dataset has no value for it with %s, so that vehicle's own value is used.", cargo, f.VehicleType), nil
}

// HubCargo implements calc.CargoProvider.
func (d *Dataset) HubCargo(hubType, cargo string) (string, string, error) {
	if err := d.knownCargo(cargo); err != nil || cargo == "average" {
		return hubType, "", err
	}
	if t, ok := d.HubCargoVariants[hubType][cargo]; ok {
		return t, fmt.Sprintf("Cargo type %q: hub type %s.", cargo, t), nil
	}
	return hubType, fmt.Sprintf("Cargo type %q: the dataset has no value for it at a %s hub, so that hub type's own value is used.", cargo, hubType), nil
}

// ValidCargo implements calc.CargoProvider.
func (d *Dataset) ValidCargo(cargo string) error { return d.knownCargo(cargo) }

func (d *Dataset) knownCargo(cargo string) error {
	var names []string
	for _, ct := range d.CargoTypes {
		if ct.Name == cargo {
			return nil
		}
		names = append(names, ct.Name)
	}
	return fmt.Errorf("%w: unknown cargo_type %q (known: %s)", calc.ErrInvalidInput, cargo, strings.Join(names, ", "))
}
