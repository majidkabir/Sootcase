package calc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// FactorProvider supplies emission factors.
type FactorProvider interface {
	LegFactor(mode Mode, vehicleType string) (Factor, error)
	HubFactor(hubType string) (HubFactor, error)
	FuelFactor(fuel string) (FuelFactor, error)
}

// DistanceFactorProvider is optionally implemented by a FactorProvider whose
// factor for a vehicle type depends on the leg's distance, such as
// "container_ship" meaning a short-sea ship on short legs. note says which
// factor a distance chose ("" when the distance made no difference).
type DistanceFactorProvider interface {
	LegFactorFor(mode Mode, vehicleType string, km float64) (f Factor, note string, err error)
}

// Where is a leg end's country (ISO 3166-1 alpha-2, "" if unknown) and
// coordinates, for factors that depend on the region.
type Where struct {
	Country  string
	Lat, Lon float64
}

// PlaceFuelProvider is optionally implemented by a FactorProvider whose
// fuel names can depend on where a leg is (e.g. GLEC's regional fuel
// factors). note says which factor was chosen.
type PlaceFuelProvider interface {
	FuelFactorAt(fuel string, from, to Where) (f FuelFactor, note string, err error)
}

// PlaceFactorProvider is optionally implemented by a FactorProvider whose
// vehicle types can depend on where a leg is (e.g. GLEC's regional road
// values or container trade lanes). note says which factor was chosen.
type PlaceFactorProvider interface {
	LegFactorAt(mode Mode, vehicleType string, km float64, from, to Where) (f Factor, note string, err error)
}

// DistanceProvider supplies the distance in km for a leg when it isn't given.
type DistanceProvider interface {
	Distance(ctx context.Context, mode Mode, from, to Location) (float64, error)
}

// DistanceMethoder is optionally implemented by a DistanceProvider to name
// how it obtains distances for a mode (e.g. "osrm", "great_circle"). The
// name is recorded on each leg result.
type DistanceMethoder interface {
	DistanceMethod(mode Mode) string
}

// ContainerLoadProvider is optionally implemented by a FactorProvider to
// resolve container load profiles (e.g. "average") to net tonnes per TEU.
type ContainerLoadProvider interface {
	ContainerLoad(name string) (float64, error)
}

// DistanceGiven is the method recorded when the input supplied distance_km.
const DistanceGiven = "given"

// Calculator computes shipment emissions.
type Calculator struct {
	Factors   FactorProvider
	Distances DistanceProvider
	// FerryVehicle is the sea vehicle type for ferry crossings found on
	// road routes (default DefaultFerryVehicle).
	FerryVehicle string
	// Places turns location codes into coordinates (optional). When it is
	// also a PlaceFinder, legs off their network move to nearby ports and
	// terminals (see connect.go).
	Places Resolver
	// RoadVehicle is the vehicle type of added road legs. Empty: the
	// shipment's first road leg's, else DefaultRoadVehicle.
	RoadVehicle string
	// NoRoadLegs adds no road legs for shipments that don't ask for them
	// (Shipment.AddRoadLegs); see connect.go.
	NoRoadLegs bool
	// NearestTerminal moves a rail leg end that is off the network to the
	// nearest rail terminal on the network that can be reached by road,
	// instead of the one giving the best trip among the nearest (as the
	// Combined Transport Directive 92/106/EEC describes combined transport:
	// road to the nearest suitable rail station).
	NearestTerminal bool
	// RetryWaits are the pauses before CalculateRetrying tries again after
	// a temporary failure (nil: DefaultRetryWaits; empty: no retries).
	RetryWaits []time.Duration
}

// Resolver finds a location by code: a UN/LOCODE or an IATA airport code.
type Resolver interface {
	Resolve(code string) (Location, bool)
}

// ErrUnknownLocation is returned for a location code that isn't known.
var ErrUnknownLocation = errors.New("unknown location code")

