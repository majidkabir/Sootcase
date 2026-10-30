package calc

import (
	"errors"
	"fmt"
	"strings"
)

// Terminal handlings at a change of mode, for route changes (recommendations,
// scenarios) that add them.
//
// GLEC Framework v3.3 counts hub operations at intermodal terminals per
// container (Module 2, Table 3, p. 97); ISO 14083 makes transshipments part
// of a transport chain's emissions. There is no GLEC default for airports
// or for Ro-Ro and bulk terminals, and GLEC leaves self-driving cargo at
// Ro-Ro terminals out of hub emissions (Section 1 Chapter 4, logistics
// hubs), so those are not counted.

// IntermodalTerminal is the hub type of an added terminal handling.
const IntermodalTerminal = "intermodal_terminal"

// Unitised says whether a leg's cargo goes through an intermodal terminal
// when the mode changes: road and rail always; inland waterway and sea only
// on container vessels; air never (no GLEC default for airports).
func Unitised(l LegResult) bool {
	switch l.Mode {
	case Road, Rail:
		return true
	case InlandWaterway, Sea:
		vt := l.VehicleType
		if l.Factor != nil {
			vt += " " + l.Factor.VehicleType
		}
		return strings.Contains(strings.ToLower(vt), "container")
	}
	return false
}

// TerminalHandlings counts the changes of mode at an intermodal terminal in
// r, and the ends of the legs in newLegs (by leg id) where the shipment
// starts or ends: the cargo was on a truck there before, so it is handled
// at the terminal from or to the truck even when the road to it is too
// short to count (GapKm).
func TerminalHandlings(r Result, newLegs map[string]bool) int {
	n := 0
	for i := 1; i < len(r.Legs); i++ {
		a, b := r.Legs[i-1], r.Legs[i]
		if a.Mode != b.Mode && Unitised(a) && Unitised(b) {
			n++
		}
	}
	if k := len(r.Legs); k > 0 {
		if l := r.Legs[0]; newLegs[l.LegID] && l.Mode != Road && Unitised(l) {
			n++
		}
		if l := r.Legs[k-1]; newLegs[l.LegID] && l.Mode != Road && Unitised(l) {
			n++
		}
	}
	return n
}

// Containers is the number of containers a shipment of weightKg is handled
// in at a terminal, and how it was set: the shipment's TEU, or its tonnes ÷
// GLEC's tonnes per TEU for its cargo type (light, heavy) or the average
// (GLEC Framework v3.3, Section 1 Chapter 4, logistics hubs: 10 t per TEU,
// 6 light, 14.5 heavy). Table 3 gives no container size, so a container is
// counted as one TEU. An error says why it can't be counted.
func (c Calculator) Containers(s Shipment, weightKg float64) (float64, string, error) {
	if _, err := c.Factors.HubFactor(IntermodalTerminal); err != nil {
		return 0, "", errors.New("the factor set has no intermodal terminal value")
	}
	if s.TEU > 0 {
		return s.TEU, "the shipment's TEU", nil
	}
	clp, ok := c.Factors.(ContainerLoadProvider)
	if !ok {
		return 0, "", errors.New("the factor set has no tonnes per TEU")
	}
	profile := DefaultContainerLoad
	if s.CargoType == "light" || s.CargoType == "heavy" {
		profile = s.CargoType
	}
	perTEU, err := clp.ContainerLoad(profile)
	if err != nil || perTEU <= 0 {
		return 0, "", errors.New("the factor set has no tonnes per TEU")
	}
	if weightKg <= 0 {
		return 0, "", errors.New("no weight")
	}
	return weightKg / 1000 / perTEU, fmt.Sprintf("TEU = tonnes ÷ %g, GLEC %s", perTEU, profile), nil
}
