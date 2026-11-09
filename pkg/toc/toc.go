// Package toc turns an operator's measured data into emission intensities,
// the ISO 14083 / GLEC way: group similar operations into a transport
// operation category (TOC) or hub operation category (HOC), total the
// energy used over a period, and divide by the activity carried out.
//
//	TOC intensity (g CO2e/tkm) = sum(fuel amount x fuel factor) / tonne-km
//	HOC intensity (g CO2e/t)   = sum(energy amount x energy factor) / tonnes handled
//
// Refrigerant losses (kg refilled in the period x the refrigerant's GWP)
// are added as direct emissions of the operation (TTW), as ISO 14083 and
// GLEC v3.3 (Module 3; Module 4 s. 1.3) ask. A HOC can be per container
// handled instead of per tonne, for container terminals.
//
// The result is a dataset layer: TOCs become leg factors whose vehicle type
// is the TOC id, HOCs become hub factors, labelled primary data (or
// modelled, when the period's data was extrapolated from a sample).
// Shipments then reference them, and get tonne-km x the TOC's intensity.
package toc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/majidkabir/sootcase/pkg/calc"
	"github.com/majidkabir/sootcase/pkg/factors"
)

// Operations is the input: one period of an operator's measured data.
type Operations struct {
	Name   string `json:"name"`
	Period string `json:"period"` // free text, e.g. "2025" or "2025-Q3"
	Year   int    `json:"year,omitempty"`
	// PeriodStart and PeriodEnd (YYYY-MM-DD, the first and the last day)
	// bound the period exactly. Optional; iLEAP exports need them.
	PeriodStart string `json:"period_start,omitempty"`
	PeriodEnd   string `json:"period_end,omitempty"`
	TOCs        []TOC  `json:"tocs"`
	HOCs        []HOC  `json:"hocs"`
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
	// Refrigerants are the refrigerant losses in the period (e.g. of
	// temperature-controlled units). Optional.
	Refrigerants []RefrigerantUse `json:"refrigerants,omitempty"`
	DataType     string           `json:"data_type,omitempty"` // primary (default) or modelled
	// TemperatureControl is ambient, refrigerated or mixed. Optional.
	TemperatureControl string `json:"temperature_control,omitempty"`
}

// HOC is a hub operation category. Give Tonnes (outbound tonnes handled,
// ISO 14083's unit) or, for container terminals, Containers (containers
// handled, counted as shipments give them on hub stops).
type HOC struct {
	ID           string           `json:"id"`
	Description  string           `json:"description,omitempty"`
	Tonnes       float64          `json:"tonnes,omitempty"`
	Containers   float64          `json:"containers,omitempty"`
	Energy       []EnergyUse      `json:"energy"` // fuels and electricity, e.g. "electricity/DNK" in kWh
	Refrigerants []RefrigerantUse `json:"refrigerants,omitempty"`
	DataType     string           `json:"data_type,omitempty"` // primary (default) or modelled
	// HubType is transshipment, storage_transshipment, warehouse,
	// liquid_bulk_terminal or maritime_container_terminal, and
	// TemperatureControl ambient, refrigerated or mixed. Optional.
	HubType            string `json:"hub_type,omitempty"`
	TemperatureControl string `json:"temperature_control,omitempty"`
}

// EnergyUse is an amount of one fuel, in that fuel factor's unit.
// ActivityShare, optional, is the share of the category's activity
// (tonne-km, tonnes or containers) done with this fuel, 0 to 1; iLEAP
// asks for it when a category uses more than one energy carrier.
type EnergyUse struct {
	Fuel          string   `json:"fuel"`
	Amount        float64  `json:"amount"`
	ActivityShare *float64 `json:"activity_share,omitempty"`
}

// Hub types and temperature controls a HOC or TOC can name.
var (
	HubTypes     = []string{"transshipment", "storage_transshipment", "warehouse", "liquid_bulk_terminal", "maritime_container_terminal"}
	Temperatures = []string{"ambient", "refrigerated", "mixed"}
)

