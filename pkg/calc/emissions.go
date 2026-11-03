package calc

// Emissions are greenhouse gas emissions in kg CO2e: vehicle operation
// (tank-to-wheel), energy provision (well-to-tank) and their sum
// (well-to-wheel). Biogenic CO2 is reported separately and is not part of
// the totals (GLEC Framework v3.3 p. 64).
type Emissions struct {
	TTWKg         float64 `json:"ttw_kg"`
	WTTKg         float64 `json:"wtt_kg"`
	WTWKg         float64 `json:"wtw_kg"`
	BiogenicCO2Kg float64 `json:"biogenic_co2_kg,omitempty"`
}

// Add adds w times o.
func (e *Emissions) Add(o Emissions, w float64) {
	e.TTWKg += w * o.TTWKg
	e.WTTKg += w * o.WTTKg
	e.WTWKg += w * o.WTWKg
	e.BiogenicCO2Kg += w * o.BiogenicCO2Kg
}

// Scaled returns e times k.
func (e Emissions) Scaled(k float64) Emissions {
	return Emissions{e.TTWKg * k, e.WTTKg * k, e.WTWKg * k, e.BiogenicCO2Kg * k}
}

// Emissions returns the result's totals.
func (r Result) Emissions() Emissions {
	return Emissions{r.TTWKg, r.WTTKg, r.WTWKg, r.BiogenicCO2Kg}
}
