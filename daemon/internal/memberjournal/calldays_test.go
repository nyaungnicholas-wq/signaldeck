package memberjournal

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// perDay builds days x callsPerDay resolved "up" calls, one entry session per
// day, the first hitsPerDay of each day's calls hits.
func perDay(days, callsPerDay, hitsPerDay int) []store.MemberCall {
	var calls []store.MemberCall
	for d := 0; d < days; d++ {
		entryTs := int64(d+1) * 86400
		for i := 0; i < callsPerDay; i++ {
			outcome := "miss"
			if i < hitsPerDay {
				outcome = "hit"
			}
			calls = append(calls, store.MemberCall{
				Call:    "up",
				Status:  "resolved",
				Outcome: outcome,
				EntryTs: entryTs,
			})
		}
	}
	return calls
}

// TestSummarizeCountsCallDaysNotCalls: calls entered the same session share
// one market move, so the floor and the interval count distinct entry sessions
// (the conservative unit), while the hit rate stays hits/resolved.
func TestSummarizeCountsCallDaysNotCalls(t *testing.T) {
	// Case 1: Clustered and withheld
	calls := perDay(10, 4, 3)
	s := Summarize(calls)
	if s.Resolved != 40 {
		t.Errorf("Resolved = %d, want 40", s.Resolved)
	}
	if s.CallDays != 10 {
		t.Errorf("CallDays = %d, want 10", s.CallDays)
	}
	if s.Hits != 30 {
		t.Errorf("Hits = %d, want 30", s.Hits)
	}
	if !s.Withheld {
		t.Error("Withheld = false, want true")
	}
	if s.HitRate != nil {
		t.Errorf("HitRate = %v, want nil", s.HitRate)
	}
	if s.CILow != nil {
		t.Errorf("CILow = %v, want nil", s.CILow)
	}
	if s.CIHigh != nil {
		t.Errorf("CIHigh = %v, want nil", s.CIHigh)
	}

	// Case 2a: 29 days -> withheld
	calls = perDay(29, 2, 1)
	s = Summarize(calls)
	if s.Resolved != 58 {
		t.Errorf("Resolved = %d, want 58", s.Resolved)
	}
	if s.CallDays != 29 {
		t.Errorf("CallDays = %d, want 29", s.CallDays)
	}
	if !s.Withheld {
		t.Error("Withheld = false, want true for 29 days")
	}

	// Case 2b: 30 days -> not withheld
	calls = perDay(30, 1, 1)
	s = Summarize(calls)
	if s.Resolved != 30 {
		t.Errorf("Resolved = %d, want 30", s.Resolved)
	}
	if s.CallDays != 30 {
		t.Errorf("CallDays = %d, want 30", s.CallDays)
	}
	if s.Withheld {
		t.Error("Withheld = true, want false for 30 days")
	}

	// Case 3: Interval on days, point estimate on calls
	var calls3 []store.MemberCall
	// First 12 days: 2 calls/day, 2 hits/day
	for d := 0; d < 12; d++ {
		entryTs := int64(d+1) * 86400
		for i := 0; i < 2; i++ {
			outcome := "miss"
			if i < 2 {
				outcome = "hit"
			}
			calls3 = append(calls3, store.MemberCall{
				Call:    "up",
				Status:  "resolved",
				Outcome: outcome,
				EntryTs: entryTs,
			})
		}
	}
	// Next 18 days (day 13 to 30): 2 calls/day, 1 hit/day
	for d := 12; d < 30; d++ {
		entryTs := int64(d+1) * 86400
		for i := 0; i < 2; i++ {
			outcome := "miss"
			if i < 1 {
				outcome = "hit"
			}
			calls3 = append(calls3, store.MemberCall{
				Call:    "up",
				Status:  "resolved",
				Outcome: outcome,
				EntryTs: entryTs,
			})
		}
	}
	s = Summarize(calls3)
	if s.Resolved != 60 {
		t.Errorf("Resolved = %d, want 60", s.Resolved)
	}
	if s.CallDays != 30 {
		t.Errorf("CallDays = %d, want 30", s.CallDays)
	}
	if s.Hits != 42 {
		t.Errorf("Hits = %d, want 42", s.Hits)
	}
	if s.Withheld {
		t.Error("Withheld = true, want false (CallDays=30)")
	}
	expectedRate := 42.0 / 60.0
	if s.HitRate == nil || *s.HitRate != expectedRate {
		t.Errorf("HitRate = %v, want %v", s.HitRate, expectedRate)
	}
	iv30 := clusterstat.WilsonEffAt(expectedRate, float64(s.CallDays), 1.959963985)
	if s.CILow == nil || *s.CILow != iv30.Lo {
		t.Errorf("CILow = %v, want %v", s.CILow, iv30.Lo)
	}
	if s.CIHigh == nil || *s.CIHigh != iv30.Hi {
		t.Errorf("CIHigh = %v, want %v", s.CIHigh, iv30.Hi)
	}
	iv60 := clusterstat.WilsonEffAt(expectedRate, 60.0, 1.959963985)
	width30 := *s.CIHigh - *s.CILow
	width60 := iv60.Hi - iv60.Lo
	if width30 <= width60 {
		t.Errorf("Interval width at 30 days (%f) should be > width at 60 calls (%f)", width30, width60)
	}

	// Case 4: Same-day calls on different symbols still count once
	var calls4 []store.MemberCall
	// 30 days of 1 call each
	for d := 0; d < 30; d++ {
		entryTs := int64(d+1) * 86400
		calls4 = append(calls4, store.MemberCall{
			Call:    "up",
			Status:  "resolved",
			Outcome: "hit",
			EntryTs: entryTs,
		})
	}
	// 9 more calls on day 1 (EntryTs = 86400)
	for i := 0; i < 9; i++ {
		calls4 = append(calls4, store.MemberCall{
			Call:    "up",
			Status:  "resolved",
			Outcome: "hit",
			EntryTs: 86400,
		})
	}
	s = Summarize(calls4)
	if s.Resolved != 39 {
		t.Errorf("Resolved = %d, want 39", s.Resolved)
	}
	if s.CallDays != 30 {
		t.Errorf("CallDays = %d, want 30", s.CallDays)
	}
	if s.Withheld {
		t.Error("Withheld = true, want false")
	}

	// Case 5: Open, void and withdrawn calls never add a call day
	var calls5 []store.MemberCall
	// 29 days of 1 call per day, 1 hit per day
	for d := 0; d < 29; d++ {
		entryTs := int64(d+1) * 86400
		calls5 = append(calls5, store.MemberCall{
			Call:    "up",
			Status:  "resolved",
			Outcome: "hit",
			EntryTs: entryTs,
		})
	}
	// Add an open call
	calls5 = append(calls5, store.MemberCall{
		Call:    "up",
		Status:  "open",
		Outcome: "",
		EntryTs: 999 * 86400,
	})
	// Add a void call
	calls5 = append(calls5, store.MemberCall{
		Call:    "up",
		Status:  "void",
		Outcome: "",
		EntryTs: 998 * 86400,
	})
	// Add a withdrawn call
	calls5 = append(calls5, store.MemberCall{Call: "up", Status: "withdrawn", EntryTs: 997 * 86400})
	s = Summarize(calls5)
	if s.Resolved != 29 {
		t.Errorf("Resolved = %d, want 29", s.Resolved)
	}
	if s.CallDays != 29 {
		t.Errorf("CallDays = %d, want 29", s.CallDays)
	}
	if !s.Withheld {
		t.Error("Withheld = false, want true (CallDays=29 < 30)")
	}
}