// RefrigerantUse is one refrigerant lost in the period: the kg refilled
// to replace leaks. Its GWP comes from the dataset (GLEC v3.3 Module 3,
// IPCC AR6), or from GWP with GWPSource for refrigerants it doesn't list.
type RefrigerantUse struct {
	Refrigerant string   `json:"refrigerant"`
	Kg          float64  `json:"kg"`
	GWP         *float64 `json:"gwp,omitempty"`
	GWPSource   string   `json:"gwp_source,omitempty"`
}

// RefrigerantProvider gives refrigerant GWPs (factors.Dataset does).
type RefrigerantProvider interface {
	Refrigerant(name string) (factors.Refrigerant, error)
}

// Parse reads and validates operations JSON.
func Parse(r io.Reader) (Operations, error) {
	var ops Operations
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ops); err != nil {
		return Operations{}, fmt.Errorf("operations: %w", err)
	}
	return Check(ops)
}

// Check validates operations (as Parse does after decoding).
func Check(ops Operations) (Operations, error) {
	if len(ops.TOCs) == 0 && len(ops.HOCs) == 0 {
		return Operations{}, errors.New("operations: no tocs or hocs")
	}
	if err := checkPeriod(ops.PeriodStart, ops.PeriodEnd); err != nil {
		return Operations{}, err
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
		if err := checkEnergy(t.ID, t.Fuels, t.Refrigerants, t.DataType); err != nil {
			return Operations{}, err
		}
		if t.TemperatureControl != "" && !slices.Contains(Temperatures, t.TemperatureControl) {
			return Operations{}, fmt.Errorf("toc %q: temperature_control must be one of %s", t.ID, strings.Join(Temperatures, ", "))
		}
	}
	for _, h := range ops.HOCs {
		if err := checkID(h.ID, ids); err != nil {
			return Operations{}, err
		}
		if h.Tonnes < 0 || h.Containers < 0 || (h.Tonnes > 0) == (h.Containers > 0) || !finite(h.Tonnes) || !finite(h.Containers) {
			return Operations{}, fmt.Errorf("hoc %q: give tonnes or containers handled (> 0), not both", h.ID)
		}
		if err := checkEnergy(h.ID, h.Energy, h.Refrigerants, h.DataType); err != nil {
			return Operations{}, err
		}
		if h.HubType != "" && !slices.Contains(HubTypes, h.HubType) {
			return Operations{}, fmt.Errorf("hoc %q: hub_type must be one of %s", h.ID, strings.Join(HubTypes, ", "))
		}
		if h.TemperatureControl != "" && !slices.Contains(Temperatures, h.TemperatureControl) {
			return Operations{}, fmt.Errorf("hoc %q: temperature_control must be one of %s", h.ID, strings.Join(Temperatures, ", "))
		}
	}
	return ops, nil
}

