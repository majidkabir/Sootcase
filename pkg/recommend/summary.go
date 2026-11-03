package recommend

import (
	"math"
	"sort"

	"github.com/majidkabir/sootcase/pkg/calc"
)

// summarise fills in the totals, kinds and lanes.
func summarise(res *Result) {
	kinds := map[string]*KindSummary{}
	for _, k := range res.Settings.Kinds {
		kinds[k] = &KindSummary{Kind: k, Area: AreaOf(k)}
	}
	type laneAcc struct {
		Lane
		kinds map[string]int
	}
	lanes := map[string]*laneAcc{}
	var order []string
	for _, sr := range res.Shipments {
		if sr.Error != "" {
			res.Failed++
			continue
		}
		res.Now.Add(sr.Now.Totals, 1)
		best := sr.Now.Totals
		bestKind := ""
		if len(sr.Options) > 0 {
			best, bestKind = sr.Options[0].Totals, sr.Options[0].Kind
		}
		res.Best.Add(best, 1)
		seen := map[string]bool{}
		for _, o := range sr.Options {
			if k := kinds[o.Kind]; k != nil && !seen[o.Kind] {
				seen[o.Kind] = true
				k.Shipments++
				k.SavedKg += o.SavedKg
			}
		}
		if k := kinds[bestKind]; k != nil {
			k.BestFor++
		}
		key := sr.Lane
		if key == "" {
			key = sr.ShipmentID
		}
		la := lanes[key]
		if la == nil {
			la = &laneAcc{Lane: Lane{Lane: key}, kinds: map[string]int{}}
			lanes[key] = la
			order = append(order, key)
		}
		la.Shipments++
		la.NowKg += sr.Now.WTWKg
		la.BestKg += best.WTWKg
		if bestKind != "" {
			la.kinds[bestKind]++
		}
	}
	res.SavedKg = res.Now.WTWKg - res.Best.WTWKg
	if res.Now.WTWKg > 0 {
		res.SavedPct = res.SavedKg / res.Now.WTWKg * 100
	}
	for _, k := range res.Settings.Kinds {
		res.Kinds = append(res.Kinds, *kinds[k])
	}
	for _, a := range Areas() {
		var ks []string
		for _, k := range a.Kinds {
			if kinds[k] != nil {
				ks = append(ks, k)
			}
		}
		a.Kinds = ks
		res.Areas = append(res.Areas, a)
	}
	for _, key := range order {
		la := lanes[key]
		la.SavedKg = la.NowKg - la.BestKg
		bk, bn := "", 0
		for _, k := range Kinds() {
			if la.kinds[k] > bn {
				bk, bn = k, la.kinds[k]
			}
		}
		la.BestKind = bk
		res.Lanes = append(res.Lanes, la.Lane)
	}
	sort.SliceStable(res.Lanes, func(i, j int) bool { return res.Lanes[i].SavedKg > res.Lanes[j].SavedKg })
	if math.IsNaN(res.SavedPct) {
		res.SavedPct = 0
	}
}

func hasSea(legs []calc.Leg) bool {
	for _, l := range legs {
		if l.Mode == calc.Sea {
			return true
		}
	}
	return false
}