// resolve fills in the coordinates of locations given by code. Locations
// with coordinates keep them; the code is then only a label.
func (c Calculator) resolve(s Shipment) (Shipment, error) {
	need := false
	for _, l := range s.Legs {
		need = need || (l.From.Code != "" && isZero(l.From)) || (l.To.Code != "" && isZero(l.To))
	}
	if !need {
		return s, nil
	}
	legs := make([]Leg, len(s.Legs))
	copy(legs, s.Legs)
	for i := range legs {
		for name, p := range map[string]*Location{"from": &legs[i].From, "to": &legs[i].To} {
			if p.Code == "" || !isZero(*p) {
				continue
			}
			if c.Places == nil {
				return s, fmt.Errorf("leg %d %s: %w %q (no place list loaded)", i+1, name, ErrUnknownLocation, p.Code)
			}
			loc, ok := c.Places.Resolve(p.Code)
			if !ok {
				return s, fmt.Errorf("leg %d %s: %w %q (give a UN/LOCODE like NLRTM or an IATA code like CPH)", i+1, name, ErrUnknownLocation, p.Code)
			}
			if p.Name != "" {
				loc.Name = p.Name
			}
			*p = loc
		}
	}
	s.Legs = legs
	return s, nil
}

// Input errors returned by Calculate. Wrapped errors carry details; match
// them with errors.Is.
var (
	ErrInvalidWeight = errors.New("shipment weight must be > 0")
	ErrNoLegs        = errors.New("shipment has no legs")
	ErrInvalidFuel   = errors.New("invalid fuel data")
	ErrInvalidTEU    = errors.New("invalid container data")
)

// Calculate returns the emissions of a shipment.
//
// Intensity method (default, modelled data):
//
//	tonne-km = (weight_kg / 1000) * distance_km
//	TTW kg   = tonne-km * TTW g/tkm / 1000
//	WTW kg   = tonne-km * WTW g/tkm / 1000
//
// Fuel method (leg.Fuel set, primary data):
//
//	share    = tonne-km / vehicle_tonne_km   (1 when vehicle_tonne_km is 0)
//	TTW kg   = fuel_amount * TTW kg/unit * share
//	WTW kg   = fuel_amount * WTW kg/unit * share
//
// Hubs (WTW only):
//
//	hub kg   = (weight_kg / 1000) * hub g/t / 1000
func (c Calculator) Calculate(ctx context.Context, s Shipment) (Result, error) {
	s, err := c.resolve(s)
	if err != nil {
		return Result{}, err
	}
	if err := validate(s); err != nil {
		return Result{}, err
	}
	weightKg, basis, profile, err := c.weight(s)
	if err != nil {
		return Result{}, err
	}
	if len(s.Legs) == 0 {
		return Result{}, ErrNoLegs
	}
	tonnes := weightKg / 1000
	res := Result{ShipmentID: s.ID, WeightKg: weightKg, WeightBasis: basis, ContainerLoad: profile}
	if n, ok := c.Factors.(DatasetNamer); ok {
		res.Dataset = n.DatasetName()
	}

	legs, warnings, err := c.plan(ctx, s.Legs, c.addRoadLegs(ctx, s))
	if err != nil {
		return Result{}, err
	}
	res.Warnings = warnings
	for _, p := range legs {
		lr, err := c.leg(ctx, tonnes, p.Leg)
		if err != nil && p.added != "" && !Temporary(err) {
			// An added leg that can't be calculated is left out, as it
			// would have been without adding it.
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s It couldn't be calculated, so it isn't counted: %v", p.note, err))
			continue
		}
		if err != nil {
			return Result{}, fmt.Errorf("leg %q: %w", p.ID, err)
		}
		lr.Added, lr.Note = p.added, p.note
		if !isZero(p.From) && !isZero(p.To) {
			from, to := p.From, p.To
			lr.From, lr.To = &from, &to
		}
		lr.WTTKg = lr.WTWKg - lr.TTWKg
		if lr.TonneKm > 0 {
			lr.IntensityG = lr.WTWKg * 1000 / lr.TonneKm
		}
		lr.DataType = dataType(lr.Method)
		if lr.Method == MethodIntensity && lr.Factor != nil && lr.Factor.DataType != "" {
			lr.DataType = lr.Factor.DataType
		}
		res.Legs = append(res.Legs, lr)
		res.TonneKm += lr.TonneKm
		res.TTWKg += lr.TTWKg
		res.WTWKg += lr.WTWKg
		res.BiogenicCO2Kg += lr.BiogenicCO2Kg
	}

	if res.TonneKm > 0 {
		res.IntensityG = res.WTWKg * 1000 / res.TonneKm
	}

	for _, h := range s.Hubs {
		hf, err := c.Factors.HubFactor(h.HubType)
		if err != nil {
			return Result{}, fmt.Errorf("hub %q: %w", h.ID, err)
		}
		if h.Containers < 0 || math.IsNaN(h.Containers) || math.IsInf(h.Containers, 0) {
			return Result{}, fmt.Errorf("%w: hub %q: containers must be >= 0", ErrInvalidInput, h.ID)
		}
		hr := HubResult{HubID: h.ID, HubType: h.HubType, WTWKg: tonnes * hf.WTWPerTonneG / 1000,
			DataType: DataDefault, Source: hf.Source}
		if hf.WTWPerContainerG > 0 {
			if h.Containers == 0 {
				return Result{}, fmt.Errorf("%w: hub %q: %s is per container handled; give containers", ErrInvalidInput, h.ID, h.HubType)
			}
			hr.Containers, hr.WTWKg = h.Containers, h.Containers*hf.WTWPerContainerG/1000
		}
		if hf.DataType != "" {
			hr.DataType = hf.DataType
		}
		res.Hubs = append(res.Hubs, hr)
		res.WTWKg += hr.WTWKg
	}
	// Hub factors are WTW only, so hubs add to WTW and, for lack of a
	// split, are not attributed to TTW or WTT.
	res.WTTKg = res.WTWKg - res.TTWKg
	for _, h := range res.Hubs {
		res.WTTKg -= h.WTWKg
	}
	var primary float64
	for _, l := range res.Legs {
		main := l.WTWKg
		for _, p := range l.Parts {
			main -= p.WTWKg
			if p.DataType == DataPrimary {
				primary += p.WTWKg
			}
		}
		if l.DataType == DataPrimary {
			primary += main
		}
	}
	for _, h := range res.Hubs {
		if h.DataType == DataPrimary {
			primary += h.WTWKg
		}
	}
	if res.WTWKg > 0 {
		res.PrimaryDataShare = primary / res.WTWKg
	}
	res.GHGProtocol = ghgScopes(res)
	if err := checkResult(res); err != nil {
		return Result{}, err
	}
	return res, nil
}

