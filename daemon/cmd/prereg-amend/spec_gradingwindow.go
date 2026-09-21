package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/forecastmon"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// GradingWindowKind re-registers the START of the directional grading window.
// Its own kind, like the two epoch corrections before it: it moves a boundary,
// and no claim, null, floor, interval, threshold or verdict rule changes.
//
// WHY THIS RECORD EXISTS. The publication gate (internal/api/accuracy.go)
// refuses to publish any directional figure while the graded window holds a
// collapsed cross-section — a day on which the whole universe received a
// handful of distinct probabilities. Since 87e7f57 (2026-09-09) that window is
// anchored to the survivorship epoch and does not roll, which was the right
// repair for a fail-open slice but has one consequence the gate's own refusal
// text states: the 2026-07-27..08-06 collapses stay in the window for ever,
// and "this clears when the window is re-registered, not by waiting". Nothing
// implemented re-registration. This does.
//
// The collapse is HISTORICAL and its cause is fixed: a34db09 and 906310c
// (2026-08-06) ended it, and every day from 2026-08-07 on carries a dispersed
// cross-section. The new epoch is the first clean day: a dry run against the
// 2026-08-05 candidate was refused by the guard below because 1d 2026-08-06
// still reads 33 distinct across 327 symbols, and the guard is the ruler. The guard below refuses
// to file if that stops being true of the database at filing time.
const GradingWindowKind = "grading-window-reregistration"

const (
	oldGradingEpochTS = store.SurvivorshipEpoch // 2026-07-24T00:00:00Z
	newGradingEpochTS = store.GradingEpoch      // 2026-08-07T00:00:00Z
)

const gradingWindowNote = "AMENDMENT — the directional grading window's start moves from the survivorship " +
	"epoch (2026-07-24) to 2026-08-07, the first day after the cross-section collapse ended. FILED " +
	"WITH KNOWLEDGE OF AN OUTCOME, stated plainly: at filing the registry has read REFUSED since " +
	"2026-09-13 over 18 collapsed day-horizons, the withheld grade inside it reads FAILED for 1d and " +
	"INSUFFICIENT DAYS for 1w, and the directional ensemble is already retired on its own live record. " +
	"A correction made after seeing a result must justify itself on what it CANNOT change: this " +
	"alters no accuracy figure, no null, no interval, no evidence floor, no auto-retire rule and no " +
	"verdict map. It removes days the gate itself classifies as not-independent-forecasts, and only " +
	"those; measured at filing the window it opens contains ZERO collapsed cross-sections, and the " +
	"grader's own credible-day floor (10) still applies to what remains. The expected consequence on " +
	"the numbers already in hand is INSUFFICIENT DAYS, not a verdict — the refusal becomes a count that " +
	"can be watched, and nothing is published until that count clears. The survivorship epoch itself " +
	"does NOT move: listing-status reconstruction still starts 2026-07-24. The prior boundary stands " +
	"unaltered above this record; a hash-chained commitment is never rewritten."

// gradingWindowMeasured is read at filing time, never transcribed: the daemon
// writes every ten minutes and a day count copied from a memory file is stale.
type gradingWindowMeasured struct {
	// Per horizon, the collapsed days the gate finds from the OLD epoch — the
	// evidence the refusal stands on — and from the NEW one, which must be empty.
	CollapsedOld map[string][]string
	CollapsedNew map[string][]string
	// Raw distinct forecast days from the new epoch, per horizon. Raw: the
	// grader drops thin, unsettled and stale-feed days on top of this, so the
	// number it reports as credible will be smaller. Disclosed as a ceiling.
	RawDaysNew   map[string]int
	TotalDaysOld int
}

func (m gradingWindowMeasured) collapsedOnOrAfterNew() int {
	n := 0
	for _, days := range m.CollapsedNew {
		n += len(days)
	}
	return n
}

