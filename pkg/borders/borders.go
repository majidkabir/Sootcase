// Package borders says which country a point or a route line is in. The
// country polygons are Natural Earth's 1:10m admin-0 countries (public
// domain), built into data/borders.bin.gz by tools/borders and embedded,
// so lookups need no outside service.
//
// Countries are keyed by ISO 3166-1 alpha-2 code (Natural Earth's
// ISO_A2_EH). Areas without one (e.g. Northern Cyprus) use Natural
// Earth's ADM0_A3 code instead, which has three letters.
//
// The 1:10m borders are accurate to about a kilometre, which is the
// accuracy of a split of a route by country.
//
// Some countries are also split into regions, keyed by ISO 3166-2 code,
// where road charges differ by region: Belgium's Flanders (BE-VLG),
// Wallonia (BE-WAL) and Brussels-Capital (BE-BRU), merged from Statbel's
// statistical sectors (drawn on 1:10,000 maps), in data/regions.bin.gz.
package borders

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
)

//go:embed data/borders.bin.gz
var embedded []byte

//go:embed data/regions.bin.gz
var embeddedRegions []byte

// Source describes the embedded data, for notices and results.
const Source = "Natural Earth 1:10m admin-0 countries v5.1.2 (public domain)"

// RegionSource describes the embedded regions, for notices and results.
const RegionSource = "Belgian regions: Statbel (Statistics Belgium), statistical sectors 2026, situation 1 January 2026, CC BY 4.0"

// Polygon is a polygon's rings as [lon, lat] points: its outer ring
// first, then its holes.
type Polygon [][][2]float64

// Country is a country's code and polygons.
type Country struct {
	Code     string
	Polygons []Polygon
}

const (
	magic = "SCB1"
	// scale is the stored coordinate precision: 1e-5 degrees, about 1 m.
	scale = 1e5
)

// Encode writes countries in the embedded format: gzip of a magic string,
// then per country its code and polygons, coordinates as zigzag varint
// deltas at 1e-5 degrees.
func Encode(w io.Writer, cs []Country) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(zw)
	buf := make([]byte, binary.MaxVarintLen64)
	putU := func(v uint64) { bw.Write(buf[:binary.PutUvarint(buf, v)]) }
	putI := func(v int64) { bw.Write(buf[:binary.PutVarint(buf, v)]) }
	bw.WriteString(magic)
	putU(uint64(len(cs)))
	for _, c := range cs {
		putU(uint64(len(c.Code)))
		bw.WriteString(c.Code)
		putU(uint64(len(c.Polygons)))
		for _, p := range c.Polygons {
			putU(uint64(len(p)))
			for _, r := range p {
				putU(uint64(len(r)))
				var px, py int64
				for _, pt := range r {
					x, y := int64(math.Round(pt[0]*scale)), int64(math.Round(pt[1]*scale))
					putI(x - px)
					putI(y - py)
					px, py = x, y
				}
			}
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// Decode reads countries written by Encode.
func Decode(b []byte) ([]Country, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(magic)) {
		return nil, errors.New("borders: not a borders file")
	}
	raw = raw[len(magic):]
	bad := errors.New("borders: truncated file")
	u := func() (uint64, error) {
		v, n := binary.Uvarint(raw)
		if n <= 0 {
			return 0, bad
		}
		raw = raw[n:]
		return v, nil
	}
	i := func() (int64, error) {
		v, n := binary.Varint(raw)
		if n <= 0 {
			return 0, bad
		}
		raw = raw[n:]
		return v, nil
	}
	// counts bounds every count by the bytes left, so a corrupt file
	// can't ask for a huge allocation.
	count := func() (int, error) {
		v, err := u()
		if err != nil {
			return 0, err
		}
		if v > uint64(len(raw)) {
			return 0, bad
		}
		return int(v), nil
	}
	nc, err := count()
	if err != nil {
		return nil, err
	}
	cs := make([]Country, 0, nc)
	for ; nc > 0; nc-- {
		l, err := count()
		if err != nil {
			return nil, err
		}
		c := Country{Code: string(raw[:l])}
		raw = raw[l:]
		np, err := count()
		if err != nil {
			return nil, err
		}
		for ; np > 0; np-- {
			nr, err := count()
			if err != nil {
				return nil, err
			}
			p := make(Polygon, 0, nr)
			for ; nr > 0; nr-- {
				npt, err := count()
				if err != nil {
					return nil, err
				}
				r := make([][2]float64, npt)
				var x, y int64
				for k := range r {
					dx, err := i()
					if err != nil {
						return nil, err
					}
					dy, err := i()
					if err != nil {
						return nil, err
					}
					x, y = x+dx, y+dy
					r[k] = [2]float64{float64(x) / scale, float64(y) / scale}
				}
				p = append(p, r)
			}
			c.Polygons = append(c.Polygons, p)
		}
		cs = append(cs, c)
	}
	return cs, nil
}

var (
	once   sync.Once
	def    *Index
	defErr error
)

// Default is the index of the embedded borders, with the embedded
// regions, built on first use.
func Default() (*Index, error) {
	once.Do(func() {
		cs, err := Decode(embedded)
		if err != nil {
			defErr = fmt.Errorf("borders: embedded data: %w", err)
			return
		}
		rs, err := Decode(embeddedRegions)
		if err != nil {
			defErr = fmt.Errorf("borders: embedded regions: %w", err)
			return
		}
		def = NewIndex(cs).WithRegions(rs)
	})
	return def, defErr
}

type bbox struct{ minLon, minLat, maxLon, maxLat float64 }

func (b bbox) has(lon, lat float64) bool {
	return lon >= b.minLon && lon <= b.maxLon && lat >= b.minLat && lat <= b.maxLat
}

func (b *bbox) add(lon, lat float64) {
	b.minLon, b.maxLon = math.Min(b.minLon, lon), math.Max(b.maxLon, lon)
	b.minLat, b.maxLat = math.Min(b.minLat, lat), math.Max(b.maxLat, lat)
}

func emptyBox() bbox { return bbox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} }

