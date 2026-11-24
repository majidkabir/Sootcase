package calc

import "testing"

func TestSectionsWithin(t *testing.T) {
	ok := []CountrySection{{Country: "DK", FromKm: 0, ToKm: 10},
		{Country: "", Ferry: true, FromKm: 10, ToKm: 20},
		{Country: "DE", FromKm: 20, ToKm: 50, Regions: []RegionSection{{Region: "DE-SH", FromKm: 20, ToKm: 30}, {Region: "DE-HH", FromKm: 30, ToKm: 50}}}}
	if err := sectionsWithin(ok, 50); err != nil {
		t.Errorf("good sections: %v", err)
	}
	if err := sectionsWithin(nil, 50); err != nil {
		t.Errorf("no sections: %v", err)
	}
	for name, bad := range map[string]func(s []CountrySection) []CountrySection{
		"gap":         func(s []CountrySection) []CountrySection { s[1].FromKm = 11; return s },
		"empty":       func(s []CountrySection) []CountrySection { s[0].ToKm = 0; s[1].FromKm = 0; return s },
		"short":       func(s []CountrySection) []CountrySection { s[2].ToKm = 40; s[2].Regions[1].ToKm = 40; return s },
		"region gap":  func(s []CountrySection) []CountrySection { s[2].Regions[1].FromKm = 31; return s },
		"regions end": func(s []CountrySection) []CountrySection { s[2].Regions[1].ToKm = 45; return s },
		"not from 0":  func(s []CountrySection) []CountrySection { s[0].FromKm = 1; return s },
	} {
		s := append([]CountrySection(nil), ok...)
		s[2].Regions = append([]RegionSection(nil), ok[2].Regions...)
		if err := sectionsWithin(bad(s), 50); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
