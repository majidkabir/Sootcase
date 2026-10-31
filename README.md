# Sootcase

*Formerly Carbonara (renamed October 2026; old links and `CARBONARA_*` settings still work).*

[![CI](https://github.com/majidkabir/Sootcase/actions/workflows/ci.yml/badge.svg)](https://github.com/majidkabir/Sootcase/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/majidkabir/Sootcase)](https://github.com/majidkabir/Sootcase/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/majidkabir/sootcase.svg)](https://pkg.go.dev/github.com/majidkabir/sootcase)
[![License: PolyForm Noncommercial](https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue)](LICENSE)
[![Try it](https://img.shields.io/badge/try%20it-sootcase.org-1F5E8C)](https://sootcase.org)

Source-available freight emissions calculator following the GLEC Framework / ISO 14083 approach. Free for non-commercial use; see [License](#license).

**Try it at [sootcase.org](https://sootcase.org)**: a free test service with worldwide road, rail, inland waterway and sea routing. Sign in with your email to use the calculator and get an API key.

![The Sootcase web page: a shipment entered leg by leg on the left; on the right its emissions, a bar per leg split into vehicle and fuel-supply emissions, and how each number was made](docs/screenshot.png)

Ships with the **GLEC Framework v3.3 default values** built in (Smart Freight Centre, used with permission): road by region, rail, air, inland waterway, container ships by trade lane, other ships by size and fuel, and logistics hubs; GLEC's fuel emission factors (Module 1) for measured fuel; plus the **UK DESNZ 2026** conversion factors for load models, ferries and the rest. Every factor is split into tank-to-wheel and well-to-tank.

## Quick start

```sh
go install github.com/majidkabir/sootcase/cmd/sootcase@latest

sootcase calc examples/cph-rotterdam.json      # one shipment -> JSON
sootcase batch examples/shipments.csv          # many shipments -> CSV
sootcase serve -addr :8080                     # HTTP API
sootcase factors                               # list vehicle types and fuels
sootcase calc -factors my-glec.json shipment.json   # use your own dataset
```

Docker:

```sh
docker run -p 8080:8080 ghcr.io/majidkabir/sootcase
curl -X POST localhost:8080/v1/calculate --data @examples/cph-rotterdam.json
```

### Everything, with real routing

[`compose.yaml`](compose.yaml) runs Sootcase with an OSRM road router and Eurostat's maritime network:

```sh
docker compose up -d            # first start downloads and prepares the data
open http://localhost:8080
```

| Setting | Default | |
|---|---|---|
| `OSRM_PBF_URL` | Denmark from Geofabrik | any `.osm.pbf`; preparing needs roughly 5–10× its size in RAM |
| `SEA_RESOLUTION_KM` | `20` | `5`, `10`, `20`, `50` or `100` |
| `SOOTCASE_PORT` | `8080` | |
| `SOOTCASE_FACTORS` | `desnz-2026,nl-iww-2026,eu-mrv-2024,glec-3.3` | as `-factors` |

Prepared data is kept in volumes and reused; changing the region re-prepares it. The maritime network (EUPL-1.2) is downloaded on your machine from a pinned SeaRoute commit and checked by SHA-256. A weekly CI job brings the whole stack up on a small region and checks that road and sea legs are actually routed.

## As a Go library

```sh
go get github.com/majidkabir/sootcase
```

```go
ds, _ := factors.Open(factors.Default) // or "desnz-2026,my-tocs.json"
c := calc.Calculator{Factors: ds, Distances: ds.Distances()}
res, err := c.Calculate(ctx, calc.Shipment{
	WeightKg: 12_000,
	Legs: []calc.Leg{{Mode: calc.Road, VehicleType: "hgv", DistanceKm: 290}},
})
// res.WTWKg, res.Legs[0].DataType, res.Legs[0].Factor.Source, ...
```

`pkg/calc` has no dependencies outside the standard library; nothing in the module does. Runnable examples for each package are on [pkg.go.dev](https://pkg.go.dev/github.com/majidkabir/sootcase) (they appear after the next release is fetched there).

## Calculation

Four methods per leg:

**Intensity (modelled data)** — the default:
```
tonne-km = weight_t × distance_km
TTW kg   = tonne-km × TTW g/tkm / 1000
WTW kg   = tonne-km × (TTW + WTT) g/tkm / 1000
```

**Fuel (primary data)** — when a leg has `fuel`:
```
share  = shipment tonne-km / vehicle_tonne_km   (1 if not given)
TTW kg = fuel_amount × TTW kg/unit × share
WTW kg = fuel_amount × (TTW + WTT) kg/unit × share
```

**Load model** — when a leg has `load_tonnes` or `load_factor` (and optionally `empty_trip_factor`), for truck classes:
```
E(load)  = emissions per vehicle-km at that load (linear between 0%, 50%, 100% laden)
g/tkm    = (E(load) + empty_trip_factor × E(empty)) / (payload_t × load_factor)
```
This is the capacity-utilisation approach EcoTransIT World and GLEC use, built entirely from DESNZ data: per-km factors at 0/50/100% laden give the load curve, and per-km ÷ per-tonne-km at 100% laden gives each class's payload (e.g. 17.9 t for an artic over 33 t). It reproduces DESNZ's per-tonne-km factors at 50% and 100% load within 0.82% WTW. (DESNZ's own well-to-tank rows per km and per tonne-km disagree by up to 3.6%; the model follows the per-km rows.) Works with aliases like `hgv`, or class ids like `hgv_non_refrigerated_all_diesel/articulated_33t`.

`load_factor` is a share of that payload, DESNZ's average full load, **not** of the truck's rated payload as in EcoTransIT: there a 40 t truck carries about 26 t at 100%, here an artic over 33 t carries 17.9 t, so the same "60%" means fewer tonnes and higher emissions per tonne. To compare with another tool, or when you know the weight, give `load_tonnes` instead: the tonnes on the truck (all cargo, averaged over the loaded km). For example, a semitrailer with 16 t on board is 60% loaded in GLEC's terms, but `load_factor: 0.6` here means 10.7 t and about a third more CO2e per tonne-km than `load_tonnes: 16`. Results and the calculation sheet say how many tonnes a load factor meant. Loads above the model's payload are real (17.9 t is an average full load, not the legal maximum), so for `load_tonnes` the model is extended along its 50–100% line up to 1.6 × the payload (28.7 t for an artic over 33 t, about a 44 t truck's legal payload), and the factor source says so. Vans have no load data in DESNZ, so no model. Cargo-type presets (bulk/average/volume load and empty-trip values) aren't included: EcoTransIT's values need their permission to reuse.

**Own values** — a leg's `own_factor` (`wtw_g_per_tkm`, `ttw_g_per_tkm`, `source`, optional `distance_basis`) uses the carrier's or your own intensity instead of a default value, as primary data (ISO 14083 7.2.3); a DAF applies when its distance basis differs from the leg's. With measured fuel, `fuel.factor` (`unit`, `wtw_kg_per_unit`, `ttw_kg_per_unit`, `source`, optional `biogenic_co2_kg_per_unit`) uses the fuel supplier's emission factor instead of the dataset's. In CSV: `own_wtw_g_per_tkm`, `own_ttw_g_per_tkm`, `own_factor_source`, `own_distance_basis`; `fuel_unit`, `fuel_wtw_kg_per_unit`, `fuel_ttw_kg_per_unit`, `fuel_factor_source`, `fuel_biogenic_co2_kg_per_unit`.

**Energy** — when a leg has `energy_kwh_per_tkm` and `country` (ISO alpha-3, e.g. `DNK`), for electric trains or trucks: the electricity used per tonne-km × that country's grid intensity, all well-to-tank (nothing is emitted at the vehicle). Grid intensities for 213 countries come from Ember via Our World in Data (2024–2025 values). There's no default energy figure: give your operator's, or your own. For metered kWh use the fuel method with fuel `electricity/DNK`.

**Repositioning** — `repositioning_km` on a truck leg: km driven empty for this trip (to the pickup, or back after delivery). They are shared over the truck's load like empty running: added to the empty trip factor as km per actual loaded km. Without a load, the shipment is taken as the whole load (a dedicated truck).

**Hubs** add `weight_t × hub g/t / 1000` (WTW) to the total. Intensity (g/tkm) covers transport only.

**GHG Protocol scopes** — every result has `ghg_protocol`: for a company buying the transport, all of it is scope 3 (category 4 for transport it pays for, inbound or outbound; category 9 for sold products moved by transport it doesn't pay for); for the operator of the vehicles, scope 1 (fuel burnt), scope 2 (electricity used, including the upstream share its factor contains) and scope 3 category 3 (producing and delivering the fuel). Hubs are well-to-wheel only and given apart. Reports take `scope3_category` (4 or 9) to name the category. Battery electric vehicle factors from DESNZ (electricity generation and T&D) are counted as energy provision (WTT), none at the vehicle (DESNZ methodology, Table 15).

**Distance** comes from `distance_km` when given. Otherwise:

| Mode | Source | Distance type |
|---|---|---|
| road | an [OSRM](https://project-osrm.org) server, with `-osrm URL` | shortest feasible (`sfd`) |
| sea | shortest route on a maritime network, with `-sea-network file.geojson` | shortest feasible (`sfd`) |
| rail | an OSRM server built with the rail profile, with `-osrm-rail URL` | shortest feasible (`sfd`) |
| inland waterway | an OSRM server built with the barge profile, with `-osrm-waterway URL` | shortest feasible (`sfd`) |
| air, and rail or inland waterway without a server | great-circle | great-circle (`gcd`) |
| road without a server | great-circle, refused for DESNZ factors (see DAF below) | great-circle (`gcd`) |

A dataset may also define `distance_adjustments` (`gcd × multiplier + add_km`) to estimate route distance from great-circle for a mode; the result then has no known type.

**Distance adjustment factors (DAF).** Following the GLEC Framework v3.3 and ISO 14083, emissions are `tonne-km × intensity × DAF`. A DAF is needed when the leg's distance type differs from the distance type a factor was built on (`distance_basis`: `sfd`, `gcd` or `actual`). Give the type of a given distance with `distance_type`. Built-in GLEC defaults:

| Mode | From → to | DAF |
|---|---|---|
| air | actual flown → great-circle | actual − 95 km (GLEC: GCD / (GCD + 95 km)) |
| road | shortest feasible → actual | × 1.05 (GLEC Section 1 Chapter 4: a DAF must be applied when the intensity is based on actual distance; 5% as in GLEC's own road default values) |
| sea | shortest feasible → actual | × 1.15 (Clean Cargo, cited by GLEC) |

DESNZ air factors are per great-circle tonne-km, as GLEC and ISO 14083 measure air; so a great-circle leg needs no DAF and a flown distance gets the air DAF. DESNZ road factors and load models are per km actually driven (fuel use from the DfT road freight survey), so a routed road distance gets the road DAF and a given `actual` distance none; GLEC gives no DAF from great-circle to actual for road, so a road leg without a router needs `distance_km`. A mismatch with no DAF is an error rather than a silent guess. Results report `distance_type`, `daf` and `daf_source` per leg.

Each leg result records `distance_method` (`given`, `osrm`, `sea_network`, `great_circle`, `great_circle_adjusted`) so every number can be traced. Plain great-circle badly understates sea distances (Rotterdam–Shanghai: 8,970 km vs 19,696 km routed), so use a sea network for sea legs.

### Sea routing

Uses Eurostat's [SeaRoute](https://github.com/eurostat/searoute) maritime network. It's EUPL-1.2 licensed, so it isn't bundled. The Compose setup prepares it for you (or run `python deploy/prepare_sea.py 20 marnet.geojson`); with GDAL it's:

```sh
curl -LO https://github.com/eurostat/searoute/raw/master/modules/core/src/main/resources/marnet/marnet_plus_20km.gpkg
ogr2ogr -f GeoJSON marnet_20km.geojson marnet_plus_20km.gpkg
sootcase calc -sea-network marnet_20km.geojson shipment.json
```

Straits and canals can be closed with `-sea-avoid suez,panama`. The Arctic routes (`northwest`, `northeast`) are closed by default; open them with `-sea-open`. Points are snapped to the nearest network node; `-sea-max-snap-km` rejects points too far from the sea.

On the 20 km network, distances are within 0.3% of SeaRoute's own output for 7 of 8 test routes (Rotterdam–Shanghai, Hamburg–New York, Yokohama–Los Angeles, …), and 1.6% for a short Baltic route where endpoint snapping dominates.

### Road routing

Run your own OSRM server; the public demo server isn't meant for production traffic:

```sh
docker run -t -v "$PWD:/data" ghcr.io/project-osrm/osrm-backend osrm-extract -p /opt/car.lua /data/denmark-latest.osm.pbf
docker run -t -v "$PWD:/data" ghcr.io/project-osrm/osrm-backend osrm-partition /data/denmark-latest.osrm
docker run -t -v "$PWD:/data" ghcr.io/project-osrm/osrm-backend osrm-customize /data/denmark-latest.osrm
docker run -t -p 5000:5000 -v "$PWD:/data" ghcr.io/project-osrm/osrm-backend osrm-routed --algorithm mld /data/denmark-latest.osrm
sootcase calc -osrm http://localhost:5000 examples/cph-rotterdam.json
```

### Rail routing

[`deploy/rail.lua`](deploy/rail.lua) is an OSRM profile for freight trains on OpenStreetMap's `railway=rail` lines, including sidings and yards that reach terminals. Trams, light rail, subways, narrow gauge and tourist lines are left out. Trains can't turn sharply or reverse at a junction, so turns over 60° carry a 100 km penalty. Points more than `-rail-max-snap-km` (default 5 km) from a line are refused.

```sh
docker compose -f compose.yaml -f compose.rail.yaml up -d
```

It works with any region, and combines with the waterway file. CI routes Saarbrücken–Saarlouis on Saarland's map each week. Without it, rail legs given as coordinates use the great-circle distance, which undercounts real track distance.

**Ferries on road routes.** When a routed road leg crosses water by ferry, those kilometres are calculated as a sea part with `-ferry-vehicle` (default `ropax`, DESNZ's large RoPax ferry), and the rest with the truck. The leg's result lists the ferry under `parts`, and reports credit it to sea. CI checks this on the Faroe Islands, where Tórshavn–Tvøroyri needs the Suðuroy ferry.

**Electrified rail.** The rail profile records which track is electrified (OpenStreetMap's `electrified` tag); results show it as `route_parts_km`. When a rail leg also has `energy_kwh_per_tkm` and `country`, track tagged `electrified=no` is calculated with the vehicle type's own factor (diesel), and the rest on that country's grid, as EcoTransIT does. Track with no tag is treated as electric, following your input. There's no default energy figure for electric freight trains: give your operator's.

### Inland waterway routing

[`deploy/barge.lua`](deploy/barge.lua) is an OSRM profile that routes along OpenStreetMap waterways a freight barge can use: CEMT class I or higher, `ship=yes`, canals with `usage=transportation`, and fairways. Routes are the shortest such path, so they're shortest feasible distances, and the inland waterway factors need no DAF on them (GLEC). Points more than `-waterway-max-snap-km` (default 10 km) from a navigable waterway are refused.

With Docker Compose, add the override file and pick a region with navigable waterways:

```sh
OSRM_PBF_URL=https://download.geofabrik.de/europe/netherlands-latest.osm.pbf \
  docker compose -f compose.yaml -f compose.waterway.yaml up -d
```

Or by hand, as for roads, with `osrm-extract -p barge.lua`, then `sootcase calc -osrm-waterway http://localhost:5001 ...`. CI routes a barge along the Saar on Saarland's map each week. Routes follow OSM's tagging, so check a few known distances for your region.

### Missing road legs

A shipment's legs should join up, a sea, rail or river leg should start and end on its network, and an air leg at airports. When they don't, Sootcase adds road legs (on by default):

- **Gaps.** When a leg starts more than 5 km from where the one before ended, a road leg is added across the gap.
- **First and last mile.** When a sea leg starts or ends inland (more than 50 km from the sea network and not at a sea port), it is moved to a large cargo sea port, and a road leg is added between the port and the inland point. The port is the one with the best trip from the point by road and on by sea, counting a road kilometre as three sea kilometres and preferring ports in the same country, so Munich goes to Hamburg for New York and to Venice for Shanghai. The port list is [`pkg/places/seaports.go`](pkg/places/seaports.go). A rail or river leg whose end is off its network (more than 5 km / 10 km away) is moved to the nearest rail terminal or port on the network that gives the best trip in the same way.
- **Airports.** The GLEC Framework measures air distance as the great circle between the origin and destination airports (Section 1 Chapter 4). An air leg whose end is more than 5 km from any airport is moved to a large airport (OurAirports `large_airport`; any airport where there is none within 500 km) chosen the same way, and a road leg is added to it.

Added road legs use the shipment's first road leg's vehicle type, or `hgv` (`-road-leg-vehicle` sets another). They are marked `added` (`gap` or `access`) with a `note` saying why, moved legs carry a `note` too, and reports count them. An added leg that can't be routed is left out with a warning, as it would have been without adding it.

Turn it off with `"add_road_legs": false` on a shipment, `?add_road_legs=false` on the API, the checkbox on the web page or `-road-legs=false` on the command line. Gaps, inland sea ends and air ends away from airports are then listed in the result's `warnings` instead. Distances given as `distance_km` are never changed.

## Verified against GLEC

[`pkg/toc/glec_test.go`](pkg/toc/glec_test.go) reproduces worked examples from the GLEC Framework v3.3 (Section 3, Module 4) end to end through Sootcase: fuel and activity data → TOC/HOC intensities → shipment emissions. Road (p.119), a hybrid diesel/electric fleet (p.121), air (p.131–132), bulk sea with hubs (p.135–136) and a two-intensity container terminal (p.138–139) all match GLEC's published figures to the precision printed, except where the example contradicts itself. Notes from doing this:

- GLEC sometimes rounds an intensity before applying it (air: 1.244 kg/tkm), so a few published results differ from the unrounded calculation in the last digit. The tests allow for that.
- Hub example: the "general processes" electricity (70,400 kg for 160,000 kWh) uses 0.44 kg/kWh, while Table 13 gives 0.335; the items of the "unspecified group" add up to 810,019 kg, not the printed 814,019 (so 0.191 kg/t, not 0.192, and 16.6 kg, not 16.7, for the 87 t transfer); and 5.1 divides the v3.2 edition's total (991,519 kg) instead of Table 14's 860,269 kg (0.20 kg/t, not 0.23). The tests follow the printed table items.
- Bulk sea: Table 11 prints 0.00676 kg/tkm as the overall TOC intensity; the text's 0.00685 is WTT + TTW and is what Sootcase gives.
- Hybrid fleet: GLEC prints 334.47 g/kWh as "equivalent" to Module 1's 92.8 g/MJ, which is 334.08 g/kWh. The test uses the printed value; Sootcase's GLEC dataset uses 92.8 g/MJ.

## Reporting

Results are shaped for ISO 14083-style reporting:

- **Per element**: every leg and hub reports its emissions separately.
- **Three components**: tank-to-wheel (`ttw_kg`), well-to-tank (`wtt_kg`) and well-to-wheel (`wtw_kg`), plus WTW intensity per tonne-km, for each leg and in total. Hub factors are WTW only.
- **Data type** per element: `primary` (measured fuel), `modelled` (load or energy model from shipment inputs) or `default` (published factor), and `primary_data_share` of total WTW.
- **Traceability**: each leg carries its factor with source and year, its `distance_method`, and the result names the `dataset`.

Sootcase isn't certified or verified against ISO 14083. Treat results as calculations for your own reporting process.

### Report over many shipments

`sootcase report` (or `POST /v1/report`) turns a set of shipments into a report with the basic content ISO 14083 asks for at the level of transport or hub services (GLEC v3.3, Section 2, Chapter 1):

- what's covered: period, shipments, legs, hub stops, and the shipments that failed (excluded and listed);
- total emissions, split into operational (TTW) and energy provision (WTT), and the emission intensity with the types of distance used;
- the same per mode of transport and per hub type, and a breakdown by energy carrier;
- data quality: shares of primary, modelled and default data, the sources of default values, and each model with its parameters;
- distance adjustment factors applied, notes on omissions and deviations, and where supporting information is.

```sh
sootcase report -org "Acme Logistics" -from 2026-01-01 -to 2026-03-31 shipments.csv > q1.md
sootcase report -format json shipments.csv > q1.json
```

It reads the same CSV as `batch`, streaming, so any size works. The ISO sentence "These calculation results have been established in accordance with ISO 14083:2023" is added only with `-declare-iso` (`"declare_iso": true` in the API): conformity depends on your data collection and boundaries, which only you can vouch for.

### Your own operational data (TOCs and HOCs)

Carriers can calculate from their own measured data: group similar operations into a *transport operation category* (TOC) or *hub operation category* (HOC), and total a period's fuel and electricity against the activity carried out:

```
TOC intensity (g CO2e/tkm) = Σ fuel × fuel factor / tonne-km carried (incl. empty running)
HOC intensity (g CO2e/t)   = Σ energy × energy factor / tonnes handled
```

`sootcase toc` turns an [operations file](examples/operations.json) into a dataset layer. Shipments then use the TOC id as their vehicle type, or the HOC id as their hub type, and are labelled primary data:

```sh
sootcase toc -o acme-2025.json examples/operations.json
#  TOC dk_road_ambient   83.54 g CO2e/tkm WTW (64.33 TTW + 19.22 WTT)
#  HOC cph_crossdock    777.4 g CO2e/t WTW
sootcase calc -factors desnz-2026,acme-2025.json shipment.json
```

Fuels are converted with the selected dataset's fuel factors, including `electricity/<country>`.

## Scenarios (what-if)

`sootcase scenario shipments.csv plan.json` (or `POST /v1/scenario`) calculates the shipments as they are and with the plan's changes, with the same method, and shows both totals and the difference. Results are scenarios, not reported emissions; every value a change uses is given by you (with its source) or a default with its source, listed in the output.

```json
{"name": "2030", "changes": [
  {"type": "road_to_rail"},
  {"type": "electrify", "vehicle_types": ["hgv_rigid"], "kwh_per_tkm": 0.06, "country": "DEU", "source": "maker's data sheet"},
  {"type": "fuel_switch", "fuel": "hvo", "share": 0.5},
  {"type": "load", "vehicle_types": ["hgv_artic_33t"], "load_tonnes": 20, "empty_trip_factor": 0.15}
]}
```

| Change | What it does | Defaults (source) |
|---|---|---|
| `road_to_rail` | Road legs longer than `min_road_km` go by rail for `share` of the cargo; the rail part must be longer than `min_rail_km` in a straight line, with road legs to and from the nearest rail terminal on the network reachable by road (`terminal`: `nearest`, or `best_trip` for the calculator's usual choice), and each terminal handling counted with GLEC v3.3's intermodal terminal value per container. The place list can't tell which terminals handle containers or trailers | 300 km and 30% (EU Transport White Paper 2011: "30% of road freight over 300 km should shift to other modes … by 2030"); 100 km (Directive 92/106/EEC on combined transport, Art. 1) |
| `electrify` | Matching road legs: no tank-to-wheel emissions, `kwh_per_tkm` × the `country`'s grid factor upstream | none: give `kwh_per_tkm`, `country` and `source` |
| `fuel_switch` | Matching diesel road legs use `fuel` (`hvo`, `b100`, `biomethane`, `biomethane_lng`, `cng`, `lng` or a dataset fuel id): energy from the leg's diesel emissions and DESNZ's diesel factor per kWh, × `energy_ratio`, × the fuel's factor per GJ (modelled, ISO 14083 7.2.4). Biofuel factors exclude biogenic CO2, which is given separately as `biogenic_co2_kg` (GLEC v3.3 p. 64) | `energy_ratio` 1 (same energy per tonne-km; an assumption, stated in the output) |
| `load` | The shipment's own matching road legs get `load_tonnes` or `load_factor`, and `empty_trip_factor` | none |

`vehicle_types` limits a change to those road vehicle types (empty: all road legs). `share` applies a change to that share of the cargo on each matching leg: emissions are (1 − share) × before + share × after. Changes apply in order; a later change works on what an earlier one left on diesel. The CSV is the same as for `batch`; up to 2,500 shipments per API request.

## Recommendations (route changes)

`sootcase recommend shipments.csv` (or `POST /v1/recommend`) looks for route changes that lower each shipment's emissions and calculates every alternative with the same method as the shipment. It lists those lower than now, best first, with totals per kind and per lane. Results are scenarios, not reported emissions.

| Kind | What it tries |
|---|---|
| `road_to_rail` | Road legs over `min_road_km` by rail, with road to and from the nearest rail terminal and a terminal handling at each end |
| `road_to_waterway` | The same legs by inland waterway (`waterway_vehicle`, default `container_barge`) |
| `road_to_short_sea` | The same legs by sea (`short_sea_vehicle`, default `roro`) |
| `air_to_sea`, `air_to_rail` | Each air leg, with the road legs next to it, as one sea or rail leg from where the cargo starts to where it ends |
| `other_port` | The large sea ports within `port_radius_km` of each port used now, with the leg on the land side going there |
| `fuller_loads` | The shipment's road vehicles at `load_tonnes` or `load_factor` (and `empty_trip_factor`, `load_vehicle`); only when you give a load |
| `larger_truck` | Same route, rigid trucks as articulated 34–40 t (GLEC default value) |
| `electric_truck` | Same route, electric truck or van (GLEC v3.3 Tables 10–11; Europe, China, India) |
| `bio_lng_truck` | Same route, bio-LNG articulated truck 34–40 t (GLEC v3.3 Table 8; Europe, South America, Asia, Africa) |

Kinds are grouped by the GLEC Framework's five reduction areas (v3.3, Section 2 Chapter 2, Figure 2, p. 67; results' `areas` and each option's `area`): optimize freight transport modes (the route changes), increase asset utilization (fuller loads), improve fleet energy efficiency (larger truck), reduce carbon content of energy (electric and bio-LNG trucks). The fifth, reduce freight transport demand, needs business data and has no kind.

Rules (defaults, with sources, all settable): road legs over 300 km (EU Transport White Paper 2011); the rail, waterway or sea part over 100 km in a straight line, road to and from a waterway or sea port within 150 km of it, and road to the nearest rail terminal (Council Directive 92/106/EEC, Article 1). Sea and waterway alternatives need their networks loaded.

Not taken into account (also in every result): transit times and costs; whether a service runs on the lane; terminal equipment; combining shipments (the CSV has no dates). New rail, waterway and sea legs run between rail terminals or ports, with road to and from the nearest one (Directive 92/106/EEC), and each terminal handling this adds counts GLEC v3.3's intermodal terminal value per container (Table 3; containers = TEU given, or tonnes ÷ GLEC's tonnes per TEU); airports, Ro-Ro and bulk terminals have no GLEC value and are not counted. Up to 1,000 shipments per API request; shipments with the same legs are calculated once and scaled by weight.

## Inputs

**Biogenic CO2.** Fuels with DESNZ's "outside of scopes" factor (biofuels, and the biofuel share of average diesel and petrol) carry `biogenic_co2_kg_per_unit`. A leg given as measured fuel reports `biogenic_co2_kg`, and results and reports sum it, separately and not in the totals, as the GLEC Framework v3.3 asks (p. 64). Fleet-average vehicle factors don't give it.

**Weight or containers.** Give `weight_kg`, or `teu` plus either `teu_net_tonnes` (net cargo tonnes per TEU) or `container_load`, a profile name from the dataset's `container_loads`. The default dataset has GLEC's: `average` 10 t, `light` 6 t, `heavy` 14.5 t per TEU (GLEC Framework v3.3, Section 1 Chapter 2, for when the mass is known only in TEU); `teu` alone uses `average`. Results show the `weight_kg` used, its `weight_basis` and the `container_load` used. Container tare weight is not added.

**Cargo type.** `cargo_type` on the shipment (or a leg) is one of GLEC v3.3's cargo or load categories: `container`, `temperature_controlled`, `light`, `heavy`, and for European rail `cars`, `chemicals`, `coal_steel`, `building_materials`, `manufactured_products`, `cereals`, `truck_trailer_on_train`, `trailer_on_train` (`average` is the default). It picks GLEC's value for that cargo only where GLEC has one for the leg's own vehicle: the container row of the same truck, the European diesel or electric train for that commodity, the reefer factor of the container trade lane, GLEC's +12% (+15% for vans) on a truck or train for temperature control, the temperature-controlled intermodal terminal. Otherwise the vehicle's own value is kept and the leg's note says so; it never changes the vehicle's size, and it doesn't apply to legs with measured fuel, a load or energy use. The dataset lists them in `cargo_types`, with `cargo_variants` and `hub_cargo_variants`.

**JSON** — see [`examples/cph-rotterdam.json`](examples/cph-rotterdam.json) and the [OpenAPI spec](internal/api/openapi.yaml).

**CSV** — one row per leg or hub stop, grouped by `shipment_id`. Columns (any order):

| Column | Use |
|---|---|
| `shipment_id` | required |
| `weight_kg`, or `teu` with `teu_net_tonnes` / `container_load` | cargo; can be on the first row only |
| `cargo_type` | optional: GLEC cargo or load category (see below) |
| `leg_id`, `mode`, `vehicle_type`, `distance_km`, `distance_type` | leg |
| `from_lat`, `from_lon`, `to_lat`, `to_lon` | leg, when `distance_km` is empty |
| `from_code`, `to_code` | leg, instead of coordinates: a UN/LOCODE (`NLRTM`) or IATA airport code (`CPH`) |
| `fuel`, `fuel_amount`, `vehicle_tonne_km` | leg, fuel method |
| `load_tonnes` or `load_factor`, `empty_trip_factor` | leg, load model |
| `repositioning_km` | leg, trucks: empty km for this trip |
| `energy_kwh_per_tkm`, `country` | leg, energy method |
| `own_wtw_g_per_tkm`, `own_ttw_g_per_tkm`, `own_factor_source`, `own_distance_basis` | leg, the carrier's or your own intensity (primary data) |
| `fuel_unit`, `fuel_wtw_kg_per_unit`, `fuel_ttw_kg_per_unit`, `fuel_factor_source`, `fuel_biogenic_co2_kg_per_unit` | leg with `fuel`, the supplier's emission factor |
| `hub_id`, `hub_type`, `hub_containers` | hub stop (row is a hub when `hub_type` is set); containers for per-container hub types |

A bad row fails only its shipment; the rest still calculate. Only failures caused by the data are final: when a shipment fails because a routing service didn't answer (unreachable, timed out, overloaded or restarting), it is tried again after 1 s and 3 s. If it still fails, it is marked `retryable` (batch results, reports' `retryable_failed`, HTTP 503 with `Retry-After` for a single shipment), so you know to send it again rather than fix it.

`batch` streams: it reads one shipment at a time and calculates in parallel (`-workers`, default one per CPU), writing results in input order. Memory stays flat — a million rows used about 50 MB. This needs each shipment's rows next to each other (sorted or grouped by `shipment_id`); split-up shipments are reported per shipment. For unsorted files use `-ungrouped`, which loads the whole file.

## Web page

`sootcase serve` also serves a page at `/`: enter a shipment leg by leg, picking each leg's start and end by name or code (cities, ports, rail and road terminals, airports; the list follows the mode), and see its emissions, a bar of each leg's share split into vehicle and fuel-supply emissions, and for every number the distance method, calculation method, data type and factor source. It opens with a worked example.

Below it, **Report** takes a shipments CSV (the `batch` columns), an organisation and a period, and shows the [report](#report-over-many-shipments): totals, modes, hubs, energy carriers, data quality, notes. Download it as Markdown or JSON, and each shipment's full calculation as CSV (a row per leg and hub: distance and how it was found, method, data type, emissions, factor source, notes on added legs) or JSON, all from the one calculation. `POST /v1/report` takes the CSV directly with `Content-Type: text/csv`, report details in the query (`?organisation=...&period_from=...`).

**Check the calculation.** Under each result, "Check the calculation step by step" lays out every step: the cargo mass, each leg's distance and how it was found, the transport activity, the intensity (the factor and its source, the load model's per-km values, the DAF, the fuel or electricity used) and the emissions, then the total and the data quality. Each step shows its arithmetic with the numbers used, and the GLEC Framework v3.3 page and ISO 14083:2023 clause it follows (the clause numbers are those GLEC lists in its "ISO References" boxes), so someone who knows the methods can redo it by hand. Download it as Markdown, or print it to PDF. The API gives the same with `POST /v1/calculate?explain=true`.

Each calculation also shows the legs on a map: the route as routed (road, rail, river, sea), or a dashed straight line where the distance was given or estimated. The map uses the bundled Leaflet and OpenStreetMap's tiles; everything else on the page works offline (no external scripts or fonts), in light or dark mode.

## Streaming (Kafka and pipes)

`sootcase stream` reads one shipment JSON per line and writes one result per line, in input order:

```sh
kcat -C -b broker:9092 -t shipments -u -q -o end \
  | sootcase stream \
  | kcat -P -b broker:9092 -t shipment-emissions
```

Each output line is `{"line": n, "shipment_id": "...", "result": {...}}`, or `{"line": n, "shipment_id": "...", "error": "..."}` for a message that's invalid or can't be calculated; bad messages never stop the stream. Unknown fields are rejected, so a misspelt field can't silently change a result. Output is flushed every 100 ms (`-flush`), so results flow while input is idle. On SIGINT/SIGTERM it flushes what's done and exits; results still in flight are dropped, so pair it with at-least-once consumption. Throughput: about 60,000 shipments/s on 2 cores in 15 MB of memory.

## API

| Method | Path | |
|---|---|---|
| `POST` | `/v1/calculate` | one shipment; `?path=true` adds each leg's route as GeoJSON `[lon, lat]` points (`legs[].path`): `true`/`simplified` (a few dozen points), `detailed` (at most 1,000) or `full` (every point) |
| `POST` | `/v1/calculate/batch` | `{"shipments": [...]}`, up to 10,000 |
| `POST` | `/v1/report` | `{"organisation", "period_from", "period_to", "shipments": [...]}`, JSON or `?format=md` |
| `GET` | `/v1/places?q=rotterdam&kind=port` | find places by name, UN/LOCODE or IATA code |
| `GET` | `/v1/factors` | loaded dataset |
| `GET` | `/openapi.yaml` | spec |
| `GET` | `/healthz` | liveness |
| `GET` | `/` | web page |

A leg's `from` and `to` can be a code instead of coordinates: `{"code": "NLRTM"}` (UN/LOCODE) or `{"code": "CPH"}` (IATA); in CSV, `from_code` and `to_code`. The place list is embedded (about 160,000 places: GeoNames cities of 5,000 people or more, UN/LOCODE transport locations with coordinates, airports with IATA codes) and rebuilt monthly by `tools/gazetteer`; searches need no outside service.

Protections for running it on a network: at most `-max-concurrent` calculation requests at once (default 4 × CPUs; more get `503` with `Retry-After`), a `-request-timeout` per request (default 30 s, then `504`), `-max-legs` per shipment (default 100), big batches taking turns (batches and reports of `-big-batch` shipments or more, default 1,000, run `-big-concurrent` at a time, default 2; the rest wait in line, first come first served), at most 32 requests at a time to each OSRM server (the rest wait instead of timing out), 10 MiB bodies, 10,000 shipments per batch, panics answered with `500` without taking the server down, and `nosniff`/`no-store` headers. There's no authentication: put it behind your own gateway if it faces the internet.

## Factor datasets

`-factors` (or `SOOTCASE_FACTORS`) takes an embedded dataset name or a JSON file, or several separated by commas to layer them: `-factors desnz-2026,my-hubs.json`. Layers are combined; defining the same factor twice is an error, so nothing is silently overridden. The exceptions are a short alias (such as `container_ship`) or a DAF defined again: it is then the later dataset's.

| Dataset | What |
|---|---|
| `glec-3.3` | GLEC Framework v3.3 default values (Section 3 Module 2), built by `tools/glec/build.py` |
| `desnz-2026` | UK government GHG conversion factors 2026, imported from the official flat file |
| `nl-iww-2026` | Inland waterway factors from the Dutch [CO2 emission factor list](https://www.co2emissiefactoren.nl/) 2026 (CE Delft STREAM 2020) |
| `eu-mrv-2024` | Container ship factors from EU MRV data for 2024, published by EMSA |
| `sample` | Placeholder values, for tests only |

The default is `desnz-2026,nl-iww-2026,eu-mrv-2024,glec-3.3`: GLEC's aliases come last, so they win; the other datasets' vehicle types stay available by their full ids.

A dataset is JSON with `legs` (g CO2e/tkm), `hubs` (g CO2e/t), `fuels` (kg CO2e/unit) and optional `load_models`, `dafs`, `distance_adjustments`, `vehicle_aliases`, `fuel_aliases` and `container_loads`, each factor with a TTW/WTT split, source and year. See [`sample.json`](pkg/factors/data/sample.json) for the shape.

**Vehicle types and fuel ids** follow the source's labels, e.g. `hgv_refrigerated_all_diesel/articulated_33t/100pct_laden` or `liquid_fuels/marine_gas_oil/tonnes`. Run `sootcase factors` to list them. Common ones have short aliases; results always show the full id used:

| Mode | Aliases |
|---|---|
| road | `hgv`, `hgv_rigid`, `hgv_artic`, `hgv_artic_33t`, `hgv_reefer`, `van`, `truck_urban` (GLEC, by region), `van_electric`, `truck_electric` (GLEC: Europe × GLEC's European electricity, China, India; elsewhere refused) |
| rail | `freight_train` (GLEC, by region), `freight_train_diesel`, `freight_train_electric` (GLEC Europe), `freight_train_reefer` (GLEC +12%; not for North America) |
| sea | `container_ship`, `container_ship_reefer` (GLEC, by trade lane), `container_ship_short_sea`, `container_ship_deep_sea` (EU MRV), `bulk_carrier`, `general_cargo`, `roro`, `ropax` (DESNZ) |
| air | `air_freight` (GLEC, aircraft type unknown; short-haul under 1,500 km; within India GLEC's Indian value), `air_freighter`, `air_belly` |
| inland_waterway | `barge` (GLEC motor vessel 85–110 m), `container_barge` (GLEC container vessel 110 m) |
| fuel (per litre) | `diesel`, `diesel_mineral`, `petrol`, `hvo`, `jet_fuel` (GLEC Module 1, by region: Europe, North America, China, India), `marine_gas_oil`, `marine_fuel_oil` (DESNZ) |

**GLEC v3.3 regions and trade lanes.** A regional alias looks at the country of both ends of the leg (from the place list): when both are in the same GLEC region it uses that region's value, otherwise the alias's default (Europe and South America). Road: North America (SmartWay, US and Canada), China, India, Asia (except China and India) and Africa (GLEC's +13% for vans, +22% for heavier vehicles on the Europe and South America values), Europe and South America (with Mexico, Central America and the Caribbean; also used for Oceania, which GLEC doesn't cover). The aliases use GLEC's "starting point" for the vehicle class in each region (e.g. 125 g CO2e/t-km WTW for HGVs in Europe); North America has none in GLEC, so SmartWay TL/Dry Van. `hgv_reefer` adds GLEC's +12% for temperature-controlled freight. GLEC road values include its +5% distance conversion, so they are per shortest feasible distance. `container_ship` picks the Clean Cargo trade lane (GLEC Tables 18–19) from the zones of the two ports (North Europe, Mediterranean/Black Sea, NE Asia, SE Asia, Middle East/India, Africa, North America EC/Gulf and WC, South America, Oceania; the country lists are Sootcase's), per TEU-km ÷ 10 t per TEU, GLEC's average. A leg's note says which region or lane was used. With a load given, the road aliases keep DESNZ's load models. GLEC values without a vehicle-size-free default (non-container ships, Ro-Ro, RoPax) are available by full id (`glec/sea/bulk_carrier/35000-59999_dwt/vlsfo`), and their aliases stay on DESNZ for now.

**Inland waterways** (`nl-iww-2026`): three bulk vessel classes (`barge_bulk_small`/`medium`/`large`, 300 t to 11,000 t) and four container vessels (`barge_container_40teu`, `96teu`, `208teu`, `348teu`). Factors are per *actual* tonne-km and include sailing empty. Give coordinates with [waterway routing](#inland-waterway-routing) set up, or the river distance as `distance_km`; a shortest feasible distance (`distance_type: sfd`) is used as is, since GLEC needs no DAF for inland waterways. Without routing, a leg with only coordinates is refused: a straight line would undercount a river route, and there's no default correction for it. Source: www.co2emissiefactoren.nl, free to use with attribution.

**Container ships** (`eu-mrv-2024`): from what container ships calling at EU ports reported for 2024 under the EU MRV regulation (Regulation (EU) 2015/757): fuel, CO2e and transport work (cargo mass × distance actually sailed), per ship, as published by the European Maritime Safety Agency ([THETIS-MRV](https://mrv.emsa.europa.eu/#public/emission-report); reproduction authorised with acknowledgement of the source). Ships that emit at least half their voyage CO2 between EU ports count as short-sea (350 ships), the rest as deep-sea (1,643 ships); each factor is its fleet's total CO2e divided by its total transport work, so big ships weigh in by the work they do. Well-to-tank is the fuel they reported × DESNZ's marine fuel oil well-to-tank. Factors are per *actual* tonne-km, so a routed sea distance gets the GLEC sea DAF (× 1.15). `container_ship` means the short-sea factor on legs under 2,000 km and the deep-sea factor from 2,000 km (Sootcase's threshold, not a standard's); the leg's note says which, and `container_ship_short_sea` or `container_ship_deep_sea` choose one. DESNZ's container ship factors by size stay available by their full ids (e.g. `cargo_ship/container_ship/8000_teu`). Rebuilt by `tools/mrv`.

Why: DESNZ's container ship factors (about 20 g CO2e per tonne-km on average, 15.5 g for the largest ships) gave Shanghai → Hamburg about 2.4× the highest of six calculators compared in Schramm & Lehner (2024); the deep-sea factor from EU MRV is in their range.

**Notes on DESNZ data:**
- Air freight comes `with_rf` and `without_rf` (radiative forcing). ISO 14083 reports without RF.
- Hub factors come from GLEC v3.3 Table 3 (GILA project; Fraunhofer IML, Politecnico di Milano, GreenRouter): `transshipment`, `storage_transshipment`, `warehouse`, `liquid_bulk_terminal`, and `_mixed` for mixed ambient and temperature-controlled sites (kg CO2e per tonne; sample sizes in the source); `intermodal_terminal` and `intermodal_terminal_temperature_controlled` are per container handled: give the hub's `containers` (CSV `hub_containers`). Add your own as a layer:

  ```json
  {"name": "my-hubs", "note": "where these values come from",
   "hubs": [{"hub_type": "my_dc", "wtw_g_per_tonne": 0, "source": "…", "year": 2026}]}
  ```
- Air factors are marked as great-circle based and road factors as actual-distance based; the GLEC default DAFs above are added. DESNZ sea factors (from IMO 2009) state no distance basis, so no DAF applies to them; GLEC's own sea default values include +15%.
- GLEC default values: Smart Freight Centre. Global Logistics Emissions Council Framework for Logistics Emissions Accounting and Reporting; v3.3 edition, revised and updated (October 2026). ISBN 978-90-833629-0-8. Used with the permission of Smart Freight Centre, which is acknowledged as the source; no affiliation or endorsement is implied. GLEC Module 1 fuels: `glec/fuel/<eu|na|cn|in|marine|air>/<fuel>/<kg|t|l>` (per litre with GLEC's density; marine per kg or tonne), the biofuel blends (`diesel_b7`, `gasoline_e10`, `diesel_hvo20`, …; GLEC gives no biogenic CO2 for them), biogenic CO2 where GLEC gives it. `diesel` is GLEC's 100% diesel; a blend by its id. Electric trucks use GLEC's European average electricity (92.8 g CO2e/MJ, the only one GLEC confirms includes infrastructure as ISO 14083 asks); the charging country's own mix is more exact through `energy_kwh_per_tkm` with `country`.

**Updating DESNZ** each year: download the "flat file" from [gov.uk](https://www.gov.uk/government/collections/government-conversion-factors-for-company-reporting) and run

```sh
sootcase import-desnz -o pkg/factors/data/desnz-2027.json ghg-conversion-factors-2027-flat-format.xlsx
```

It reports how many rows it imported, skipped as blank, or couldn't match to a well-to-tank factor.

**Updating grid intensities**: `sootcase import-grid -o pkg/factors/data/grid/ember-owid.json owid-energy-data.csv`, with the CSV from [owid/energy-data](https://github.com/owid/energy-data).

**World road routing on one server.** A road graph of the whole planet needs more memory than a 256 GB server has. `-osrm-regions` (or `$SOOTCASE_OSRM_REGIONS`) takes several OSRM servers, each built for an overlapping part of the world, as `name=url@minlon,minlat,maxlon,maxlat;...`; each road leg is routed by the smallest region that holds both its ends. When no region holds both (Shanghai to Hamburg with Asia and Europe built separately), the leg is joined where the two regions overlap: any road between them crosses that overlap, so Sootcase tries points along it (every 0.5°, then every 0.05° near the best), snapped to the road network, and routes the two halves through the best one. The result matches one graph of both regions to within a few kilometres. Regions that don't overlap aren't joined, and the leg is reported as an error.

## Performance

Measured with [`tools/loadtest`](tools/loadtest/main.go) against `sootcase serve` with the default datasets, the server pinned to **one CPU core** and the load generator to another (Go 1.24, Linux, a small cloud VM). Each shipment has a road and a sea leg, distances by great-circle; routing via OSRM adds its own time.

| Request | Concurrent | Throughput | Latency p50 / p99 |
|---|---:|---:|---:|
| `/v1/calculate`, one shipment | 1 | 6,600 req/s | 0.14 / 0.39 ms |
| `/v1/calculate`, one shipment | 32 | 17,000 req/s | 1.6 / 5.6 ms |
| `/v1/calculate/batch`, 100 shipments | 4 | 59,000 shipments/s | 6.4 / 17 ms |
| `/v1/calculate/batch`, 1,000 shipments | 4 | 59,000 shipments/s | 66 / 157 ms |
| `/v1/report`, 1,000 shipments | 4 | 86,000 shipments/s | 39 / 164 ms |

Throughput scales with cores. Under overload (32 concurrent 1,000-shipment batches on one core) requests beyond `-max-concurrent` got an immediate `503` with `Retry-After`, throughput held at 59,000 shipments/s, and the server stayed at 48 MB of memory. To run it yourself:

```sh
sootcase serve &
go run ./tools/loadtest -kind calc -c 32 -d 20s
go run ./tools/loadtest -kind batch -batch 1000 -c 4 -d 20s
```

## Layout

| Path | What |
|---|---|
| `pkg/calc` | Domain model and calculation. Stdlib only. |
| `pkg/factors` | Factor datasets, embedded or from file. |
| `pkg/distance` | Great-circle with per-mode adjustment, OSRM road and waterway routing, maritime network routing. |
| `pkg/report` | ISO 14083 style reports over many shipments. |
| `pkg/csvio` | CSV input (in-memory or streaming) and output. |
| `pkg/batch` | Parallel, ordered, bounded-memory batch processing. |
| `pkg/ndjson` | Newline-delimited JSON input and output for streaming. |
| `pkg/toc` | Transport and hub operation categories from operational data. |
| `pkg/places` | Place search and location codes (embedded GeoNames, UN/LOCODE, OurAirports). |
| `internal/desnz` | DESNZ flat-file importer. |
| `internal/xlsx` | Minimal stdlib-only .xlsx reader. |
| `internal/grid` | Grid intensity importer (Our World in Data / Ember). |
| `internal/api` | HTTP handlers and OpenAPI spec. |
| `cmd/sootcase` | CLI. |
| `pkg/scenario` | What-if comparisons: the same shipments with changed inputs. |
| `pkg/recommend` | Route changes that lower emissions: alternatives per shipment, calculated with the same method. |
| `tools/loadtest` | Load generator for a running server. |
| `tools/gazetteer` | Builds the embedded place list. |

## Roadmap

- [x] Intensity and fuel-based methods, hubs
- [x] Factor datasets (embedded + file)
- [x] Great-circle distance
- [x] CLI: `calc`, `batch`, `serve`, `factors`
- [x] HTTP API + OpenAPI
- [x] CI, GoReleaser, Docker image
- [x] Real dataset: UK DESNZ 2026, with importer for future years
- [x] Dataset-level distance adjustments
- [x] GLEC v3.3 worked examples as golden tests (Module 4: road, hybrid fleet, air, bulk sea, hub)
- [x] GLEC v3.3 default values: regional road, rail and air, container trade lanes, hubs
- [x] Short aliases for common vehicle types and fuels
- [x] OSRM road routing
- [x] Sea routing on a maritime network (SeaRoute)
- [x] GLEC distance types and DAFs (air: actual − 95 km → great-circle; road: × 1.05; sea: × 1.15)
- [x] Containers in TEU
- [x] Truck load model (load factor, empty trip factor)
- [x] Electricity by country (213 grids) for electric rail and trucks
- [x] Dataset layering (e.g. DESNZ + your own hub factors)
- [x] Bundled hub / warehouse factors (GLEC v3.3 Table 3, with Smart Freight Centre's permission)
- [x] Web page for `sootcase serve`
- [x] Streaming, parallel batch
- [x] NDJSON stream mode for pipes and Kafka
- [x] Input validation, fuzzing of every parser in CI, API limits
- [x] Docker Compose with OSRM and the sea network
- [x] Transport and hub operation categories from your own data
- [x] Package docs and runnable examples
- [x] Bundled inland waterway factors (Dutch CO2 emission factor list)
- [x] Inland waterway routing (OSRM barge profile on OpenStreetMap)
- [x] ISO 14083 style reports (`sootcase report`, `/v1/report`)
- [x] What-if scenarios (`sootcase scenario`, `/v1/scenario`): road to rail, electric trucks, other fuels, loads
- [x] Recommendations (`sootcase recommend`, `/v1/recommend`): road to rail, waterway or short sea, air to sea or rail, other ports, fuller loads
- [x] Terminal handlings in recommendations and scenarios: new rail, waterway and sea legs between terminals, GLEC v3.3 intermodal terminal value per container
- [x] Missing road legs: gaps between legs, first and last mile to ports, rail terminals and airports
- [x] Cargo-type presets (GLEC v3.3's cargo and load categories)

## Releasing

Either:

- **From GitHub** (works on mobile): Actions → Release → Run workflow, enter a version like `v0.1.0`. It runs the tests, creates the tag, then builds.
- **From a terminal**: `git tag v0.1.0 && git push origin v0.1.0`.

Binaries for Linux, macOS and Windows go to GitHub Releases, the image to `ghcr.io/majidkabir/sootcase`.

## License

Code: [PolyForm Noncommercial 1.0.0](LICENSE). Free for personal, educational, research, non-profit and government use; commercial use needs a separate licence from the author. This matches the terms of the GLEC Framework, which Sootcase follows. Versions up to and including v0.1.0 were released under Apache 2.0 and stay available under it.

Required Notice: Copyright 2026 majidkabir (https://github.com/majidkabir/sootcase)

DESNZ data: Crown copyright, contains public sector information licensed under the [Open Government Licence v3.0](https://www.nationalarchives.gov.uk/doc/open-government-licence/version/3/). Grid intensities: Ember via Our World in Data, [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). See [NOTICE](NOTICE).
