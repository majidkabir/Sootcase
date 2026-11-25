package calc

import (
	"errors"
	"fmt"
	"sort"
)

// LoadModel gives a vehicle's emissions per vehicle-km as a function of
// load, so shipments can use their own load factor and empty running
// instead of a fleet-average factor:
//
//	E(lf)          = emissions per vehicle-km at load factor lf, interpolated
//	                 linearly between Points
//	g/tkm (TTW/WTT) = (E(lf) + etf * E(0)) / (PayloadT * lf)
//
// lf is the share of payload capacity used on the loaded trip (0 < lf <= 1),
// etf the empty trip factor: empty km per loaded km.
type LoadModel struct {
	Mode        Mode        `json:"mode"`
	VehicleType string      `json:"vehicle_type"` // vehicle class, without a load suffix
	PayloadT    float64     `json:"payload_t"`
	Points      []LoadPoint `json:"points"`
	Source      string      `json:"source"`
	Year        int         `json:"year"`
	// DistanceBasis is the distance type the per-km values are for (sfd,
	// gcd, actual; "" unknown), as for Factor.DistanceBasis.
	DistanceBasis string `json:"distance_basis,omitempty"`
}

// LoadPoint is emissions per vehicle-km at one load factor.
type LoadPoint struct {
	Load float64 `json:"load"`          // 0..1
	TTW  float64 `json:"ttw_g_per_vkm"` // tank-to-wheel
	WTT  float64 `json:"wtt_g_per_vkm"` // well-to-tank
}

// LoadModelProvider is optionally implemented by a FactorProvider.
type LoadModelProvider interface {
	LoadModel(mode Mode, vehicleType string) (LoadModel, error)
}

// ErrInvalidLoad reports bad load_factor or empty_trip_factor input.
var ErrInvalidLoad = errors.New("invalid load data")

// Validate checks the model is usable: points sorted, covering 0 and 1.
func (m LoadModel) Validate() error {
	if m.PayloadT <= 0 {
		return fmt.Errorf("load model %s/%s: payload must be > 0", m.Mode, m.VehicleType)
	}
	if len(m.Points) < 2 {
		return fmt.Errorf("load model %s/%s: need at least 2 points", m.Mode, m.VehicleType)
	}
	if !sort.SliceIsSorted(m.Points, func(i, j int) bool { return m.Points[i].Load < m.Points[j].Load }) {
		return fmt.Errorf("load model %s/%s: points must be sorted by load", m.Mode, m.VehicleType)
	}
	if m.Points[0].Load != 0 || m.Points[len(m.Points)-1].Load != 1 {
		return fmt.Errorf("load model %s/%s: points must span load 0 to 1", m.Mode, m.VehicleType)
	}
	for i, p := range m.Points {
		if p.TTW < 0 || p.WTT < 0 {
			return fmt.Errorf("load model %s/%s: negative value", m.Mode, m.VehicleType)
		}
		if i > 0 && p.Load == m.Points[i-1].Load {
			return fmt.Errorf("load model %s/%s: duplicate load %v", m.Mode, m.VehicleType, p.Load)
		}
	}
	return nil
}

// MaxLoadFactor is how far beyond its payload (load 1) a load model is
// extended, for a load given in tonnes: DESNZ's "100% laden" is an average
// full load (17.9 t for an articulated truck over 33 t), and 1.6 × that is
// about a 44 t truck's legal payload.
const MaxLoadFactor = 1.6

// at interpolates emissions per vehicle-km at load lf, beyond the last
// point along the last segment.
func (m LoadModel) at(lf float64) (ttw, wtt float64) {
	ps := m.Points
	for i := 1; i < len(ps); i++ {
		if lf <= ps[i].Load || i == len(ps)-1 {
			a, b := ps[i-1], ps[i]
			t := (lf - a.Load) / (b.Load - a.Load)
			return a.TTW + t*(b.TTW-a.TTW), a.WTT + t*(b.WTT-a.WTT)
		}
	}
	last := ps[len(ps)-1]
	return last.TTW, last.WTT
}

// At returns emissions per vehicle-km (TTW, WTT) at load factor lf, as the
// model interpolates them.
func (m LoadModel) At(lf float64) (ttw, wtt float64) { return m.at(lf) }

// PerTonneKm returns TTW and WTT in g CO2e per tonne-km for a load factor
// and empty trip factor.
func (m LoadModel) PerTonneKm(lf, etf float64) (ttw, wtt float64, err error) {
	return m.perTonneKm(lf, etf, 1)
}

// perTonneKm is PerTonneKm allowing load factors up to max.
func (m LoadModel) perTonneKm(lf, etf, max float64) (ttw, wtt float64, err error) {
	if lf <= 0 || lf > max {
		return 0, 0, fmt.Errorf("%w: load_factor must be > 0 and <= 1, got %v", ErrInvalidLoad, lf)
	}
	if etf < 0 {
		return 0, 0, fmt.Errorf("%w: empty_trip_factor must be >= 0, got %v", ErrInvalidLoad, etf)
	}
	lt, lw := m.at(lf)
	et, ew := m.at(0)
	t := m.PayloadT * lf
	return (lt + etf*et) / t, (lw + etf*ew) / t, nil
}

// LoadFits says why a load can't be put on a leg, "" if it can: the
// vehicle has no load model, the load factor carries less than the
// shipment's own tonnes, or load_tonnes is more than the model goes (as the
// calculator refuses them). For changes that put one load on many legs
// (scenarios, recommendations), so a leg it doesn't fit is left as it was.
func LoadFits(f FactorProvider, l Leg, loadTonnes, loadFactor, tonnes float64) string {
	lmp, ok := f.(LoadModelProvider)
	if !ok {
		return "the dataset has no load models"
	}
	m, err := lmp.LoadModel(l.Mode, l.VehicleType)
	if err != nil {
		return fmt.Sprintf("no load model for %s", l.VehicleType)
	}
	switch {
	case loadTonnes > 0 && loadTonnes > MaxLoadFactor*m.PayloadT:
		return fmt.Sprintf("load_tonnes %g t is more than the %s load model goes (%.1f t)", loadTonnes, l.VehicleType, MaxLoadFactor*m.PayloadT)
	case loadTonnes > 0 && loadTonnes < tonnes*(1-1e-9):
		return fmt.Sprintf("load_tonnes %g t is less than the shipment's own %g t", loadTonnes, round3(tonnes))
	case loadTonnes == 0 && loadFactor*m.PayloadT < tonnes*(1-1e-9):
		return fmt.Sprintf("load_factor %g is %.2f t on a %s, less than the shipment's own %g t", loadFactor, loadFactor*m.PayloadT, l.VehicleType, round3(tonnes))
	}
	return ""
}
