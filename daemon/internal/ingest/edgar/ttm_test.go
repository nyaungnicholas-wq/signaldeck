package edgar

import (
	"testing"
	"time"
)

func TestTTMFacts(t *testing.T) {
	day := func(s string) int64 {
		tm, _ := time.Parse("2006-01-02", s)
		return tm.Unix()
	}

	t.Run("Case1_fiscalYearLateSeptember", func(t *testing.T) {
		m := map[string]conceptUnits{
			"Revenues": {
				Units: map[string][]unitPoint{
					"USD": {
						{Start: "2023-10-01", End: "2024-09-28", Val: 391, Filed: "2024-11-01"},
						{Start: "2023-10-01", End: "2024-03-30", Val: 210, Filed: "2024-05-03"},
						{Start: "2023-12-31", End: "2024-03-30", Val: 90, Filed: "2024-05-03"},
						{Start: "2024-09-29", End: "2025-03-29", Val: 220, Filed: "2025-05-02"},
						{Start: "2024-12-29", End: "2025-03-29", Val: 95, Filed: "2025-05-02"},
					},
				},
			},
		}
		got := ttmFacts(m, []string{"Revenues"}, "USD")
		want := []Fact{{391, day("2024-09-28")}, {401, day("2025-03-29")}}
		if len(got) != len(want) {
			t.Fatalf("len got=%d want=%d got=%v want=%v", len(got), len(want), got, want)
		}
		for i := range want {
			if got[i].Value != want[i].Value || got[i].AsOf != want[i].AsOf {
				t.Fatalf("got[%d]=%v want[%d]=%v", i, got[i], i, want[i])
			}
		}
	})

	t.Run("Case2_restatement", func(t *testing.T) {
		m := map[string]conceptUnits{
			"Revenues": {
				Units: map[string][]unitPoint{
					"USD": {
						{Start: "2023-10-01", End: "2024-09-28", Val: 391, Filed: "2024-11-01"},
						{Start: "2023-10-01", End: "2024-09-28", Val: 395, Filed: "2025-11-01"},
					},
				},
			},
		}
		got := ttmFacts(m, []string{"Revenues"}, "USD")
		want := []Fact{{395, day("2024-09-28")}}
		if len(got) != len(want) {
			t.Fatalf("len got=%d want=%d got=%v want=%v", len(got), len(want), got, want)
		}
		if got[0].Value != want[0].Value || got[0].AsOf != want[0].AsOf {
			t.Fatalf("got=%v want=%v", got, want)
		}
	})

	t.Run("Case3_fallbackConcept", func(t *testing.T) {
		m := map[string]conceptUnits{
			"A": {
				Units: map[string][]unitPoint{
					"USD": {{Start: "2025-12-01", End: "2025-12-31", Val: 100, Filed: "2026-01-15"}},
				},
			},
			"B": {
				Units: map[string][]unitPoint{
					"USD": {{Start: "2025-01-01", End: "2025-12-31", Val: 7, Filed: "2026-02-01"}},
				},
			},
		}
		got := ttmFacts(m, []string{"A", "B"}, "USD")
		want := []Fact{{7, day("2025-12-31")}}
		if len(got) != len(want) {
			t.Fatalf("len got=%d want=%d got=%v want=%v", len(got), len(want), got, want)
		}
		if got[0].Value != want[0].Value || got[0].AsOf != want[0].AsOf {
			t.Fatalf("got=%v want=%v", got, want)
		}
	})

	t.Run("Case4_onlyInstantsAndGarbage", func(t *testing.T) {
		m := map[string]conceptUnits{
			"Revenues": {
				Units: map[string][]unitPoint{
					"USD": {
						{Start: "", End: "2024-12-31", Val: 100, Filed: "2025-01-15"},
						{Start: "2024-01-01", End: "garbage", Val: 200, Filed: "2025-01-15"},
					},
				},
			},
		}
		got := ttmFacts(m, []string{"Revenues"}, "USD")
		if len(got) != 0 {
			t.Fatalf("want len 0 got=%v", got)
		}
	})
}

// The concept with the newest data wins over an earlier-listed stale one.
func TestTTMFactsPrefersFreshestConcept(t *testing.T) {
	m := map[string]conceptUnits{
		"Old": {Units: map[string][]unitPoint{"USD": {{Start: "2021-01-01", End: "2021-12-31", Val: 1, Filed: "2022-02-01"}}}},
		"New": {Units: map[string][]unitPoint{"USD": {{Start: "2025-01-01", End: "2025-12-31", Val: 9, Filed: "2026-02-01"}}}},
	}
	got := ttmFacts(m, []string{"Old", "New"}, "USD")
	if len(got) != 1 || got[0].Value != 9 {
		t.Fatalf("got %+v, want the 2025 value 9 from the newer concept", got)
	}
}
