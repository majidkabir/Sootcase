package recommend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Run finds alternatives for each shipment, with up to workers shipments
// at once. Shipments with the same legs (and no measured fuel) are
// calculated once and scaled by weight: emissions are proportional to the
// tonnes carried.
func Run(ctx context.Context, c calc.Calculator, shipments []calc.Shipment, set Settings, workers int) (Result, error) {
	set, skipped, err := set.Validate()
	if err != nil {
		return Result{}, err
	}
	for _, k := range []struct {
		kind    string
		mode    calc.Mode
		vehicle string
	}{{Rail, calc.Rail, set.RailVehicle}, {AirToRail, calc.Rail, set.RailVehicle}, {Waterway, calc.InlandWaterway, set.WaterwayVehicle},
		{ShortSea, calc.Sea, set.ShortSeaVehicle}, {AirToSea, calc.Sea, set.SeaVehicle}} {
		if set.has(k.kind) {
			if _, err := c.Factors.LegFactor(k.mode, k.vehicle); err != nil {
				return Result{}, fmt.Errorf("%w: %s vehicle %q: %v", ErrInvalidSettings, k.kind, k.vehicle, err)
			}
		}
	}
	if set.has(Load) && set.LoadVehicle != "" {
		if _, err := c.Factors.LegFactor(calc.Road, set.LoadVehicle); err != nil {
			return Result{}, fmt.Errorf("%w: load_vehicle %q: %v", ErrInvalidSettings, set.LoadVehicle, err)
		}
	}
	// Sea and waterway alternatives need their networks: a straight line
	// would cross land.
	if dm, ok := c.Distances.(calc.DistanceMethoder); ok {
		var kinds []string
		for _, k := range set.Kinds {
			m := calc.Mode("")
			switch k {
			case Waterway:
				m = calc.InlandWaterway
			case ShortSea, AirToSea, OtherPort:
				m = calc.Sea
			}
			if m != "" && strings.HasPrefix(dm.DistanceMethod(m), "great_circle") {
				skipped[k] = fmt.Sprintf("no %s network loaded: distances would be straight lines across land", network(m))
				continue
			}
			kinds = append(kinds, k)
		}
		set.Kinds = kinds
	}
	res := Result{Label: Label, Settings: set, Skipped: skipped, Sources: sources(c.Factors, set), Limitations: Limitations()}

	// Group shipments that only differ in weight.
	type group struct {
		first int
		all   []int
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, s := range shipments {
		key, ok := groupKey(s)
		if set.has(Load) && set.LoadTonnes != nil && s.WeightKg/1000 > *set.LoadTonnes {
			key += "|over load" // fuller loads aren't possible for these
		}
		if g := byKey[key]; ok && g != nil {
			g.all = append(g.all, i)
			continue
		}
		g := &group{first: i, all: []int{i}}
		groups = append(groups, g)
		if ok {
			byKey[key] = g
		}
	}

	out := make([]ShipmentResult, len(shipments))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan *group)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, max(1, len(groups))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				first := analyse(ctx, c, shipments[g.first], set)
				for _, i := range g.all {
					out[i] = rescale(first, shipments[i])
				}
			}
		}()
	}
	for _, g := range groups {
		if ctx.Err() != nil {
			break
		}
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	res.Shipments = out
	summarise(&res)
	return res, nil
}

// groupKey is a shipment's legs and cargo basis without the weight; ok is
// false when emissions aren't proportional to weight: measured fuel, a
// dedicated truck (repositioning without a load), hubs with a fixed number
// of containers, or cargo not given by weight.
func groupKey(s calc.Shipment) (string, bool) {
	if s.WeightKg <= 0 || s.TEU != 0 || s.TEUNetTonnes != 0 || s.ContainerLoad != "" {
		return "", false
	}
	for _, l := range s.Legs {
		if l.Fuel != nil || (l.RepositioningKm > 0 && l.LoadFactor == 0 && l.LoadTonnes == 0) {
			return "", false
		}
	}
	for _, h := range s.Hubs {
		if h.Containers > 0 {
			return "", false
		}
	}
	b, err := json.Marshal(struct {
		Cargo string
		Legs  []calc.Leg
		Hubs  []calc.HubStop
		Add   *bool
	}{s.CargoType, s.Legs, s.Hubs, s.AddRoadLegs})
	if err != nil {
		return "", false
	}
	return string(b), true
}

