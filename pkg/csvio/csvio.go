// Package csvio reads shipments from CSV and writes results as CSV.
//
// Input has a header row; column order is free and unknown columns are
// ignored. One row per leg, or per hub stop when hub_type is set. Rows are
// grouped into shipments by shipment_id: anywhere in the file for Read,
// next to each other for the streaming Reader.
//
//	shipment_id                                     required
//	weight_kg, or teu with teu_net_tonnes or container_load   cargo
//	leg_id, mode, vehicle_type, distance_km         leg
//	distance_type                                   leg: sfd, gcd or actual (optional)
//	from_lat, from_lon, to_lat, to_lon              leg, used when distance_km is empty
//	from_code, to_code                              leg, UN/LOCODE or IATA code instead of coordinates
//	fuel, fuel_amount, vehicle_tonne_km             leg, fuel-based method
//	load_factor or load_tonnes, empty_trip_factor   leg, load model method
//	repositioning_km                                leg, empty km for this trip (trucks)
//	energy_kwh_per_tkm, country                     leg, energy method
//	hub_id, hub_type                                hub stop
//	hub_containers                                  hub stop, containers handled (per-container hub types)
package csvio

import (
	"encoding/csv"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Item is a parsed shipment, or the error that stopped it from parsing.
type Item struct {
	Shipment calc.Shipment
	Err      error
	// Line is the input line where the shipment starts (1 is the first
	// line of the file, including any header).
	Line int
}

// Read parses all shipments from r into memory. Row-level problems are
// reported on the affected shipment's Item, so one bad row doesn't fail the
// whole file. Structural problems (no header, missing required columns)
// return an error. A shipment's rows may be anywhere in the file; for large
// files use Reader, which streams but needs each shipment's rows together.
func Read(r io.Reader) ([]Item, error) {
	cr, col, err := open(r)
	if err != nil {
		return nil, err
	}
	var items []*Item
	byID := map[string]*Item{}
	line := 1
	for {
		rec, err := cr.Read()
		line++
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		row := rowReader{rec: rec, col: col}
		id := row.str("shipment_id")
		if id == "" {
			return nil, fmt.Errorf("line %d: empty shipment_id", line)
		}
		it, ok := byID[id]
		if !ok {
			it = &Item{Shipment: calc.Shipment{ID: id}, Line: line}
			byID[id] = it
			items = append(items, it)
		}
		if it.Err != nil {
			continue
		}
		if err := addRow(&it.Shipment, row); err != nil {
			it.Err = fmt.Errorf("line %d: %w", line, err)
		}
	}

	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = *it
	}
	return out, nil
}

func open(r io.Reader) (*csv.Reader, map[string]int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	if _, ok := col["shipment_id"]; !ok {
		return nil, nil, errors.New(`missing required column "shipment_id"`)
	}
	_, hasW := col["weight_kg"]
	_, hasT := col["teu"]
	if !hasW && !hasT {
		return nil, nil, errors.New(`need a "weight_kg" or "teu" column`)
	}
	cr.ReuseRecord = true
	return cr, col, nil
}

// Reader streams shipments from CSV one at a time, so memory stays flat
// however large the file. Each shipment's rows must be next to each other
// (as when the file is sorted or grouped by shipment_id); a shipment_id
// that reappears after another shipment is returned as an error Item.
type Reader struct {
	cr      *csv.Reader
	col     map[string]int
	line    int
	cur     *Item
	curLine int
	next    []string // a row read ahead that starts the next shipment
	// seen maps a 64-bit hash of each finished shipment_id to its first
	// line, to catch ungrouped files without keeping every id in memory.
	seen map[uint64]int
	done bool
}

// NewReader reads the header and returns a streaming Reader.
func NewReader(r io.Reader) (*Reader, error) {
	cr, col, err := open(r)
	if err != nil {
		return nil, err
	}
	return &Reader{cr: cr, col: col, line: 1, seen: map[uint64]int{}}, nil
}

// Next returns the next shipment, or io.EOF after the last one. Other
// errors are structural (malformed CSV, empty shipment_id) and end the read.
func (r *Reader) Next() (Item, error) {
	for {
		var rec []string
		if r.next != nil {
			rec, r.next = r.next, nil
		} else if !r.done {
			var err error
			rec, err = r.cr.Read()
			r.line++
			switch {
			case errors.Is(err, io.EOF):
				r.done = true
			case err != nil:
				return Item{}, fmt.Errorf("line %d: %w", r.line, err)
			}
		}
		if rec == nil {
			if r.cur == nil {
				return Item{}, io.EOF
			}
			it := *r.cur
			r.cur = nil
			return it, nil
		}

		row := rowReader{rec: rec, col: r.col}
		id := row.str("shipment_id")
		if id == "" {
			return Item{}, fmt.Errorf("line %d: empty shipment_id", r.line)
		}
		if r.cur != nil && r.cur.Shipment.ID != id {
			// Hold this row for the next call; it starts another shipment.
			r.next = append([]string(nil), rec...)
			it := *r.cur
			r.cur = nil
			return it, nil
		}
		if r.cur == nil {
			r.cur = &Item{Shipment: calc.Shipment{ID: id}, Line: r.line}
			h := fnv.New64a()
			_, _ = h.Write([]byte(id))
			if first, dup := r.seen[h.Sum64()]; dup {
				r.cur.Err = fmt.Errorf("line %d: rows for shipment %q must be together (first seen on line %d)", r.line, id, first)
			} else {
				r.seen[h.Sum64()] = r.line
			}
		}
		if r.cur.Err == nil {
			if err := addRow(&r.cur.Shipment, row); err != nil {
				r.cur.Err = fmt.Errorf("line %d: %w", r.line, err)
			}
		}
	}
}

