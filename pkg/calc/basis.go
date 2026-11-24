package calc

// Version is the Sootcase version that results name in their Basis.
// cmd/sootcase sets it from its build version.
var Version = "dev"

// The method results are calculated by (Basis) and what they say about
// Regulation (EU) 2026/1030 (CountEmissions EU). The Regulation bases
// calculations on EN ISO 14083:2023 (Art. 3(1)) and applies from
// 2 December 2030 (Art. 20(2)). Only a tool certified under it and on the
// Commission's list (Art. 9(2), 11) may give out its Art. 10(1) statement,
// so Sootcase never does: results say they are not certified.
const (
	Standard      = "EN ISO 14083:2023"
	Framework     = "GLEC Framework v3.3"
	Certification = "Not certified under Regulation (EU) 2026/1030 (applies from 2 December 2030)"
)

// Basis is what a result was calculated by: the standard, the framework
// and the Sootcase version.
type Basis struct {
	Standard  string `json:"standard"`
	Framework string `json:"framework"`
	Sootcase  string `json:"sootcase"`
}

// CurrentBasis is this build's Basis.
func CurrentBasis() Basis {
	return Basis{Standard: Standard, Framework: Framework, Sootcase: Version}
}

// String is the basis in one line, e.g. for a CSV cell or a report.
func (b Basis) String() string {
	return b.Standard + ", " + b.Framework + ", Sootcase " + b.Sootcase
}
