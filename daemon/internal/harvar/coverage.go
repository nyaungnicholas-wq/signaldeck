// Package harvar backtests Value-at-Risk forecasts.
//
// A VaR forecast at level alpha claims that the loss will exceed it only alpha
// of the time. A BREACH is a day where the realised loss did exceed it. Two
// separate things can be wrong, and a model can pass one while failing the
// other:
//
//   - Kupiec tests the RATE. Did roughly alpha of days breach?
//   - Christoffersen tests the TIMING. Were the breaches independent, or did
//     they arrive in clusters? A model can have exactly the right NUMBER of bad
//     days and still be wrong about WHEN, and that is the more dangerous
//     failure: clustered breaches are what a drawdown feels like.
//
// # These tests are meaningful PER SYMBOL only
//
// Do not pool breaches across symbols. A market-wide down day breaches every
// symbol at once, so a pooled independence test returns a p-value near zero
// for any model whatsoever. That is real CROSS-SECTIONAL dependence, and it is
// not what the independence test is about -- it tests SERIAL dependence within
// one series.
//
// The honest panel headline is therefore not a pooled test statistic. It is
// the pooled breach RATE with a block bootstrap for its interval, alongside
// the DISTRIBUTION of per-symbol p-values compared against the 5% rejection
// rate you would expect by chance. Reporting a pooled chi-square here would
// manufacture significance out of the fact that stocks move together.
//
// # Expected shortfall is deliberately NOT tested here
//
// ES is not elicitable and there is no Kupiec analogue for it. Attaching a
// coverage test to an ES number and calling it "backtested" would be a
// fabricated statistic, which is the one thing this platform exists not to
// publish. ES may be reported descriptively -- for instance the ratio of mean
// realised loss on breach days to the predicted ES, clearly labelled as a
// description and not a hypothesis test. The correct upgrade is the
// Acerbi-Szekely Z2 test, and it is out of scope.
package harvar

import "math"

// MinObs is the smallest sample these tests will accept.
//
// Below it they return OK=false rather than a number. The chi-square
// distribution of a likelihood ratio is asymptotic, so a p-value computed from
// a dozen observations is not a weak result -- it is a made-up one. Exported
// because callers need to know why they were refused, and because the backtest
// harness reports how many symbols fell below it.
const MinObs = 30

// CoverageResult is one coverage test.
type CoverageResult struct {
	LR   float64 // the likelihood-ratio statistic
	P    float64 // its p-value
	DF   int     // chi-square degrees of freedom
	N    int     // observations
	Br   int     // observed breaches
	Rate float64 // observed breach rate, Br/N
	OK   bool    // false when the test could not be computed
}

// Kupiec is the unconditional-coverage (proportion-of-failures) test.
//
//	LR_uc = -2 * ln[ ((1-a)^(N-x) * a^x) / ((1-p)^(N-x) * p^x) ]
//
// where x is the breach count, p = x/N, a is the nominal level.
// chi-square with 1 degree of freedom.
func Kupiec(breaches []bool, alpha float64) CoverageResult {
	// NaN must be rejected EXPLICITLY: every comparison against NaN is false,
	// so `alpha <= 0 || alpha >= 1` lets it straight through and the test then
	// returns a NaN p-value that reads as "not significant" to any caller
	// comparing it to 0.05.
	if math.IsNaN(alpha) || alpha <= 0 || alpha >= 1 {
		return CoverageResult{OK: false}
	}
	n := len(breaches)
	if n < MinObs {
		return CoverageResult{OK: false}
	}
	var x int
	for _, b := range breaches {
		if b {
			x++
		}
	}
	// x == 0 or x == n puts the MLE on the boundary, where ln(p) or ln(1-p) is
	// -Inf. The 0*ln(0) = 0 convention below -- the correct limit -- is what
	// keeps those cases finite instead of NaN, and they are not rare: a
	// well-behaved 1% VaR over 30 days is expected to breach 0.3 times.
	p := float64(x) / float64(n)
	// log-likelihood under null (alpha)
	l0 := (float64(n-x) * math.Log(1-alpha)) + (float64(x) * math.Log(alpha))
	// log-likelihood under alternative (p), with 0*ln(0) = 0
	var l1 float64
	if x > 0 {
		l1 += float64(x) * math.Log(p)
	}
	if n-x > 0 {
		l1 += float64(n-x) * math.Log(1-p)
	}
	lr := -2.0 * (l0 - l1)
	if lr < 0 {
		lr = 0 // numerical safety
	}
	pv := ChiSqSF(lr, 1)
	if pv < 0 {
		pv = 0
	} else if pv > 1 {
		pv = 1
	}
	return CoverageResult{
		LR:   lr,
		P:    pv,
		DF:   1,
		N:    n,
		Br:   x,
		Rate: p,
		OK:   true,
	}
}

