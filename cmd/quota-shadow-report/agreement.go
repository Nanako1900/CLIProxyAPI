package main

import "sort"

// reasonOrder lists shadow reasons in ranking-key order; unknown reasons sort after.
var reasonOrder = map[string]int{
	"soft_limit":     0,
	"perish_long":    1,
	"perish_short":   2,
	"short_idle":     3,
	"long_remaining": 4,
	"id":             5,
	"only_candidate": 6,
	"no_candidates":  7,
}

// ratio is a count of matching cases out of a total.
type ratio struct {
	Hit   int
	Total int
}

func (r *ratio) add(hit bool) {
	r.Total++
	if hit {
		r.Hit++
	}
}

// reasonAgreement is the agreement of in-pool selections for one shadow reason.
type reasonAgreement struct {
	Reason string
	ratio
}

// agreementStats covers report sections 1 and 2.
type agreementStats struct {
	Selections int
	// InPool counts agreement where the real pick was among the scored candidates.
	InPool ratio
	// OutOfPool counts selections whose real pick was outside the scored pool;
	// their disagreement is structural (affinity across tiers, plugin filters).
	OutOfPool    int
	NoCandidates int
	ByReason     []reasonAgreement
	FirstAttempt ratio
	Failover     ratio
	// SoftLimited counts in-pool picks the shadow view marked soft-limited, out of
	// picks whose candidate entry was found (the list is truncated at 128).
	SoftLimited ratio
	// SoftAvoidable counts soft-limited picks where another candidate was not soft-limited.
	SoftAvoidable int
	SoftUnknown   int
}

func analyzeAgreement(selections []selection) agreementStats {
	stats := agreementStats{Selections: len(selections)}
	byReason := make(map[string]*reasonAgreement)
	for _, sel := range selections {
		if sel.Reason == "no_candidates" || sel.Shadow == "" {
			stats.NoCandidates++
		}
		if !sel.InPool {
			stats.OutOfPool++
			continue
		}
		stats.InPool.add(sel.Agree)
		row := byReason[sel.Reason]
		if row == nil {
			row = &reasonAgreement{Reason: sel.Reason}
			byReason[sel.Reason] = row
		}
		row.add(sel.Agree)
		if sel.Attempt == 0 {
			stats.FirstAttempt.add(sel.Agree)
		} else {
			stats.Failover.add(sel.Agree)
		}
		switch sel.ActualSoft {
		case softYes:
			stats.SoftLimited.add(true)
			if sel.SoftFreeAlternative {
				stats.SoftAvoidable++
			}
		case softNo:
			stats.SoftLimited.add(false)
		default:
			stats.SoftUnknown++
		}
	}
	for _, row := range byReason {
		stats.ByReason = append(stats.ByReason, *row)
	}
	sort.Slice(stats.ByReason, func(i, j int) bool {
		return reasonLess(stats.ByReason[i].Reason, stats.ByReason[j].Reason)
	})
	return stats
}

func reasonLess(a, b string) bool {
	rankA, okA := reasonOrder[a]
	rankB, okB := reasonOrder[b]
	switch {
	case okA && okB:
		return rankA < rankB
	case okA != okB:
		return okA
	default:
		return a < b
	}
}
