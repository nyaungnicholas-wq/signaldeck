package structregime

import (
	"math"
	"testing"
)

// trend63 must call the same regime as trend21 on the same series, at the
// quarterly horizon, with its OWN measured accuracy table.
func TestTrend63Predictor(t *testing.T) {
	closes := make([]float64, 800)
	p := 100.0
	for i := range closes {
		p *= 1.001
		closes[i] = p
	}
	f, ok := PredictTrend63(closes)
	if !ok {
		t.Fatal("expected forecast")
	}
	if f.Kind != KindTrend63 || f.HorizonDays != 63 {
		t.Fatalf("kind=%s horizon=%d", f.Kind, f.HorizonDays)
	}
	if f.Regime != "uptrend" {
		t.Fatalf("regime = %s", f.Regime)
	}
	if f.HistoricalAccuracy != accuracyFor(KindTrend63, f.Conviction) {
		t.Fatalf("accuracy not from the trend63 table: %.3f", f.HistoricalAccuracy)
	}
}

// trend63 tiers must be monotone non-decreasing in conviction and never claim
// above the measured top tier (0.837; CI 0.789-0.883 — the shipped number is
// the point measurement, not the CI's optimistic edge).
func TestTrend63TierMonotonicityAndCeiling(t *testing.T) {
	prev := 0.0
	for _, c := range []float64{0.1, 0.5, 0.8, 0.9, 1.0} {
		a := accuracyFor(KindTrend63, c)
		if a < prev {
			t.Fatalf("trend63 tiers not monotone at conv %.1f", c)
		}
		prev = a
	}
	if top := accuracyFor(KindTrend63, 1.0); top > 0.837 {
		t.Fatalf("trend63 claims %.3f above the measured 0.837 ceiling", top)
	}
	if lo := accuracyFor(KindTrend63, 0.1); lo != 0.700 {
		t.Fatalf("trend63 low tier %.3f != measured 0.700", lo)
	}
}

// Thin history is an honest absence for trend63 too.
func TestTrend63ThinHistoryRefuses(t *testing.T) {
	short := make([]float64, 100)
	for i := range short {
		short[i] = 100
	}
	if _, ok := PredictTrend63(short); ok {
		t.Fatal("trend63 should refuse thin history")
	}
}

// ── resolution helpers (credibility wave) ──

// A call that stays on its side of the SMA200 resolves to the same label; a
// crash through the SMA resolves to the other one — on hand-built series.
func TestResolveTrendAt(t *testing.T) {
	n := 320
	up := make([]float64, n)
	p := 100.0
	for i := range up {
		p *= 1.002
		up[i] = p
	}
	tCall := 280
	res, ok := ResolveTrendAt(up, tCall, 21)
	if !ok || res.Actual != "uptrend" || res.KeyValue <= 0 {
		t.Fatalf("persistent uptrend: ok=%v actual=%s key=%.2f", ok, res.Actual, res.KeyValue)
	}
	// crash right after the call: close falls to half — far below SMA200
	crash := append([]float64{}, up...)
	for i := tCall + 1; i < n; i++ {
		crash[i] = up[tCall] * 0.5
	}
	res, ok = ResolveTrendAt(crash, tCall, 21)
	if !ok || res.Actual != "downtrend" || res.KeyValue >= 0 {
		t.Fatalf("crash: ok=%v actual=%s key=%.2f", ok, res.Actual, res.KeyValue)
	}
	// missing forward bars ⇒ honest refusal, never a guess
	if _, ok := ResolveTrendAt(up[:tCall+10], tCall, 21); ok {
		t.Fatal("must refuse without the horizon bar")
	}
}

