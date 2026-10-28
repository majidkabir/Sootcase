package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/majidkabir/sootcase/pkg/batch"
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/report"
)

// runReport calculates a shipments CSV and prints an ISO 14083 style
// report over all of it.
func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var c common
	c.register(fs)
	var m report.Meta
	fs.StringVar(&m.Title, "title", "", "report title")
	fs.StringVar(&m.Organisation, "org", "", "organisation reporting")
	fs.StringVar(&m.PeriodFrom, "from", "", "start of the period covered, YYYY-MM-DD")
	fs.StringVar(&m.PeriodTo, "to", "", "end of the period covered, YYYY-MM-DD")
	fs.StringVar(&m.Reference, "reference", "", "where supporting information can be found")
	fs.BoolVar(&m.DeclareISO, "declare-iso", false,
		"add the ISO 14083:2023 conformity statement (only if your data collection and boundaries conform)")
	fs.IntVar(&m.Scope3Category, "scope3-category", 0,
		"GHG Protocol scope 3 category for the reporting company: 4 (transport it pays for) or 9 (sold products, transport it doesn't pay for)")
	format := fs.String("format", "md", "md or json")
	workers := fs.Int("workers", 0, "parallel calculations (default: one per CPU)")
	ungrouped := fs.Bool("ungrouped", false, "allow a shipment's rows anywhere in the file (loads the whole file into memory)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("report: expected one CSV file")
	}
	if m.Scope3Category != 0 && m.Scope3Category != 4 && m.Scope3Category != 9 {
		return fmt.Errorf("report: -scope3-category must be 4 or 9")
	}
	if *format != "md" && *format != "json" {
		return fmt.Errorf("report: -format must be md or json")
	}
	for _, d := range []string{m.PeriodFrom, m.PeriodTo} {
		if _, err := time.Parse(time.DateOnly, d); d != "" && err != nil {
			return fmt.Errorf("report: dates must be YYYY-MM-DD, got %q", d)
		}
	}
	calculator, _, err := c.calculator()
	if err != nil {
		return err
	}
	f, err := open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()

	var src batch.Source
	if *ungrouped {
		items, err := csvio.Read(f)
		if err != nil {
			return err
		}
		src = &sliceSource{items: items}
	} else {
		r, err := csvio.NewReader(bufio.NewReaderSize(f, 1<<20))
		if err != nil {
			return err
		}
		src = r
	}

	b := report.New(m)
	stats, err := batch.Process(context.Background(), calculator, src, *workers,
		func(it csvio.Item, res calc.Result, err error) error {
			b.Add(it.Shipment.ID, res, err)
			return nil
		})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d shipments, %d failed\n", stats.Shipments, stats.Failed)
	rep := b.Report()
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	return report.WriteMarkdown(os.Stdout, rep)
}