// slabDeg is the height of the latitude bands that index a ring's edges:
// a point's crossing test only looks at the edges of its band.
const slabDeg = 0.05

// slabMin is the ring size from which edges are indexed by band.
const slabMin = 64

type ring struct {
	pts   [][2]float64
	box   bbox
	slab0 int       // band of slabs[0]
	slabs [][]int32 // per band, the edges (start index) that span it
}

func newRing(pts [][2]float64) ring {
	r := ring{pts: pts, box: emptyBox()}
	for _, p := range pts {
		r.box.add(p[0], p[1])
	}
	if len(pts) < slabMin {
		return r
	}
	r.slab0 = band(r.box.minLat)
	r.slabs = make([][]int32, band(r.box.maxLat)-r.slab0+1)
	for k := 0; k+1 < len(pts); k++ {
		a, b := pts[k][1], pts[k+1][1]
		lo, hi := band(math.Min(a, b)), band(math.Max(a, b))
		for s := lo; s <= hi; s++ {
			r.slabs[s-r.slab0] = append(r.slabs[s-r.slab0], int32(k))
		}
	}
	return r
}

func band(lat float64) int { return int(math.Floor(lat / slabDeg)) }

// crossings counts the ring's edges crossed by a ray from the point
// towards east (even-odd rule).
func (r *ring) crossings(lon, lat float64) int {
	n := 0
	edge := func(k int) {
		x1, y1, x2, y2 := r.pts[k][0], r.pts[k][1], r.pts[k+1][0], r.pts[k+1][1]
		if (y1 > lat) != (y2 > lat) && lon < x1+(lat-y1)*(x2-x1)/(y2-y1) {
			n++
		}
	}
	if r.slabs == nil {
		for k := 0; k+1 < len(r.pts); k++ {
			edge(k)
		}
		return n
	}
	s := band(lat) - r.slab0
	if s < 0 || s >= len(r.slabs) {
		return 0
	}
	for _, k := range r.slabs[s] {
		edge(int(k))
	}
	return n
}

type poly struct {
	rings []ring
	box   bbox
}

