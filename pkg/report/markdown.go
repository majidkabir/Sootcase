package report

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteMarkdown renders the report as Markdown.
func WriteMarkdown(w io.Writer, r Report) error {
	bw := bufio.NewWriter(w)
	p := func(format string, a ...any) { fmt.Fprintf(bw, format, a...) }

	p("# %s\n\n", esc(r.Title))
	if r.Organisation != "" {
		p("**Organisation:** %s  \n", esc(r.Organisation))
	}
	p("**Period:** %s  \n", periodText(r.Period))
	p("**Reporting level:** transport or hub services  \n")
	p("**Factor datasets:** %s  \n", esc(strings.Join(r.Datasets, ", ")))
	p("**Generated:** %s\n\n", r.Generated.Format("2006-01-02 15:04 UTC"))
	p("%s\n\n", esc(r.Statement))

	c := r.Coverage
	p("## Coverage\n\n")
	p("%d shipments (%d calculated, %d excluded), %d transport legs, %d hub stops, %s t of freight.\n\n",
		c.Shipments, c.Calculated, c.Failed, c.Legs, c.HubStops, num(c.WeightT, 1))

	t := r.Total
	p("## Total\n\n")
	p("| | kg CO2e |\n|---|---:|\n")
	p("| **Total GHG emissions (WTW)** | **%s** |\n", num(t.WTWKg, 1))
	p("| Transport, operational (TTW) | %s |\n", num(t.TTWKg, 1))
	p("| Transport, energy provision (WTT) | %s |\n", num(t.WTTKg, 1))
	p("| Hub operations (WTW) | %s |\n\n", num(t.HubWTWKg, 1))
	if t.BiogenicCO2Kg > 0 {
		p("Biogenic CO2, reported separately and not included above (GLEC Framework v3.2 p. 64): %s kg, from measured biofuel use.\n\n", num(t.BiogenicCO2Kg, 1))
	}
	p("Transport activity: %s tonne-km (%s).  \n", num(t.TonneKm, 0), distText(t.DistanceTypes))
	p("Transport emission intensity (WTW): **%s g CO2e/tkm**.\n\n", num(t.IntensityG, 2))

	g := r.GHG
	p("## GHG Protocol scopes\n\n")
	switch g.Scope3Category {
	case 4:
		p("For the reporting company, all %s kg are **scope 3, category 4** (upstream transportation and distribution: transport it pays for).\n\n", num(g.Scope3Kg, 1))
	case 9:
		p("For the reporting company, all %s kg are **scope 3, category 9** (downstream transportation and distribution: sold products moved by transport it doesn't pay for).\n\n", num(g.Scope3Kg, 1))
	default:
		p("For a company buying the transport, all %s kg are **scope 3**: category 4 for transport it pays for (inbound or outbound), category 9 for sold products moved by transport it doesn't pay for.\n\n", num(g.Scope3Kg, 1))
	}
	p("For the operator of the vehicles:\n\n| | kg CO2e |\n|---|---:|\n")
	p("| Scope 1: fuel burnt in the vehicles | %s |\n", num(g.OperatorScope1Kg, 1))
	p("| Scope 2: electricity used by the vehicles (location-based) | %s |\n", num(g.OperatorScope2Kg, 1))
	p("| Scope 3, category 3: producing and delivering the fuel | %s |\n", num(g.OperatorScope3Cat3Kg, 1))
	p("| Hub operations (not split by scope) | %s |\n\n", num(g.HubsKg, 1))
	p("GHG Protocol Corporate Value Chain (Scope 3) Standard and its technical guidance, chapters 3, 4 and 9. " +
		"Electricity includes the upstream share its factor contains, which isn't split off.\n\n")

	if len(r.Modes) > 0 {
		p("## By mode of transport\n\n")
		p("| Mode | Legs | Tonne-km | Distance type | TTW kg | WTT kg | WTW kg | g CO2e/tkm |\n")
		p("|---|---:|---:|---|---:|---:|---:|---:|\n")
		for _, m := range r.Modes {
			p("| %s | %d | %s | %s | %s | %s | %s | %s |\n", esc(string(m.Mode)), m.Legs, num(m.TonneKm, 0),
				distText(m.DistanceTypes), num(m.TTWKg, 1), num(m.WTTKg, 1), num(m.WTWKg, 1), num(m.IntensityG, 2))
		}
		p("\n")
	}

	if len(r.Hubs) > 0 {
		p("## By hub operation\n\n")
		p("| Hub type | Stops | Tonnes | WTW kg | g CO2e/t |\n|---|---:|---:|---:|---:|\n")
		for _, h := range r.Hubs {
			p("| %s | %d | %s | %s | %s |\n", esc(h.HubType), h.Stops, num(h.Tonnes, 1), num(h.WTWKg, 1), num(h.IntensityGT, 1))
		}
		p("\n")
	}

	if len(r.Energy) > 0 {
		p("## By energy carrier (transport)\n\n")
		p("| Energy carrier | TTW kg | WTT kg | WTW kg | Share |\n|---|---:|---:|---:|---:|\n")
		for _, e := range r.Energy {
			name := e.Carrier
			if name == Unspecified {
				name = "not stated (published intensity or load model)"
			}
			p("| %s | %s | %s | %s | %s |\n", esc(name), num(e.TTWKg, 1), num(e.WTTKg, 1), num(e.WTWKg, 1), pct(e.Share))
		}
		p("\n")
	}

	d := r.Data
	p("## Data quality\n\n")
	p("Share of total emissions from primary data **%s**, modelled data **%s**, default values **%s**.\n\n",
		pct(d.Primary), pct(d.Modelled), pct(d.Default))
	if len(d.DefaultSources) > 0 {
		p("Default values used:\n\n| Source | WTW kg | Share |\n|---|---:|---:|\n")
		for _, s := range d.DefaultSources {
			p("| %s | %s | %s |\n", esc(s.Source), num(s.WTWKg, 1), pct(s.Share))
		}
		p("\n")
	}
	for _, m := range d.Models {
		p("Model: %s. %s, %s kg WTW.\n\n", esc(m.Model), plural(m.Legs, "leg"), num(m.WTWKg, 1))
		keys := make([]string, 0, len(m.Parameters))
		for k := range m.Parameters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p("- %s: %s\n", strings.ReplaceAll(k, "_", " "), esc(m.Parameters[k]))
		}
		p("\n")
	}

	if len(r.DAFs) > 0 {
		p("## Distance adjustment factors\n\n| Source | Legs | Factor |\n|---|---:|---|\n")
		for _, a := range r.DAFs {
			f := num(a.Min, 3)
			if a.Max != a.Min {
				f += " to " + num(a.Max, 3)
			}
			p("| %s | %d | %s |\n", esc(a.Source), a.Legs, f)
		}
		p("\n")
	}

	if len(r.Notes) > 0 {
		p("## Notes, omissions and deviations\n\n")
		for _, n := range r.Notes {
			p("- %s\n", esc(n))
		}
		p("\n")
	}
	p("## Supporting information\n\n%s\n", esc(r.Reference))
	return bw.Flush()
}

func periodText(p Period) string {
	switch {
	case p.From != "" && p.To != "":
		return esc(p.From) + " to " + esc(p.To)
	case p.From != "":
		return "from " + esc(p.From)
	case p.To != "":
		return "until " + esc(p.To)
	}
	return "not stated"
}

func distText(m map[string]float64) string {
	if len(m) == 0 {
		return "-"
	}
	if len(m) == 1 {
		for k := range m {
			return distName(k)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = distName(k) + " " + pct(m[k])
	}
	return strings.Join(parts, ", ")
}

func distName(t string) string {
	switch t {
	case "sfd":
		return "SFD"
	case "gcd":
		return "GCD"
	case "actual":
		return "actual"
	}
	return "unspecified"
}

// num formats with thousands separators.
func num(f float64, decimals int) string {
	s := fmt.Sprintf("%.*f", decimals, f)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// esc keeps user text from breaking tables or adding markup.
func esc(s string) string {
	r := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "<", "&lt;", ">", "&gt;",
		"*", "\\*", "_", "\\_", "`", "\\`", "[", "\\[", "]", "\\]", "#", "\\#")
	return r.Replace(s)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
