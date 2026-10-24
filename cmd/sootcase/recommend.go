package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/recommend"
)

// runRecommend lists route changes that lower a shipments CSV's emissions.
func runRecommend(args []string) error {
	fs := flag.NewFlagSet("recommend", flag.ExitOnError)
	var c common
	c.register(fs)
	format := fs.String("format", "text", "text or json")
	settingsFile := fs.String("settings", "", "settings JSON file (kinds, min_road_km, ...; default: all kinds, stated defaults)")
	workers := fs.Int("workers", 0, "parallel calculations (default: one per CPU)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("recommend: expected a shipments CSV")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("recommend: -format must be text or json")
	}
	calculator, _, err := c.calculator()
	if err != nil {
		return err
	}
	var set recommend.Settings
	if *settingsFile != "" {
		sf, err := open(*settingsFile)
		if err != nil {
			return err
		}
		defer sf.Close()
		dec := json.NewDecoder(sf)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&set); err != nil {
			return fmt.Errorf("recommend: settings: %w", err)
		}
	}
	f, err := open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	items, err := csvio.Read(f)
	if err != nil {
		return err
	}
	var shipments []calc.Shipment
	var bad []recommend.ShipmentResult
	for _, it := range items {
		if it.Err != nil {
			bad = append(bad, recommend.ShipmentResult{ShipmentID: it.Shipment.ID, Error: it.Err.Error()})
		} else {
			shipments = append(shipments, it.Shipment)
		}
	}
	n := *workers
	if n <= 0 {
		n = runtime.NumCPU()
	}
	res, err := recommend.Run(context.Background(), calculator, shipments, set, n)
	if err != nil {
		return err
	}
	res.Shipments = append(res.Shipments, bad...)
	res.Failed += len(bad)
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	writeRecommend(os.Stdout, res)
	return nil
}

func writeRecommend(w io.Writer, r recommend.Result) {
	fmt.Fprintln(w, r.Label)
	fmt.Fprintf(w, "\nNow: %.1f kg CO2e WTW. With each shipment's best alternative: %.1f kg (%.1f kg, %.1f%% less).\n",
		r.Now.WTWKg, r.Best.WTWKg, r.SavedKg, r.SavedPct)
	fmt.Fprintln(w, "\nBy kind (kinds overlap; don't add them up):")
	for _, k := range r.Kinds {
		fmt.Fprintf(w, "  %-18s %5d shipments  %12.1f kg saved  best for %d\n", k.Kind, k.Shipments, k.SavedKg, k.BestFor)
	}
	for _, k := range recommend.Kinds() {
		if why, ok := r.Skipped[k]; ok {
			fmt.Fprintf(w, "  %-18s skipped: %s\n", k, why)
		}
	}
	fmt.Fprintln(w, "\nLanes, biggest saving first:")
	for i, l := range r.Lanes {
		if i == 20 {
			fmt.Fprintf(w, "  … %d more lanes (use -format json)\n", len(r.Lanes)-20)
			break
		}
		kind := l.BestKind
		if kind == "" {
			kind = "no lower alternative"
		}
		fmt.Fprintf(w, "  %-50s %4d  %10.1f → %10.1f kg  %s\n", l.Lane, l.Shipments, l.NowKg, l.BestKg, kind)
	}
	fmt.Fprintln(w, "\nBased on:")
	for _, s := range r.Sources {
		fmt.Fprintf(w, "  - %s\n", s)
	}
	fmt.Fprintln(w, "\nNot taken into account:")
	for _, s := range r.Limitations {
		fmt.Fprintf(w, "  - %s\n", s)
	}
	if r.Failed > 0 {
		fmt.Fprintf(w, "\n%d shipments failed:\n", r.Failed)
		for _, s := range r.Shipments {
			if s.Error != "" {
				fmt.Fprintf(w, "   %s: %s\n", s.ShipmentID, strings.TrimSpace(s.Error))
			}
		}
	}
}
