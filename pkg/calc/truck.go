package calc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Vehicle describes the truck on a road leg, so that the route keeps to
// roads it may use: no lower bridge, lighter weight limit, hgv ban or
// tunnel its dangerous goods are barred from. Every field is optional; one
// left out puts no limit for it. VehicleType still picks the emission
// factors; Vehicle only restricts the roads.
type Vehicle struct {
	HeightM       *float64 `json:"height_m,omitempty"`        // with load, 1.5-5.0
	WidthM        *float64 `json:"width_m,omitempty"`         // without mirrors, 1.5-3.5
	LengthM       *float64 `json:"length_m,omitempty"`        // whole combination, 4-30
	GrossWeightKg *float64 `json:"gross_weight_kg,omitempty"` // truck, trailer and cargo, 3,500-60,000
	AxleLoadKg    *float64 `json:"axle_load_kg,omitempty"`    // heaviest axle, 1,000-15,000
	Axles         *int     `json:"axles,omitempty"`           // of the combination, 2-12
	Trailer       string   `json:"trailer,omitempty"`         // none, semi or drawbar
	Hazmat        *bool    `json:"hazmat,omitempty"`          // carries dangerous goods (ADR)
	// TunnelCode is the load's ADR tunnel restriction code (B, C, D or E,
	// ADR 1.9.5); it implies Hazmat.
	TunnelCode string `json:"tunnel_code,omitempty"`
}

// TruckProfile is what road routing for a vehicle is built for (the
// road router's truck classes): roads are left out when their limits are
// below these, so any truck within them may use the roads routed on.
// A vehicle beyond one of them can't be routed.
var TruckProfile = struct {
	HeightM, WidthM, LengthM, GrossWeightKg, AxleLoadKg float64
}{4.0, 2.60, 18.75, 40000, 11500}

// ErrInvalidVehicle reports a vehicle field out of range (an input error,
// answered with HTTP 400).
var ErrInvalidVehicle = fmt.Errorf("%w: vehicle", ErrInvalidInput)

// ErrNoTruckRoute reports that no route keeps to the vehicle's limits, or
// that the router can't route for them.
var ErrNoTruckRoute = errors.New("no route for this vehicle")

// Restrictions is what a road leg was routed for: the vehicle's fields
// as read, and the limits of the roads left out (TruckProfile), each
// only when it was applied.
type Restrictions struct {
	Vehicle
	// RoutedFor is the profile the roads were chosen for: a road is left
	// out when its limit is below one of these.
	RoutedFor TruckLimits `json:"routed_for"`
	// Excluded are the road router's classes left out (see the core's
	// deploy/truck.lua).
	Excluded []string `json:"excluded"`
}

// TruckLimits are the limits a route was chosen for.
type TruckLimits struct {
	HeightM       float64 `json:"height_m,omitempty"`
	WidthM        float64 `json:"width_m,omitempty"`
	LengthM       float64 `json:"length_m,omitempty"`
	GrossWeightKg float64 `json:"gross_weight_kg,omitempty"`
	AxleLoadKg    float64 `json:"axle_load_kg,omitempty"`
	HGVAccess     bool    `json:"hgv_access"`            // hgv=no left out; hgv=destination/delivery only at the ends
	TunnelCode    string  `json:"tunnel_code,omitempty"` // tunnels barring this ADR code left out
	HazmatNo      bool    `json:"hazmat_no,omitempty"`   // hazmat=no left out
}

func (v Vehicle) check() error {
	bad := func(field string, format string, a ...any) error {
		return fmt.Errorf("%w.%s: "+format, append([]any{ErrInvalidVehicle, field}, a...)...)
	}
	for _, r := range []struct {
		name     string
		v        *float64
		min, max float64
	}{
		{"height_m", v.HeightM, 1.5, 5.0}, {"width_m", v.WidthM, 1.5, 3.5}, {"length_m", v.LengthM, 4, 30},
		{"gross_weight_kg", v.GrossWeightKg, 3500, 60000}, {"axle_load_kg", v.AxleLoadKg, 1000, 15000},
	} {
		if r.v != nil && (!finite(*r.v) || *r.v < r.min || *r.v > r.max) {
			return bad(r.name, "must be from %g to %g, got %v", r.min, r.max, *r.v)
		}
	}
	if v.Axles != nil && (*v.Axles < 2 || *v.Axles > 12) {
		return bad("axles", "must be from 2 to 12, got %d", *v.Axles)
	}
	switch v.Trailer {
	case "", "none", "semi", "drawbar":
	default:
		return bad("trailer", "must be none, semi or drawbar, got %q", v.Trailer)
	}
	switch v.TunnelCode {
	case "", "B", "C", "D", "E":
	default:
		return bad("tunnel_code", "must be B, C, D or E, got %q", v.TunnelCode)
	}
	if v.TunnelCode != "" && v.Hazmat != nil && !*v.Hazmat {
		return bad("tunnel_code", "is for dangerous goods, but hazmat is false")
	}
	return nil
}

// Exclude is the road router's classes to leave out for v, and what that
// routes for: roads closed to trucks or limited below TruckProfile, and
// tunnels barring the load's code when it carries dangerous goods. A
// vehicle beyond TruckProfile gets ErrNoTruckRoute.
func (v Vehicle) Exclude() ([]string, TruckLimits, error) {
	p := TruckProfile
	over := func(name string, got *float64, limit float64, unit string) error {
		if got != nil && *got > limit {
			return fmt.Errorf("%w: %s %g %s is above what road routing is built for (%g %s)", ErrNoTruckRoute, name, *got, unit, limit, unit)
		}
		return nil
	}
	for _, err := range []error{
		over("height", v.HeightM, p.HeightM, "m"), over("width", v.WidthM, p.WidthM, "m"), over("length", v.LengthM, p.LengthM, "m"),
		over("gross weight", v.GrossWeightKg, p.GrossWeightKg, "kg"), over("axle load", v.AxleLoadKg, p.AxleLoadKg, "kg"),
	} {
		if err != nil {
			return nil, TruckLimits{}, err
		}
	}
	// One class (truck) holds hgv access, size and weight limits, so a
	// vehicle gets all of them: a road too low for 4.0 m is left out for
	// a lower truck too (the router is built for fixed limits; OSRM allows
	// few classes).
	ex := []string{"truck"}
	lim := TruckLimits{HeightM: p.HeightM, WidthM: p.WidthM, LengthM: p.LengthM,
		GrossWeightKg: p.GrossWeightKg, AxleLoadKg: p.AxleLoadKg, HGVAccess: true}
	switch {
	case v.TunnelCode != "":
		ex = append(ex, "hz"+strings.ToLower(v.TunnelCode))
		lim.TunnelCode, lim.HazmatNo = v.TunnelCode, true
	case v.Hazmat != nil && *v.Hazmat:
		// Dangerous goods without a code: hazmat=no roads, which the router
		// holds in each code's class; E's has the fewest tunnels besides.
		ex = append(ex, "hze")
		lim.TunnelCode, lim.HazmatNo = "E", true
	}
	return ex, lim, nil
}

type truckKey struct{}

// WithVehicle routes road legs for v (see Vehicle.Exclude); the
// calculator sets it for a leg that gives a vehicle.
func WithVehicle(ctx context.Context, v *Vehicle) context.Context {
	return context.WithValue(ctx, truckKey{}, v)
}

// VehicleOf is the vehicle road legs are routed for, nil for none.
func VehicleOf(ctx context.Context) *Vehicle {
	v, _ := ctx.Value(truckKey{}).(*Vehicle)
	return v
}
