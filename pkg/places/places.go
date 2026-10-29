// Package places finds transport locations by name or code: cities
// (GeoNames), ports, rail and road terminals and other UN/LOCODE
// locations, and airports by IATA code (OurAirports). The list is built
// by tools/gazetteer and embedded, so searches need no outside service.
package places

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/majidkabir/sootcase/pkg/calc"
)

//go:embed data/places.tsv.gz
var embedded []byte

// Kinds of place. A UN/LOCODE location can be several at once.
const (
	City    = "city"    // a city or town (GeoNames, population 5,000 or more)
	Port    = "port"    // sea or inland port (UN/LOCODE function 1)
	Rail    = "rail"    // rail terminal (function 2)
	Road    = "road"    // road terminal (function 3)
	Airport = "airport" // airport with an IATA code (OurAirports)
	Inland  = "inland"  // multimodal / inland clearance depot (function 6)
	Border  = "border"  // border crossing (function B)
)

var kindLetter = map[byte]string{'c': City, 'p': Port, 'r': Rail, 't': Road, 'a': Airport, 'i': Inland, 'b': Border}

// Place is a searchable location.
type Place struct {
	ID          string   `json:"id"`             // "locode:NLRTM", "iata:CPH", "geonames:2618425"
	Code        string   `json:"code,omitempty"` // UN/LOCODE or IATA code
	Kinds       []string `json:"kinds"`
	Name        string   `json:"name"`
	AltName     string   `json:"alt_name,omitempty"` // e.g. the English name of "København"
	Region      string   `json:"region,omitempty"`
	Country     string   `json:"country"` // ISO 3166-1 alpha-2
	CountryName string   `json:"country_name,omitempty"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`

	rank  int
	name  string // folded, with a leading space: " kobenhavn"
	alt   string // folded, with a leading space
	words string // folded " name alt region country "
}

// Index is a loaded place list.
type Index struct {
	places []Place
	byCode map[string]int

	latOnce sync.Once
	byLat   []int32 // place indices sorted by latitude, for Near

	gridOnce  sync.Once
	grid      map[int32][]int32 // place indices by 1° cell, for Country
	countries sync.Map          // Country results by rounded coordinates
	nCached   atomic.Int64
}

func cell(lat, lon float64) (int, int) {
	return int(math.Floor(lat)), int(math.Floor(lon))
}

func cellKey(la, lo int) int32 {
	lo = ((lo+180)%360+360)%360 - 180
	return int32((la+90)*360 + lo + 180)
}

// latWindow returns the indices of the places with latitude within
// [lo, hi], from a latitude-sorted copy built on first use.
func (ix *Index) latWindow(lo, hi float64) []int32 {
	ix.latOnce.Do(func() {
		ix.byLat = make([]int32, len(ix.places))
		for i := range ix.byLat {
			ix.byLat[i] = int32(i)
		}
		sort.SliceStable(ix.byLat, func(a, b int) bool { return ix.places[ix.byLat[a]].Lat < ix.places[ix.byLat[b]].Lat })
	})
	a := sort.Search(len(ix.byLat), func(i int) bool { return ix.places[ix.byLat[i]].Lat >= lo })
	b := sort.Search(len(ix.byLat), func(i int) bool { return ix.places[ix.byLat[i]].Lat > hi })
	return ix.byLat[a:b]
}

var (
	once    sync.Once
	def     *Index
	loadErr error
)

// Default returns the embedded place list, loading it on first use.
func Default() (*Index, error) {
	once.Do(func() { def, loadErr = Load(embedded) })
	return def, loadErr
}

// Load reads a gzipped place list made by tools/gazetteer.
func Load(gz []byte) (*Index, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("places: %w", err)
	}
	defer zr.Close()
	countries := map[string]string{}
	ix := &Index{byCode: map[string]int{}}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		f := strings.Split(sc.Text(), "\t")
		switch {
		case len(f) == 3 && f[0] == "C":
			countries[f[1]] = f[2]
		case len(f) == 10 && f[0] == "P":
			lat, err1 := strconv.ParseFloat(f[7], 64)
			lon, err2 := strconv.ParseFloat(f[8], 64)
			rank, err3 := strconv.Atoi(f[9])
			if err1 != nil || err2 != nil || err3 != nil {
				return nil, fmt.Errorf("places: line %d: bad number", n)
			}
			p := Place{ID: f[1], Name: f[3], AltName: f[4], Region: f[5], Country: f[6], Lat: lat, Lon: lon, rank: rank}
			for i := 0; i < len(f[2]); i++ {
				if k, ok := kindLetter[f[2][i]]; ok {
					p.Kinds = append(p.Kinds, k)
				}
			}
			if _, code, ok := strings.Cut(p.ID, ":"); ok && !strings.HasPrefix(p.ID, "geonames:") {
				p.Code = code
			}
			ix.places = append(ix.places, p)
		case len(f) == 1 && f[0] == "":
		default:
			return nil, fmt.Errorf("places: line %d: unexpected format", n)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("places: %w", err)
	}
	for i := range ix.places {
		p := &ix.places[i]
		p.CountryName = countries[p.Country]
		p.name, p.alt = " "+fold(p.Name), " "+fold(p.AltName)
		p.words = p.name + p.alt + " " + fold(p.Region) + " " + fold(p.CountryName) + " " + strings.ToLower(p.Country) + " "
		ix.byCode[p.ID] = i
		if p.Code != "" {
			if _, dup := ix.byCode[p.Code]; !dup {
				ix.byCode[p.Code] = i
			}
		}
	}
	return ix, nil
}

