package recommend

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Settings say which route changes to try and the rules they follow.
// Pointers are optional values with defaults.
type Settings struct {
	// Kinds to try; empty means all (fuller_loads only when a load is given).
	Kinds []string `json:"kinds,omitempty"`
	// MinRoadKm: road legs longer than this are tried on rail, waterway and
	// short sea.
	MinRoadKm *float64 `json:"min_road_km,omitempty"`
	// MinMainKm: the rail, waterway or sea part must be longer than this in
	// a straight line.
	MinMainKm *float64 `json:"min_main_km,omitempty"`
	// MaxPortRoadKm: road to and from a waterway or sea port must stay
	// within this of it in a straight line (road to waterway and short sea).
	MaxPortRoadKm *float64 `json:"max_port_road_km,omitempty"`
	// Terminal: how rail terminals are chosen, "nearest" (default) or
	// "best_trip".
	Terminal string `json:"terminal,omitempty"`
	// Vehicle types of the new legs.
	RailVehicle     string `json:"rail_vehicle,omitempty"`      // default freight_train
	WaterwayVehicle string `json:"waterway_vehicle,omitempty"`  // default container_barge
	ShortSeaVehicle string `json:"short_sea_vehicle,omitempty"` // default roro
	SeaVehicle      string `json:"sea_vehicle,omitempty"`       // default container_ship (air to sea)
	// Other ports: how far from the port used now to look, and how many.
	PortRadiusKm *float64 `json:"port_radius_km,omitempty"`
	MaxPorts     *int     `json:"max_ports,omitempty"`
	// Fuller loads: the load on the shipment's road vehicles (load_tonnes or
	// load_factor, as in the calculator's load model), empty km per loaded
	// km, an optional other vehicle type, and where the values come from.
	LoadTonnes      *float64 `json:"load_tonnes,omitempty"`
	LoadFactor      *float64 `json:"load_factor,omitempty"`
	EmptyTripFactor *float64 `json:"empty_trip_factor,omitempty"`
	LoadVehicle     string   `json:"load_vehicle,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// ErrInvalidSettings reports settings that can't be used.
var ErrInvalidSettings = errors.New("invalid recommendation settings")

func ptr[T any](v T) *T { return &v }

func val[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// Validate checks the settings and fills in defaults. skipped says why a
// kind asked for implicitly won't be tried.
func (s Settings) Validate() (Settings, map[string]string, error) {
	bad := func(msg string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(msg, a...))
	}
	skipped := map[string]string{}
	known := map[string]bool{}
	for _, k := range Kinds() {
		known[k] = true
	}
	hasLoad := s.LoadTonnes != nil || s.LoadFactor != nil
	if len(s.Kinds) == 0 {
		for _, k := range Kinds() {
			if k == Load && !hasLoad {
				skipped[Load] = "give load_tonnes or load_factor to compare fuller loads"
				continue
			}
			s.Kinds = append(s.Kinds, k)
		}
	} else {
		seen := map[string]bool{}
		var ks []string
		for _, k := range s.Kinds {
			if !known[k] {
				return s, nil, bad("unknown kind %q; use %s", k, strings.Join(Kinds(), ", "))
			}
			if !seen[k] {
				seen[k] = true
				ks = append(ks, k)
			}
		}
		s.Kinds = ks
		if seen[Load] && !hasLoad {
			return s, nil, bad("fuller_loads needs load_tonnes or load_factor")
		}
	}
	s.MinRoadKm = ptr(val(s.MinRoadKm, DefaultMinRoadKm))
	s.MinMainKm = ptr(val(s.MinMainKm, DefaultMinMainKm))
	s.MaxPortRoadKm = ptr(val(s.MaxPortRoadKm, DefaultMaxPortRoadKm))
	s.PortRadiusKm = ptr(val(s.PortRadiusKm, DefaultPortRadiusKm))
	s.MaxPorts = ptr(val(s.MaxPorts, DefaultMaxPorts))
	for name, v := range map[string]float64{"min_road_km": *s.MinRoadKm, "min_main_km": *s.MinMainKm, "max_port_road_km": *s.MaxPortRoadKm} {
		if !(v >= 0) || v > 20000 {
			return s, nil, bad("%s must be between 0 and 20000", name)
		}
	}
	if !(*s.PortRadiusKm > 0) || *s.PortRadiusKm > 3000 {
		return s, nil, bad("port_radius_km must be > 0 and <= 3000")
	}
	if *s.MaxPorts < 1 || *s.MaxPorts > 10 {
		return s, nil, bad("max_ports must be 1 to 10")
	}
	switch s.Terminal {
	case "":
		s.Terminal = TerminalNearest
	case TerminalNearest, TerminalBestTrip:
	default:
		return s, nil, bad("terminal must be %s or %s", TerminalNearest, TerminalBestTrip)
	}
	if s.RailVehicle == "" {
		s.RailVehicle = "freight_train"
	}
	if s.WaterwayVehicle == "" {
		s.WaterwayVehicle = "container_barge"
	}
	if s.ShortSeaVehicle == "" {
		s.ShortSeaVehicle = "roro"
	}
	if s.SeaVehicle == "" {
		s.SeaVehicle = "container_ship"
	}
	if hasLoad {
		if s.LoadTonnes != nil && s.LoadFactor != nil {
			return s, nil, bad("give load_tonnes or load_factor, not both")
		}
		if s.LoadTonnes != nil && !(*s.LoadTonnes > 0) {
			return s, nil, bad("load_tonnes must be > 0")
		}
		if s.LoadFactor != nil && (!(*s.LoadFactor > 0) || *s.LoadFactor > 1) {
			return s, nil, bad("load_factor must be > 0 and <= 1")
		}
		if s.EmptyTripFactor != nil && (!(*s.EmptyTripFactor >= 0) || *s.EmptyTripFactor > 1) {
			return s, nil, bad("empty_trip_factor must be 0 to 1")
		}
	} else if s.EmptyTripFactor != nil || s.LoadVehicle != "" {
		return s, nil, bad("empty_trip_factor and load_vehicle need load_tonnes or load_factor")
	}
	return s, skipped, nil
}

func (s Settings) has(kind string) bool { return slices.Contains(s.Kinds, kind) }
