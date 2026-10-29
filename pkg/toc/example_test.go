package toc_test

import (
	"fmt"
	"strings"

	"github.com/majidkabir/sootcase/pkg/factors"
	"github.com/majidkabir/sootcase/pkg/toc"
)

// Build a transport operation category's intensity from a year of fuel data.
func ExampleBuild() {
	ops, err := toc.Parse(strings.NewReader(`{
	  "name": "acme", "period": "2025",
	  "tocs": [{"id": "dk_road", "mode": "road", "tonne_km": 1000000,
	            "fuels": [{"fuel": "diesel", "amount": 25000}]}]}`))
	if err != nil {
		panic(err)
	}
	ds, _ := factors.Open(factors.Default)
	layer, err := toc.Build(ops, ds)
	if err != nil {
		panic(err)
	}
	f := layer.Legs[0]
	fmt.Printf("%s: %.2f g CO2e/tkm WTW, %s data\n", f.VehicleType, f.WTW(), f.DataType)
	// Output:
	// dk_road: 86.94 g CO2e/tkm WTW, primary data
}
