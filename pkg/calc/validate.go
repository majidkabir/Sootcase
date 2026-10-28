package calc

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidInput reports a malformed number or location in a shipment.
var ErrInvalidInput = errors.New("invalid input")

// validate rejects inputs that would otherwise produce silent nonsense:
// non-finite or negative numbers, out-of-range coordinates, and legs with
// neither a distance nor coordinates (which would come out as 0 km).
func validate(s Shipment) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, a...)...)
	}
	for name, v := range map[string]float64{"weight_kg": s.WeightKg, "teu": s.TEU, "teu_net_tonnes": s.TEUNetTonnes} {
		if !finite(v) || v < 0 {
			return bad("%s must be a number >= 0, got %v", name, v)
		}
	}
	for i, l := range s.Legs {
		where := fmt.Sprintf("leg %q", l.ID)
		if l.ID == "" {
			where = fmt.Sprintf("leg %d", i+1)
		}
		nums := map[string]float64{
			"distance_km": l.DistanceKm, "load_factor": l.LoadFactor, "load_tonnes": l.LoadTonnes,
			"empty_trip_factor": l.EmptyTripFactor, "energy_kwh_per_tkm": l.EnergyKWhPerTkm,
			"repositioning_km": l.RepositioningKm,
		}
		if l.Fuel != nil {
			nums["fuel.amount"] = l.Fuel.Amount
			nums["fuel.vehicle_tonne_km"] = l.Fuel.VehicleTonneKm
		}
		for name, v := range nums {
			if !finite(v) {
				return bad("%s: %s must be a finite number, got %v", where, name, v)
			}
		}
		if !validDistanceType(l.DistanceType) {
			return bad("%s: distance_type must be sfd, gcd or actual, got %q", where, l.DistanceType)
		}
		if l.RepositioningKm < 0 {
			return bad("%s: repositioning_km must be >= 0, got %v", where, l.RepositioningKm)
		}
		if l.DistanceKm < 0 {
			return bad("%s: distance_km must be >= 0, got %v", where, l.DistanceKm)
		}
		for name, p := range map[string]Location{"from": l.From, "to": l.To} {
			if !finite(p.Lat) || !finite(p.Lon) || p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
				return bad("%s: %s must have lat in [-90, 90] and lon in [-180, 180], got %v, %v", where, name, p.Lat, p.Lon)
			}
		}
		if l.DistanceKm == 0 && (isZero(l.From) || isZero(l.To)) {
			return bad("%s: needs distance_km, or both from and to coordinates", where)
		}
	}
	return nil
}

// checkResult guards against overflow from extreme but finite inputs.
func checkResult(r Result) error {
	for _, v := range []float64{r.WeightKg, r.TonneKm, r.TTWKg, r.WTTKg, r.WTWKg, r.IntensityG, r.PrimaryDataShare} {
		if !finite(v) {
			return fmt.Errorf("%w: result is not a finite number; check for extreme inputs", ErrInvalidInput)
		}
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// isZero treats (0, 0) as "not given": JSON leaves missing coordinates at
// zero, and no freight leg starts or ends at that point in the Atlantic.
func isZero(p Location) bool { return p.Lat == 0 && p.Lon == 0 }