func addRow(s *calc.Shipment, row rowReader) error {
	// Shipment-level values may be given on the first row only.
	for _, f := range []struct {
		col string
		dst *float64
	}{{"weight_kg", &s.WeightKg}, {"teu", &s.TEU}, {"teu_net_tonnes", &s.TEUNetTonnes}} {
		v, err := row.float(f.col)
		if err != nil {
			return err
		}
		switch {
		case v == 0:
		case *f.dst == 0:
			*f.dst = v
		case *f.dst != v:
			return fmt.Errorf("%s %v conflicts with earlier %v", f.col, v, *f.dst)
		}
	}
	if cl := row.str("container_load"); cl != "" {
		if s.ContainerLoad != "" && s.ContainerLoad != cl {
			return fmt.Errorf("container_load %q conflicts with earlier %q", cl, s.ContainerLoad)
		}
		s.ContainerLoad = cl
	}

	if ht := row.str("hub_type"); ht != "" {
		n, err := row.float("hub_containers")
		if err != nil {
			return err
		}
		s.Hubs = append(s.Hubs, calc.HubStop{ID: row.str("hub_id"), HubType: ht, Containers: n})
		return nil
	}

	leg := calc.Leg{
		ID:           row.str("leg_id"),
		Mode:         calc.Mode(strings.ToLower(row.str("mode"))),
		VehicleType:  row.str("vehicle_type"),
		DistanceType: strings.ToLower(row.str("distance_type")),
	}
	if leg.Mode == "" {
		return errors.New("leg row without mode")
	}
	var errs []error
	get := func(name string) float64 {
		v, err := row.float(name)
		errs = append(errs, err)
		return v
	}
	leg.DistanceKm = get("distance_km")
	leg.From = calc.Location{Code: row.str("from_code"), Lat: get("from_lat"), Lon: get("from_lon")}
	leg.To = calc.Location{Code: row.str("to_code"), Lat: get("to_lat"), Lon: get("to_lon")}
	leg.LoadFactor = get("load_factor")
	leg.LoadTonnes = get("load_tonnes")
	leg.EmptyTripFactor = get("empty_trip_factor")
	leg.RepositioningKm = get("repositioning_km")
	leg.EnergyKWhPerTkm = get("energy_kwh_per_tkm")
	leg.Country = row.str("country")
	if fuel := row.str("fuel"); fuel != "" {
		leg.Fuel = &calc.FuelUse{Fuel: fuel, Amount: get("fuel_amount"), VehicleTonneKm: get("vehicle_tonne_km")}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	s.Legs = append(s.Legs, leg)
	return nil
}

type rowReader struct {
	rec []string
	col map[string]int
}

func (r rowReader) str(name string) string {
	i, ok := r.col[name]
	if !ok || i >= len(r.rec) {
		return ""
	}
	return strings.TrimSpace(r.rec[i])
}

// float returns 0 for an empty or missing cell.
func (r rowReader) float(name string) (float64, error) {
	s := r.str(name)
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", name, s)
	}
	return v, nil
}

// ResultHeader is the header row written by Writer.
// added_road_legs counts the road legs the calculator added; warnings are
// the result's warnings, joined by " | ". The operator_* columns split WTW
// by GHG Protocol scope for the operator of the vehicles (calc.GHGScopes);
// for a shipper all of wtw_kg is scope 3, category 4 or 9.
var ResultHeader = []string{"shipment_id", "weight_kg", "tonne_km", "ttw_kg", "wtt_kg", "wtw_kg",
	"intensity_g_per_tkm", "primary_data_share", "dataset", "error", "added_road_legs", "warnings",
	"operator_scope1_kg", "operator_scope2_kg", "operator_scope3_cat3_kg", "hubs_kg"}

// Writer writes one summary row per shipment.
type Writer struct{ cw *csv.Writer }

// NewWriter writes the header and returns a Writer.
func NewWriter(w io.Writer) (*Writer, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(ResultHeader); err != nil {
		return nil, err
	}
	return &Writer{cw: cw}, nil
}

// Write writes a result, or the error for that shipment.
func (w *Writer) Write(id string, r calc.Result, err error) error {
	if err != nil {
		return w.cw.Write([]string{id, "", "", "", "", "", "", "", "", err.Error(), "", "", "", "", "", ""})
	}
	added := 0
	for _, l := range r.Legs {
		if l.Added != "" {
			added++
		}
	}
	return w.cw.Write([]string{id, num(r.WeightKg), num(r.TonneKm), num(r.TTWKg), num(r.WTTKg), num(r.WTWKg),
		num(r.IntensityG), num(r.PrimaryDataShare), r.Dataset, "", strconv.Itoa(added), strings.Join(r.Warnings, " | "),
		num(r.GHGProtocol.OperatorScope1Kg), num(r.GHGProtocol.OperatorScope2Kg), num(r.GHGProtocol.OperatorScope3Cat3Kg), num(r.GHGProtocol.HubsKg)})
}

// Flush flushes buffered rows and reports any write error.
func (w *Writer) Flush() error {
	w.cw.Flush()
	return w.cw.Error()
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
