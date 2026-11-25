package distance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/majidkabir/sootcase/pkg/borders"
	"github.com/majidkabir/sootcase/pkg/calc"
)

// OSRM gets distances from an OSRM server's route service
// (http://project-osrm.org/docs/v5.24.0/api/#route-service): road
// distances from a car profile, or inland waterway distances from the
// barge profile in deploy/barge.lua.
//
// Point it at your own server: the public demo server is not meant for
// production traffic.
type OSRM struct {
	BaseURL string       // e.g. "http://localhost:5000"
	Profile string       // default "driving"
	Client  *http.Client // default: 10s timeout
	// Method names the distance source in results; default "osrm".
	Method string
	// MaxSnapKm rejects points farther than this from the routable network
	// (0 = no limit). OSRM snaps a point to the nearest edge however far.
	MaxSnapKm float64
	// PartModes maps OSRM travel modes of route steps (e.g. "ferry") to
	// calc route part kinds (e.g. calc.PartFerry). When set, routes are
	// fetched with steps and Route reports the km of each part.
	PartModes map[string]string
	// MaxInFlight caps the requests to this server at a time (default
	// DefaultMaxInFlight). More would only queue inside OSRM and time out
	// when many big batches run at once; here they wait their turn, with
	// the request's timeout counting from when it is sent.
	MaxInFlight int
	// Countries splits routes by country when the calculation asks for it
	// (calc.WithCountryKm). Nil: routes have no CountryKm.
	Countries CountrySplitter

	mu         sync.Mutex
	cache      map[string]calc.Route
	splitCache map[string]calc.Route // routes split by country (WantCountryKm)
	tableCache map[string]calc.Route // km from distance tables (Prefetch), for batches only
	semOnce    sync.Once
	sem        chan struct{}
}

// CountrySplitter splits a line of [lon, lat] points into km by country
// code, "" for none. *borders.Index implements it.
type CountrySplitter interface {
	SplitKm(line [][2]float64) map[string]float64
}

// RegionSplitter is a CountrySplitter that also splits the km in some
// countries by region (ISO 3166-2 code). *borders.Index implements it;
// routes then get RegionKm.
type RegionSplitter interface {
	CountrySplitter
	SplitKmRegions(line [][2]float64) (countries, regions map[string]float64)
}

// StretchSplitter is a RegionSplitter that also gives a line's stretches
// by country (and region) in order. *borders.Index implements it; routes
// then get CountrySections.
type StretchSplitter interface {
	RegionSplitter
	SplitStretches(line [][2]float64) (countries, regions map[string]float64, stretches []borders.Stretch)
}

// DefaultMaxInFlight is OSRM.MaxInFlight's default.
const DefaultMaxInFlight = 32

