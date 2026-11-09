package ileap

import (
	"math"
	"strconv"
)

// dec formats x as an iLEAP Decimal: plain digits, no exponent, rounded
// to 10 significant digits so float noise (0.30000000000000004) is gone.
func dec(x float64) Decimal {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return ""
	}
	if x == 0 {
		return "0"
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'g', 10, 64), 64)
	return Decimal(strconv.FormatFloat(r, 'f', -1, 64))
}

// num parses a Decimal; ok is false when it isn't a plain decimal number.
func num(d Decimal) (float64, bool) {
	s := string(d)
	if s == "" {
		return 0, false
	}
	for i, c := range s {
		switch {
		case c >= '0' && c <= '9', c == '.':
		case c == '-' && i == 0:
		default:
			return 0, false
		}
	}
	x, err := strconv.ParseFloat(s, 64)
	return x, err == nil
}