// dataType labels a leg by how its emissions were obtained: measured fuel is
// primary data; the load and energy methods model the operation from
// shipment-specific inputs; the intensity method uses published defaults.
func dataType(method string) string {
	switch method {
	case MethodFuel:
		return DataPrimary
	case MethodLoadModel, MethodEnergy:
		return DataModelled
	default:
		return DataDefault
	}
}

// DefaultContainerLoad is the container load profile used for TEU given
// without net tonnes: GLEC Framework v3.3 (Section 1 Chapter 2, p. 21; and
// Chapter 4, sea): when the mass is known only in TEU, an average of 10 t
// per TEU can be assumed.
const DefaultContainerLoad = "average"

// weight returns the cargo weight in kg, how it was determined, and the
// container load profile used ("" if none).
func (c Calculator) weight(s Shipment) (float64, string, string, error) {
	if s.TEU == 0 && s.TEUNetTonnes == 0 && s.ContainerLoad == "" {
		if s.WeightKg <= 0 {
			return 0, "", "", ErrInvalidWeight
		}
		return s.WeightKg, "weight", "", nil
	}
	switch {
	case s.WeightKg > 0:
		return 0, "", "", fmt.Errorf("%w: give weight_kg or teu, not both", ErrInvalidTEU)
	case s.TEU <= 0:
		return 0, "", "", fmt.Errorf("%w: teu must be > 0", ErrInvalidTEU)
	case s.TEUNetTonnes > 0 && s.ContainerLoad != "":
		return 0, "", "", fmt.Errorf("%w: give teu_net_tonnes or container_load, not both", ErrInvalidTEU)
	}
	perTEU, profile := s.TEUNetTonnes, s.ContainerLoad
	if profile == "" && perTEU == 0 {
		profile = DefaultContainerLoad
	}
	if profile != "" {
		clp, ok := c.Factors.(ContainerLoadProvider)
		if !ok {
			return 0, "", "", fmt.Errorf("%w: give teu_net_tonnes; the dataset has no container load profiles", ErrInvalidTEU)
		}
		var err error
		if perTEU, err = clp.ContainerLoad(profile); err != nil {
			return 0, "", "", fmt.Errorf("%w: %v", ErrInvalidTEU, err)
		}
	}
	if perTEU <= 0 {
		return 0, "", "", fmt.Errorf("%w: need teu_net_tonnes or container_load", ErrInvalidTEU)
	}
	return s.TEU * perTEU * 1000, "teu", profile, nil
}