// Len is the number of places.
func (ix *Index) Len() int { return len(ix.places) }

// Lookup finds a place by UN/LOCODE ("NLRTM", "NL RTM"), IATA code
// ("CPH") or id ("geonames:2618425").
func (ix *Index) Lookup(code string) (Place, bool) {
	c := strings.TrimSpace(code)
	if !strings.Contains(c, ":") {
		c = strings.ToUpper(strings.ReplaceAll(c, " ", ""))
	}
	i, ok := ix.byCode[c]
	if !ok {
		return Place{}, false
	}
	return ix.places[i], true
}

// Search returns up to limit places matching q by code or name, best
// first. With kinds, only places of one of those kinds.
func (ix *Index) Search(q string, kinds []string, limit int) []Place {
	if limit <= 0 {
		limit = 10
	}
	fq := fold(q)
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(q), " ", ""))
	if fq == "" {
		return nil
	}
	tokens := strings.Fields(fq)
	fq = " " + fq
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	type hit struct{ i, score int }
	var hits []hit
	for i := range ix.places {
		p := &ix.places[i]
		if len(want) > 0 && !anyKind(p.Kinds, want) {
			continue
		}
		s := 0
		switch {
		case p.Code != "" && p.Code == code:
			s = 1000
		case p.name == fq:
			s = 500
		case p.alt == fq: // usually the English name
			s = 480
		case len(fq) < 3:
		case strings.HasPrefix(p.name, fq):
			s = 400
		case strings.HasPrefix(p.alt, fq):
			s = 350
		case strings.Contains(p.name, fq):
			s = 300
		case strings.Contains(p.alt, fq):
			s = 250
		case len(tokens) > 1 && allWordPrefixes(p.words, tokens):
			s = 200
		}
		if s > 0 {
			hits = append(hits, hit{i, s + p.rank})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return ix.places[hits[a].i].Name < ix.places[hits[b].i].Name
	})
	out := make([]Place, 0, min(limit, len(hits)))
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, ix.places[h.i])
	}
	return out
}

func anyKind(kinds []string, want map[string]bool) bool {
	for _, k := range kinds {
		if want[k] {
			return true
		}
	}
	return false
}

func allWordPrefixes(words string, tokens []string) bool {
	for _, t := range tokens {
		if !strings.Contains(words, " "+t) {
			return false
		}
	}
	return true
}

// Resolve finds a location by code for the calculator.
func (ix *Index) Resolve(code string) (calc.Location, bool) {
	p, ok := ix.Lookup(code)
	if !ok {
		return calc.Location{}, false
	}
	return ix.location(p), true
}

func (ix *Index) location(p Place) calc.Location {
	name := p.Name
	if p.AltName != "" && len(p.Kinds) > 0 && p.Kinds[0] != Airport {
		name = p.AltName // usually the English name
	}
	if p.Code != "" {
		name += " (" + p.Code + ")"
	}
	return calc.Location{Name: name, Code: p.Code, Lat: p.Lat, Lon: p.Lon}
}

// Codes resolves location codes with the embedded place list, loading it
// on first use.
type Codes struct{}

func (Codes) Resolve(code string) (calc.Location, bool) {
	ix, err := Default()
	if err != nil {
		return calc.Location{}, false
	}
	return ix.Resolve(code)
}

// Near returns the places that are all of the given kinds within maxKm of
// p, nearest first, at most n, as calculator locations. Besides the kinds
// of place, SeaPort asks for one of the large cargo sea ports.
func (ix *Index) Near(p calc.Location, kinds []string, maxKm float64, n int) []calc.Location {
	type hit struct {
		i  int
		km float64
	}
	var hits []hit
	dLat := maxKm / 111.0
	for _, j := range ix.latWindow(p.Lat-dLat, p.Lat+dLat) {
		i := int(j)
		q := &ix.places[i]
		if !q.allKinds(kinds) {
			continue
		}
		if km := haversine(p.Lat, p.Lon, q.Lat, q.Lon); km <= maxKm {
			hits = append(hits, hit{i, km})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].km != hits[b].km {
			return hits[a].km < hits[b].km
		}
		return hits[a].i < hits[b].i
	})
	out := make([]calc.Location, 0, min(n, len(hits)))
	for _, h := range hits[:min(n, len(hits))] {
		out = append(out, ix.location(ix.places[h.i]))
	}
	return out
}

