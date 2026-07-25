package splitfix

import "testing"

// The detector's job is to separate two things that look identical to a
// threshold: an unadjusted split (price moves by a clean ratio, value is
// continuous) and a real market move (large, but on no particular ratio).
// Confusing them either leaves corruption in place or queues a re-backfill
// every time a stock crashes, so both directions are tested.

func series(closes []float64, vols []float64) []Bar {
	out := make([]Bar, len(closes))
	for i, c := range closes {
		v := 0.0
		if vols != nil {
			v = vols[i]
		}
		out[i] = Bar{Ts: int64(i+1) * 86400, Close: c, Volume: v}
	}
	return out
}

func TestDetectsForwardSplit(t *testing.T) {
	// 2:1 split: price halves, volume roughly doubles.
	bars := series(
		[]float64{100, 101, 99, 100, 50, 51, 49},
		[]float64{1e6, 1e6, 1e6, 1e6, 2e6, 2e6, 2e6})
	rep := Detect(bars)
	if len(rep.Suspects) != 1 {
		t.Fatalf("want 1 suspect, got %d: %+v", len(rep.Suspects), rep.Suspects)
	}
	s := rep.Suspects[0]
	if s.NearestName != "2:1" {
		t.Fatalf("nearest = %q, want 2:1", s.NearestName)
	}
	if !s.VolumeCorro || s.Confidence != "high" {
		t.Fatalf("volume should corroborate a 2:1: %+v", s)
	}
}

func TestDetectsReverseSplit(t *testing.T) {
	// 1:10 reverse: price 10x, volume ~1/10. This is the KIDZ/ELPW shape from
	// the 2026-07-17 audit (44x, 32x artifacts).
	bars := series(
		[]float64{2, 2.1, 1.9, 2, 20, 21, 19},
		[]float64{1e7, 1e7, 1e7, 1e7, 1e6, 1e6, 1e6})
	rep := Detect(bars)
	if len(rep.Suspects) != 1 || rep.Suspects[0].NearestName != "1:10 (reverse)" {
		t.Fatalf("want one 1:10 reverse, got %+v", rep.Suspects)
	}
}

func TestIgnoresRealCrash(t *testing.T) {
	// -70% in a day is catastrophic but lands on no split ratio (ratio 3.33,
	// nearest 3:1 is 11% off). Must NOT be queued for repair.
	bars := series([]float64{100, 100, 100, 30}, nil)
	if rep := Detect(bars); len(rep.Suspects) != 0 {
		t.Fatalf("a real crash must not be called a split: %+v", rep.Suspects)
	}
}

func TestIgnoresOrdinaryVolatility(t *testing.T) {
	bars := series([]float64{100, 130, 90, 115, 95, 120}, nil)
	if rep := Detect(bars); len(rep.Suspects) != 0 {
		t.Fatalf("ordinary volatility flagged: %+v", rep.Suspects)
	}
}

func TestJumpBelowThresholdIgnored(t *testing.T) {
	// -25% lands under MinJump entirely — ordinary bad-news territory.
	bars := series([]float64{100, 100, 75}, nil)
	if rep := Detect(bars); len(rep.Suspects) != 0 {
		t.Fatalf("sub-threshold move flagged: %+v", rep.Suspects)
	}
}

// The band between MinJump and ImpossibleJump is where a crash and a split are
// both plausible, so evidence requirements tighten there.
func TestMidBandRequiresCorroboration(t *testing.T) {
	// -60%: ratio 2.5 is a known factor and the move is real, but with no
	// volume and a ratio that is only approximately 2.5 it stays unproven.
	loose := Detect(series([]float64{100, 100, 40.6}, nil))
	if len(loose.Suspects) != 0 {
		t.Fatalf("mid-band with loose ratio and no volume must not flag: %+v", loose.Suspects)
	}
	// Same move, exact 2.5 ratio — precision alone settles it.
	exact := Detect(series([]float64{100, 100, 40}, nil))
	if len(exact.Suspects) != 1 {
		t.Fatalf("mid-band with exact ratio should flag: %+v", exact.Suspects)
	}
	// Loose ratio but volume corroborates the 2.5x share multiplication.
	withVol := Detect(series([]float64{100, 100, 40.6}, []float64{1e6, 1e6, 2.5e6}))
	if len(withVol.Suspects) != 1 {
		t.Fatalf("mid-band with volume corroboration should flag: %+v", withVol.Suspects)
	}
}

func TestRecentFlagMarksLiveContamination(t *testing.T) {
	// A split at the very end is actively poisoning live forecasts.
	bars := series([]float64{100, 100, 100, 50}, nil)
	rep := Detect(bars)
	if !rep.Recent {
		t.Fatal("a split on the last bar must mark the report Recent")
	}

	// The same split buried deeper than RecentSessions is historical damage:
	// still worth repairing, but not a live-forecast emergency.
	closes := []float64{100, 100, 50}
	for i := 0; i < RecentSessions+10; i++ {
		closes = append(closes, 50)
	}
	if rep := Detect(series(closes, nil)); !(len(rep.Suspects) == 1 && !rep.Recent) {
		t.Fatalf("old split should be found but not Recent: %+v", rep)
	}
}

func TestUnsortedInputIsHandled(t *testing.T) {
	bars := []Bar{
		{Ts: 4 * 86400, Close: 50},
		{Ts: 1 * 86400, Close: 100},
		{Ts: 3 * 86400, Close: 100},
		{Ts: 2 * 86400, Close: 100},
	}
	if rep := Detect(bars); len(rep.Suspects) != 1 {
		t.Fatalf("detector must sort by ts first: %+v", rep.Suspects)
	}
}

func TestNonPositiveCloseSkipped(t *testing.T) {
	// A zero close is a different defect; dividing by it would produce Inf.
	bars := series([]float64{100, 0, 50}, nil)
	for _, s := range Detect(bars).Suspects {
		if s.PrevClose <= 0 || s.Close <= 0 {
			t.Fatalf("non-positive close used in a ratio: %+v", s)
		}
	}
}

func TestShortSeriesIsSafe(t *testing.T) {
	if len(Detect(nil).Suspects) != 0 || len(Detect(series([]float64{100}, nil)).Suspects) != 0 {
		t.Fatal("short series must yield no suspects")
	}
}

func TestVolumeAbsenceDowngradesNotRejects(t *testing.T) {
	// An exact 2:1 with no volume data is still high confidence via ratio
	// precision; a slightly-off ratio without volume drops to medium.
	exact := Detect(series([]float64{100, 100, 50}, nil))
	if exact.Suspects[0].Confidence != "high" {
		t.Fatalf("exact ratio should be high confidence: %+v", exact.Suspects[0])
	}
	// Slightly-off ratio WITH volume corroboration: detected, and volume is
	// what carries it, so confidence stays high.
	off := Detect(series([]float64{100, 100, 51}, []float64{1e6, 1e6, 1.96e6}))
	if len(off.Suspects) != 1 || off.Suspects[0].Confidence != "high" {
		t.Fatalf("inexact ratio with volume should flag high: %+v", off.Suspects)
	}
}
