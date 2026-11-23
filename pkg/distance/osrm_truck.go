package distance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// excluded is the classes a query leaves out: the vehicle's and ferries.
func (q routeQuery) excluded() []string {
	ex := slices.Clone(q.exclude)
	if q.avoidFerries && !slices.Contains(ex, "ferry") {
		ex = append(ex, "ferry")
	}
	return ex
}

// truckClassText says what roads a class of the truck profile holds
// (deploy/truck.lua).
func truckClassText(c string) string {
	p := calc.TruckProfile
	switch c {
	case "truck":
		return fmt.Sprintf("roads closed to trucks (hgv=no, destination or delivery) or limited below %g m high, %.2f m wide, %g m long, %g t or %g t per axle",
			p.HeightM, p.WidthM, p.LengthM, p.GrossWeightKg/1000, p.AxleLoadKg/1000)
	case "ferry":
		return "ferries"
	}
	if len(c) == 3 && strings.HasPrefix(c, "hz") {
		return "tunnels closed to tunnel code " + strings.ToUpper(c[2:])
	}
	return c
}

// noTruckRoute says why no route keeps to a vehicle's limits: the classes
// without which a route is found. err is the router's no-route error.
func (o *OSRM) noTruckRoute(ctx context.Context, m calc.Mode, key string, q routeQuery, err error) error {
	plain := routeQuery{avoidFerries: q.avoidFerries}
	var blocking []string
	for _, c := range q.exclude {
		t := plain
		for _, x := range q.exclude {
			if x != c {
				t.exclude = append(t.exclude, x)
			}
		}
		_, e := o.fetch(ctx, m, key, t)
		switch {
		case e == nil:
			blocking = append(blocking, truckClassText(c))
		case calc.Temporary(e) || ctx.Err() != nil:
			return e
		}
	}
	if len(blocking) == 0 {
		return fmt.Errorf("%w: none keeps out of %s (%v)", calc.ErrNoTruckRoute, strings.Join(mapText(q.exclude), ", "), err)
	}
	return fmt.Errorf("%w: every route uses %s", calc.ErrNoTruckRoute, strings.Join(blocking, " or "))
}

func mapText(cs []string) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = truckClassText(c)
	}
	return out
}

// movedStopM is how much farther a vehicle's route may start or end from
// the point asked for than any road's nearest point before it warns: the
// stop is then on a road the vehicle may not use.
const movedStopM = 50

// movedStops warns when the vehicle's route starts or ends farther from
// from or to than the nearest road of any kind: the stop is on a road the
// vehicle may not use (an hgv ban, a low bridge or a dead end behind one),
// and the route reaches the nearest road it may use instead. snapM is the
// route's metres from each point to the network.
func (o *OSRM) movedStops(ctx context.Context, from, to calc.Location, snapM [2]float64) ([]calc.Warning, error) {
	var out []calc.Warning
	for i, p := range []calc.Location{from, to} {
		if snapM[i] <= movedStopM {
			continue
		}
		near, err := o.nearestM(ctx, p)
		if err != nil {
			return nil, err
		}
		if snapM[i]-near > movedStopM {
			end := "starts"
			if i == 1 {
				end = "ends"
			}
			out = append(out, calc.Warning{Code: calc.WarnTruckStop, Text: fmt.Sprintf(
				"the %s point is on a road this vehicle may not use; the route %s on the nearest road it may, %.0f m from the point (the nearest road of any kind is %.0f m away)",
				[]string{"start", "end"}[i], end, snapM[i], near)})
		}
	}
	return out, nil
}

// nearestM is the metres from p to the nearest road of any kind.
func (o *OSRM) nearestM(ctx context.Context, p calc.Location) (float64, error) {
	profile := o.Profile
	if profile == "" {
		profile = "driving"
	}
	u := fmt.Sprintf("%s/nearest/v1/%s/%.6f,%.6f?number=1", o.BaseURL, url.PathEscape(profile), p.Lon, p.Lat)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	release, err := o.acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("osrm: %w", err)
	}
	defer release()
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("osrm: %w (%v)", calc.ErrTemporary, err)
	}
	defer resp.Body.Close()
	var body struct {
		Code      string `json:"code"`
		Waypoints []struct {
			Distance float64 `json:"distance"`
		} `json:"waypoints"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body); err != nil || body.Code != "Ok" || len(body.Waypoints) == 0 {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return 0, fmt.Errorf("osrm: %w (nearest: HTTP %d)", calc.ErrTemporary, resp.StatusCode)
		}
		return 0, fmt.Errorf("osrm: nearest: HTTP %d, %s", resp.StatusCode, body.Code)
	}
	return body.Waypoints[0].Distance, nil
}
