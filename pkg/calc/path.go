package calc

import (
	"context"
	"fmt"
)

type pathKey struct{}

// PathDetail is how detailed a leg's path for a map is.
type PathDetail string

// Path details: Simplified is the router's simplified line (a few dozen
// points per route), Full every point of the route on the network, and
// Detailed the full line thinned to at most MaxDetailedPoints.
const (
	PathSimplified PathDetail = "simplified"
	PathDetailed   PathDetail = "detailed"
	PathFull       PathDetail = "full"
)

// MaxDetailedPoints is the most points a Detailed path keeps per route.
const MaxDetailedPoints = 1000

// ParsePathDetail reads a path detail: simplified (also "true", "1"),
// detailed or full.
func ParsePathDetail(s string) (PathDetail, error) {
	switch s {
	case "true", "1", string(PathSimplified):
		return PathSimplified, nil
	case string(PathDetailed):
		return PathDetailed, nil
	case string(PathFull):
		return PathFull, nil
	}
	return "", fmt.Errorf("%w: path must be true, simplified, detailed or full", ErrInvalidInput)
}

// WithPath asks routers to also return each leg's path, simplified, for
// drawing it on a map. Meant for single calculations: paths cost router
// time and response size, so batches don't ask for them.
func WithPath(ctx context.Context) context.Context { return WithPathDetail(ctx, PathSimplified) }

// WithPathDetail is WithPath with the given detail.
func WithPathDetail(ctx context.Context, d PathDetail) context.Context {
	return context.WithValue(ctx, pathKey{}, d)
}

// WantPath says whether the calculation asked for paths.
func WantPath(ctx context.Context) bool { return PathDetailOf(ctx) != "" }

// PathDetailOf is the path detail asked for, "" for no path.
func PathDetailOf(ctx context.Context) PathDetail {
	d, _ := ctx.Value(pathKey{}).(PathDetail)
	return d
}

// ThinPath keeps at most n points of p, evenly along it by index, with its
// first and last.
func ThinPath(p [][2]float64, n int) [][2]float64 {
	if n < 2 || len(p) <= n {
		return p
	}
	out := make([][2]float64, 0, n)
	step := float64(len(p)-1) / float64(n-1)
	for i := 0; i < n-1; i++ {
		out = append(out, p[int(float64(i)*step)])
	}
	return append(out, p[len(p)-1])
}
