package factors_test

import (
	"fmt"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Look up factors by alias or full id; results carry the full id and source.
func ExampleDataset_LegFactor() {
	ds, err := factors.Open(factors.Default)
	if err != nil {
		panic(err)
	}
	f, err := ds.LegFactor(calc.Road, "hgv")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s: %.2f g CO2e/tkm WTW (%d), per %s\n", f.VehicleType, f.WTW(), f.Year, f.DistanceBasis)
	// Output:
	// glec/road/eu_sa/rigid_26_32t/diesel: 125.00 g CO2e/tkm WTW (2026), per sfd
}

// Grid intensities come with every dataset; "electricity/<ISO3>" works as a fuel.
func ExampleDataset_Grid() {
	ds, _ := factors.Open(factors.Default)
	g, _ := ds.Grid("DNK")
	f, _ := ds.FuelFactor("electricity/DNK")
	fmt.Printf("%s %d: %.2f g/kWh, as fuel %.5f kg/%s\n", g.Country, g.Year, g.G, f.WTT, f.Unit)
	// Output:
	// DNK 2025: 114.41 g/kWh, as fuel 0.11441 kg/kWh
}