// rescale turns a group's first result into another member's.
func rescale(r ShipmentResult, s calc.Shipment) ShipmentResult {
	out := r
	out.ShipmentID = s.ID
	if r.WeightKg <= 0 || s.WeightKg == r.WeightKg || r.Error != "" {
		out.WeightKg = s.WeightKg
		return out
	}
	k := s.WeightKg / r.WeightKg
	out.WeightKg = s.WeightKg
	out.Now = r.Now.scale(k)
	out.Options = make([]Option, len(r.Options))
	for i, o := range r.Options {
		o.Route = o.Route.scale(k)
		o.SavedKg *= k
		out.Options[i] = o
	}
	return out
}

func (r Route) scale(k float64) Route {
	out := r
	out.Totals = r.Totals.Scaled(k)
	out.Steps = make([]Step, len(r.Steps))
	for i, s := range r.Steps {
		s.WTWKg *= k
		out.Steps[i] = s
	}
	if r.Terminals != nil {
		out.Terminals = make([]calc.HubResult, len(r.Terminals))
		for i, h := range r.Terminals {
			h.WTWKg *= k
			h.Containers *= k
			out.Terminals[i] = h
		}
	}
	return out
}

// sources lists what the settings are based on.
func sources(f calc.FactorProvider, set Settings) []string {
	var s []string
	road := set.has(Rail) || set.has(Waterway) || set.has(ShortSea)
	if road && *set.MinRoadKm == DefaultMinRoadKm {
		s = append(s, SourceWhitePaper)
	}
	if *set.MinMainKm == DefaultMinMainKm {
		s = append(s, SourceMainKm)
	}
	if (set.has(Waterway) || set.has(ShortSea)) && *set.MaxPortRoadKm == DefaultMaxPortRoadKm {
		s = append(s, SourcePortRoad)
	}
	if set.has(Rail) || set.has(AirToRail) {
		if set.Terminal == TerminalBestTrip {
			s = append(s, SourceBestTrip)
		} else {
			s = append(s, SourceTerminal)
		}
	}
	if set.has(AirToSea) || set.has(AirToRail) {
		s = append(s, SourceAir)
	}
	if set.has(OtherPort) {
		s = append(s, SourcePorts)
	}
	seen := map[string]bool{}
	for _, v := range []struct {
		kind    string
		mode    calc.Mode
		vehicle string
		what    string
	}{{Rail, calc.Rail, set.RailVehicle, "Rail"}, {AirToRail, calc.Rail, set.RailVehicle, "Rail"},
		{Waterway, calc.InlandWaterway, set.WaterwayVehicle, "Inland waterway"}, {ShortSea, calc.Sea, set.ShortSeaVehicle, "Short sea"},
		{AirToSea, calc.Sea, set.SeaVehicle, "Sea"}} {
		if !set.has(v.kind) || seen[v.what+v.vehicle] {
			continue
		}
		seen[v.what+v.vehicle] = true
		if lf, err := f.LegFactor(v.mode, v.vehicle); err == nil {
			s = append(s, fmt.Sprintf("%s (%s): %s", v.what, v.vehicle, lf.Source))
		}
	}
	for _, v := range []struct{ kind, alias, what string }{
		{LargerTruck, vehArtic, "Larger truck (rigid to articulated)"}, {ElectricTruck, vehElectric, "Electric truck"},
		{ElectricTruck, vehElectricVan, "Electric van"}, {BioLNGTruck, vehBioLNG, "Bio-LNG truck"}} {
		if !set.has(v.kind) {
			continue
		}
		if lf, err := f.LegFactor(calc.Road, v.alias); err == nil {
			s = append(s, fmt.Sprintf("%s (%s): %s", v.what, v.alias, lf.Source))
		}
	}
	s = append(s, SourceAreas)
	if set.has(Load) {
		l := "Fuller loads: the calculator's load model for each road vehicle"
		if set.Source != "" {
			l += "; load given by the user: " + set.Source
		} else {
			l += "; load given by the user"
		}
		s = append(s, l)
	}
	return s
}
