// Command loadtest measures a running Sootcase server's throughput and
// latency.
//
//	go run ./tools/loadtest -url http://localhost:8080 -kind calc -c 32 -d 20s
//
// Kinds: calc (POST /v1/calculate, one shipment), batch (POST
// /v1/calculate/batch with -batch shipments) and report (POST /v1/report
// with -batch shipments). Shipments are random but repeatable (-seed):
// a road leg and a sea leg between points in Europe, each with its
// distance given (the great circle between its ends), so that the
// calculation is measured and not a router. With -route the distances
// are left out and the server routes every leg: it then needs road
// routing (-osrm) and a sea network (-sea-network).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type loc struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type leg struct {
	Mode        string  `json:"mode"`
	VehicleType string  `json:"vehicle_type"`
	From        loc     `json:"from"`
	To          loc     `json:"to"`
	DistanceKm  float64 `json:"distance_km,omitempty"`
}

type shipment struct {
	ID       string  `json:"id"`
	WeightKg float64 `json:"weight_kg"`
	Legs     []leg   `json:"legs"`
}

// bbox is where random points fall: minLon, minLat, maxLon, maxLat.
var bbox = [4]float64{-2, 43, 20, 55}

func randLoc(r *rand.Rand) loc {
	return loc{Lat: bbox[1] + r.Float64()*(bbox[3]-bbox[1]), Lon: bbox[0] + r.Float64()*(bbox[2]-bbox[0])}
}

var roadOnly, route bool

// randLeg is a leg between two random points, with the great circle
// between them as its distance unless the server is to route it.
func randLeg(r *rand.Rand, mode, vehicle string) leg {
	l := leg{Mode: mode, VehicleType: vehicle, From: randLoc(r), To: randLoc(r)}
	if !route {
		l.DistanceKm = math.Max(1, greatCircleKm(l.From, l.To))
	}
	return l
}

func greatCircleKm(a, b loc) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	h := math.Pow(math.Sin(rad(b.Lat-a.Lat)/2), 2) +
		math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Pow(math.Sin(rad(b.Lon-a.Lon)/2), 2)
	return 2 * 6371 * math.Asin(math.Sqrt(h))
}

func randShipment(r *rand.Rand, i int) shipment {
	s := shipment{ID: fmt.Sprintf("S%d", i), WeightKg: 500 + r.Float64()*20_000, Legs: []leg{randLeg(r, "road", "hgv")}}
	if !roadOnly {
		s.Legs = append(s.Legs, randLeg(r, "sea", "container_ship"))
	}
	return s
}

func main() {
	base := flag.String("url", "http://localhost:8080", "server base URL")
	kind := flag.String("kind", "calc", "calc, batch or report")
	conc := flag.Int("c", 16, "concurrent requests")
	dur := flag.Duration("d", 15*time.Second, "test duration")
	batch := flag.Int("batch", 100, "shipments per batch or report request")
	seed := flag.Int64("seed", 1, "random seed")
	bboxS := flag.String("bbox", "", "minLon,minLat,maxLon,maxLat for random points (default much of Europe)")
	fresh := flag.Bool("fresh", false, "new random points for every request, so nothing is served from a cache")
	flag.BoolVar(&roadOnly, "road-only", false, "one road leg per shipment, no sea leg")
	flag.BoolVar(&route, "route", false, "leave the distances out, so the server routes every leg (it needs routing set up)")
	flag.Parse()
	if *bboxS != "" {
		parts := strings.Split(*bboxS, ",")
		if len(parts) != 4 {
			fmt.Fprintln(os.Stderr, "bad -bbox")
			os.Exit(2)
		}
		for i, p := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, "bad -bbox:", err)
				os.Exit(2)
			}
			bbox[i] = v
		}
	}

	path, perReq := "/v1/calculate", 1
	switch *kind {
	case "calc":
	case "batch":
		path, perReq = "/v1/calculate/batch", *batch
	case "report":
		path, perReq = "/v1/report", *batch
	default:
		fmt.Fprintln(os.Stderr, "unknown -kind")
		os.Exit(2)
	}

	makeBody := func(r *rand.Rand, i int) []byte {
		var v any
		if perReq == 1 {
			v = randShipment(r, i)
		} else {
			ss := make([]shipment, perReq)
			for j := range ss {
				ss[j] = randShipment(r, i*perReq+j)
			}
			v = map[string]any{"shipments": ss}
		}
		b, _ := json.Marshal(v)
		return b
	}
	// Pre-build 256 request bodies so encoding doesn't load the client; with
	// -fresh every request gets its own.
	r := rand.New(rand.NewSource(*seed))
	bodies := make([][]byte, 256)
	for i := range bodies {
		bodies[i] = makeBody(r, i)
	}

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *conc, MaxConnsPerHost: *conc}, Timeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()

	var (
		mu         sync.Mutex
		latencies  []time.Duration
		okN, errN  atomic.Int64
		shipFailed atomic.Int64
		statuses   sync.Map
		wg         sync.WaitGroup
	)
	start := time.Now()
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			var local []time.Duration
			wr := rand.New(rand.NewSource(*seed + int64(w) + 1000))
			for i := w; ctx.Err() == nil; i += *conc {
				body := bodies[i%len(bodies)]
				if *fresh {
					body = makeBody(wr, i)
				}
				t0 := time.Now()
				resp, err := client.Post(*base+path, "application/json", bytes.NewReader(body))
				if err != nil {
					if ctx.Err() == nil {
						errN.Add(1)
					}
					continue
				}
				var failed struct {
					Failed   int `json:"failed"`
					Coverage struct {
						Failed int `json:"failed"`
					} `json:"coverage"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&failed)
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if n := failed.Failed + failed.Coverage.Failed; resp.StatusCode == http.StatusOK && n > 0 {
					// The request worked but shipments inside it failed.
					shipFailed.Add(int64(n))
				}
				if resp.StatusCode == http.StatusOK {
					okN.Add(1)
					local = append(local, time.Since(t0))
				} else {
					errN.Add(1)
					n, _ := statuses.LoadOrStore(resp.StatusCode, new(atomic.Int64))
					n.(*atomic.Int64).Add(1)
				}
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	q := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(p*float64(len(latencies)-1))]
	}
	reqs := float64(okN.Load()) / elapsed
	fmt.Printf("%s, %d concurrent, %.0fs: %.0f requests/s (%.0f shipments/s), p50 %v, p95 %v, p99 %v, %d errors, %d shipments failed\n",
		*kind, *conc, elapsed, reqs, reqs*float64(perReq),
		q(.50).Round(10*time.Microsecond), q(.95).Round(10*time.Microsecond), q(.99).Round(10*time.Microsecond), errN.Load(), shipFailed.Load())
	statuses.Range(func(k, v any) bool {
		fmt.Printf("  HTTP %v: %d\n", k, v.(*atomic.Int64).Load())
		return true
	})
}
