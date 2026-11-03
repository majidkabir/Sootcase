package distance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// Routes between regions.
//
// When no region holds both ends of a route (Shanghai to Hamburg with Asia
// and Europe built separately), the route is joined at a gateway: a point
// where the two regions overlap. Any road from one region to the other
// crosses their overlap, so the best gateway gives the same route as one
// graph of both regions would, to within the spacing of the gateways.
//
// Gateways are points on the overlap's edge, moved a little inside it,
// every GatewayStepDeg degrees; each is snapped to the road network. Two
// distance tables (origin to all gateways in the origin's region, all
// gateways to the destination in the destination's region) give the best
// one, a second, finer pass around it improves it, and the route is the two
// halves routed through it.

// GatewayStepDeg is the gateway spacing on the first pass, in degrees.
const GatewayStepDeg = 0.5

// gatewayFineDeg is the spacing on the second pass, within gatewayFineSpan
// degrees of the first pass's best gateway.
const (
	gatewayFineDeg  = 0.05
	gatewayFineSpan = 1.0
)

// box is a bounding box in degrees.
type box struct{ minLon, minLat, maxLon, maxLat float64 }

func (b box) contains(lon, lat float64) bool {
	return lon >= b.minLon && lon <= b.maxLon && lat >= b.minLat && lat <= b.maxLat
}

// overlap returns the part two regions share, or false when they share no
// area (regions that only touch along a line don't count).
func overlap(a, b Region) (box, bool) {
	o := box{math.Max(a.MinLon, b.MinLon), math.Max(a.MinLat, b.MinLat), math.Min(a.MaxLon, b.MaxLon), math.Min(a.MaxLat, b.MaxLat)}
	return o, o.minLon < o.maxLon && o.minLat < o.maxLat
}

// edgePoints returns points every step degrees along the edge of o moved
// inward by up to a degree (a quarter of the box at most), so that they lie
// inside both regions' road networks rather than on their cut edges.
func edgePoints(o box, step float64) []calc.Location {
	in := math.Min(1, math.Min(o.maxLon-o.minLon, o.maxLat-o.minLat)/4)
	b := box{o.minLon + in, o.minLat + in, o.maxLon - in, o.maxLat - in}
	var ps []calc.Location
	along := func(x0, y0, x1, y1 float64) { // excluding the end point
		n := int(math.Ceil(math.Max(math.Abs(x1-x0), math.Abs(y1-y0)) / step))
		for i := 0; i < n; i++ {
			f := float64(i) / float64(n)
			ps = append(ps, calc.Location{Lon: x0 + f*(x1-x0), Lat: y0 + f*(y1-y0)})
		}
	}
	along(b.minLon, b.minLat, b.maxLon, b.minLat)
	along(b.maxLon, b.minLat, b.maxLon, b.maxLat)
	along(b.maxLon, b.maxLat, b.minLon, b.maxLat)
	along(b.minLon, b.maxLat, b.minLon, b.minLat)
	return ps
}

// gateway is the best joining point found for one pair of regions.
type gateway struct {
	a, b       *Region
	at         calc.Location // on the road network
	km         float64       // origin to gateway to destination
	fromSnapKm float64
	toSnapKm   float64
}

// gatewayChunk is how many gateways go into one table request, keeping
// request URLs short.
const gatewayChunk = 100

// search finds the best of the candidate points to join a and b at.
func search(ctx context.Context, a, b *Region, o box, from, to calc.Location, cands []calc.Location) (gateway, error) {
	best := gateway{a: a, b: b, km: math.Inf(1)}
	seen := map[string]bool{}
	for start := 0; start < len(cands); start += gatewayChunk {
		chunk := cands[start:min(start+gatewayChunk, len(cands))]
		coords := []string{coord(from)}
		for _, c := range chunk {
			coords = append(coords, coord(c))
		}
		t1, err := a.OSRM.table(ctx, coords, 1, "")
		if err != nil {
			return gateway{}, err
		}
		best.fromSnapKm = t1.srcSnap[0] / 1000
		// Gateways the origin reaches, snapped to a point both regions hold.
		var at []calc.Location
		var d1 []float64
		for i, d := range t1.dist[0] {
			lon, lat := t1.dstLoc[i][0], t1.dstLoc[i][1]
			k := fmt.Sprintf("%.5f,%.5f", lon, lat)
			if d == nil || !o.contains(lon, lat) || seen[k] {
				continue
			}
			seen[k] = true
			at = append(at, calc.Location{Lon: lon, Lat: lat})
			d1 = append(d1, *d)
		}
		if len(at) == 0 {
			continue
		}
		coords = coords[:0]
		for _, g := range at {
			coords = append(coords, coord(g))
		}
		coords = append(coords, coord(to))
		t2, err := b.OSRM.table(ctx, coords, len(at), "")
		if err != nil {
			return gateway{}, err
		}
		best.toSnapKm = t2.dstSnap[0] / 1000
		for i := range at {
			if d2 := t2.dist[i][0]; d2 != nil && (d1[i]+*d2)/1000 < best.km {
				best.at, best.km = at[i], (d1[i]+*d2)/1000
			}
		}
	}
	if math.IsInf(best.km, 1) {
		return best, errNoGateway
	}
	return best, nil
}

