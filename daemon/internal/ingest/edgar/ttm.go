package edgar

import (
	"sort"
)

// ttmFacts returns the trailing-twelve-month series of the concept in prefer
// (under unit) whose series reaches the latest period end, ascending by AsOf.
// Issuers switch concepts over time (NVDA tagged revenue under one name until
// 2022 and another after), so the first concept that exists can be years
// stale; ties go to the earlier entry in prefer.
func ttmFacts(concepts map[string]conceptUnits, prefer []string, unit string) []Fact {
	var best []Fact
	for _, cname := range prefer {
		cu, ok := concepts[cname]
		if !ok {
			continue
		}
		points, ok := cu.Units[unit]
		if !ok || len(points) == 0 {
			continue
		}
		facts := computeTTM(points)
		if len(facts) > 0 && (best == nil || facts[len(facts)-1].AsOf > best[len(best)-1].AsOf) {
			best = facts
		}
	}
	return best
}

type classifiedPoint struct {
	Start    int64
	End      int64
	Val      float64
	Days     int
	Filed    string
	Index    int
	IsAnnual bool
}

func computeTTM(points []unitPoint) []Fact {
	var classified []classifiedPoint
	for i, p := range points {
		start, ok1 := parseDay(p.Start)
		end, ok2 := parseDay(p.End)
		if !ok1 || !ok2 || end <= start {
			continue
		}
		days := int((end-start)/86400) + 1
		isAnnual := days >= 350 && days <= 380
		isPartial := days >= 80 && days < 350
		if !isAnnual && !isPartial {
			continue
		}
		classified = append(classified, classifiedPoint{
			Start:    start,
			End:      end,
			Val:      p.Val,
			Days:     days,
			Filed:    p.Filed,
			Index:    i,
			IsAnnual: isAnnual,
		})
	}
	if len(classified) == 0 {
		return nil
	}

	// Deduplicate by (Start, End): keep greatest Filed, tie-break by later index
	dedup := make(map[[2]int64]classifiedPoint)
	for _, cp := range classified {
		key := [2]int64{cp.Start, cp.End}
		existing, ok := dedup[key]
		if !ok || cp.Filed > existing.Filed || (cp.Filed == existing.Filed && cp.Index > existing.Index) {
			dedup[key] = cp
		}
	}
	classified = classified[:0]
	for _, cp := range dedup {
		classified = append(classified, cp)
	}

	// Group by End
	endMap := make(map[int64][]classifiedPoint)
	for _, cp := range classified {
		endMap[cp.End] = append(endMap[cp.End], cp)
	}

	// Distinct ends ascending
	ends := make([]int64, 0, len(endMap))
	for e := range endMap {
		ends = append(ends, e)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })

	var facts []Fact
	for _, e := range ends {
		pts := endMap[e]
		// Check for annual point ending exactly at E
		var annualAtE *classifiedPoint
		for i := range pts {
			if pts[i].IsAnnual {
				annualAtE = &pts[i]
				break
			}
		}
		if annualAtE != nil {
			facts = append(facts, Fact{Value: annualAtE.Val, AsOf: e})
			continue
		}

		// Find partial point Y with longest duration
		var bestY *classifiedPoint
		for i := range pts {
			if !pts[i].IsAnnual {
				if bestY == nil || pts[i].Days > bestY.Days {
					bestY = &pts[i]
				}
			}
		}
		if bestY == nil {
			continue
		}
		Y := bestY
		D := Y.Days
		S := Y.Start

		// Find annual point A with End in [S-10days, S]
		targetA := S - 10*86400
		var bestA *classifiedPoint
		bestADist := int64(1 << 62)
		for _, cp := range classified {
			if !cp.IsAnnual {
				continue
			}
			if cp.End < targetA || cp.End > S {
				continue
			}
			dist := abs(cp.End - (S - 86400)) // closest to S-1 day
			if dist < bestADist {
				bestADist = dist
				bestA = &cp
			}
		}
		if bestA == nil {
			continue
		}

		// Find point P (annual or partial) with |days(P)-D|<=10, End within 10 days of E-365, Start within 10 days of S-365
		targetPEnd := e - 365*86400
		targetPStart := S - 365*86400
		var bestP *classifiedPoint
		var bestPDist int64 = 1 << 62
		for _, cp := range classified {
			if abs(int64(cp.Days-D)) > 10 {
				continue
			}
			if abs(cp.End-targetPEnd) > 10*86400 {
				continue
			}
			if abs(cp.Start-targetPStart) > 10*86400 {
				continue
			}
			dist := abs(cp.End - targetPEnd)
			if dist < bestPDist {
				bestPDist = dist
				bestP = &cp
			}
		}
		if bestP == nil {
			continue
		}

		ttm := bestA.Val + Y.Val - bestP.Val
		facts = append(facts, Fact{Value: ttm, AsOf: e})
	}
	return facts
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// latestOf returns a pointer to a copy of the last element of fs, or nil when fs is empty.
func latestOf(fs []Fact) *Fact {
	if len(fs) == 0 {
		return nil
	}
	last := fs[len(fs)-1]
	return &last
}