// ChristoffersenInd is the independence test, built from the 2x2 matrix of
// transitions between "breach" and "no breach" on consecutive days.
// Let n_ij be the count of transitions from state i to state j (0 = no
// breach, 1 = breach). Let
//
//	pi01 = n01/(n00+n01), pi11 = n11/(n10+n11),
//	pi   = (n01+n11)/(n00+n01+n10+n11)
//
// Then
//
//	LR_ind = -2 * ln[ ((1-pi)^(n00+n10) * pi^(n01+n11)) /
//	                  ((1-pi01)^n00 * pi01^n01 * (1-pi11)^n10 * pi11^n11) ]
//
// chi-square with 1 degree of freedom.
// Set Rate and Br from the whole series as usual.
// If either row of the transition matrix is empty (n00+n01 == 0, or
// n10+n11 == 0) the test is not identified and this returns OK=false.
//
// That is the COMMON case, not an edge case. At alpha=0.01 over 250 sessions
// the expected breach count is 2.5, so n10+n11 is frequently 0 or 1 and there
// is simply no evidence about what follows a breach. Refusing is the honest
// answer; a p-value from an unidentified model would be noise wearing a
// number.
func ChristoffersenInd(breaches []bool) CoverageResult {
	n := len(breaches)
	if n < MinObs {
		return CoverageResult{OK: false}
	}
	var n00, n01, n10, n11 int
	for i := 0; i < n-1; i++ {
		cur := 0
		if breaches[i] {
			cur = 1
		}
		nxt := 0
		if breaches[i+1] {
			nxt = 1
		}
		switch cur*2 + nxt {
		case 0: // 00
			n00++
		case 1: // 01
			n01++
		case 2: // 10
			n10++
		case 3: // 11
			n11++
		}
	}
	if n00+n01 == 0 || n10+n11 == 0 {
		return CoverageResult{OK: false}
	}
	pi01 := float64(n01) / float64(n00+n01)
	pi11 := float64(n11) / float64(n10+n11)
	trans := n00 + n01 + n10 + n11 // = n-1
	pi := float64(n01+n11) / float64(trans)
	// log-likelihood under null (same pi regardless of previous state)
	var l0 float64
	if n00+n10 > 0 {
		l0 += float64(n00+n10) * math.Log(1-pi)
	}
	if n01+n11 > 0 {
		l0 += float64(n01+n11) * math.Log(pi)
	}
	// log-likelihood under alternative (saturation)
	var l1 float64
	if n00 > 0 {
		l1 += float64(n00) * math.Log(1-pi01)
	}
	if n01 > 0 {
		l1 += float64(n01) * math.Log(pi01)
	}
	if n10 > 0 {
		l1 += float64(n10) * math.Log(1-pi11)
	}
	if n11 > 0 {
		l1 += float64(n11) * math.Log(pi11)
	}
	lr := -2.0 * (l0 - l1)
	if lr < 0 {
		lr = 0
	}
	pv := ChiSqSF(lr, 1)
	if pv < 0 {
		pv = 0
	} else if pv > 1 {
		pv = 1
	}
	// Br and Rate describe the WHOLE series, not the transition pairs. n01+n11
	// counts breaches that had a predecessor, so it silently drops a breach on
	// the very first day and would disagree with Kupiec's count on the same
	// input.
	br := 0
	for _, b := range breaches {
		if b {
			br++
		}
	}
	return CoverageResult{
		LR:   lr,
		P:    pv,
		DF:   1,
		N:    n,
		Br:   br,
		Rate: float64(br) / float64(n),
		OK:   true,
	}
}

// ChristoffersenCC is conditional coverage: LR_cc = LR_uc + LR_ind,
// chi-square with 2 degrees of freedom. Compute the two components and add
// them; if either component is not OK, the result is not OK.
func ChristoffersenCC(breaches []bool, alpha float64) CoverageResult {
	uc := Kupiec(breaches, alpha)
	ind := ChristoffersenInd(breaches)
	if !uc.OK || !ind.OK {
		return CoverageResult{OK: false}
	}
	lr := uc.LR + ind.LR
	pv := ChiSqSF(lr, 2)
	if pv < 0 {
		pv = 0
	} else if pv > 1 {
		pv = 1
	}
	return CoverageResult{
		LR:   lr,
		P:    pv,
		DF:   2,
		N:    uc.N,
		Br:   uc.Br,
		Rate: uc.Rate,
		OK:   true,
	}
}

// ChiSqSF is the chi-square survival function P(X > x).
// Only 1 and 2 degrees of freedom are ever needed here, and BOTH have exact
// closed forms, so no special-function library and no series expansion:
//
//	df 1: erfc(sqrt(x/2))
//	df 2: exp(-x/2)
//
// Return 1 for x <= 0. Return NaN for any df other than 1 or 2, so an
// unsupported call is loud rather than silently wrong.
func ChiSqSF(x float64, df int) float64 {
	if x <= 0 {
		return 1
	}
	switch df {
	case 1:
		return math.Erfc(math.Sqrt(x / 2))
	case 2:
		return math.Exp(-x / 2)
	default:
		return math.NaN()
	}
}