var errNoGateway = errors.New("no road joins the two regions where they overlap")

func coord(l calc.Location) string { return fmt.Sprintf("%.5f,%.5f", l.Lon, l.Lat) }

// join finds the best gateway between regions a and b: a coarse pass along
// the overlap's edge, then a finer one near the best point.
func join(ctx context.Context, a, b *Region, from, to calc.Location) (gateway, error) {
	o, _ := overlap(*a, *b)
	coarse := edgePoints(o, GatewayStepDeg)
	g, err := search(ctx, a, b, o, from, to, coarse)
	if err != nil {
		return g, err
	}
	var fine []calc.Location
	for _, p := range edgePoints(o, gatewayFineDeg) {
		if math.Abs(p.Lon-g.at.Lon) <= gatewayFineSpan && math.Abs(p.Lat-g.at.Lat) <= gatewayFineSpan {
			fine = append(fine, p)
		}
	}
	if len(fine) > 0 {
		if f, err := search(ctx, a, b, o, from, to, fine); err == nil && f.km < g.km {
			g = f
		} else if err != nil && calc.Temporary(err) {
			return g, err
		}
	}
	return g, nil
}

// viaCache remembers the gateway chosen for a route, so a batch repeating
// a lane searches once.
var viaCache = struct {
	sync.Mutex
	m map[string]gateway
}{}

const maxViaEntries = 10_000

// routeVia routes from and to through a gateway when no region holds both.
// noRegion is returned when no pair of overlapping regions does.
func (rs Regions) routeVia(ctx context.Context, m calc.Mode, from, to calc.Location, noRegion error) (calc.Route, error) {
	var key string
	if len(rs) > 0 {
		key = fmt.Sprintf("%p|%s", rs[0].OSRM, cacheKey(from, to))
	}
	viaCache.Lock()
	g, ok := viaCache.m[key]
	viaCache.Unlock()

	if !ok {
		var tried bool
		var lastErr error
		best := gateway{km: math.Inf(1)}
		for i := range rs {
			for j := range rs {
				a, b := &rs[i], &rs[j]
				if i == j || !a.contains(from) || !b.contains(to) {
					continue
				}
				if _, ok := overlap(*a, *b); !ok {
					continue
				}
				tried = true
				c, err := join(ctx, a, b, from, to)
				if err != nil {
					if calc.Temporary(err) || ctx.Err() != nil {
						return calc.Route{}, err
					}
					lastErr = err
					continue
				}
				if c.km < best.km {
					best = c
				}
			}
		}
		if !tried {
			return calc.Route{}, noRegion
		}
		if math.IsInf(best.km, 1) {
			return calc.Route{}, fmt.Errorf("osrm: no road route between %.4f,%.4f and %.4f,%.4f across routing regions: %w",
				from.Lat, from.Lon, to.Lat, to.Lon, lastErr)
		}
		if mx := best.a.OSRM.MaxSnapKm; mx > 0 && (best.fromSnapKm > mx || best.toSnapKm > mx) {
			return calc.Route{}, &calc.OffNetworkError{Mode: m, FromKm: best.fromSnapKm, ToKm: best.toSnapKm, MaxKm: mx}
		}
		g = best
		viaCache.Lock()
		if viaCache.m == nil || len(viaCache.m) >= maxViaEntries {
			viaCache.m = map[string]gateway{}
		}
		viaCache.m[key] = g
		viaCache.Unlock()
	}

	r1, err := g.a.OSRM.Route(ctx, m, from, g.at)
	if err != nil {
		return calc.Route{}, err
	}
	r2, err := g.b.OSRM.Route(ctx, m, g.at, to)
	if err != nil {
		return calc.Route{}, err
	}
	r := calc.Route{Km: r1.Km + r2.Km}
	for _, part := range []map[string]float64{r1.Parts, r2.Parts} {
		for k, v := range part {
			if r.Parts == nil {
				r.Parts = map[string]float64{}
			}
			r.Parts[k] += v
		}
	}
	for _, ck := range []map[string]float64{r1.CountryKm, r2.CountryKm} {
		for k, v := range ck {
			if r.CountryKm == nil {
				r.CountryKm = map[string]float64{}
			}
			r.CountryKm[k] += v
		}
	}
	for _, rk := range []map[string]float64{r1.RegionKm, r2.RegionKm} {
		for k, v := range rk {
			if r.RegionKm == nil {
				r.RegionKm = map[string]float64{}
			}
			r.RegionKm[k] += v
		}
	}
	if len(r1.Path) > 0 || len(r2.Path) > 0 {
		r.Path = append(append([][2]float64{}, r1.Path...), r2.Path...)
	}
	return r, nil
}
