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
	"strings"
	"sync"
	"time"

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

	mu      sync.Mutex
	cache   map[string]calc.Route
	semOnce sync.Once
	sem     chan struct{}
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

// maxCacheEntries bounds the per-process route cache.
const maxCacheEntries = 50_000

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
	// Routes with a path (single calculations, for a map) skip the cache:
	// cached routes have no path.
	withPath := calc.WantPath(ctx)
	if !withPath {
		o.mu.Lock()
		if r, ok := o.cache[key]; ok {
			o.mu.Unlock()
			return r, nil
		}
		o.mu.Unlock()
	}

	profile := o.Profile
	if profile == "" {
		profile = "driving"
	}
	steps, limit := "false", int64(maxResponse)
	if len(o.PartModes) > 0 {
		steps, limit = "true", maxStepsResponse
	}
	overview, geometries := "false", "polyline"
	detail := calc.PathDetailOf(ctx)
	if withPath {
		overview, geometries, limit = "simplified", "geojson", maxStepsResponse
		if detail != calc.PathSimplified {
			overview = "full"
		}
	}
	u := fmt.Sprintf("%s/route/v1/%s/%s?overview=%s&alternatives=false&steps=%s&geometries=%s",
		o.BaseURL, url.PathEscape(profile), key, overview, steps, geometries)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return calc.Route{}, err
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	release, err := o.acquire(ctx)
	if err != nil {
		return calc.Route{}, fmt.Errorf("osrm: %w", err)
	}
	resp, err := client.Do(req)
	release()
	if err != nil {
		if ctx.Err() != nil {
			return calc.Route{}, fmt.Errorf("osrm: %w", ctx.Err())
		}
		// Not reachable, reset or too slow: it may answer next time.
		return calc.Route{}, fmt.Errorf("osrm: %w (%v)", calc.ErrTemporary, err)
	}
	defer resp.Body.Close()
	// An overloaded or restarting server, or a proxy in front of it.
	busy := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Routes  []struct {
			Distance float64 `json:"distance"` // metres
			Geometry struct {
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
			Legs []struct {
				Steps []struct {
					Distance float64 `json:"distance"`
					Mode     string  `json:"mode"`
				} `json:"steps"`
			} `json:"legs"`
		} `json:"routes"`
		Waypoints []struct {
			Distance float64 `json:"distance"` // metres from the input point to the network
		} `json:"waypoints"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(&body); err != nil {
		if busy || resp.StatusCode == http.StatusOK { // cut off or garbled on the way
			return calc.Route{}, fmt.Errorf("osrm: %w (HTTP %d, unreadable response: %v)", calc.ErrTemporary, resp.StatusCode, err)
		}
		return calc.Route{}, fmt.Errorf("osrm: HTTP %d, unreadable response: %w", resp.StatusCode, err)
	}
	if body.Code != "Ok" {
		// NoRoute, NoSegment, InvalidQuery and the like are answers about
		// the input; an error with a 5xx status is the server's.
		if busy {
			return calc.Route{}, fmt.Errorf("osrm: %w (HTTP %d, %s: %s)", calc.ErrTemporary, resp.StatusCode, body.Code, body.Message)
		}
		return calc.Route{}, fmt.Errorf("osrm: %s: %s", body.Code, body.Message)
	}
	if len(body.Routes) == 0 {
		return calc.Route{}, errors.New("osrm: no route")
	}
	if o.MaxSnapKm > 0 && len(body.Waypoints) == 2 {
		e := &calc.OffNetworkError{Mode: m, FromKm: body.Waypoints[0].Distance / 1000, ToKm: body.Waypoints[1].Distance / 1000, MaxKm: o.MaxSnapKm}
		if e.FromOff() || e.ToOff() {
			return calc.Route{}, e
		}
	}
	r := calc.Route{Km: body.Routes[0].Distance / 1000}
	if withPath {
		r.Path = body.Routes[0].Geometry.Coordinates
		if detail == calc.PathDetailed {
			r.Path = calc.ThinPath(r.Path, calc.MaxDetailedPoints)
		}
	}
	for _, leg := range body.Routes[0].Legs {
		for _, st := range leg.Steps {
			if kind, ok := o.PartModes[st.Mode]; ok && st.Distance > 0 {
				if r.Parts == nil {
					r.Parts = map[string]float64{}
				}
				r.Parts[kind] += st.Distance / 1000
			}
		}
	}
	for k, v := range r.Parts { // step distances are rounded; keep parts within the route
		r.Parts[k] = math.Min(v, r.Km)
	}

	if withPath {
		return r, nil
	}
	o.mu.Lock()
	if o.cache == nil || len(o.cache) >= maxCacheEntries {
		o.cache = make(map[string]calc.Route)
	}
	o.cache[key] = r
	o.mu.Unlock()
	return r, nil
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