func (p *poly) contains(lon, lat float64) bool {
	if !p.box.has(lon, lat) {
		return false
	}
	n := 0
	for k := range p.rings {
		if p.rings[k].box.has(lon, lat) {
			n += p.rings[k].crossings(lon, lat)
		}
	}
	return n%2 == 1
}

type country struct {
	code  string
	polys []poly
}

func (c *country) contains(lon, lat float64) bool {
	for k := range c.polys {
		if c.polys[k].contains(lon, lat) {
			return true
		}
	}
	return false
}

type cell struct{ lon, lat int }

// Index answers which country a point is in. It is safe for concurrent
// use.
type Index struct {
	countries []country
	cells     map[cell][]int32 // 1-degree cell -> countries whose polygons may cover it
	// regions splits the countries in regioned into regions (ISO 3166-2
	// codes, "BE-VLG"), see WithRegions.
	regions  *Index
	regioned map[string]bool
}

// WithRegions returns the index with regions inside its countries: each
// region's code is its country's code, a hyphen and the region's (ISO
// 3166-2). SplitKmRegions uses them.
func (ix *Index) WithRegions(rs []Country) *Index {
	out := *ix
	out.regions = NewIndex(rs)
	out.regioned = map[string]bool{}
	for _, r := range rs {
		if c, _, ok := strings.Cut(r.Code, "-"); ok {
			out.regioned[c] = true
		}
	}
	return &out
}

// NewIndex indexes countries for lookups.
func NewIndex(cs []Country) *Index {
	ix := &Index{cells: map[cell][]int32{}}
	for ci, c := range cs {
		ct := country{code: c.Code}
		seen := map[cell]bool{}
		for _, p := range c.Polygons {
			pp := poly{box: emptyBox()}
			for _, pts := range p {
				r := newRing(pts)
				pp.box.add(r.box.minLon, r.box.minLat)
				pp.box.add(r.box.maxLon, r.box.maxLat)
				pp.rings = append(pp.rings, r)
			}
			if len(pp.rings) == 0 {
				continue
			}
			for x := int(math.Floor(pp.box.minLon)); x <= int(math.Floor(pp.box.maxLon)); x++ {
				for y := int(math.Floor(pp.box.minLat)); y <= int(math.Floor(pp.box.maxLat)); y++ {
					k := cell{x, y}
					if !seen[k] {
						seen[k] = true
						ix.cells[k] = append(ix.cells[k], int32(ci))
					}
				}
			}
			ct.polys = append(ct.polys, pp)
		}
		ix.countries = append(ix.countries, ct)
	}
	return ix
}

func cellOf(lon, lat float64) cell { return cell{int(math.Floor(lon)), int(math.Floor(lat))} }

// Country is the code of the country the point is in, "" if none (sea).
func (ix *Index) Country(lon, lat float64) string {
	c, _ := ix.find(lon, lat, -1)
	if c < 0 {
		return ""
	}
	return ix.countries[c].code
}

// find returns the country containing the point, trying hint first.
func (ix *Index) find(lon, lat float64, hint int) (int, bool) {
	if hint >= 0 && ix.countries[hint].contains(lon, lat) {
		return hint, true
	}
	for _, c := range ix.cells[cellOf(lon, lat)] {
		if int(c) != hint && ix.countries[c].contains(lon, lat) {
			return int(c), true
		}
	}
	return -1, false
}

// nearest is the country whose border is nearest the point, within
// maxKm, or -1. Points on bridges and in tunnels between countries are
// outside every polygon; the nearest coast is the usual (equidistance)
// basis of a sea border.
func (ix *Index) nearest(lon, lat, maxKm float64) int {
	dLat := maxKm / 111.2
	cosLat := math.Max(math.Cos(lat*math.Pi/180), 0.01)
	dLon := dLat / cosLat
	near := bbox{lon - dLon, lat - dLat, lon + dLon, lat + dLat}
	best, bestD := -1, math.Inf(1)
	tried := map[int32]bool{}
	for x := int(math.Floor(near.minLon)); x <= int(math.Floor(near.maxLon)); x++ {
		for y := int(math.Floor(near.minLat)); y <= int(math.Floor(near.maxLat)); y++ {
			for _, ci := range ix.cells[cell{x, y}] {
				if tried[ci] {
					continue
				}
				tried[ci] = true
				for _, p := range ix.countries[ci].polys {
					if !overlaps(p.box, near) {
						continue
					}
					for _, r := range p.rings {
						if !overlaps(r.box, near) {
							continue
						}
						for k := 0; k+1 < len(r.pts); k++ {
							if d := segDistKm(lon, lat, cosLat, r.pts[k], r.pts[k+1]); d < bestD {
								best, bestD = int(ci), d
							}
						}
					}
				}
			}
		}
	}
	if bestD > maxKm {
		return -1
	}
	return best
}