// Liquidity resolution: median AT CALL TIME is causal (bars <= t only) and the
// forward window is exactly t+1..t+21.
func TestResolveLiquidityAt(t *testing.T) {
	n := 320
	closes := make([]float64, n)
	vols := make([]float64, n)
	tCall := 280
	for i := range closes {
		closes[i] = 100
		if i <= tCall {
			vols[i] = 1e6
		} else {
			vols[i] = 5e6 // forward surge
		}
	}
	res, ok := ResolveLiquidityAt(closes, vols, tCall)
	if !ok || res.Actual != "active" {
		t.Fatalf("forward surge: ok=%v actual=%s", ok, res.Actual)
	}
	// key number = fwd mean − causal median = log(5e6/1e6) exactly (flat series)
	want := math.Log(5.0)
	if math.Abs(res.KeyValue-want) > 1e-9 {
		t.Fatalf("key %.6f != causal delta %.6f — median must come from bars <= call only", res.KeyValue, want)
	}
	// forward drought resolves quiet
	for i := tCall + 1; i < n; i++ {
		vols[i] = 2e5
	}
	res, ok = ResolveLiquidityAt(closes, vols, tCall)
	if !ok || res.Actual != "quiet" {
		t.Fatalf("forward drought: ok=%v actual=%s", ok, res.Actual)
	}
}

// Vol21 resolution: forward realized vol vs the causal trailing median.
func TestResolveVol21At(t *testing.T) {
	n := 320
	rets := make([]float64, n)
	tCall := 280
	for i := range rets {
		s := 0.005
		if i > tCall {
			s = 0.05 // vol explodes after the call
		}
		if i%2 == 0 {
			rets[i] = s
		} else {
			rets[i] = -s
		}
	}
	res, ok := ResolveVol21At(rets, tCall)
	if !ok || res.Actual != "elevated" || res.KeyValue <= 0 {
		t.Fatalf("vol explosion: ok=%v actual=%s key=%.4f", ok, res.Actual, res.KeyValue)
	}
	// calm forward window resolves calm — trailing had an elevated stretch so
	// the median sits above the tiny forward vol
	for i := range rets {
		s := 0.05
		if i > tCall {
			s = 0.005
		}
		if i%2 == 0 {
			rets[i] = s
		} else {
			rets[i] = -s
		}
	}
	res, ok = ResolveVol21At(rets, tCall)
	if !ok || res.Actual != "calm" || res.KeyValue >= 0 {
		t.Fatalf("vol collapse: ok=%v actual=%s key=%.4f", ok, res.Actual, res.KeyValue)
	}
}

// NO LOOKAHEAD: bars beyond t+horizon must never influence a resolution —
// truncating the series at the horizon bar gives the identical result.
func TestResolveTruncationInvariance(t *testing.T) {
	n := 400
	closes := make([]float64, n)
	vols := make([]float64, n)
	rets := make([]float64, n)
	p := 100.0
	for i := range closes {
		if i%3 == 0 {
			p *= 1.004
		} else {
			p *= 0.999
		}
		closes[i] = p
		vols[i] = 1e6 + float64(i%17)*1e5
		if i%2 == 0 {
			rets[i] = 0.004
		} else {
			rets[i] = -0.003
		}
	}
	tCall := 300
	// wildly different future beyond the horizon
	fut := func(x []float64, from int, v float64) []float64 {
		y := append([]float64{}, x...)
		for i := from; i < len(y); i++ {
			y[i] = v
		}
		return y
	}
	r1, ok1 := ResolveTrendAt(closes[:tCall+21+1], tCall, 21)
	r2, ok2 := ResolveTrendAt(fut(closes, tCall+22, 1e9), tCall, 21)
	if ok1 != ok2 || r1 != r2 {
		t.Fatal("trend resolution read beyond the horizon bar")
	}
	l1, ok1 := ResolveLiquidityAt(closes[:tCall+21+1], vols[:tCall+21+1], tCall)
	l2, ok2 := ResolveLiquidityAt(closes, fut(vols, tCall+22, 1e12), tCall)
	if ok1 != ok2 || l1 != l2 {
		t.Fatal("liquidity resolution read beyond the horizon bar")
	}
	v1, ok1 := ResolveVol21At(rets[:tCall+21+1], tCall)
	v2, ok2 := ResolveVol21At(fut(rets, tCall+22, 0.5), tCall)
	if ok1 != ok2 || v1 != v2 {
		t.Fatal("vol21 resolution read beyond the horizon bar")
	}
}