// measureGradingWindow runs the gate's OWN ruler (forecastmon.DayStat.Collapsed
// over store.ForecastDayStats) rather than a re-derived heuristic. The 2026-09-13
// collapse root-cause work recorded that counting distinct probabilities over
// unfolded intraday rows gives 3,725 where the gate says 33; the only number
// that means anything here is the one the gate computes.
func measureGradingWindow(ctx context.Context, dbPath string) (gradingWindowMeasured, error) {
	m := gradingWindowMeasured{
		CollapsedOld: map[string][]string{},
		CollapsedNew: map[string][]string{},
		RawDaysNew:   map[string]int{},
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return m, err
	}
	defer st.Close() //nolint:errcheck

	oldSince := time.Unix(oldGradingEpochTS, 0).UTC()
	newSince := time.Unix(newGradingEpochTS, 0).UTC()
	for _, h := range []string{"1d", "1w"} {
		stats, err := st.ForecastDayStats(ctx, h, oldSince)
		if err != nil {
			return m, err
		}
		m.TotalDaysOld += len(stats)
		for _, s := range stats {
			fd := forecastmon.DayStat{Day: s.Day, Symbols: s.Symbols, DistinctProbs: s.DistinctProbs}
			isNew := s.Day >= newSince.Format("2006-01-02")
			if isNew {
				m.RawDaysNew[h]++
			}
			if !fd.Collapsed() {
				continue
			}
			line := fmt.Sprintf("%s (%d distinct across %d symbols)", s.Day, s.DistinctProbs, s.Symbols)
			m.CollapsedOld[h] = append(m.CollapsedOld[h], line)
			if isNew {
				m.CollapsedNew[h] = append(m.CollapsedNew[h], line)
			}
		}
		sort.Strings(m.CollapsedOld[h])
		sort.Strings(m.CollapsedNew[h])
	}
	return m, nil
}

func jsonStringList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func gradingWindowSpec(m gradingWindowMeasured) string {
	return `{
  "kind": "` + GradingWindowKind + `",
  "filedOn": "` + time.Now().UTC().Format("2006-01-02") + `",
  "amends": "the directional grading window start: store.SurvivorshipEpoch / tools/accuracy_registry.py SURVIVORSHIP_EPOCH = 2026-07-24 used as the graded-population boundary",
  "correctedTo": "store.GradingEpoch / tools/accuracy_registry.py GRADING_EPOCH = 2026-08-07",
  "correctionType": "grading-window-start-only",
  "claimsChanged": false,
  "filedWithKnowledgeOfOutcome": true,
  "survivorshipEpochMoves": false,
  "defect": {
    "one": "The publication gate refuses the whole directional table while ANY collapsed cross-section sits in the graded window (87e7f57, F3, 2026-09-09). The window is anchored to the survivorship epoch and does not roll, so the 2026-07-27..08-06 collapses — a fixed defect, ended by a34db09 and 906310c on 2026-08-06 — stay inside it indefinitely.",
    "two": "The refusal text instructs 'this clears when the window is re-registered', and no code path, tool or runbook implemented that. The gate named an exit that did not exist."
  },
  "measuredStateAtFiling": {
    "graded_days_from_old_epoch": ` + itoa(m.TotalDaysOld) + `,
    "collapsed_from_old_epoch": {
      "1d": ` + jsonStringList(m.CollapsedOld["1d"]) + `,
      "1w": ` + jsonStringList(m.CollapsedOld["1w"]) + `
    },
    "collapsed_from_new_epoch": {
      "1d": ` + jsonStringList(m.CollapsedNew["1d"]) + `,
      "1w": ` + jsonStringList(m.CollapsedNew["1w"]) + `
    },
    "raw_forecast_days_from_new_epoch": {
      "1d": ` + itoa(m.RawDaysNew["1d"]) + `,
      "1w": ` + itoa(m.RawDaysNew["1w"]) + `,
      "note": "raw distinct call days on the gate's own dedup; the grader further drops thin, unsettled and stale-feed days, so credible days will be fewer"
    }
  },
  "whyThisEpoch": "2026-08-07 is the first day after the last collapsed cross-section. 2026-08-05 was tried first and REFUSED by this command's own guard: 1d 2026-08-06 still reads 33 distinct across 327 symbols (ratio 0.10 < 0.15). The epoch is chosen by the collapse table on the gate's ruler, not by any accuracy number; measuredStateAtFiling.collapsed_from_new_epoch must be empty for this record to file.",
  "whatThisCannotChange": [
    "every accuracy, null, interval and skill figure",
    "the evidence floors (30 independent observations, 10 distinct credible days)",
    "the auto-retire rule and its frozen thresholds",
    "the collapse detector (forecastmon.MinDistinctRatio 0.15, MinSymbolsForCollapse 30)",
    "the survivorship epoch used for listing-status reconstruction, which stays 2026-07-24",
    "the retirement already on record for the directional ensemble"
  ],
  "expectedConsequence": "On the rows in hand the grader reports INSUFFICIENT DAYS for both horizons (measured 2026-09-13: 1w 6 of 10 credible days of 38, 32 degenerate). That is a count, not a verdict, and it moves only as dispersed days accrue. Nothing publishes until the floor clears, and when it does the number it publishes is whatever the record says."
}`
}
