package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/batch"
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/ileap"
	"github.com/majidkabir/sootcase/pkg/places"
	"github.com/majidkabir/sootcase/pkg/toc"
)

// runILEAP writes shipments (and/or an operator's TOCs and HOCs) in the
// iLEAP data model, listing what iLEAP requires that the data lacks.
func runILEAP(args []string) error {
	fs := flag.NewFlagSet("ileap", flag.ExitOnError)
	var c common
	c.register(fs)
	company := fs.String("company", "", "name of the company owning the data (iLEAP companyName)")
	ids := fs.String("company-ids", "", "comma-separated ids of that company, e.g. urn:... (optional)")
	from := fs.String("from", "", "first day of the period the data covers, YYYY-MM-DD")
	to := fs.String("to", "", "last day of the period the data covers, YYYY-MM-DD")
	opsFile := fs.String("operations", "", "operations JSON (as for \"sootcase toc\"): export its TOCs and HOCs, and use them for the shipments")
	_ = fs.Parse(args)
	if fs.NArg() > 1 || (fs.NArg() == 0 && *opsFile == "") {
		return errors.New("ileap: expected a shipments CSV file, -operations file.json, or both")
	}
	o := ileap.Options{CompanyName: *company, CreatedAt: time.Now(), Country: places.Codes{}.Country}
	for _, id := range strings.Split(*ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			o.CompanyIDs = append(o.CompanyIDs, id)
		}
	}
	var err error
	if o.PeriodStart, o.PeriodEnd, err = period(*from, *to); err != nil {
		return fmt.Errorf("ileap: %w", err)
	}
	ds, err := c.dataset()
	if err != nil {
		return err
	}
	o.Fuels = ds
	var opsEx *ileap.Export
	if *opsFile != "" {
		f, err := open(*opsFile)
		if err != nil {
			return err
		}
		ops, err := toc.Parse(f)
		f.Close()
		if err != nil {
			return err
		}
		ex := ileap.FromOperations(ops, ds, o)
		opsEx = &ex
		o.Operator = ileap.Operator(ex)
		if fs.NArg() == 1 {
			// The shipments are calculated with the operator's categories layered on.
			layer, err := toc.Build(ops, ds)
			if err != nil {
				return err
			}
			path := filepath.Join(os.TempDir(), fmt.Sprintf("sootcase-ileap-%d.json", os.Getpid()))
			raw, _ := json.Marshal(layer)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				return err
			}
			defer os.Remove(path)
			base := c.factorFile
			if base == "" {
				base = factors.Default
			}
			c.factorFile = base + "," + path
		}
	}
	out := ileap.Export{SpecVersion: ileap.SpecVersion}
	if fs.NArg() == 1 {
		calculator, _, err := c.calculator()
		if err != nil {
			return err
		}
		f, err := open(fs.Arg(0))
		if err != nil {
			return err
		}
		defer f.Close()
		r, err := csvio.NewReader(bufio.NewReaderSize(f, 1<<20))
		if err != nil {
			return err
		}
		var results []calc.Result
		var failed []string
		stats, err := batch.Process(context.Background(), calculator, r, 0,
			func(it csvio.Item, res calc.Result, err error) error {
				if err != nil {
					failed = append(failed, fmt.Sprintf("shipment %s: %v", it.Shipment.ID, err))
				} else {
					results = append(results, res)
				}
				return nil
			})
		if err != nil {
			return err
		}
		if stats.Shipments == 0 {
			return errNoShipments
		}
		out = ileap.FromResults(results, o)
		for _, m := range failed {
			out.Notes = append(out.Notes, "Not exported, the calculation failed: "+m)
		}
	}
	if opsEx != nil {
		out = ileap.Merge(out, *opsEx)
	}
	fmt.Fprintf(os.Stderr, "%d shipment footprints, %d TOCs, %d HOCs; conformant: %v (%d gaps)\n",
		len(out.ShipmentFootprints), len(out.TOCs), len(out.HOCs), out.Conformant, len(out.Gaps))
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// period turns the first and last day of a period into iLEAP's start and
// (exclusive) end; both empty is no period.
func period(from, to string) (time.Time, time.Time, error) {
	if from == "" && to == "" {
		return time.Time{}, time.Time{}, nil
	}
	a, err1 := time.Parse(time.DateOnly, from)
	b, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil || b.Before(a) {
		return time.Time{}, time.Time{}, errors.New("give the period's first and last day as YYYY-MM-DD")
	}
	return a, b.AddDate(0, 0, 1), nil
}
