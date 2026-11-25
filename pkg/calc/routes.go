package calc

import (
	"context"
	"fmt"
	"strconv"
)

// MaxAlternatives is the most routes a road leg can be asked for: its
// primary route and up to MaxAlternatives-1 others.
const MaxAlternatives = 5

// RouteOptions are asked for with WithRouteOptions. They apply to routed
// road legs.
type RouteOptions struct {
	// Alternatives is how many routes to look for per road leg, the
	// primary included (1 to MaxAlternatives). Above 1, each leg gets up
	// to Alternatives-1 other routes in LegResult.Alternatives; 0 or 1
	// leaves the result as it is.
	Alternatives int
	// AvoidFerries routes road legs on land only.
	AvoidFerries bool
}

type routeOptionsKey struct{}

// WithRouteOptions asks routers for alternative routes or land-only routes
// (see RouteOptions). Meant for single calculations.
func WithRouteOptions(ctx context.Context, o RouteOptions) context.Context {
	return context.WithValue(ctx, routeOptionsKey{}, o)
}

// RouteOptionsOf is the route options asked for (the zero value if none).
func RouteOptionsOf(ctx context.Context) RouteOptions {
	o, _ := ctx.Value(routeOptionsKey{}).(RouteOptions)
	return o
}

// ParseAlternatives reads the number of routes asked for: 1 to
// MaxAlternatives.
func ParseAlternatives(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxAlternatives {
		return 0, fmt.Errorf("%w: alternatives must be a whole number from 1 to %d", ErrInvalidInput, MaxAlternatives)
	}
	return n, nil
}

// RouteSummary says what tells a route apart, for a person: its main roads
// by their reference (e.g. "E47", "A 7"), in the order driven, and its
// ferry crossings ([] for a land-only route). Both come from
// OpenStreetMap, as the router has them.
type RouteSummary struct {
	Roads   []string        `json:"roads"`
	Ferries []FerryCrossing `json:"ferries"`
}

// FerryCrossing is a ferry crossing on a road route. From and To are the
// two ports when the name gives them ("Rødby - Puttgarden").
type FerryCrossing struct {
	Name       string  `json:"name"`
	From       string  `json:"from,omitempty"`
	To         string  `json:"to,omitempty"`
	DistanceKm float64 `json:"distance_km"`
}