// checkPeriod checks the optional exact period: both dates or neither,
// YYYY-MM-DD, the last day not before the first.
func checkPeriod(start, end string) error {
	if start == "" && end == "" {
		return nil
	}
	a, err1 := time.Parse(time.DateOnly, start)
	b, err2 := time.Parse(time.DateOnly, end)
	if err1 != nil || err2 != nil || b.Before(a) {
		return errors.New("operations: give period_start and period_end (the first and the last day) as YYYY-MM-DD")
	}
	return nil
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

func checkEnergy(id string, uses []EnergyUse, refs []RefrigerantUse, dataType string) error {
	if len(uses) == 0 {
		return fmt.Errorf("%q: no fuels or energy listed", id)
	}
	for _, u := range uses {
		if strings.TrimSpace(u.Fuel) == "" || u.Amount < 0 || !finite(u.Amount) {
			return fmt.Errorf("%q: each fuel needs a name and an amount >= 0", id)
		}
		if u.ActivityShare != nil && (*u.ActivityShare <= 0 || *u.ActivityShare > 1 || !finite(*u.ActivityShare)) {
			return fmt.Errorf("%q: %s: activity_share must be more than 0 and at most 1", id, u.Fuel)
		}
	}
	for _, r := range refs {
		if strings.TrimSpace(r.Refrigerant) == "" || r.Kg < 0 || !finite(r.Kg) {
			return fmt.Errorf("%q: each refrigerant needs a name and kg >= 0", id)
		}
		if r.GWP != nil && (*r.GWP < 0 || !finite(*r.GWP) || strings.TrimSpace(r.GWPSource) == "") {
			return fmt.Errorf("%q: refrigerant %s: gwp must be >= 0, with gwp_source", id, r.Refrigerant)
		}
		if r.GWP == nil && r.GWPSource != "" {
			return fmt.Errorf("%q: refrigerant %s: gwp_source without gwp", id, r.Refrigerant)
		}
	}
	switch dataType {
	case "", calc.DataPrimary, calc.DataModelled:
		return nil
	}
	return fmt.Errorf("%q: data_type must be primary or modelled", id)
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// dataType is the label a category's factor gets: primary unless given.
func dataType(t string) string {
	if t == "" {
		return calc.DataPrimary
	}
	return t
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
		ttw, wtt, desc, err := sum(t.Fuels, t.Refrigerants, fuels)
		if err != nil {
			return factors.Dataset{}, fmt.Errorf("toc %q: %w", t.ID, err)
		}
		ds.Legs = append(ds.Legs, calc.Factor{
			Mode: t.Mode, VehicleType: t.ID, Year: ops.Year, DataType: dataType(t.DataType), DistanceBasis: t.DistanceBasis,
			TTW: ttw * 1000 / t.TonneKm, WTT: wtt * 1000 / t.TonneKm,
			Source: fmt.Sprintf("TOC %s (%s, %s): %s over %.0f tonne-km", t.ID, ops.Name, ops.Period, desc, t.TonneKm),
		})
	}
	for _, h := range ops.HOCs {
		ttw, wtt, desc, err := sum(h.Energy, h.Refrigerants, fuels)
		if err != nil {
			return factors.Dataset{}, fmt.Errorf("hoc %q: %w", h.ID, err)
		}
		hf := calc.HubFactor{HubType: h.ID, Year: ops.Year, DataType: dataType(h.DataType)}
		if h.Containers > 0 {
			ttwC, wttC := ttw*1000/h.Containers, wtt*1000/h.Containers
			hf.WTWPerContainerG, hf.TTWPerContainerG, hf.WTTPerContainerG = ttwC+wttC, &ttwC, &wttC
			hf.Source = fmt.Sprintf("HOC %s (%s, %s): %s over %.0f containers", h.ID, ops.Name, ops.Period, desc, h.Containers)
		} else {
			ttwT, wttT := ttw*1000/h.Tonnes, wtt*1000/h.Tonnes
			hf.WTWPerTonneG, hf.TTWPerTonneG, hf.WTTPerTonneG = ttwT+wttT, &ttwT, &wttT
			hf.Source = fmt.Sprintf("HOC %s (%s, %s): %s over %.0f tonnes", h.ID, ops.Name, ops.Period, desc, h.Tonnes)
		}
		ds.Hubs = append(ds.Hubs, hf)
	}
	return ds, nil
}

// Totals returns TTW and WTT kg CO2e for a category's energy uses and
// refrigerant losses (refrigerants count as TTW).
func Totals(uses []EnergyUse, refs []RefrigerantUse, fuels calc.FactorProvider) (ttw, wtt float64, err error) {
	ttw, wtt, _, err = sum(uses, refs, fuels)
	return
}

// sum returns TTW and WTT kg CO2e for a list of energy uses and
// refrigerant losses, and a short description of them. Refrigerant losses
// are direct emissions (TTW); their production is not counted.
func sum(uses []EnergyUse, refs []RefrigerantUse, fuels calc.FactorProvider) (ttw, wtt float64, desc string, err error) {
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
	for _, r := range refs {
		gwp, src := 0.0, r.GWPSource
		if r.GWP != nil {
			gwp = *r.GWP
		} else {
			rp, ok := fuels.(RefrigerantProvider)
			if !ok {
				return 0, 0, "", fmt.Errorf("refrigerant %s: no GWP table in the dataset; give gwp and gwp_source", r.Refrigerant)
			}
			ref, err := rp.Refrigerant(r.Refrigerant)
			if err != nil {
				return 0, 0, "", err
			}
			gwp, src = ref.GWP100, ref.Source
		}
		ttw += r.Kg * gwp
		parts = append(parts, fmt.Sprintf("%g kg %s lost (GWP %g, %s)", r.Kg, r.Refrigerant, gwp, src))
	}
	return ttw, wtt, strings.Join(parts, " + "), nil
}
