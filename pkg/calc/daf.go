package calc

import (
	"errors"
	"fmt"
)

// Distance types, as the GLEC Framework and ISO 14083 distinguish them.
const (
	DistSFD    = "sfd"    // shortest feasible distance (e.g. road or sea network routing)
	DistGCD    = "gcd"    // great-circle distance
	DistActual = "actual" // distance actually travelled (odometer, logbook, flight track)
)

func validDistanceType(t string) bool {
	return t == "" || t == DistSFD || t == DistGCD || t == DistActual
}

// DAF is a distance adjustment factor: it converts a leg's distance of one
// type into the distance type an emission intensity was built on, so that
// tonne-km and intensity match:  to_km = from_km x Multiplier + AddKm.
// A zero Multiplier means 1.
type DAF struct {
	Mode       Mode    `json:"mode"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Multiplier float64 `json:"multiplier,omitempty"`
	AddKm      float64 `json:"add_km,omitempty"`
	Source     string  `json:"source"`
}

// Apply converts a distance.
func (d DAF) Apply(km float64) float64 {
	m := d.Multiplier
	if m == 0 {
		m = 1
	}
	return km*m + d.AddKm
}

// Validate checks the DAF's types and values.
func (d DAF) Validate() error {
	if d.Mode == "" || !validDistanceType(d.From) || !validDistanceType(d.To) || d.From == "" || d.To == "" || d.From == d.To {
		return fmt.Errorf("DAF %s %s->%s: need a mode and two different distance types (sfd, gcd, actual)", d.Mode, d.From, d.To)
	}
	if d.Multiplier < 0 || !finite(d.Multiplier) || !finite(d.AddKm) {
		return fmt.Errorf("DAF %s %s->%s: multiplier must be >= 0 and values finite", d.Mode, d.From, d.To)
	}
	return nil
}

// DAFProvider is optionally implemented by a FactorProvider.
type DAFProvider interface {
	DAF(mode Mode, from, to string) (DAF, bool)
}

// DistanceTyper is optionally implemented by a DistanceProvider to say which
// distance type it returns for a mode ("" if unknown).
type DistanceTyper interface {
	DistanceType(mode Mode) string
}

// ErrNoDAF reports a distance-type mismatch with no DAF to bridge it.
var ErrNoDAF = errors.New("no distance adjustment factor")

// ErrNoDistance reports a road, rail, sea or inland waterway leg whose only
// distance is a great circle.
var ErrNoDistance = errors.New("no distance for the leg")

// adjust returns the distance to multiply by an intensity: km of type
// legType turned into the type the factor is per (basis), and the DAF that
// did it. No DAF is applied (nil) when the types match or one of them is
// unknown, with one exception that is an error: a great circle on a
// network mode. A type mismatch without a DAF for it is an error too.
func (c Calculator) adjust(mode Mode, legType, basis string, km float64) (float64, *DAF, error) {
	if legType == "" || legType == basis {
		return km, nil, nil
	}
	// A great circle on a network mode: a straight line is no road, rail,
	// sea or river distance of any type (Rotterdam to Shanghai is 8,900 km
	// as the crow flies and 19,700 km by sea), unless the dataset has a DAF
	// that makes it one.
	straight := legType == DistGCD && mode != Air
	noRoute := fmt.Errorf("%w: a straight line (great circle) is not a %s distance; give distance_km, "+
		"the distance by %s, or set up routing for %s legs", ErrNoDistance, mode, mode, mode)
	if basis == "" {
		// A factor that states no basis takes the distance as it is.
		if straight {
			return 0, nil, noRoute
		}
		return km, nil, nil
	}
	var d DAF
	ok := false
	if p, has := c.Factors.(DAFProvider); has {
		d, ok = p.DAF(mode, legType, basis)
	}
	if !ok && straight {
		return 0, nil, noRoute
	}
	if !ok {
		return 0, nil, fmt.Errorf("%w: the distance is %s but the factor is per %s tonne-km; "+
			"give distance_km with distance_type %q, or add a DAF for %s from %s to %s to the dataset",
			ErrNoDAF, legType, basis, basis, mode, legType, basis)
	}
	adj := d.Apply(km)
	if !(adj > 0) || !finite(adj) {
		return 0, nil, fmt.Errorf("%w: the %s DAF from %s to %s turns %.1f km into %.1f km", ErrInvalidInput, mode, legType, basis, km, adj)
	}
	return adj, &d, nil
}