func (c Calculator) leg(ctx context.Context, tonnes float64, leg Leg) (LegResult, error) {
	rt, method, dtype, err := c.distance(ctx, leg)
	if err != nil {
		return LegResult{}, err
	}
	dist, routeParts := rt.Km, rt.Parts

	// Split off parts calculated differently, and calculate the rest of the
	// leg (mainKm) as usual.
	mainKm := dist
	var parts []LegPart
	if ferryKm := routeParts[PartFerry]; leg.Mode == Road && ferryKm > 0 {
		if ferryKm >= dist {
			return LegResult{}, fmt.Errorf("%w: the road route is all ferry; give it as a sea leg", ErrInvalidInput)
		}
		p, err := c.ferry(tonnes, ferryKm, dtype)
		if err != nil {
			return LegResult{}, err
		}
		parts = append(parts, p)
		mainKm -= ferryKm
	}
	// Electric rail with known non-electrified track: that track runs on
	// the vehicle's own factor (diesel), the rest on electricity. Track with
	// unknown electrification follows the input (electric).
	if dieselKm := routeParts[PartNotElectrified]; leg.Mode == Rail && leg.EnergyKWhPerTkm != 0 && dieselKm > 0 && dieselKm < dist {
		p, err := c.electricPart(tonnes, dist-dieselKm, leg)
		if err != nil {
			return LegResult{}, err
		}
		parts = append(parts, p)
		mainKm = dieselKm
		leg.EnergyKWhPerTkm, leg.Country = 0, ""
	} else if leg.Mode == Rail && leg.EnergyKWhPerTkm != 0 && dieselKm >= dist {
		// No electrified track at all: the vehicle's own factor for all of it.
		leg.EnergyKWhPerTkm, leg.Country = 0, ""
	}

	lr, err := c.main(tonnes, mainKm, dtype, leg)
	if err != nil {
		return LegResult{}, err
	}
	lr.LegID, lr.Mode, lr.VehicleType = leg.ID, leg.Mode, leg.VehicleType
	lr.DistanceKm, lr.DistanceMethod, lr.DistanceType = dist, method, dtype
	lr.TonneKm = tonnes * dist
	lr.RouteParts = routeParts
	lr.Path = rt.Path
	if WantPath(ctx) && len(lr.Path) == 0 && !isZero(leg.From) && !isZero(leg.To) {
		lr.Path = [][2]float64{{leg.From.Lon, leg.From.Lat}, {leg.To.Lon, leg.To.Lat}}
	}
	for _, p := range parts {
		lr.TTWKg += p.TTWKg
		lr.WTWKg += p.WTWKg
	}
	lr.Parts = parts
	return lr, nil
}

// main calculates a leg, or the part of it left after LegParts, over km.
func (c Calculator) main(tonnes, dist float64, dtype string, leg Leg) (LegResult, error) {
	lr := LegResult{TonneKm: tonnes * dist}

	methods := 0
	if leg.LoadFactor != 0 && leg.LoadTonnes != 0 {
		return LegResult{}, fmt.Errorf("%w: give load_factor or load_tonnes, not both", ErrInvalidLoad)
	}
	if leg.RepositioningKm != 0 {
		if leg.Mode != Road {
			return LegResult{}, fmt.Errorf("%w: repositioning_km is for trucks (road legs); for other modes it is in the vehicle's factor or measured fuel", ErrInvalidLoad)
		}
		if leg.Fuel != nil || leg.EnergyKWhPerTkm != 0 {
			return LegResult{}, fmt.Errorf("%w: repositioning_km uses the load model; with measured fuel or energy, include the empty km in the fuel or energy", ErrInvalidLoad)
		}
		if leg.LoadFactor == 0 && leg.LoadTonnes == 0 {
			leg.LoadTonnes = tonnes // a dedicated truck: the shipment is its whole load
		}
	}
	for _, set := range []bool{leg.Fuel != nil, leg.LoadFactor != 0 || leg.LoadTonnes != 0, leg.EnergyKWhPerTkm != 0} {
		if set {
			methods++
		}
	}
	if methods > 1 {
		return LegResult{}, fmt.Errorf("leg uses more than one of fuel, load_factor and energy_kwh_per_tkm")
	}
	if leg.Country != "" && leg.EnergyKWhPerTkm == 0 {
		return LegResult{}, fmt.Errorf("%w: country is only used with energy_kwh_per_tkm", ErrInvalidEnergy)
	}
	if leg.EnergyKWhPerTkm != 0 {
		return c.energyLeg(lr, leg)
	}
	if leg.EmptyTripFactor != 0 && leg.LoadFactor == 0 && leg.LoadTonnes == 0 {
		return LegResult{}, fmt.Errorf("%w: empty_trip_factor needs load_factor or load_tonnes", ErrInvalidLoad)
	}
	if leg.Fuel != nil {
		return c.fuelLeg(lr, *leg.Fuel, c.where(leg.From), c.where(leg.To))
	}
	if leg.LoadFactor != 0 || leg.LoadTonnes != 0 {
		return c.loadLeg(lr, leg, tonnes, dist, dtype)
	}

	var f Factor
	var err error
	if pp, ok := c.Factors.(PlaceFactorProvider); ok {
		f, lr.FactorNote, err = pp.LegFactorAt(leg.Mode, leg.VehicleType, dist, c.where(leg.From), c.where(leg.To))
	} else if dp, ok := c.Factors.(DistanceFactorProvider); ok {
		f, lr.FactorNote, err = dp.LegFactorFor(leg.Mode, leg.VehicleType, dist)
	} else {
		f, err = c.Factors.LegFactor(leg.Mode, leg.VehicleType)
	}
	if err != nil {
		return LegResult{}, err
	}
	// Emissions = tonne-km x intensity x DAF (GLEC Framework, ISO 14083).
	adjKm, daf, err := c.adjust(leg.Mode, dtype, f.DistanceBasis, dist)
	if err != nil {
		return LegResult{}, err
	}
	if daf != nil && dist > 0 {
		lr.DAF, lr.DAFSource = adjKm/dist, daf.Source
	}
	adjTkm := tonnes * adjKm
	lr.Method = MethodIntensity
	lr.Factor = &f
	lr.TTWKg = adjTkm * f.TTW / 1000
	lr.WTWKg = adjTkm * f.WTW() / 1000
	return lr, nil
}

