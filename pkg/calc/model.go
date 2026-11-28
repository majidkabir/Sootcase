package calc

// Mode is a transport mode.
type Mode string

// Transport modes.
const (
	Road           Mode = "road"
	Rail           Mode = "rail"
	Sea            Mode = "sea"
	Air            Mode = "air"
	InlandWaterway Mode = "inland_waterway"
)

// KnownMode says whether m is one of the modes above. A dataset may still
// carry factors for a mode of its own.
func KnownMode(m Mode) bool {
	switch m {
	case Road, Rail, Sea, Air, InlandWaterway:
		return true
	}
	return false
}

// Location is a point on the map. Instead of coordinates it can give a
// Code, a UN/LOCODE ("NLRTM") or an IATA airport code ("CPH"), which the
// Calculator's Places turns into coordinates.
type Location struct {
	Name string  `json:"name,omitempty"`
	Code string  `json:"code,omitempty"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Leg is one transport movement within a shipment.
type Leg struct {
	ID          string `json:"id"`
	Mode        Mode   `json:"mode"`
	VehicleType string `json:"vehicle_type"`
	// CargoType overrides the shipment's for this leg (see Shipment.CargoType).
	CargoType string   `json:"cargo_type,omitempty"`
	From      Location `json:"from"`
	To        Location `json:"to"`
	// DistanceKm overrides the DistanceProvider when > 0 (e.g. actual odometer distance).
	DistanceKm float64 `json:"distance_km,omitempty"`
	// DistanceType says what kind of distance DistanceKm is: "sfd", "gcd"
	// or "actual". When it differs from the factor's distance basis, the
	// dataset's DAF converts between them. Empty means "same as the factor".
	DistanceType string `json:"distance_type,omitempty"`
	// Fuel, when set, switches the leg to the fuel-based (primary data) method.
	Fuel *FuelUse `json:"fuel,omitempty"`
	// LoadFactor (0..1] switches the leg to the dataset's load model for the
	// vehicle, with EmptyTripFactor empty km per loaded km. It is the share
	// of the load model's payload (LegResult.PayloadT; DESNZ's "100% laden",
	// 17.9 t for an articulated truck over 33 t), not of the vehicle's rated
	// payload as in EcoTransIT. LoadTonnes instead gives the tonnes on the
	// vehicle (all cargo, average over the loaded km), which means the same
	// in any tool; give one or the other.
	LoadFactor      float64 `json:"load_factor,omitempty"`
	LoadTonnes      float64 `json:"load_tonnes,omitempty"`
	EmptyTripFactor float64 `json:"empty_trip_factor,omitempty"`
	// RepositioningKm is km the truck drives empty for this trip (to the
	// pickup, or back after delivery), shared over its load like empty
	// running (GLEC Framework: empty running and repositioning belong to the
	// loaded trips). It uses the load model; without a load, the shipment is
	// taken as the truck's whole load (a dedicated truck).
	RepositioningKm float64 `json:"repositioning_km,omitempty"`
	// EnergyKWhPerTkm with Country switches the leg to the energy method:
	// electricity per tonne-km times the country's grid intensity.
	EnergyKWhPerTkm float64 `json:"energy_kwh_per_tkm,omitempty"`
	Country         string  `json:"country,omitempty"`
	// OwnFactor is the carrier's or your own intensity for this leg, used
	// instead of a default value (primary data, ISO 14083 7.2.3).
	OwnFactor *OwnFactor `json:"own_factor,omitempty"`
	// Vehicle (road legs) routes the leg on roads this truck may use: its
	// size, weight and dangerous goods (see Vehicle). Routed legs only.
	Vehicle *Vehicle `json:"vehicle,omitempty"`
	// AtTerminals (rail, inland waterway, sea) makes the leg start and end
	// at a rail terminal or port: an end that isn't at one moves to the
	// nearest one, with a road leg to it, even when the end is on the
	// network (combined transport: road to and from the nearest suitable
	// station or port, Council Directive 92/106/EEC, Article 1).
	// Recommendations set it on their new legs; not part of the API.
	AtTerminals bool `json:"-"`
	// teu is the shipment's TEU when it was given in TEU, for values
	// published per TEU-km (Factor.TonnesPerTEU).
	teu float64
}

// FuelUse is measured fuel for the vehicle trip a leg is part of.
type FuelUse struct {
	Fuel   string  `json:"fuel"`   // e.g. "diesel", must match a FuelFactor
	Amount float64 `json:"amount"` // in the FuelFactor's unit
	// VehicleTonneKm is the total tonne-km carried on that trip, used to
	// allocate fuel to this shipment by tonne-km share. 0 means the whole
	// trip belongs to this shipment.
	VehicleTonneKm float64 `json:"vehicle_tonne_km,omitempty"`
	// Factor is the fuel supplier's or your own emission factor for this
	// fuel, used instead of the dataset's (Fuel is then just its name).
	Factor *OwnFuelFactor `json:"factor,omitempty"`
}

// OwnFactor is an intensity from the carrier or your own measurements, in
// grams CO2e per tonne-km.
type OwnFactor struct {
	WTW float64 `json:"wtw_g_per_tkm"` // well-to-wheel
	// TTW is the tank-to-wheel part of WTW. Required: without it the split
	// into TTW and WTT, and so the GHG Protocol scopes, would be a guess
	// (0 is right only when no fuel is burnt in the vehicle, e.g. electric).
	TTW    *float64 `json:"ttw_g_per_tkm"`
	Source string   `json:"source"` // who measured it, and for what
	// DistanceBasis is the distance type the intensity is per tonne-km of:
	// sfd, gcd or actual. Empty: the same as the leg's distance, so no
	// distance adjustment factor.
	DistanceBasis string `json:"distance_basis,omitempty"`
}

// OwnFuelFactor is a fuel's emission factor from the supplier or your own
// data, in kg CO2e per unit of fuel.
type OwnFuelFactor struct {
	Unit string  `json:"unit"`            // e.g. "l", "kg", "kWh"
	WTW  float64 `json:"wtw_kg_per_unit"` // well-to-wheel
	// TTW is the tank-to-wheel part of WTW; required, as for OwnFactor.
	TTW    *float64 `json:"ttw_kg_per_unit"`
	Source string   `json:"source"`
	// BiogenicCO2 is biogenic CO2 per unit, reported apart (optional).
	BiogenicCO2 float64 `json:"biogenic_co2_kg_per_unit,omitempty"`
}

// FuelFactor converts fuel to emissions, in kg CO2e per unit of fuel.
type FuelFactor struct {
	Fuel   string  `json:"fuel"`
	Unit   string  `json:"unit"` // e.g. "l", "kg", "kWh"
	TTW    float64 `json:"ttw_kg_per_unit"`
	WTT    float64 `json:"wtt_kg_per_unit"`
	Source string  `json:"source"`
	Year   int     `json:"year"`
	// BiogenicCO2 is the CO2 of biological origin released by burning a
	// unit (kg). It is not part of TTW or WTW: the GLEC Framework v3.3 and
	// the GHG Protocol report it separately (GLEC p. 64).
	BiogenicCO2 float64 `json:"biogenic_co2_kg_per_unit,omitempty"`
}

// WTW is the well-to-wheel factor (WTT + TTW).
func (f FuelFactor) WTW() float64 { return f.TTW + f.WTT }

// Calculation methods recorded on each leg result.
const (
	MethodIntensity = "intensity"
	MethodFuel      = "fuel"
	MethodLoadModel = "load_model"
)

// HubStop is a transshipment or warehouse activity between legs.
type HubStop struct {
	ID      string `json:"id"`
	HubType string `json:"hub_type"`
	// Containers is the number of containers handled, for hub types whose
	// factor is per container (GLEC's intermodal terminals).
	Containers float64 `json:"containers,omitempty"`
}

// Shipment is the unit of calculation.
type Shipment struct {
	ID string `json:"id"`
	// WeightKg is the cargo weight. For containers, give TEU instead, with
	// either TEUNetTonnes or a ContainerLoad profile from the dataset.
	WeightKg      float64 `json:"weight_kg,omitempty"`
	TEU           float64 `json:"teu,omitempty"`
	TEUNetTonnes  float64 `json:"teu_net_tonnes,omitempty"`
	ContainerLoad string  `json:"container_load,omitempty"`
	// CargoType (e.g. "container", "temperature_controlled") picks the
	// dataset's value for that cargo where it has one for the leg's vehicle
	// (GLEC's cargo and load categories); otherwise the vehicle's own value
	// is kept and the leg's note says so. Default values only.
	CargoType string    `json:"cargo_type,omitempty"`
	Legs      []Leg     `json:"legs"`
	Hubs      []HubStop `json:"hubs,omitempty"`
	// AddRoadLegs adds road legs where the legs don't join up or a sea,
	// rail or waterway leg starts or ends off its network (see connect.go).
	// nil means on, unless the context says otherwise (WithoutRoadLegs).
	AddRoadLegs *bool `json:"add_road_legs,omitempty"`
}

// Factor is an intensity-based emission factor for a mode/vehicle type,
// expressed in grams CO2e per tonne-km.
type Factor struct {
	Mode        Mode    `json:"mode"`
	VehicleType string  `json:"vehicle_type"`
	TTW         float64 `json:"ttw_g_per_tkm"` // tank-to-wheel
	WTT         float64 `json:"wtt_g_per_tkm"` // well-to-tank
	Source      string  `json:"source"`
	Year        int     `json:"year"`
	// DataType overrides the default label for factors built from the
	// operator's own measurements (e.g. transport operation categories).
	DataType string `json:"data_type,omitempty"`
	// DistanceBasis is the distance type the intensity is per tonne-km of
	// ("sfd", "gcd", "actual"); empty if unknown, which disables DAFs.
	DistanceBasis string `json:"distance_basis,omitempty"`
	// ElectricityG is the part of WTT that is the electricity the vehicle
	// runs on, for a factor that mixes fuel and electric traction (GLEC's
	// European rail average): the operator's scope 2, the rest of WTT its
	// scope 3 category 3. Factors for electric vehicles only leave it out;
	// all of their emissions are electricity (see ghgScopes).
	ElectricityG float64 `json:"electricity_g_per_tkm,omitempty"`
	// TonnesPerTEU marks a value published per TEU-km (GLEC's container
	// trade lanes), held here per tonne-km at this many tonnes per TEU. A
	// shipment given in TEU is then calculated per TEU-km (TEU × km × the
	// published value, GLEC Framework v3.3 p. 134), whatever its tonnes.
	TonnesPerTEU float64 `json:"tonnes_per_teu,omitempty"`
}

// WTW is the well-to-wheel factor (WTT + TTW).
func (f Factor) WTW() float64 { return f.TTW + f.WTT }

// HubFactor is a hub emission factor in grams CO2e (WTW) per tonne handled.
type HubFactor struct {
	HubType      string  `json:"hub_type"`
	WTWPerTonneG float64 `json:"wtw_g_per_tonne"`
	// TTWPerTonneG and WTTPerTonneG split WTWPerTonneG when the split is known
	// (hub operation categories from the operator's own energy use; ISO 14083
	// 13.3.2 asks for both at hub level). GLEC's hub defaults give WTW only,
	// so they leave these out.
	TTWPerTonneG *float64 `json:"ttw_g_per_tonne,omitempty"`
	WTTPerTonneG *float64 `json:"wtt_g_per_tonne,omitempty"`
	// WTWPerContainerG, when set, makes the factor per container handled
	// instead of per tonne (HubStop.Containers). TTWPerContainerG and
	// WTTPerContainerG split it when the split is known, as above.
	WTWPerContainerG float64  `json:"wtw_g_per_container,omitempty"`
	TTWPerContainerG *float64 `json:"ttw_g_per_container,omitempty"`
	WTTPerContainerG *float64 `json:"wtt_g_per_container,omitempty"`
	Source           string   `json:"source"`
	Year             int      `json:"year"`
	DataType         string   `json:"data_type,omitempty"` // as Factor.DataType
}

// LegResult is the outcome for one leg.
type LegResult struct {
	LegID string `json:"leg_id"`
	// CargoType is the cargo type the leg was calculated for: its own,
	// the shipment's, or (an added road leg) the leg it reaches.
	CargoType string `json:"cargo_type,omitempty"`
	// Added says why the calculator added this road leg (AddedGap or
	// AddedAccess); empty for the shipment's own legs. Note explains an
	// added leg, or a leg whose end was moved to a port or terminal. From
	// and To are where the leg was calculated from and to, when known.
	Added string `json:"added,omitempty"`
	Note  string `json:"note,omitempty"`
	// FactorNote says which factor a vehicle type meant for this leg's
	// distance, when that depends on it (e.g. "container_ship").
	FactorNote string `json:"factor_note,omitempty"`
	// FactorTonnes is the tonnes the intensity was applied to when they
	// aren't the cargo's: a value published per TEU-km on a shipment given
	// in TEU (TEU × the value's tonnes per TEU).
	FactorTonnes float64   `json:"factor_tonnes,omitempty"`
	From         *Location `json:"from,omitempty"`
	To           *Location `json:"to,omitempty"`
	// Mode and VehicleType are the leg's, as given.
	Mode        Mode    `json:"mode"`
	VehicleType string  `json:"vehicle_type,omitempty"`
	DistanceKm  float64 `json:"distance_km"`
	// DistanceMethod says where the distance came from: "given" (input),
	// or the provider's name, e.g. "great_circle", "osrm".
	DistanceMethod string `json:"distance_method,omitempty"`
	// DistanceType is the kind of distance DistanceKm is (sfd, gcd, actual),
	// when known. When a DAF bridged it to the factor's basis, DAF holds
	// the ratio applied to emissions and DAFSource where it came from.
	DistanceType string  `json:"distance_type,omitempty"`
	DAF          float64 `json:"daf,omitempty"`
	DAFSource    string  `json:"daf_source,omitempty"`
	TonneKm      float64 `json:"tonne_km"`
	TTWKg        float64 `json:"ttw_kg"`
	WTTKg        float64 `json:"wtt_kg"`
	WTWKg        float64 `json:"wtw_kg"`
	// IntensityG is WTW g CO2e per tonne-km for this leg.
	IntensityG float64 `json:"intensity_g_per_tkm"`
	// BiogenicCO2Kg is the biogenic CO2 of the fuel burnt (fuel method with
	// a fuel that has a biogenic factor), reported separately and not part
	// of TTW or WTW (GLEC Framework v3.3 p. 64).
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
	Method        string  `json:"method"`
	// DataType is primary, modelled or default (see Data* constants).
	DataType string `json:"data_type"`
	// Factor is set for the intensity method, FuelFactor and
	// AllocationShare for the fuel method.
	Factor          *Factor     `json:"factor,omitempty"`
	FuelFactor      *FuelFactor `json:"fuel_factor,omitempty"`
	AllocationShare float64     `json:"allocation_share,omitempty"`
	// Set for the load model method: the inputs used.
	// LoadFactor is the load as a share of PayloadT, the model's payload
	// ("100% laden"); LoadTonnes is set when the input gave tonnes.
	LoadFactor      float64 `json:"load_factor,omitempty"`
	LoadTonnes      float64 `json:"load_tonnes,omitempty"`
	EmptyTripFactor float64 `json:"empty_trip_factor,omitempty"`
	PayloadT        float64 `json:"payload_t,omitempty"`
	// RepositioningKm is the empty km given for the leg; they are included
	// in EmptyTripFactor.
	RepositioningKm float64 `json:"repositioning_km,omitempty"`
	// Set for the energy method.
	EnergyKWhPerTkm float64 `json:"energy_kwh_per_tkm,omitempty"`
	Country         string  `json:"country,omitempty"`
	// RouteParts is the routed distance by kind (e.g. ferry, electrified),
	// when the distance provider reports it.
	RouteParts map[string]float64 `json:"route_parts_km,omitempty"`
	// CountryKm is the routed distance on land by country (ISO 3166-1
	// alpha-2), when asked for (WithCountryKm) and the router can split
	// it. Ferry crossings are not in it (they are in RouteParts), so on a
	// leg with a ferry it adds up to the distance less the ferry km.
	// CountryUnknown is distance the borders can't place.
	CountryKm map[string]float64 `json:"country_km,omitempty"`
	// RegionKm splits CountryKm further where road charges differ by
	// region: Belgium's Flanders (BE-VLG), Wallonia (BE-WAL) and
	// Brussels-Capital (BE-BRU), by ISO 3166-2 code. A country's regions add
	// up to its CountryKm. Only with CountryKm, for legs in such a country.
	RegionKm map[string]float64 `json:"region_km,omitempty"`
	// CountrySections is the leg's route by country in route order, with
	// CountryKm: each section's km along the leg (DistanceKm), the first
	// from 0, each from where the one before ends, the last to DistanceKm.
	// A ferry crossing is a section of its own (Ferry, no country). A
	// country's land sections add up to its CountryKm; a country the route
	// enters twice has two sections. Sections in Germany and Belgium have
	// their regions (ISO 3166-2) the same way.
	CountrySections []CountrySection `json:"country_sections,omitempty"`
	// Path is the leg's route as GeoJSON [lon, lat] points, when asked for
	// (single calculations only). Two points mean a straight line: the
	// distance was given or estimated, not routed.
	Path [][2]float64 `json:"path,omitempty"`
	// Parts are calculated separately and included in the leg's totals:
	// a ferry crossing on a road leg, or the electrified track of a rail
	// leg. Method and Factor above describe the rest of the leg.
	Parts []LegPart `json:"parts,omitempty"`
	// Summary (what tells the route apart: main roads and ferries) and
	// DurationH (the router's driving time in hours, ferry time included)
	// are set on every routed road leg of a single calculation (since
	// v0.18.0; before, only when alternatives were asked for); in a batch a
	// leg whose km came from a distance table (Prefetch) has neither. Alternatives are the other routes, when
	// asked for (RouteOptions.Alternatives > 1), each calculated exactly
	// like this one. ID names an alternative ("alt1", "alt2"); the leg's
	// own route has none.
	ID           string        `json:"id,omitempty"`
	Summary      *RouteSummary `json:"summary,omitempty"`
	DurationH    float64       `json:"duration_h,omitempty"`
	Alternatives []LegResult   `json:"alternatives,omitempty"`
	// AirPollutants are the leg's exhaust air pollutants (GLEC v3.3 Module
	// 6, Tier 1), from the fuel it burnt.
	AirPollutants *LegAirPollutants `json:"air_pollutants,omitempty"`
	// Restrictions is what a road leg with a vehicle was routed for (see
	// Leg.Vehicle); left out when the leg has none or wasn't routed.
	Restrictions *Restrictions `json:"restrictions,omitempty"`
	// warnings are the route's, for the result's Warnings.
	warnings []Warning
}

// HubResult is the outcome for one hub stop.
type HubResult struct {
	HubID      string  `json:"hub_id"`
	HubType    string  `json:"hub_type"`
	Note       string  `json:"note,omitempty"` // e.g. the hub type the cargo type chose
	Containers float64 `json:"containers,omitempty"`
	WTWKg      float64 `json:"wtw_kg"`
	// TTWKg and WTTKg split WTWKg when the hub factor gives the split; left
	// out (not zero) when it doesn't, e.g. for GLEC's WTW-only defaults.
	TTWKg    *float64 `json:"ttw_kg,omitempty"`
	WTTKg    *float64 `json:"wtt_kg,omitempty"`
	DataType string   `json:"data_type"`
	Source   string   `json:"source"`
}

// Data types, as ISO 14083 distinguishes them: measured by the operator
// (primary), calculated from a model of the operation (modelled), or taken
// from a published default value (default).
const (
	DataPrimary  = "primary"
	DataModelled = "modelled"
	DataDefault  = "default"
)

// Result is the outcome for a whole shipment.
type Result struct {
	ShipmentID string `json:"shipment_id"`
	// WeightKg is the cargo weight used; WeightBasis says how it was set:
	// "weight" (given) or "teu" (TEU x net tonnes per TEU).
	WeightKg    float64 `json:"weight_kg"`
	WeightBasis string  `json:"weight_basis"`
	// ContainerLoad is the container load profile that set the tonnes per
	// TEU: the shipment's, or DefaultContainerLoad when only TEU was given.
	ContainerLoad string `json:"container_load,omitempty"`
	// TEU is the shipment's TEU when it was given in TEU (weight_basis teu).
	TEU     float64     `json:"teu,omitempty"`
	Legs    []LegResult `json:"legs"`
	Hubs    []HubResult `json:"hubs,omitempty"`
	TonneKm float64     `json:"tonne_km"`
	TTWKg   float64     `json:"ttw_kg"`
	WTTKg   float64     `json:"wtt_kg"`
	WTWKg   float64     `json:"wtw_kg"`
	// IntensityG is transport WTW intensity in g CO2e per tonne-km (hubs excluded).
	IntensityG float64 `json:"intensity_g_per_tkm"`
	// BiogenicCO2Kg sums the legs' biogenic CO2: reported separately, not
	// part of the totals (GLEC Framework v3.3 p. 64). Fleet-average factors
	// don't give it, so only measured fuel contributes.
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
	// GHGProtocol places the emissions in GHG Protocol scopes (see GHGScopes).
	GHGProtocol GHGScopes `json:"ghg_protocol"`
	// AirPollutants are the shipment's exhaust air pollutants, summed over
	// its legs (hubs are outside GLEC Module 6).
	AirPollutants *AirPollutantTotals `json:"air_pollutants,omitempty"`
	// PrimaryDataShare is the share of WTW emissions computed from primary
	// data (0..1).
	PrimaryDataShare float64 `json:"primary_data_share"`
	// Dataset names the factor dataset used, when the provider reports it.
	Dataset string `json:"dataset,omitempty"`
	// Basis is what the result was calculated by (CurrentBasis), and
	// Certification what it says about Regulation (EU) 2026/1030.
	Basis         Basis  `json:"basis"`
	Certification string `json:"certification"`
	// Warnings are things left as given that may make the result too low:
	// gaps between legs not covered, sea legs starting or ending inland.
	Warnings []Warning `json:"warnings,omitempty"`
}

// DatasetNamer is optionally implemented by a FactorProvider.
type DatasetNamer interface {
	DatasetName() string
}

// CountrySection is a stretch of a leg's route in one country (see
// LegResult.CountrySections), km from the leg's start.
type CountrySection struct {
	// Country is ISO 3166-1 alpha-2 as in CountryKm ("" on a ferry).
	Country string  `json:"country"`
	FromKm  float64 `json:"from_km"`
	ToKm    float64 `json:"to_km"`
	Ferry   bool    `json:"ferry,omitempty"`
	// Regions split the section by region (ISO 3166-2, e.g. DE-BY), in
	// route order, where the borders have regions (Germany, Belgium).
	Regions []RegionSection `json:"regions,omitempty"`
}

// RegionSection is a stretch of a CountrySection in one region.
type RegionSection struct {
	Region string  `json:"region"`
	FromKm float64 `json:"from_km"`
	ToKm   float64 `json:"to_km"`
}
