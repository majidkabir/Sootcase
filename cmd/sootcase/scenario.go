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
	"github.com/majidkabir/sootcase/pkg/scenario"
)

// runScenario compares a shipments CSV as it is with a plan applied.
func runScenario(args []string) error {
	fs := flag.NewFlagSet("scenario", flag.ExitOnError)
	var c common
	c.register(fs)
	format := fs.String("format", "text", "text or json")
	workers := fs.Int("workers", 0, "parallel calculations (default: one per CPU)")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("scenario: expected a shipments CSV and a plan JSON file")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("scenario: -format must be text or json")
	}
	calculator, _, err := c.calculator()
	if err != nil {
		return err
	}
	pf, err := open(fs.Arg(1))
	if err != nil {
		return err
	}
	defer pf.Close()
	var plan scenario.Plan
	dec := json.NewDecoder(pf)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return fmt.Errorf("scenario: plan: %w", err)
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
	var bad []scenario.ShipmentResult
	for _, it := range items {
		if it.Err != nil {
			bad = append(bad, scenario.ShipmentResult{ShipmentID: it.Shipment.ID, Error: it.Err.Error()})
		} else {
			shipments = append(shipments, it.Shipment)
		}
	}
	n := *workers
	if n <= 0 {
		n = runtime.NumCPU()
	}
	res, err := scenario.RunParallel(context.Background(), calculator, shipments, plan, n)
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
	writeScenario(os.Stdout, res)
	return nil
}

func writeScenario(w io.Writer, r scenario.Result) {
	if r.Plan != "" {
		fmt.Fprintf(w, "Scenario: %s\n", r.Plan)
	}
	fmt.Fprintln(w, r.Label)
	fmt.Fprintf(w, "\n%-10s %14s %14s\n", "", "now", "scenario")
	fmt.Fprintf(w, "%-10s %11.1f kg %11.1f kg\n", "TTW", r.Base.TTWKg, r.Scenario.TTWKg)
	fmt.Fprintf(w, "%-10s %11.1f kg %11.1f kg\n", "WTT", r.Base.WTTKg, r.Scenario.WTTKg)
	fmt.Fprintf(w, "%-10s %11.1f kg %11.1f kg\n", "WTW", r.Base.WTWKg, r.Scenario.WTWKg)
	fmt.Fprintf(w, "\nChange: %+.1f kg CO2e WTW (%+.1f%%)\n", r.DiffKg, r.DiffPct)
	for i, c := range r.Changes {
		fmt.Fprintf(w, "\n%d. %s, share %.0f%%: %d shipments, %d legs\n", i+1, c.Type, *c.Share*100, c.ShipmentsAffected, c.LegsAffected)
		for _, s := range c.Sources {
			fmt.Fprintf(w, "   - %s\n", s)
		}
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