// where is a location's country, from Places when it can tell.
func (c Calculator) where(l Location) Where {
	w := Where{Lat: l.Lat, Lon: l.Lon}
	if cr, ok := c.Places.(Countrier); ok && !isZero(l) {
		w.Country = cr.Country(l)
	}
	return w
}

func (c Calculator) fuelLeg(lr LegResult, fu FuelUse, from, to Where) (LegResult, error) {
	if fu.Amount < 0 || fu.VehicleTonneKm < 0 {
		return LegResult{}, fmt.Errorf("%w: amount and vehicle_tonne_km must be >= 0", ErrInvalidFuel)
	}
	share := 1.0
	if fu.VehicleTonneKm > 0 {
		share = lr.TonneKm / fu.VehicleTonneKm
		if share > 1+1e-9 {
			return LegResult{}, fmt.Errorf("%w: shipment tonne-km %.2f exceeds vehicle tonne-km %.2f",
				ErrInvalidFuel, lr.TonneKm, fu.VehicleTonneKm)
		}
	}
	var ff FuelFactor
	var err error
	if pf, ok := c.Factors.(PlaceFuelProvider); ok {
		ff, lr.FactorNote, err = pf.FuelFactorAt(fu.Fuel, from, to)
	} else {
		ff, err = c.Factors.FuelFactor(fu.Fuel)
	}
	if err != nil {
		return LegResult{}, err
	}
	lr.Method = MethodFuel
	lr.FuelFactor = &ff
	lr.AllocationShare = share
	lr.TTWKg = fu.Amount * ff.TTW * share
	lr.WTWKg = fu.Amount * ff.WTW() * share
	lr.BiogenicCO2Kg = fu.Amount * ff.BiogenicCO2 * share
	return lr, nil
}