func overlaps(a, b bbox) bool {
	return a.minLon <= b.maxLon && b.minLon <= a.maxLon && a.minLat <= b.maxLat && b.minLat <= a.maxLat
}

// segDistKm is the distance from a point to a segment, on a local flat
// projection (good to well under a percent within tens of kilometres).
func segDistKm(lon, lat, cosLat float64, a, b [2]float64) float64 {
	ax, ay := (a[0]-lon)*cosLat*111.2, (a[1]-lat)*111.2
	bx, by := (b[0]-lon)*cosLat*111.2, (b[1]-lat)*111.2
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
	}
	x, y := ax+t*dx, ay+t*dy
	return math.Hypot(x, y)
}

// NearestKm is how far from a border a point outside every country may
// be and still count for the nearest country (a bridge or tunnel between
// countries). Farther points are at sea and count for no country.
const NearestKm = 30

// SplitKm splits a line of [lon, lat] points by country: each segment's
// great-circle length counts for the country its midpoint is in. Points
// outside every country count for the nearest one within NearestKm;
// farther ones are at sea and count for "".
func (ix *Index) SplitKm(line [][2]float64) map[string]float64 {
	countries, _ := ix.split(line, false)
	return countries
}

// SplitKmRegions is SplitKm, and the km in the countries that have
// regions split by region (ISO 3166-2 code) as well: each segment counts
// for the region its midpoint is in, else the nearest region within
// NearestKm (the regions and the countries come from different sources
// and their outer borders differ by up to about a kilometre), else "".
// The regions of a country add up to its km.
func (ix *Index) SplitKmRegions(line [][2]float64) (countries, regions map[string]float64) {
	return ix.split(line, true)
}

func (ix *Index) split(line [][2]float64, withRegions bool) (countries, regions map[string]float64) {
	out := map[string]float64{}
	if withRegions && ix.regions != nil {
		regions = map[string]float64{}
	}
	hint, rhint := -1, -1
	for k := 0; k+1 < len(line); k++ {
		a, b := line[k], line[k+1]
		km := haversineKm(a, b)
		if km == 0 {
			continue
		}
		mLon, mLat := (a[0]+b[0])/2, (a[1]+b[1])/2
		if math.Abs(a[0]-b[0]) > 180 { // crosses the antimeridian
			mLon = math.Remainder(mLon+180, 360)
		}
		c, ok := ix.find(mLon, mLat, hint)
		if !ok { // a near border first: it is much cheaper to search
			if c = ix.nearest(mLon, mLat, 3); c < 0 {
				c = ix.nearest(mLon, mLat, NearestKm)
			}
		}
		code := ""
		if c >= 0 {
			code, hint = ix.countries[c].code, c
		}
		out[code] += km
		if regions != nil && ix.regioned[code] {
			r, ok := ix.regions.find(mLon, mLat, rhint)
			if !ok {
				if r = ix.regions.nearest(mLon, mLat, 3); r < 0 {
					r = ix.regions.nearest(mLon, mLat, NearestKm)
				}
			}
			rc := ""
			if r >= 0 {
				rc, rhint = ix.regions.countries[r].code, r
			}
			regions[rc] += km
		}
	}
	return out, regions
}

func haversineKm(a, b [2]float64) float64 {
	const r = 6371.0088 // mean Earth radius, km
	rad := math.Pi / 180
	dLat, dLon := (b[1]-a[1])*rad, (b[0]-a[0])*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a[1]*rad)*math.Cos(b[1]*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}
