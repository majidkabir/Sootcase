package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/majidkabir/sootcase/internal/desnz"
	"github.com/majidkabir/sootcase/internal/grid"
	"github.com/majidkabir/sootcase/internal/xlsx"
	"github.com/majidkabir/sootcase/pkg/toc"
)

// runImportDESNZ converts the UK DESNZ flat-file spreadsheet to a dataset.
func runImportDESNZ(args []string) error {
	fs := flag.NewFlagSet("import-desnz", flag.ExitOnError)
	out := fs.String("o", "-", "output JSON file (default stdout)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("import-desnz: expected the DESNZ flat-file .xlsx")
	}

	fh, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return err
	}
	book, err := xlsx.Open(fh, st.Size())
	if err != nil {
		return err
	}

	// The factor sheet name has varied between years, so try each sheet.
	var res desnz.Result
	var lastErr error
	found := false
	for _, name := range book.SheetNames() {
		rows, err := book.Rows(name)
		if err != nil {
			return err
		}
		if res, lastErr = desnz.Parse(rows); lastErr == nil {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("import-desnz: no sheet with flat-file factors: %w", lastErr)
	}

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	fmt.Fprintf(os.Stderr, "%s: %d leg factors, %d fuel factors, %d blank rows skipped, %d warnings\n",
		res.Dataset.Name, len(res.Dataset.Legs), len(res.Dataset.Fuels), res.Blank, len(res.Warnings))

	w := os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res.Dataset)
}

// runImportGrid converts Our World in Data's owid-energy-data.csv to a grid table.
func runImportGrid(args []string) error {
	fs := flag.NewFlagSet("import-grid", flag.ExitOnError)
	out := fs.String("o", "-", "output JSON file (default stdout)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("import-grid: expected owid-energy-data.csv")
	}
	f, err := open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := grid.ParseOWID(f)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: %d countries\n", g.Name, len(g.Grids))
	w := os.Stdout
	if *out != "-" {
		fh, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer fh.Close()
		w = fh
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}

// runTOC builds a dataset layer from measured operational data.
func runTOC(args []string) error {
	fs := flag.NewFlagSet("toc", flag.ExitOnError)
	var c common
	c.register(fs)
	out := fs.String("o", "-", "output JSON file (default stdout)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("toc: expected an operations JSON file")
	}
	ds, err := c.dataset()
	if err != nil {
		return err
	}
	f, err := open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	ops, err := toc.Parse(f)
	if err != nil {
		return err
	}
	layer, err := toc.Build(ops, ds)
	if err != nil {
		return err
	}
	for _, l := range layer.Legs {
		fmt.Fprintf(os.Stderr, "TOC %-24s %8.2f g CO2e/tkm WTW (%.2f TTW + %.2f WTT)\n", l.VehicleType, l.WTW(), l.TTW, l.WTT)
	}
	for _, h := range layer.Hubs {
		if h.TTWPerTonneG != nil && h.WTTPerTonneG != nil {
			fmt.Fprintf(os.Stderr, "HOC %-24s %8.1f g CO2e/t WTW (%.1f TTW + %.1f WTT)\n", h.HubType, h.WTWPerTonneG, *h.TTWPerTonneG, *h.WTTPerTonneG)
		} else {
			fmt.Fprintf(os.Stderr, "HOC %-24s %8.1f g CO2e/t WTW\n", h.HubType, h.WTWPerTonneG)
		}
	}
	w := os.Stdout
	if *out != "-" {
		fh, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer fh.Close()
		w = fh
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(layer)
}