func (c Calculator) loadLeg(lr LegResult, leg Leg, tonnes, dist float64, dtype string) (LegResult, error) {
	lmp, ok := c.Factors.(LoadModelProvider)
	if !ok {
		return LegResult{}, fmt.Errorf("%w: dataset has no load models", ErrInvalidLoad)
	}
	m, err := lmp.LoadModel(leg.Mode, leg.VehicleType)
	if err != nil {
		return LegResult{}, err
	}
	// A load factor is a share of the model's payload: the dataset's average
	// full load, not the truck's rated payload (as in GLEC and EcoTransIT),
	// so the source says how many tonnes it means.
	// Repositioning km are empty running for this trip: as empty km per
	// loaded km (actual), they join the empty trip factor.
	adjKm, daf, err := c.adjust(leg.Mode, dtype, m.DistanceBasis, dist)
	if err != nil {
		return LegResult{}, err
	}
	etf := leg.EmptyTripFactor
	if leg.RepositioningKm > 0 {
		if adjKm <= 0 {
			return LegResult{}, fmt.Errorf("%w: repositioning_km needs a loaded distance > 0", ErrInvalidLoad)
		}
		etf += leg.RepositioningKm / adjKm
	}
	lf, load := leg.LoadFactor, fmt.Sprintf("load factor %g = %.1f t on the vehicle, a share of the model's %.1f t payload "+
		"(an average full load, not the rated payload; give load_tonnes to set the tonnes)", leg.LoadFactor, leg.LoadFactor*m.PayloadT, m.PayloadT)
	var ttw, wtt float64
	if leg.LoadTonnes != 0 {
		// Tonnes on the vehicle, as a share of the model's payload. Loads
		// above it are real (its "100% laden" is an average full load, not
		// the legal maximum), so the model is extended linearly up to
		// MaxLoadFactor.
		if !(leg.LoadTonnes > 0) {
			return LegResult{}, fmt.Errorf("%w: load_tonnes must be > 0, got %v", ErrInvalidLoad, leg.LoadTonnes)
		}
		lf = leg.LoadTonnes / m.PayloadT
		if lf > MaxLoadFactor {
			return LegResult{}, fmt.Errorf("%w: load_tonnes %g t is more than this vehicle's load model goes: %.1f t (%g × its %.1f t payload)",
				ErrInvalidLoad, leg.LoadTonnes, MaxLoadFactor*m.PayloadT, MaxLoadFactor, m.PayloadT)
		}
		load = fmt.Sprintf("load %g t, %.0f%% of the model's payload", leg.LoadTonnes, lf*100)
		if lf > 1 {
			load += ", extended beyond its 100% laden"
		}
		ttw, wtt, err = m.perTonneKm(lf, etf, MaxLoadFactor)
	} else {
		ttw, wtt, err = m.PerTonneKm(lf, etf)
	}
	if err != nil {
		return LegResult{}, err
	}
	f := Factor{
		Mode: m.Mode, VehicleType: m.VehicleType, TTW: ttw, WTT: wtt, Year: m.Year,
		Source: fmt.Sprintf("load model (%s, empty trip factor %g%s, payload %.1f t) from %s",
			load, round3(etf), repoText(leg.RepositioningKm), m.PayloadT, m.Source),
	}
	f.DistanceBasis = m.DistanceBasis
	// Emissions = tonne-km x intensity x DAF, as for fleet-average factors.
	if daf != nil && dist > 0 {
		lr.DAF, lr.DAFSource = adjKm/dist, daf.Source
	}
	adjTkm := tonnes * adjKm
	lr.Method = MethodLoadModel
	lr.Factor = &f
	lr.LoadFactor, lr.LoadTonnes, lr.EmptyTripFactor, lr.PayloadT = lf, leg.LoadTonnes, etf, m.PayloadT
	lr.RepositioningKm = leg.RepositioningKm
	lr.TTWKg = adjTkm * ttw / 1000
	lr.WTWKg = adjTkm * f.WTW() / 1000
	return lr, nil
}

func (c Calculator) distance(ctx context.Context, leg Leg) (rt Route, method, dtype string, err error) {
	if leg.DistanceKm > 0 {
		return Route{Km: leg.DistanceKm}, DistanceGiven, leg.DistanceType, nil
	}
	if leg.DistanceType != "" {
		return Route{}, "", "", fmt.Errorf("%w: distance_type describes distance_km, which is not given", ErrInvalidInput)
	}
	if c.Distances == nil {
		return Route{}, "", "", errors.New("no distance given and no distance provider")
	}
	if r, ok := c.Distances.(Router); ok {
		rt, err = r.Route(ctx, leg.Mode, leg.From, leg.To)
	} else {
		rt.Km, err = c.Distances.Distance(ctx, leg.Mode, leg.From, leg.To)
	}
	if err != nil {
		return Route{}, "", "", fmt.Errorf("distance: %w", err)
	}
	d := rt.Km
	for k, v := range rt.Parts {
		if !(v >= 0) || v > d*(1+1e-9) {
			return Route{}, "", "", fmt.Errorf("distance: route part %s of %v km in a %v km route", k, v, d)
		}
	}
	if dm, ok := c.Distances.(DistanceMethoder); ok {
		method = dm.DistanceMethod(leg.Mode)
	}
	if dt, ok := c.Distances.(DistanceTyper); ok {
		dtype = dt.DistanceType(leg.Mode)
	}
	return rt, method, dtype, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func repoText(km float64) string {
	if km <= 0 {
		return ""
	}
	return fmt.Sprintf(", including %g km repositioning", km)
}
