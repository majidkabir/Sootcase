// Package toc turns an operator's measured data into emission intensities,
// the ISO 14083 / GLEC way: group similar operations into a transport
// operation category (TOC) or hub operation category (HOC), total the
// energy used over a period, and divide by the activity carried out.
//
//	TOC intensity (g CO2e/tkm) = sum(fuel amount x fuel factor) / tonne-km
//	HOC intensity (g CO2e/t)   = sum(energy amount x energy factor) / tonnes handled
//
// The result is a dataset layer: TOCs become leg factors whose vehicle type
// is the TOC id, HOCs become hub factors, all labelled primary data.
// Shipments then reference them, and get tonne-km x the TOC's intensity.
package toc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Operations is the input: one period of an operator's measured data.
type Operations struct {
	Name   string `json:"name"`
	Period string `json:"period"` // free text, e.g. "2025" or "2025-Q3"
	Year   int    `json:"year,omitempty"`
	TOCs   []TOC  `json:"tocs"`
	HOCs   []HOC  `json:"hocs"`
}

// TOC is a transport operation category.
type TOC struct {
	ID          string    `json:"id"`
	Mode        calc.Mode `json:"mode"`
	Description string    `json:"description,omitempty"`
	TonneKm     float64   `json:"tonne_km"` // all tonne-km carried in the period
	// DistanceBasis is the distance type TonneKm was measured on (sfd, gcd,
	// actual), so shipments on other distance types get a DAF. Optional.
	DistanceBasis string      `json:"distance_basis,omitempty"`
	Fuels         []EnergyUse `json:"fuels"` // all fuel and electricity used, incl. empty running
}

// HOC is a hub operation category.
type HOC struct {
	ID          string      `json:"id"`
	Description string      `json:"description,omitempty"`
	Tonnes      float64     `json:"tonnes"` // tonnes handled in the period
	Energy      []EnergyUse `json:"energy"` // fuels and electricity, e.g. "electricity/DNK" in kWh
}

// EnergyUse is an amount of one fuel, in that fuel factor's unit.
type EnergyUse struct {
	Fuel   string  `json:"fuel"`
	Amount float64 `json:"amount"`
}

// Parse reads and validates operations JSON.
func Parse(r io.Reader) (Operations, error) {
	var ops Operations
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ops); err != nil {
		return Operations{}, fmt.Errorf("operations: %w", err)
	}
	if len(ops.TOCs) == 0 && len(ops.HOCs) == 0 {
		return Operations{}, errors.New("operations: no tocs or hocs")
	}
	ids := map[string]bool{}
	for _, t := range ops.TOCs {
		if err := checkID(t.ID, ids); err != nil {
			return Operations{}, err
		}
		if t.Mode == "" {
			return Operations{}, fmt.Errorf("toc %q: mode is required", t.ID)
		}
		if t.TonneKm <= 0 {
			return Operations{}, fmt.Errorf("toc %q: tonne_km must be > 0", t.ID)
		}
		switch t.DistanceBasis {
		case "", calc.DistSFD, calc.DistGCD, calc.DistActual:
		default:
			return Operations{}, fmt.Errorf("toc %q: distance_basis must be sfd, gcd or actual", t.ID)
		}
		if err := checkEnergy(t.ID, t.Fuels); err != nil {
			return Operations{}, err
		}
	}
	for _, h := range ops.HOCs {
		if err := checkID(h.ID, ids); err != nil {
			return Operations{}, err
		}
		if h.Tonnes <= 0 {
			return Operations{}, fmt.Errorf("hoc %q: tonnes must be > 0", h.ID)
		}
		if err := checkEnergy(h.ID, h.Energy); err != nil {
			return Operations{}, err
		}
	}
	return ops, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_./-]*$`)

func checkID(id string, seen map[string]bool) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("id %q: use lowercase letters, digits, _ . / -", id)
	}
	if seen[id] {
		return fmt.Errorf("id %q used twice", id)
	}
	seen[id] = true
	return nil
}

func checkEnergy(id string, uses []EnergyUse) error {
	if len(uses) == 0 {
		return fmt.Errorf("%q: no fuels or energy listed", id)
	}
	for _, u := range uses {
		if strings.TrimSpace(u.Fuel) == "" || u.Amount < 0 {
			return fmt.Errorf("%q: each fuel needs a name and an amount >= 0", id)
		}
	}
	return nil
}

// Build converts operations into a dataset layer, using fuel factors
// (including "electricity/<ISO3>") from the given provider.
func Build(ops Operations, fuels calc.FactorProvider) (factors.Dataset, error) {
	ds := factors.Dataset{
		Name: ops.Name,
		Note: fmt.Sprintf("Transport and hub operation categories built from measured operational data (%s), period %s.",
			ops.Name, ops.Period),
	}
	for _, t := range ops.TOCs {
		ttw, wtt, desc, err := sum(t.Fuels, fuels)
		if err != nil {
			return factors.Dataset{}, fmt.Errorf("toc %q: %w", t.ID, err)
		}
		ds.Legs = append(ds.Legs, calc.Factor{
			Mode: t.Mode, VehicleType: t.ID, Year: ops.Year, DataType: calc.DataPrimary, DistanceBasis: t.DistanceBasis,
			TTW: ttw * 1000 / t.TonneKm, WTT: wtt * 1000 / t.TonneKm,
			Source: fmt.Sprintf("TOC %s (%s, %s): %s over %.0f tonne-km", t.ID, ops.Name, ops.Period, desc, t.TonneKm),
		})
	}
	for _, h := range ops.HOCs {
		ttw, wtt, desc, err := sum(h.Energy, fuels)
		if err != nil {
			return factors.Dataset{}, fmt.Errorf("hoc %q: %w", h.ID, err)
		}
		ttwT, wttT := ttw*1000/h.Tonnes, wtt*1000/h.Tonnes
		ds.Hubs = append(ds.Hubs, calc.HubFactor{
			HubType: h.ID, Year: ops.Year, DataType: calc.DataPrimary,
			WTWPerTonneG: ttwT + wttT, TTWPerTonneG: &ttwT, WTTPerTonneG: &wttT,
			Source: fmt.Sprintf("HOC %s (%s, %s): %s over %.0f tonnes", h.ID, ops.Name, ops.Period, desc, h.Tonnes),
		})
	}
	return ds, nil
}

// sum returns TTW and WTT kg CO2e for a list of energy uses, and a short
// description of them.
func sum(uses []EnergyUse, fuels calc.FactorProvider) (ttw, wtt float64, desc string, err error) {
	var parts []string
	for _, u := range uses {
		f, err := fuels.FuelFactor(u.Fuel)
		if err != nil {
			return 0, 0, "", err
		}
		ttw += u.Amount * f.TTW
		wtt += u.Amount * f.WTT
		parts = append(parts, fmt.Sprintf("%g %s %s", u.Amount, f.Unit, u.Fuel))
	}
	return ttw, wtt, strings.Join(parts, " + "), nil
}
