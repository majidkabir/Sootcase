// Package recommend looks for route changes that lower a shipment's
// emissions: road freight to rail, inland waterway or short sea, air freight
// to sea or rail, another sea port, and fuller loads. Each alternative is a
// changed copy of the shipment calculated with the same method as the
// shipment itself (GLEC Framework v3.3, ISO 14083), so the difference comes
// only from the route. Results are scenarios, not reported emissions.
//
// The rules an alternative must meet are stated defaults with sources, and
// the user can change them:
//
//   - road legs longer than min_road_km (default 300 km, EU Transport White
//     Paper 2011: road freight over 300 km should shift to rail or waterborne
//     transport) are tried on rail, inland waterway and short sea;
//   - the rail, waterway or sea part must be longer than min_main_km in a
//     straight line (default 100 km, Council Directive 92/106/EEC, Article 1);
//   - road to and from a waterway or sea port must stay within
//     max_port_road_km of it in a straight line (default 150 km, same
//     Article); road to and from rail goes to the nearest rail terminal on
//     the network (same Article: "the nearest suitable rail loading station").
//
// New rail, waterway and sea legs run between rail terminals or ports: an
// end that isn't at one moves to the nearest, with a road leg to it, even
// when it is on the network (same Article: combined transport is road to
// and from the nearest suitable station or port; calc.Leg.AtTerminals).
//
// Vehicle changes keep the route and change the vehicle of road legs on a
// fleet-average default value, with GLEC v3.3 default values: rigid to
// articulated truck 34-40 t (larger_truck), electric truck or van
// (electric_truck: Europe, China, India), bio-LNG articulated truck
// (bio_lng_truck: Europe, South America, Asia and Africa). Kinds are grouped
// by GLEC's five reduction areas (Areas).
//
// Extra transshipments: each terminal handling an alternative adds (a change
// of mode at a rail, waterway or container-ship terminal, and the ends of
// the new legs where the shipment starts or ends, as the cargo was on a
// truck there) adds GLEC Framework v3.3's default value for intermodal
// terminals (Module 2, Table 3, p. 97, per container), for the shipment's
// containers: its TEU, or its tonnes ÷ GLEC's tonnes per TEU (Section 1
// Chapter 4, logistics hubs). There is no GLEC default for airports or
// Ro-Ro and bulk terminals, so those are not counted.
//
// What it doesn't know, and says so (Limitations): transit times, costs,
// and whether a service runs on the lane.
package recommend

// Kinds of route change.
const (
	Rail      = "road_to_rail"
	Waterway  = "road_to_waterway"
	ShortSea  = "road_to_short_sea"
	AirToSea  = "air_to_sea"
	AirToRail = "air_to_rail"
	OtherPort = "other_port"
	Load      = "fuller_loads"
	// Vehicle changes on the same route (GLEC default values).
	LargerTruck   = "larger_truck"
	ElectricTruck = "electric_truck"
	BioLNGTruck   = "bio_lng_truck"
)

// Kinds lists every kind of route change, in the order they are tried.
func Kinds() []string {
	return []string{Rail, Waterway, ShortSea, AirToSea, AirToRail, OtherPort, Load, LargerTruck, ElectricTruck, BioLNGTruck}
}

// Area is one of the GLEC Framework's five areas for reducing freight
// emissions (GLEC v3.3, Section 2 Chapter 2, Figure 2, p. 67: McKinnon 2018 and
// GLEC). Freight demand (supply chain restructuring) needs business data
// Sootcase doesn't have, so no kind falls in it.
type Area struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Kinds []string `json:"kinds"`
}

// Areas are GLEC's five reduction areas and the kinds in each, in GLEC's
// order.
func Areas() []Area {
	return []Area{
		{"reduce_demand", "Reduce freight transport demand", nil},
		{"transport_modes", "Optimize freight transport modes", []string{Rail, Waterway, ShortSea, AirToSea, AirToRail, OtherPort}},
		{"asset_utilization", "Increase asset utilization", []string{Load}},
		{"energy_efficiency", "Improve fleet energy efficiency", []string{LargerTruck}},
		{"carbon_content", "Reduce carbon content of energy", []string{ElectricTruck, BioLNGTruck}},
	}
}

// AreaOf is the GLEC reduction area of a kind.
func AreaOf(kind string) string {
	for _, a := range Areas() {
		for _, k := range a.Kinds {
			if k == kind {
				return a.ID
			}
		}
	}
	return ""
}

// SourceAreas names where the five reduction areas come from.
const SourceAreas = "Reduction areas: GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 2 Chapter 2 \"Beyond reporting\" (p. 67), " +
	"Figure 2, Areas and approaches for reducing emissions in freight transport (McKinnon 2018 and GLEC)"

// Vehicle aliases the vehicle changes use (GLEC v3.3 default values).
const (
	vehArtic       = "hgv_artic"
	vehElectric    = "truck_electric"
	vehElectricVan = "van_electric"
	vehBioLNG      = "truck_bio_lng"
)

// Defaults and their sources.
const (
	DefaultMinRoadKm     = 300.0
	DefaultMinMainKm     = 100.0
	DefaultMaxPortRoadKm = 150.0
	DefaultPortRadiusKm  = 800.0
	DefaultMaxPorts      = 4

	TerminalNearest  = "nearest"
	TerminalBestTrip = "best_trip"

	Label = "Scenario: alternatives recalculated with the same method (GLEC Framework v3.3, ISO 14083), not reported emissions."

	SourceWhitePaper = "Road legs over 300 km: EU Transport White Paper 2011 (Council document 11255/11, II.8): \"30% of road freight over " +
		"300 km should shift to other modes such as rail or waterborne transport by 2030\""
	SourceMainKm = "Rail, waterway or sea part over 100 km: Council Directive 92/106/EEC (combined transport), Article 1: \"rail or inland " +
		"waterway or maritime services where this section exceeds 100 km as the crow flies\""
	SourcePortRoad = "Road to and from a waterway or sea port within 150 km of it: Council Directive 92/106/EEC, Article 1: \"within a radius " +
		"not exceeding 150 km as the crow flies from the inland waterway port or seaport of loading or unloading\""
	SourceTerminal = "Road to and from the nearest rail terminal on the rail network that can be reached by road: Council Directive " +
		"92/106/EEC, Article 1 (\"the nearest suitable rail loading station\"). Whether a terminal handles containers or trailers can't " +
		"be checked: the place list (UN/LOCODE) marks rail terminals, not their equipment"
	SourceBestTrip = "Road to and from the rail terminal giving the best trip among the nearest ones (terminal: best_trip)"
	SourcePorts    = "Other sea ports: the large cargo sea ports in Sootcase's place list (UN/LOCODE codes) within the search radius of " +
		"the port used now, with the onward leg in the same mode as now"
	SourceAir = "Air freight moved as one leg from where the cargo starts to where it ends (with the road legs next to the flight), " +
		"by sea or rail; ports and terminals are chosen as for any shipment (the calculator's access legs)"
)