// acquire waits for a request slot; release with the returned func.
func (o *OSRM) acquire(ctx context.Context) (func(), error) {
	o.semOnce.Do(func() {
		n := o.MaxInFlight
		if n <= 0 {
			n = DefaultMaxInFlight
		}
		o.sem = make(chan struct{}, n)
	})
	select {
	case o.sem <- struct{}{}:
		return func() { <-o.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RoadParts reports ferry crossings on road routes (OSRM's car profile
// gives ferries the "ferry" mode).
var RoadParts = map[string]string{"ferry": calc.PartFerry}

// RailParts reports electrification on rail routes. deploy/rail.lua marks
// electrified track with OSRM's "train" mode, track tagged
// electrified=no with "driving", and track without the tag with
// "cycling": OSRM splits steps where the mode changes, and these are only
// labels.
var RailParts = map[string]string{
	"train":   calc.PartElectrified,
	"driving": calc.PartNotElectrified,
	"cycling": calc.PartElectrificationUnknown,
}

// maxCacheEntries bounds the per-process route cache;
// maxSplitCacheEntries the cache of routes split by country, which are
// bigger and asked for less often.
const (
	maxCacheEntries      = 50_000
	maxSplitCacheEntries = 10_000
)

// NewOSRM validates baseURL and returns a provider.
func NewOSRM(baseURL string) (*OSRM, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("osrm: invalid base URL %q", baseURL)
	}
	return &OSRM{BaseURL: strings.TrimRight(baseURL, "/")}, nil
}

// Distance implements calc.DistanceProvider. The mode is ignored: OSRM routes
// whatever its profile is built for.
func (o *OSRM) Distance(ctx context.Context, m calc.Mode, from, to calc.Location) (float64, error) {
	r, err := o.Route(ctx, m, from, to)
	return r.Km, err
}

// maxResponse caps an OSRM response; routes with steps are much larger.
const (
	maxResponse      = 1 << 20
	maxStepsResponse = 64 << 20
)

// Route implements calc.Router.
func (o *OSRM) Route(ctx context.Context, m calc.Mode, from, to calc.Location) (calc.Route, error) {
	key := cacheKey(from, to)
	// Routes split by country need each step's geometry; they are cached
	// apart from plain routes.
	withCountries := calc.WantCountryKm(ctx) && o.Countries != nil
	// Route options (alternatives, land only) are for road legs.
	opts := calc.RouteOptionsOf(ctx)
	if m != calc.Road {
		opts = calc.RouteOptions{}
	}
	// A vehicle (road legs) leaves out the router's classes for roads it
	// may not use.
	var truck []string
	if v := calc.VehicleOf(ctx); v != nil && m == calc.Road {
		ex, _, err := v.Exclude()
		if err != nil {
			return calc.Route{}, err
		}
		truck = ex
		if len(truck) > 1 && opts.AvoidFerries {
			// The truck profile has no set for a tunnel code with ferries
			// left out (OSRM allows 7 sets; deploy/truck.lua).
			return calc.Route{}, fmt.Errorf("%w: road routing has no profile for dangerous goods with ferries avoided", calc.ErrNoTruckRoute)
		}
	}
	// Routes with a path (single calculations, for a map), with options or
	// for a vehicle skip the cache: cached routes have none of these.
	withPath := calc.WantPath(ctx)
	cached := !withPath && opts == (calc.RouteOptions{}) && truck == nil
	if cached {
		o.mu.Lock()
		cache := o.cache
		if withCountries {
			cache = o.splitCache
		}
		if r, ok := cache[key]; ok {
			o.mu.Unlock()
			return r, nil
		}
		if r, ok := o.tableCache[key]; ok && !withCountries && calc.UseTableRoutes(ctx) {
			o.mu.Unlock()
			return r, nil
		}
		o.mu.Unlock()
	}

	q := routeQuery{path: calc.PathDetailOf(ctx), countries: withCountries,
		summary: m == calc.Road, avoidFerries: opts.AvoidFerries, exclude: truck}
	if opts.Alternatives > 1 {
		// OSRM's alternatives=N, N as asked (at most MaxAlternatives): some
		// are dropped as too long or too like another.
		q.alternatives, q.nodes = min(opts.Alternatives, calc.MaxAlternatives), true
	}
	routes, err := o.fetch(ctx, m, key, q)
	if err != nil && q.alternatives > osrmDefaultMaxAlternatives && !calc.Temporary(err) && !isNoRoute(err) && ctx.Err() == nil {
		// A server left at osrm-routed's default --max-alternatives
		// refuses more: ask for what it allows.
		q.alternatives = osrmDefaultMaxAlternatives
		routes, err = o.fetch(ctx, m, key, q)
	}
	if err != nil && truck != nil && !errors.Is(err, calc.ErrNoTruckRoute) && !calc.Temporary(err) && isNoRoute(err) && ctx.Err() == nil {
		return calc.Route{}, o.noTruckRoute(ctx, m, key, q, err)
	}
	if err != nil {
		if opts.AvoidFerries && !calc.Temporary(err) && isNoRoute(err) {
			return calc.Route{}, fmt.Errorf("no land route (ferries avoided): %w", err)
		}
		return calc.Route{}, err
	}
	r := routes[0].route
	if truck != nil {
		w, err := o.movedStops(ctx, from, to, routes[0].snapM)
		if err != nil {
			return calc.Route{}, err
		}
		r.Warnings = w
	}
	if opts.Alternatives > 1 {
		cands := routes[1:]
		// Ferry against land: a route with a ferry gets the best land-only
		// route among its alternatives, when there is one.
		// (Not for dangerous goods: the truck profile can't leave out
		// ferries and their tunnels together.)
		if !opts.AvoidFerries && r.Parts[calc.PartFerry] > 0 && len(truck) <= 1 {
			land := q
			land.alternatives, land.avoidFerries = 0, true
			lr, err := o.fetch(ctx, m, key, land)
			switch {
			case err == nil:
				cands = append(cands, lr[0])
			case calc.Temporary(err) || ctx.Err() != nil:
				return calc.Route{}, err // not a shorter list instead
			}
		}
		r.Alternatives = pickAlternatives(routes[0], cands, opts.Alternatives-1)
	}

	if cached {
		o.mu.Lock()
		cache, maxEntries := &o.cache, maxCacheEntries
		if withCountries { // apart, so that split routes don't push plain ones out
			cache, maxEntries = &o.splitCache, maxSplitCacheEntries
		}
		if *cache == nil || len(*cache) >= maxEntries {
			*cache = make(map[string]calc.Route)
		}
		(*cache)[key] = r
		o.mu.Unlock()
	}
	return r, nil
}

// osrmDefaultMaxAlternatives is osrm-routed's default --max-alternatives;
// run it with --max-alternatives 5 for calc.MaxAlternatives.
const osrmDefaultMaxAlternatives = 3

// routeQuery is what a route request asks OSRM for.
type routeQuery struct {
	path         calc.PathDetail // "" for no path
	countries    bool            // km by country: each step's geometry
	summary      bool            // roads, ferries and duration
	alternatives int             // other routes to look for (0: none)
	nodes        bool            // node annotations, to compare routes
	avoidFerries bool
	exclude      []string // the router's classes to leave out (a vehicle's)
}

// osrmRoute is one route of a response, with what is needed to compare it
// with others.
type osrmRoute struct {
	route   calc.Route
	weight  float64              // OSRM's cost, which it ranks routes by
	edges   map[[2]int64]float64 // metres by node pair (with nodes)
	ferries string               // its ferry crossings' names, as a key
	snapM   [2]float64           // metres from the start and end to the network
}

// fetch asks OSRM for the route from key's points, and others when q asks.
func (o *OSRM) fetch(ctx context.Context, m calc.Mode, key string, q routeQuery) ([]osrmRoute, error) {
	profile := o.Profile
	if profile == "" {
		profile = "driving"
	}
	steps, limit := "false", int64(maxResponse)
	if len(o.PartModes) > 0 || q.summary {
		steps, limit = "true", maxStepsResponse
	}
	overview, geometries := "false", "polyline"
	if q.countries {
		steps, limit, geometries = "true", maxStepsResponse, "geojson"
	}
	if q.path != "" {
		overview, geometries, limit = "simplified", "geojson", maxStepsResponse
		if q.path != calc.PathSimplified {
			overview = "full"
		}
	}
	alternatives := "false"
	if q.alternatives > 0 {
		alternatives = strconv.Itoa(q.alternatives)
	}
	u := fmt.Sprintf("%s/route/v1/%s/%s?overview=%s&alternatives=%s&steps=%s&geometries=%s",
		o.BaseURL, url.PathEscape(profile), key, overview, alternatives, steps, geometries)
	if q.nodes {
		u += "&annotations=nodes,distance"
	}
	if ex := q.excluded(); len(ex) > 0 {
		u += "&exclude=" + strings.Join(ex, ",")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	release, err := o.acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("osrm: %w", err)
	}
	// The slot is held until the response is read: OSRM is still sending
	// it (MaxInFlight limits the requests the server works on).
	defer release()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("osrm: %w", ctx.Err())
		}
		// Not reachable, reset or too slow: it may answer next time.
		return nil, fmt.Errorf("osrm: %w (%v)", calc.ErrTemporary, err)
	}
	defer resp.Body.Close()
	// An overloaded or restarting server, or a proxy in front of it.
	busy := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500

	var body struct {
		Code      string     `json:"code"`
		Message   string     `json:"message"`
		Routes    []rawRoute `json:"routes"`
		Waypoints []struct {
			Distance float64 `json:"distance"` // metres from the input point to the network
		} `json:"waypoints"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(&body); err != nil {
		if busy || resp.StatusCode == http.StatusOK { // cut off or garbled on the way
			return nil, fmt.Errorf("osrm: %w (HTTP %d, unreadable response: %v)", calc.ErrTemporary, resp.StatusCode, err)
		}
		return nil, fmt.Errorf("osrm: HTTP %d, unreadable response: %w", resp.StatusCode, err)
	}
	if body.Code != "Ok" {
		// NoRoute, NoSegment, InvalidQuery and the like are answers about
		// the input; an error with a 5xx status is the server's.
		if busy {
			return nil, fmt.Errorf("osrm: %w (HTTP %d, %s: %s)", calc.ErrTemporary, resp.StatusCode, body.Code, body.Message)
		}
		if len(q.exclude) > 0 && body.Code == "InvalidValue" && strings.Contains(strings.ToLower(body.Message), "exclude") {
			// Data built without the truck profile's classes.
			return nil, fmt.Errorf("%w: this server's road routing has no truck data (%s)", calc.ErrNoTruckRoute, body.Message)
		}
		return nil, fmt.Errorf("osrm: %s: %s", body.Code, body.Message)
	}
	if len(body.Routes) == 0 {
		return nil, errors.New("osrm: no route")
	}
	if o.MaxSnapKm > 0 && len(body.Waypoints) == 2 {
		e := &calc.OffNetworkError{Mode: m, FromKm: body.Waypoints[0].Distance / 1000, ToKm: body.Waypoints[1].Distance / 1000, MaxKm: o.MaxSnapKm}
		if e.FromOff() || e.ToOff() {
			return nil, e
		}
	}
	out := make([]osrmRoute, len(body.Routes))
	for i, raw := range body.Routes {
		out[i] = o.build(raw, q)
		if len(body.Waypoints) == 2 {
			out[i].snapM = [2]float64{body.Waypoints[0].Distance, body.Waypoints[1].Distance}
		}
	}
	return out, nil
}

// rawRoute is a route as OSRM sends it.
type rawRoute struct {
	Distance float64      `json:"distance"` // metres
	Duration float64      `json:"duration"` // seconds
	Weight   float64      `json:"weight"`
	Geometry lineGeometry `json:"geometry"`
	Legs     []struct {
		Steps      []osrmStep `json:"steps"`
		Annotation struct {
			Nodes    []int64   `json:"nodes"`
			Distance []float64 `json:"distance"` // metres between consecutive nodes
		} `json:"annotation"`
	} `json:"legs"`
}

// build reads one route of a response.
func (o *OSRM) build(raw rawRoute, q routeQuery) osrmRoute {
	// OSRM gives metres to 0.1 m; km rounded to that, without float noise
	// (240890.1 m is 240.8901 km, not 240.89010000000002).
	r := calc.Route{Km: math.Round(raw.Distance*10) / 1e4}
	if q.path != "" {
		r.Path = raw.Geometry.Coordinates
		if q.path == calc.PathDetailed {
			r.Path = calc.ThinPath(r.Path, calc.MaxDetailedPoints)
		}
	}
	var steps []osrmStep
	for _, leg := range raw.Legs {
		steps = append(steps, leg.Steps...)
	}
	for _, st := range steps {
		if kind, ok := o.PartModes[st.Mode]; ok && st.Distance > 0 {
			if r.Parts == nil {
				r.Parts = map[string]float64{}
			}
			r.Parts[kind] += st.Distance / 1000
		}
	}
	for k, v := range r.Parts { // step distances are rounded; keep parts within the route
		r.Parts[k] = math.Min(v, r.Km)
	}
	if q.countries {
		o.splitByCountry(&r, steps)
	}
	out := osrmRoute{weight: raw.Weight}
	if q.summary {
		r.Summary = o.summary(steps, r.Km)
		r.DurationH = raw.Duration / 3600
		var names []string
		for _, f := range r.Summary.Ferries {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		out.ferries = strings.Join(names, "|")
	}
	if q.nodes {
		out.edges = map[[2]int64]float64{}
		for _, leg := range raw.Legs {
			a := leg.Annotation
			for i := 0; i+1 < len(a.Nodes) && i < len(a.Distance); i++ {
				out.edges[[2]int64{a.Nodes[i], a.Nodes[i+1]}] += a.Distance[i]
			}
		}
	}
	out.route = r
	return out
}

// isNoRoute says whether err is OSRM finding no route between the points.
func isNoRoute(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NoRoute") || strings.Contains(s, "no route")
}

// osrmStep is a route step as OSRM sends it. Geometry is GeoJSON when
// asked for (geometries=geojson), else a polyline string; it is only read
// to split a route by country.
type osrmStep struct {
	Distance float64         `json:"distance"` // metres
	Mode     string          `json:"mode"`
	Name     string          `json:"name"`
	Ref      string          `json:"ref"`
	Geometry json.RawMessage `json:"geometry"`
}

// splitByCountry sets r's CountryKm (and RegionKm, when o.Countries has
// regions) from the steps' lines: each step's km is shared out in
// proportion to its line's length in each country and region. Ferry parts
// are left out (they are in r.Parts); a step without a line counts as
// unknown. Step distances are rounded, so the km are scaled to the
// route's km on land, then rounded to 0.1 m with the rounding on the
// largest entry, so that countries add up to the km on land and regions
// to their country.
func (o *OSRM) splitByCountry(r *calc.Route, steps []osrmStep) {
	ck, rk := map[string]float64{}, map[string]float64{}
	rs, _ := o.Countries.(RegionSplitter)
	ss, _ := o.Countries.(StretchSplitter)
	var secs []section // in route order, when ss
	for _, st := range steps {
		if st.Distance <= 0 {
			continue
		}
		km := st.Distance / 1000
		if o.PartModes[st.Mode] == calc.PartFerry {
			if ss != nil {
				secs = append(secs, section{ferry: true, km: km})
			}
			continue
		}
		var g lineGeometry
		_ = json.Unmarshal(st.Geometry, &g) // a polyline or nothing: no line
		var countries, regions map[string]float64
		var stretches []borders.Stretch
		switch {
		case ss != nil:
			countries, regions, stretches = ss.SplitStretches(g.Coordinates)
		case rs != nil:
			countries, regions = rs.SplitKmRegions(g.Coordinates)
		default:
			countries = o.Countries.SplitKm(g.Coordinates)
		}
		var lineKm float64
		for _, v := range countries {
			lineKm += v
		}
		if lineKm <= 0 {
			ck[calc.CountryUnknown] += km
			if ss != nil {
				secs = append(secs, section{country: calc.CountryUnknown, km: km})
			}
			continue
		}
		for c, v := range countries {
			ck[unknownIfEmpty(c)] += v / lineKm * km
		}
		for c, v := range regions {
			rk[unknownIfEmpty(c)] += v / lineKm * km
		}
		for _, x := range stretches {
			secs = append(secs, section{country: unknownIfEmpty(x.Country), region: x.Region, km: x.Km / lineKm * km})
		}
	}
	ferry := math.Min(r.Parts[calc.PartFerry], r.Km)
	land := r.Km - ferry
	if land <= 0 {
		return
	}
	var sum float64
	for _, v := range ck {
		sum += v
	}
	if sum <= 0 {
		return
	}
	for c := range ck {
		ck[c] *= land / sum
	}
	for c := range rk {
		rk[c] *= land / sum // as their countries' km
	}
	r.CountryKm = roundKm(ck, land)
	if len(rk) > 0 {
		// Each country's regions add up to its rounded km.
		byCountry := map[string]map[string]float64{}
		for c, v := range rk {
			cc, _, _ := strings.Cut(c, "-")
			if byCountry[cc] == nil {
				byCountry[cc] = map[string]float64{}
			}
			byCountry[cc][c] = v
		}
		r.RegionKm = map[string]float64{}
		for cc, m := range byCountry {
			for c, v := range roundKm(m, r.CountryKm[cc]) {
				r.RegionKm[c] = v
			}
		}
	}
	if ss != nil {
		var ferrySteps float64
		for _, x := range secs {
			if x.ferry {
				ferrySteps += x.km
			}
		}
		for i := range secs { // as the km on land and the ferry part
			if secs[i].ferry {
				secs[i].km *= ferry / ferrySteps
			} else {
				secs[i].km *= land / sum
			}
		}
		r.CountrySections = countrySections(secs, r.Km)
	}
}

// section is a stretch of a route in one country and region, or a ferry.
type section struct {
	country, region string
	ferry           bool
	km              float64
}

// countrySections lays secs end to end from 0 (km rounded to 0.1 m, the
// last ending at total), leaves out stretches that round to nothing and
// joins neighbours in the same country (and region).
func countrySections(secs []section, total float64) []calc.CountrySection {
	round := func(v float64) float64 { return math.Round(v*1e4) / 1e4 }
	var out []calc.CountrySection
	var at float64
	for i, x := range secs {
		from := round(at)
		at += x.km
		to := round(at)
		if i == len(secs)-1 {
			to = total
		}
		if to <= from {
			continue
		}
		country := x.country
		if x.ferry {
			country = ""
		}
		if n := len(out); n > 0 && out[n-1].Ferry == x.ferry && out[n-1].Country == country {
			out[n-1].ToKm = to
		} else {
			out = append(out, calc.CountrySection{Country: country, FromKm: from, ToKm: to, Ferry: x.ferry})
		}
		cs := &out[len(out)-1]
		if x.region == "" {
			continue
		}
		if n := len(cs.Regions); n > 0 && cs.Regions[n-1].Region == x.region {
			cs.Regions[n-1].ToKm = to
		} else {
			cs.Regions = append(cs.Regions, calc.RegionSection{Region: x.region, FromKm: from, ToKm: to})
		}
	}
	if n := len(out); n > 0 { // the last ends at total even when its own last stretch was left out
		out[n-1].ToKm = total
		if m := len(out[n-1].Regions); m > 0 {
			out[n-1].Regions[m-1].ToKm = total
		}
	}
	return out
}

func unknownIfEmpty(code string) string {
	if code == "" {
		return calc.CountryUnknown
	}
	return code
}

// roundKm rounds each value to 0.1 m and puts what rounding lost or added
// on the largest, so that they add up to total (itself rounded).
func roundKm(m map[string]float64, total float64) map[string]float64 {
	round := func(v float64) float64 { return math.Round(v*1e4) / 1e4 }
	out := make(map[string]float64, len(m))
	var sum float64
	largest := ""
	for c, v := range m {
		out[c] = round(v)
		sum += out[c]
		if largest == "" || v > m[largest] || (v == m[largest] && c < largest) {
			largest = c
		}
	}
	if largest != "" {
		out[largest] = round(out[largest] + round(total) - sum)
	}
	return out
}

// DistanceMethod implements calc.DistanceMethoder.
func (o *OSRM) DistanceMethod(calc.Mode) string {
	if o.Method != "" {
		return o.Method
	}
	return "osrm"
}

// DistanceType implements calc.DistanceTyper: a routed shortest path on a
// road or waterway network is a shortest feasible distance.
func (o *OSRM) DistanceType(calc.Mode) string { return calc.DistSFD }

// ByMode sends each mode to its own provider, and the rest to Fallback.
type ByMode struct {
	Providers map[calc.Mode]calc.DistanceProvider
	Fallback  calc.DistanceProvider
}

func (b ByMode) pick(m calc.Mode) calc.DistanceProvider {
	if p, ok := b.Providers[m]; ok {
		return p
	}
	return b.Fallback
}

// Distance implements calc.DistanceProvider.
func (b ByMode) Distance(ctx context.Context, m calc.Mode, from, to calc.Location) (float64, error) {
	p := b.pick(m)
	if p == nil {
		return 0, fmt.Errorf("no distance provider for mode %s", m)
	}
	return p.Distance(ctx, m, from, to)
}

// DistanceType implements calc.DistanceTyper.
func (b ByMode) DistanceType(m calc.Mode) string {
	if dt, ok := b.pick(m).(calc.DistanceTyper); ok {
		return dt.DistanceType(m)
	}
	return ""
}

// Route implements calc.Router: providers that can't break a route down
// return it without parts.
func (b ByMode) Route(ctx context.Context, m calc.Mode, from, to calc.Location) (calc.Route, error) {
	p := b.pick(m)
	if p == nil {
		return calc.Route{}, fmt.Errorf("no distance provider for mode %s", m)
	}
	if r, ok := p.(calc.Router); ok {
		return r.Route(ctx, m, from, to)
	}
	d, err := p.Distance(ctx, m, from, to)
	return calc.Route{Km: d}, err
}

// SnapKm implements calc.Snapper for the modes whose provider does.
func (b ByMode) SnapKm(m calc.Mode, p calc.Location, maxKm float64) (float64, bool) {
	if s, ok := b.pick(m).(calc.Snapper); ok {
		return s.SnapKm(m, p, maxKm)
	}
	return 0, false
}

// DistanceMethod implements calc.DistanceMethoder.
func (b ByMode) DistanceMethod(m calc.Mode) string {
	if dm, ok := b.pick(m).(calc.DistanceMethoder); ok {
		return dm.DistanceMethod(m)
	}
	return ""
}

// lineGeometry is a route's or step's geometry: GeoJSON's coordinates when
// asked for with geometries=geojson. OSRM sends other geometries (the
// default polyline) as an encoded string, which is not needed here and is
// skipped.
type lineGeometry struct {
	Coordinates [][2]float64 `json:"coordinates"`
}

func (g *lineGeometry) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		*g = lineGeometry{}
		return nil
	}
	type plain lineGeometry
	return json.Unmarshal(b, (*plain)(g))
}
