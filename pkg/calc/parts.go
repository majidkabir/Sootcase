package calc

import (
	"context"
	"fmt"
)

// Route is a routed distance with a breakdown: km by kind of part, for
// the parts the calculator treats differently from the rest of the leg.
type Route struct {
	Km    float64
	Parts map[string]float64 // e.g. PartFerry: 18.2
	// Path is the route as [lon, lat] points, when asked for (WithPath).
	Path [][2]float64
	// CountryKm is the km on land by country (ISO 3166-1 alpha-2), without
	// ferry parts, when asked for (WithCountryKm).
	CountryKm map[string]float64
}

// CountryUnknown is the CountryKm key for distance that is in no
// country's borders (e.g. a long bridge far from any coast).
const CountryUnknown = "unknown"

// Router is optionally implemented by a DistanceProvider that can say what
// a route is made of. When it is, the calculator uses Route instead of
// Distance.
type Router interface {
	Route(ctx context.Context, mode Mode, from, to Location) (Route, error)
}

// Route part kinds.
const (
	// PartFerry is a ferry crossing on a road route. It is calculated as a
	// sea leg with Calculator.FerryVehicle.
	PartFerry = "ferry"
	// PartElectrified and PartNotElectrified are rail track with and
	// without electrification; PartElectrificationUnknown is track whose
	// electrification isn't recorded.
	PartElectrified            = "electrified"
	PartNotElectrified         = "not_electrified"
	PartElectrificationUnknown = "electrification_unknown"
)

// DefaultFerryVehicle is the sea vehicle type used for ferry crossings on
// road routes when Calculator.FerryVehicle is empty.
const DefaultFerryVehicle = "ropax"

// LegPart is a part of a leg calculated differently from the rest of it:
// a ferry crossing on a road leg, or the electrified track of a rail leg.
// The leg's totals include its parts; the leg's own Method and Factor
// describe the rest.
type LegPart struct {
	Kind       string  `json:"kind"` // PartFerry or PartElectrified
	Mode       Mode    `json:"mode"`
	DistanceKm float64 `json:"distance_km"`
	TonneKm    float64 `json:"tonne_km"`
	TTWKg      float64 `json:"ttw_kg"`
	WTTKg      float64 `json:"wtt_kg"`
	WTWKg      float64 `json:"wtw_kg"`
	Method     string  `json:"method"`
	DataType   string  `json:"data_type"`
	Factor     *Factor `json:"factor,omitempty"`
	DAF        float64 `json:"daf,omitempty"`
	DAFSource  string  `json:"daf_source,omitempty"`
	// Set for the electric part: the energy method's inputs.
	EnergyKWhPerTkm float64 `json:"energy_kwh_per_tkm,omitempty"`
	Country         string  `json:"country,omitempty"`
}

func (c Calculator) ferry(tonnes, ferryKm float64, dtype string) (LegPart, error) {
	vt := c.FerryVehicle
	if vt == "" {
		vt = DefaultFerryVehicle
	}
	f, err := c.Factors.LegFactor(Sea, vt)
	if err != nil {
		return LegPart{}, fmt.Errorf("the road route has %.1f km by ferry, and the ferry factor failed: %w", ferryKm, err)
	}
	adjKm, daf, err := c.adjust(Sea, dtype, f.DistanceBasis, ferryKm)
	if err != nil {
		return LegPart{}, fmt.Errorf("ferry crossing: %w", err)
	}
	p := LegPart{Kind: PartFerry, Mode: Sea, DistanceKm: ferryKm, TonneKm: tonnes * ferryKm,
		Method: MethodIntensity, DataType: DataDefault, Factor: &f}
	if f.DataType != "" {
		p.DataType = f.DataType
	}
	if daf != nil {
		p.DAF, p.DAFSource = adjKm/ferryKm, daf.Source
	}
	p.TTWKg = tonnes * adjKm * f.TTW / 1000
	p.WTWKg = tonnes * adjKm * f.WTW() / 1000
	p.WTTKg = p.WTWKg - p.TTWKg
	return p, nil
}

// electricPart is the electrified track of a rail leg, by the energy method.
func (c Calculator) electricPart(tonnes, km float64, leg Leg) (LegPart, error) {
	lr, err := c.energyLeg(LegResult{TonneKm: tonnes * km}, leg)
	if err != nil {
		return LegPart{}, err
	}
	return LegPart{Kind: PartElectrified, Mode: leg.Mode, DistanceKm: km, TonneKm: lr.TonneKm,
		TTWKg: lr.TTWKg, WTTKg: lr.WTWKg - lr.TTWKg, WTWKg: lr.WTWKg, Method: MethodEnergy,
		DataType: DataModelled, Factor: lr.Factor, Country: lr.Country, EnergyKWhPerTkm: lr.EnergyKWhPerTkm}, nil
}

// Lane is an origin-destination pair for one mode.
type Lane struct {
	Mode     Mode
	From, To Location
}

// Prefetcher is optionally implemented by a DistanceProvider that can
// fetch many distances at once more cheaply than one by one (e.g. an OSRM
// distance table). It fills its own cache; lanes it can't answer are left
// for Distance or Route.
type Prefetcher interface {
	Prefetch(ctx context.Context, lanes []Lane)
}

// Prefetch asks the distance provider, if it is a Prefetcher, for the
// distinct lanes of shipments whose legs have no distance given. It is a
// best-effort speed-up before calculating a batch.
func (c Calculator) Prefetch(ctx context.Context, shipments []Shipment) {
	p, ok := c.Distances.(Prefetcher)
	if !ok {
		return
	}
	seen := map[Lane]bool{}
	var lanes []Lane
	for _, s := range shipments {
		s, err := c.resolve(s)
		if err != nil {
			continue
		}
		legs := append([]Leg(nil), s.Legs...)
		if c.addRoadLegs(ctx, s) {
			for _, g := range gapLanes(s.Legs) {
				legs = append(legs, Leg{Mode: g.Mode, From: g.From, To: g.To})
			}
		}
		for _, l := range legs {
			if l.DistanceKm > 0 || l.From == (Location{}) || l.To == (Location{}) {
				continue
			}
			ln := Lane{Mode: l.Mode, From: Location{Lat: l.From.Lat, Lon: l.From.Lon}, To: Location{Lat: l.To.Lat, Lon: l.To.Lon}}
			if !seen[ln] {
				seen[ln] = true
				lanes = append(lanes, ln)
			}
		}
	}
	if len(lanes) > 0 {
		p.Prefetch(ctx, lanes)
	}
}