// SeaPort is the kind Near takes for the large cargo sea ports.
const SeaPort = "seaport"

// LargeAirport is the kind Near takes for large airports (OurAirports type
// large_airport, ranked 60 or more).
const LargeAirport = "largeairport"

func (p *Place) allKinds(want []string) bool {
	for _, w := range want {
		found := (w == SeaPort && seaports[p.Code]) || (w == LargeAirport && p.isAirport() && p.rank >= 60)
		for _, h := range p.Kinds {
			found = found || h == w
		}
		if !found {
			return false
		}
	}
	return true
}

func (p *Place) isAirport() bool {
	for _, k := range p.Kinds {
		if k == Airport {
			return true
		}
	}
	return false
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0088
	φ1, φ2 := lat1*math.Pi/180, lat2*math.Pi/180
	dφ, dλ := φ2-φ1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Near is Index.Near on the embedded place list.
func (Codes) Near(p calc.Location, kinds []string, maxKm float64, n int) []calc.Location {
	ix, err := Default()
	if err != nil {
		return nil
	}
	return ix.Near(p, kinds, maxKm, n)
}

// Country is the country of the nearest place within 100 km of p, ""
// if there is none.
func (ix *Index) Country(p calc.Location) string {
	if p.Code != "" && len(p.Code) == 5 {
		if q, ok := ix.Lookup(p.Code); ok && q.Lat == p.Lat && q.Lon == p.Lon {
			return q.Country
		}
	}
	key := [2]int32{int32(math.Round(p.Lat * 1e4)), int32(math.Round(p.Lon * 1e4))}
	if c, ok := ix.countries.Load(key); ok {
		return c.(string)
	}
	ix.gridOnce.Do(func() {
		ix.grid = map[int32][]int32{}
		for i := range ix.places {
			la, lo := cell(ix.places[i].Lat, ix.places[i].Lon)
			k := cellKey(la, lo)
			ix.grid[k] = append(ix.grid[k], int32(i))
		}
	})
	const maxKm = 100.0
	la0, lo0 := cell(p.Lat, p.Lon)
	dLon := 1
	if c := math.Cos(p.Lat * math.Pi / 180); c > 0.01 {
		dLon = int(math.Ceil(maxKm / (111.2 * c)))
	} else {
		dLon = 180
	}
	if dLon > 180 {
		dLon = 180
	}
	best, bestKm := "", maxKm
	for la := la0 - 1; la <= la0+1; la++ {
		if la < -90 || la > 89 {
			continue
		}
		for lo := lo0 - dLon; lo <= lo0+dLon; lo++ {
			for _, j := range ix.grid[cellKey(la, lo)] {
				q := &ix.places[j]
				if km := haversine(p.Lat, p.Lon, q.Lat, q.Lon); km < bestKm {
					best, bestKm = q.Country, km
				}
			}
		}
	}
	if ix.nCached.Add(1) <= 500_000 { // bounded: a cache, not a copy of every point seen
		ix.countries.Store(key, best)
	}
	return best
}

// Country is Index.Country on the embedded place list.
func (Codes) Country(p calc.Location) string {
	ix, err := Default()
	if err != nil {
		return ""
	}
	return ix.Country(p)
}

// Town names the town a point is in or near, for labels: the biggest city
// (GeoNames, by the list's rank) within 15 km, so a city centre is named
// after the city rather than one of its districts; else the nearest city
// within 30 km. km is the distance to it; ok is false when there is none.
func (ix *Index) Town(p calc.Location) (name string, km float64, ok bool) {
	var best *Place
	bestKm, near, nearKm := 0.0, (*Place)(nil), math.Inf(1)
	for _, j := range ix.latWindow(p.Lat-30/111.0, p.Lat+30/111.0) {
		q := &ix.places[j]
		if !q.allKinds([]string{City}) {
			continue
		}
		d := haversine(p.Lat, p.Lon, q.Lat, q.Lon)
		if d > 30 {
			continue
		}
		if d < nearKm {
			near, nearKm = q, d
		}
		if d <= 15 && (best == nil || q.rank > best.rank || q.rank == best.rank && d < bestKm) {
			best, bestKm = q, d
		}
	}
	if best == nil {
		best, bestKm = near, nearKm
	}
	if best == nil {
		return "", 0, false
	}
	n := best.Name
	if best.AltName != "" {
		n = best.AltName
	}
	return n, bestKm, true
}

// Town is Index.Town on the embedded place list.
func (Codes) Town(p calc.Location) (string, float64, bool) {
	ix, err := Default()
	if err != nil {
		return "", 0, false
	}
	return ix.Town(p)
}
