// Command borders builds pkg/borders/data/borders.bin.gz, the country
// polygons that split a route's distance by country: Natural Earth's
// 1:10m admin-0 countries, release v5.1.2 (public domain).
//
//	go run ./tools/borders -fetch -out pkg/borders/data/borders.bin.gz
//
// Without -fetch it reads -in, the ne_10m_admin_0_countries.geojson file.
// Countries are keyed by ISO_A2_EH (ISO 3166-1 alpha-2, with Natural
// Earth's fixes for France, Norway and Kosovo, which gets the widely used
// user-assigned code XK). An area without one counts for the country ISO
// puts it in (ADM0_ISO: Northern Cyprus and the UN buffer zone are CY,
// Somaliland SO); the rest, with no ISO code at all (the UK's bases in
// Cyprus, disputed areas such as the Siachen Glacier), keep Natural
// Earth's three-letter ADM0_A3. Features with the same code are merged.
//
// With -key, features are keyed by that property instead. This builds
// pkg/borders/data/regions.bin.gz, the regions within countries, from the
// GeoJSON that tools/borders/regions.py writes:
//
//	go run ./tools/borders -in be_regions.geojson -key code -out pkg/borders/data/regions.bin.gz
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/majidkabir/sootcase/pkg/borders"
)

const source = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/v5.1.2/geojson/ne_10m_admin_0_countries.geojson"

func main() {
	fetch := flag.Bool("fetch", false, "download the GeoJSON from "+source)
	in := flag.String("in", "ne_10m_admin_0_countries.geojson", "GeoJSON file (without -fetch)")
	out := flag.String("out", "pkg/borders/data/borders.bin.gz", "output file")
	key := flag.String("key", "", "property that holds each feature's code (default: Natural Earth's ISO_A2_EH, else ADM0_A3)")
	flag.Parse()

	var raw []byte
	var err error
	if *fetch {
		raw, err = get(source)
	} else {
		raw, err = os.ReadFile(*in)
	}
	if err != nil {
		log.Fatal(err)
	}
	cs, err := build(raw, *key)
	if err != nil {
		log.Fatal(err)
	}
	var buf bytes.Buffer
	if err := borders.Encode(&buf, cs); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	pts := 0
	for _, c := range cs {
		for _, p := range c.Polygons {
			for _, r := range p {
				pts += len(r)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "%d areas, %d points, %d bytes -> %s\n", len(cs), pts, buf.Len(), *out)
}

func get(u string) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

type feature struct {
	Properties map[string]any `json:"properties"`
	Geometry   struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

func build(raw []byte, key string) ([]borders.Country, error) {
	var fc struct {
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, err
	}
	// ISO 3166-1 alpha-3 to alpha-2, for areas that ISO counts as part of
	// another country (ADM0_ISO).
	alpha2 := map[string]string{}
	for _, f := range fc.Features {
		a3, _ := f.Properties["ISO_A3_EH"].(string)
		a2, _ := f.Properties["ISO_A2_EH"].(string)
		if len(a3) == 3 && a3 != "-99" && len(a2) == 2 {
			alpha2[a3] = a2
		}
	}
	byCode := map[string]*borders.Country{}
	for _, f := range fc.Features {
		var code string
		if key != "" {
			code, _ = f.Properties[key].(string)
		} else if code, _ = f.Properties["ISO_A2_EH"].(string); code == "" || code == "-99" {
			iso, _ := f.Properties["ADM0_ISO"].(string)
			if code = alpha2[iso]; code == "" {
				code, _ = f.Properties["ADM0_A3"].(string)
			}
		}
		if code == "" {
			return nil, fmt.Errorf("feature %v has no code", f.Properties["NAME"])
		}
		var polys [][][][2]float64
		switch f.Geometry.Type {
		case "Polygon":
			var p [][][2]float64
			if err := json.Unmarshal(f.Geometry.Coordinates, &p); err != nil {
				return nil, err
			}
			polys = [][][][2]float64{p}
		case "MultiPolygon":
			if err := json.Unmarshal(f.Geometry.Coordinates, &polys); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%s: geometry %s", code, f.Geometry.Type)
		}
		c := byCode[code]
		if c == nil {
			c = &borders.Country{Code: code}
			byCode[code] = c
		}
		for _, p := range polys {
			var pp borders.Polygon
			for _, r := range p {
				for _, pt := range r {
					if math.Abs(pt[0]) > 180 || math.Abs(pt[1]) > 90 {
						return nil, fmt.Errorf("%s: point %v out of range", code, pt)
					}
				}
				if r = clean(r); len(r) >= 4 {
					pp = append(pp, r)
				}
			}
			if len(pp) > 0 {
				c.Polygons = append(c.Polygons, pp)
			}
		}
	}
	var cs []borders.Country
	for _, c := range byCode {
		cs = append(cs, *c)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Code < cs[j].Code })
	return cs, nil
}

// clean rounds a ring to the stored precision and drops repeated points,
// keeping it closed.
func clean(r [][2]float64) [][2]float64 {
	var out [][2]float64
	for _, p := range r {
		q := [2]float64{math.Round(p[0]*1e5) / 1e5, math.Round(p[1]*1e5) / 1e5}
		if len(out) > 0 && out[len(out)-1] == q {
			continue
		}
		out = append(out, q)
	}
	if len(out) > 0 && out[0] != out[len(out)-1] {
		out = append(out, out[0])
	}
	return out
}
