package batch_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/majidkabir/sootcase/pkg/batch"
	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/csvio"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Stream shipments from CSV and calculate them in parallel; results arrive
// in input order.
func ExampleProcess() {
	in := `shipment_id,weight_kg,mode,vehicle_type,distance_km
A,1000,road,hgv,100
B,2000,rail,freight_train,300
B,,road,van,20
C,0,road,hgv,100
`
	ds, _ := factors.Open(factors.Default)
	r, err := csvio.NewReader(strings.NewReader(in))
	if err != nil {
		panic(err)
	}
	stats, err := batch.Process(context.Background(), calc.Calculator{Factors: ds}, r, 4,
		func(it csvio.Item, res calc.Result, err error) error {
			if err != nil {
				fmt.Printf("%s: %v\n", it.Shipment.ID, err)
				return nil
			}
			fmt.Printf("%s: %.2f kg CO2e\n", it.Shipment.ID, res.WTWKg)
			return nil
		})
	fmt.Println(stats.Shipments, "shipments,", stats.Failed, "failed,", err)
	// Output:
	// A: 12.50 kg CO2e
	// B: 44.70 kg CO2e
	// C: shipment weight must be > 0
	// 3 shipments, 1 failed, <nil>
}
