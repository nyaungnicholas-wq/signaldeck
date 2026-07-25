// Package sentcorr measures whether news-text sentiment carries information
// about FORWARD returns — and, more importantly, whether any of that
// information survives controlling for price.
//
// # WHY THE CONTROL IS THE WHOLE POINT
//
// Every signal this platform has tested is price-derived, and the documented
// directional ceiling (~55%) came out of that. Text is genuinely orthogonal
// input, which is the entire reason it is worth testing. But naive sentiment IC
// is the easiest fake in this whole domain: headlines describe what the stock
// just did. "Shares surge on strong guidance" is published BECAUSE the price
// rose. A study that regresses sentiment on forward returns without removing
// contemporaneous and trailing price movement is measuring momentum with extra
// steps, and it will report a confident number.
//
// So the headline metric here is not the raw correlation. It is the PARTIAL
// correlation of sentiment with forward return, controlling for the same-session
// return and the trailing 5-session return. If that partial IC collapses to zero
// while the raw IC looks healthy, the honest conclusion is that sentiment adds
// nothing the price did not already say — and this package is built to reach
// that conclusion cleanly rather than to avoid it.
//
// The other disciplines are the ones the alpha loop already learned the hard
// way, and they are enforced here rather than trusted to the caller:
//
//   - NON-OVERLAPPING forward windows. Daily samples of a 21-session forward
//     return share 20 of 21 days; pooling them inflates n by ~21x and turns
//     noise into significance. Observations are thinned per symbol so no two
//     forward windows overlap.
//   - BLOCK-CLUSTERED confidence intervals, resampling whole CALENDAR MONTHS.
//     Sentiment is cross-sectionally correlated — one macro day moves every
//     name's headlines together — so treating symbol-days as independent
//     understates the interval badly.
//   - A MATCHED NULL for the hit-rate: the base rate is the best CONSTANT
//     prediction (always-up or always-down) on the same labels, not 50%. This
//     is the correction that killed the 52-week-high "82% accurate" signal.
//   - EXPLICIT GATES on sample size, symbol count and era coverage, with the
//     metrics withheld (nil, not zero) and a stated reason when unmet.
//
// The package is pure: no store, no clock, no network, no randomness beyond a
// fixed-seed bootstrap. It is given observations and returns a verdict.
package sentcorr

import (
	"fmt"
	"math"
	"sort"
)

// Obs is one independent (symbol, session) observation.
//
// CRITICAL ON TIMING: Day/SessionIdx must be the session on which the sentiment
// was ALREADY KNOWN and therefore actionable, and Fwd must be measured strictly
// after it. A headline published after the close belongs to the next session.
// Building Obs from a headline's own calendar day silently grants lookahead on
// every after-hours article, which is most of them.
type Obs struct {
	SymbolID int64
	Day      string // YYYY-MM-DD of the actionable session (reporting only)
	// SessionIdx is the symbol's own trading-session index for Day. Used to
	// enforce non-overlap; calendar days cannot do that job across weekends and
	// holidays.
	SessionIdx int
	// Sent is the sentiment feature value, in [-1,+1].
	Sent float64
	// NHeadlines is how many POLAR headlines produced Sent — evidence weight,
	// and the basis of the minimum-coverage filter.
	NHeadlines int
	// Ret0 is the return of the session on which the sentiment became
	// actionable. Control variable: the news and this move are contemporaneous,
	// so any correlation between them is not forward-looking information.
	Ret0 float64
	// RetTrail is the trailing 5-session return ENDING at that session.
	// Control variable: it strips the momentum that headline tone tracks.
	RetTrail float64
	// Fwd is the forward return over the study horizon, starting after the
	// actionable session.
	Fwd float64
}

// Config parameterises one study.
type Config struct {
	// Horizon in trading sessions. Also the non-overlap stride.
	Horizon int
	// MinObs is the floor for reporting any metric at all.
	MinObs int
	// MinSymbols guards against a "fleet" result driven by three names.
	MinSymbols int
	// MinMonths is the era-coverage gate: a relationship that only existed in
	// one quarter is a period artifact until proven otherwise.
	MinMonths int
	// MinHeadlines drops observations with thinner evidence than this.
	MinHeadlines int
	// CostPerSide is the round-trip-halved trading cost used for the cost-net
	// quintile spread, as a fraction (0.001 = 10bps a side).
	CostPerSide float64
	// Bootstrap iterations for the clustered CI. 0 disables the CI.
	Bootstrap int
	// FamilySize is how many hypotheses are being tested TOGETHER — here, how
	// many horizons the runner studies in one pass. The reported interval is
	// Bonferroni-widened by this factor.
	//
	// This is not pedantry. Testing three horizons and reporting whichever one
	// clears a nominal 95% interval is a one-in-seven chance of a false positive
	// dressed as a finding, and "the 5-day horizon is significant" is exactly the
	// shape a p-hacked result takes. Widening the interval makes each horizon
	// HARDER to clear, so there is no incentive to add horizons.
	FamilySize int
}

// DefaultConfig is the study configuration used by the worker.
func DefaultConfig(horizon int) Config {
	return Config{
		Horizon:      horizon,
		MinObs:       300,
		MinSymbols:   20,
		MinMonths:    6,
		MinHeadlines: 1,
		CostPerSide:  0.001,
		Bootstrap:    600,
		FamilySize:   1,
	}
}

// Result is one study's verdict. Every metric is a pointer: nil means WITHHELD
// (not measurable on this sample), which is a different claim from zero.
type Result struct {
	Horizon int `json:"horizon"`

	// Sample description, always reported even when gated — how far the data is
	// from being able to answer is itself the useful output today.
	RawObs      int    `json:"rawObs"`  // before non-overlap thinning
	Obs         int    `json:"obs"`     // independent observations studied
	Symbols     int    `json:"symbols"` //
	Months      int    `json:"months"`  // distinct calendar months covered
	FirstDay    string `json:"firstDay"`
	LastDay     string `json:"lastDay"`
	Overlapping int    `json:"overlappingDropped"` // thinned away by non-overlap

	Gated      bool   `json:"gated"`
	GateReason string `json:"gateReason,omitempty"`

	// RawIC is the plain Pearson correlation of sentiment with forward return.
	// Reported for comparison ONLY — it is the number that is contaminated by
	// price. Never quote it as the finding.
	RawIC *float64 `json:"rawIC"`
	// RawSpearman is the rank version, robust to the fat tails of returns.
	RawSpearman *float64 `json:"rawSpearman"`

	// PartialIC is THE HEADLINE METRIC: correlation of sentiment with forward
	// return after both are residualised on the same-session and trailing-5d
	// returns. This is what "sentiment adds information price did not have"
	// would have to show up as.
	PartialIC *float64 `json:"partialIC"`
	// PartialLo/PartialHi are the FAMILY-ADJUSTED interval — the one a decision
	// should be based on, Bonferroni-widened over the horizons tested together.
	PartialLo *float64 `json:"partialICLo"`
	PartialHi *float64 `json:"partialICHi"`
	// Nominal 95% bounds, reported alongside so the cost of the correction is
	// visible rather than hidden.
	PartialLo95 *float64 `json:"partialIC95Lo"`
	PartialHi95 *float64 `json:"partialIC95Hi"`
	// CILevel describes what PartialLo/PartialHi actually are.
	CILevel string `json:"ciLevel"`
	// FamilySize is the number of jointly-tested hypotheses the correction covers.
	FamilySize int `json:"familySize"`
	// PriceExplained is 1 - |PartialIC|/|RawIC|: the share of the raw correlation
	// that turned out to be price contamination. NEGATIVE means the controls
	// STRENGTHENED the relationship — price was masking the text effect, not
	// manufacturing it. nil when RawIC is ~0 and the ratio is meaningless.
	PriceExplained *float64 `json:"priceExplainedShare"`

	// HitRate is the sign-agreement rate of sentiment with forward return, and
	// BaseRate is the best CONSTANT prediction on the same labels. Edge is the
	// difference — the only one of the three that means anything.
	HitRate  *float64 `json:"hitRate"`
	BaseRate *float64 `json:"baseRate"`
	Edge     *float64 `json:"edgeVsConstant"`

	// Quintile forward-return means, most-negative to most-positive sentiment,
	// plus the top-minus-bottom spread gross and net of two-side cost.
	Quintiles   []QuintileRow `json:"quintiles"`
	SpreadGross *float64      `json:"spreadGross"`
	SpreadNet   *float64      `json:"spreadNet"`
	// SpreadAligned is the spread of the portfolio whose DIRECTION MATCHES the
	// measured IC, net of cost: long top / short bottom when the IC is positive,
	// and the reverse when it is negative.
	//
	// Reporting only top-minus-bottom would judge a negative-IC finding by a
	// portfolio nobody would hold. A reliably CONTRARIAN signal is still a
	// signal, and its tradeability has to be assessed in the direction the
	// evidence actually points.
	SpreadAligned *float64 `json:"spreadAlignedNet"`
	// AlignedSide names that portfolio in words, so the number cannot be read
	// backwards.
	AlignedSide string `json:"alignedSide,omitempty"`
	// Monotonic reports whether quintile means increase across all five buckets.
	// A signal that works should be ordered, not just top-vs-bottom lucky.
	Monotonic *bool `json:"monotonic"`

	// Verdict is the plain-English conclusion, generated from the numbers above
	// rather than written by hand, so it cannot drift away from them.
	Verdict string `json:"verdict"`
}

// QuintileRow is one sentiment bucket's forward-return summary.
type QuintileRow struct {
	Quintile int     `json:"quintile"` // 1 = most negative sentiment
	N        int     `json:"n"`
	MeanSent float64 `json:"meanSent"`
	MeanFwd  float64 `json:"meanFwd"`
}

// Study runs the whole measurement. obs may arrive in any order and may contain
// overlapping windows; Study thins and sorts defensively.
func Study(obs []Obs, cfg Config) Result {
	if cfg.Horizon <= 0 {
		cfg.Horizon = 1
	}
	family := cfg.FamilySize
	if family < 1 {
		family = 1
	}
	// Recorded BEFORE the gates: a gated result must carry the same evidential
	// standard as an ungated one, or a later run could quietly be judged against
	// a narrower interval than the one this study committed to.
	res := Result{
		Horizon: cfg.Horizon, RawObs: len(obs),
		FamilySize: family, CILevel: ciLabel(family),
	}

	// Evidence filter first, so the non-overlap stride is spent on usable rows.
	kept := make([]Obs, 0, len(obs))
	for _, o := range obs {
		if o.NHeadlines >= cfg.MinHeadlines && isFinite(o.Sent) && isFinite(o.Fwd) &&
			isFinite(o.Ret0) && isFinite(o.RetTrail) {
			kept = append(kept, o)
		}
	}

	ind := thinNonOverlapping(kept, cfg.Horizon)
	res.Obs = len(ind)
	res.Overlapping = len(kept) - len(ind)

	syms := map[int64]bool{}
	months := map[string]bool{}
	for _, o := range ind {
		syms[o.SymbolID] = true
		if len(o.Day) >= 7 {
			months[o.Day[:7]] = true
		}
	}
	res.Symbols = len(syms)
	res.Months = len(months)
	if len(ind) > 0 {
		days := make([]string, 0, len(ind))
		for _, o := range ind {
			days = append(days, o.Day)
		}
		sort.Strings(days)
		res.FirstDay, res.LastDay = days[0], days[len(days)-1]
	}

	// Gates. Report the sample, withhold the metrics, say why.
	switch {
	case res.Obs < cfg.MinObs:
		res.Gated = true
		res.GateReason = fmt.Sprintf("insufficient independent observations (%d/%d) — "+
			"daily samples of a %d-session forward return overlap, so only non-overlapping "+
			"windows count", res.Obs, cfg.MinObs, cfg.Horizon)
	case res.Symbols < cfg.MinSymbols:
		res.Gated = true
		res.GateReason = fmt.Sprintf("too few symbols (%d/%d) — a fleet-wide claim from "+
			"a handful of names is a claim about those names", res.Symbols, cfg.MinSymbols)
	case res.Months < cfg.MinMonths:
		res.Gated = true
		res.GateReason = fmt.Sprintf("insufficient era coverage (%d/%d calendar months) — "+
			"a relationship confined to one period is a period artifact until it survives others",
			res.Months, cfg.MinMonths)
	}
	if res.Gated {
		res.Quintiles = []QuintileRow{}
		res.Verdict = "No verdict: " + res.GateReason
		return res
	}

	sent := field(ind, func(o Obs) float64 { return o.Sent })
	fwd := field(ind, func(o Obs) float64 { return o.Fwd })
	ret0 := field(ind, func(o Obs) float64 { return o.Ret0 })
	trail := field(ind, func(o Obs) float64 { return o.RetTrail })

	if v, ok := pearson(sent, fwd); ok {
		res.RawIC = &v
	}
	if v, ok := spearman(sent, fwd); ok {
		res.RawSpearman = &v
	}

	// The headline number: partial correlation controlling for price.
	if v, ok := partial(sent, fwd, ret0, trail); ok {
		res.PartialIC = &v
		if res.RawIC != nil && math.Abs(*res.RawIC) > 0.005 {
			share := 1 - math.Abs(v)/math.Abs(*res.RawIC)
			res.PriceExplained = &share
		}
		if cfg.Bootstrap > 0 {
			stats, ok := bootstrapPartialStats(ind, cfg.Bootstrap)
			if ok {
				lo95, hi95 := percentiles(stats, 0.05)
				res.PartialLo95, res.PartialHi95 = &lo95, &hi95
				lo, hi := percentiles(stats, 0.05/float64(family))
				res.PartialLo, res.PartialHi = &lo, &hi
			}
		}
	}

	// Hit rate against the best constant call. A sentiment sign is only a
	// prediction if it beats "always guess the majority direction".
	hits, n := 0, 0
	ups := 0
	for i := range ind {
		if sent[i] == 0 || fwd[i] == 0 {
			continue
		}
		n++
		if fwd[i] > 0 {
			ups++
		}
		if (sent[i] > 0) == (fwd[i] > 0) {
			hits++
		}
	}
	if n > 0 {
		hr := float64(hits) / float64(n)
		up := float64(ups) / float64(n)
		base := math.Max(up, 1-up) // the better constant call
		edge := hr - base
		res.HitRate, res.BaseRate, res.Edge = &hr, &base, &edge
	}

	res.Quintiles = quintiles(ind)
	if len(res.Quintiles) == 5 {
		gross := res.Quintiles[4].MeanFwd - res.Quintiles[0].MeanFwd
		// Long the top quintile, short the bottom: two positions, two sides
		// each, so four crossings of the cost.
		net := gross - 4*cfg.CostPerSide
		mono := true
		for i := 1; i < 5; i++ {
			if res.Quintiles[i].MeanFwd < res.Quintiles[i-1].MeanFwd {
				mono = false
				break
			}
		}
		res.SpreadGross, res.SpreadNet, res.Monotonic = &gross, &net, &mono

		// The portfolio pointing the same way as the measured IC. When the IC is
		// negative the signal is contrarian, and judging it by a long-top/
		// short-bottom book would condemn a position nobody would take.
		aligned := net
		res.AlignedSide = "long the most-positive-sentiment quintile, short the most-negative"
		if res.PartialIC != nil && *res.PartialIC < 0 {
			aligned = -gross - 4*cfg.CostPerSide
			res.AlignedSide = "CONTRARIAN: long the most-NEGATIVE-sentiment quintile, short the most-positive"
		}
		res.SpreadAligned = &aligned
	}

	res.Verdict = verdict(res)
	return res
}

// thinNonOverlapping keeps, per symbol, a maximal set of observations whose
// forward windows do not overlap: walk the symbol's sessions in order and take
// an observation only when it starts at or after the previous one's window end.
//
// This is the correction that matters most to the sample size. On daily
// sentiment with a 21-session horizon it removes ~95% of rows — and every one of
// those rows was re-reporting the same forward move.
func thinNonOverlapping(obs []Obs, horizon int) []Obs {
	bySym := map[int64][]Obs{}
	for _, o := range obs {
		bySym[o.SymbolID] = append(bySym[o.SymbolID], o)
	}
	// Deterministic symbol order so a bootstrap or a diff is reproducible.
	ids := make([]int64, 0, len(bySym))
	for id := range bySym {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]Obs, 0, len(obs))
	for _, id := range ids {
		rows := bySym[id]
		sort.Slice(rows, func(i, j int) bool { return rows[i].SessionIdx < rows[j].SessionIdx })
		next := math.MinInt32
		for _, o := range rows {
			if o.SessionIdx >= next {
				out = append(out, o)
				next = o.SessionIdx + horizon
			}
		}
	}
	return out
}

// quintiles buckets by sentiment and reports each bucket's mean forward return.
// Returns nil unless every bucket has at least 5 observations — five buckets
// from 20 rows is decoration, not evidence.
func quintiles(obs []Obs) []QuintileRow {
	if len(obs) < 25 {
		return []QuintileRow{}
	}
	s := append([]Obs(nil), obs...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Sent != s[j].Sent {
			return s[i].Sent < s[j].Sent
		}
		// Stable tiebreak: sentiment values repeat heavily (many headlines share
		// a lexicon score), and an unstable sort would make the buckets depend
		// on input order.
		if s[i].SymbolID != s[j].SymbolID {
			return s[i].SymbolID < s[j].SymbolID
		}
		return s[i].SessionIdx < s[j].SessionIdx
	})
	out := make([]QuintileRow, 0, 5)
	n := len(s)
	for q := 0; q < 5; q++ {
		lo := q * n / 5
		hi := (q + 1) * n / 5
		if hi <= lo {
			return []QuintileRow{}
		}
		var sumS, sumF float64
		for _, o := range s[lo:hi] {
			sumS += o.Sent
			sumF += o.Fwd
		}
		cnt := float64(hi - lo)
		out = append(out, QuintileRow{
			Quintile: q + 1, N: hi - lo,
			MeanSent: sumS / cnt, MeanFwd: sumF / cnt,
		})
	}
	return out
}

// verdict renders the conclusion from the measured numbers. Deliberately
// mechanical: the text cannot say something the metrics do not.
func verdict(r Result) string {
	if r.PartialIC == nil {
		return "No verdict: partial correlation not computable on this sample."
	}
	p := *r.PartialIC
	ciExcludesZero := r.PartialLo != nil && r.PartialHi != nil &&
		((*r.PartialLo > 0 && *r.PartialHi > 0) || (*r.PartialLo < 0 && *r.PartialHi < 0))

	lead := fmt.Sprintf("Partial IC %+.4f over %d independent observations "+
		"(%d symbols, %d months)", p, r.Obs, r.Symbols, r.Months)
	if r.PartialLo != nil {
		lead += fmt.Sprintf(", clustered %s CI [%+.4f, %+.4f]", r.CILevel, *r.PartialLo, *r.PartialHi)
	}
	lead += ". "

	if !ciExcludesZero {
		s := lead + "The interval includes zero: news sentiment shows NO measurable " +
			"forward information once same-session and trailing price moves are controlled for. "
		if r.RawIC != nil && math.Abs(*r.RawIC) > math.Abs(p)+0.01 {
			s += fmt.Sprintf("The raw correlation (%+.4f) is larger, which is the point — "+
				"it was reading price, not text. ", *r.RawIC)
		}
		if r.PartialLo95 != nil && r.PartialHi95 != nil &&
			((*r.PartialLo95 > 0) == (*r.PartialHi95 > 0)) {
			s += fmt.Sprintf("The NOMINAL 95%% interval [%+.4f, %+.4f] would have excluded zero, "+
				"but %d horizons were tested together and correcting for that is what keeps "+
				"'one of three horizons looked significant' from being reported as a finding. ",
				*r.PartialLo95, *r.PartialHi95, r.FamilySize)
		}
		return s + "This is a negative result, and it is a real one: it retires sentiment as a return predictor at this horizon."
	}

	direction := "sentiment predicts forward returns in the SAME direction"
	if p < 0 {
		direction = "the relationship is CONTRARIAN — positive headlines precede WEAKER forward returns"
	}
	s := lead + "The interval excludes zero, so sentiment carries information beyond price at " +
		"this horizon, and " + direction + ". "

	if r.SpreadAligned != nil {
		s += fmt.Sprintf("Trading it in the direction the evidence points (%s) returns %+.2f%% "+
			"per period net of cost", r.AlignedSide, 100**r.SpreadAligned)
		if *r.SpreadAligned <= 0 {
			s += " — statistically real and tradeable are different claims, and only the first holds here. "
		} else if r.Monotonic != nil && !*r.Monotonic {
			s += ", but quintile means are NOT monotonic, so the spread rests on the extremes rather than on an ordered relationship. That is the fragile kind of positive result. "
		} else {
			s += ", with monotonic quintile means. "
		}
	}
	if p < 0 {
		// Emitted from the SIGN, not written by hand, so a contrarian finding can
		// never be reported without it. "Buy the bad news" is the single strategy
		// shape this database's universe is least able to evaluate: the symbol
		// list is names tracked TODAY, so companies whose bad news kept getting
		// worse until they delisted are absent from every bucket they would have
		// dragged down.
		s += "SURVIVORSHIP WARNING: a contrarian result is the most bias-exposed kind " +
			"there is here. The universe is currently-tracked symbols, so names whose " +
			"negative headlines preceded delisting are missing entirely — exactly the " +
			"observations that would weaken a buy-the-bad-news bucket. Treat the aligned " +
			"spread as an upper bound, not an estimate. "
	}
	return s + "One era of coverage is not a validated edge: this needs to survive on history the " +
		"backfill has not reached yet before it is anything more than a lead."
}

// ciLabel names the interval the family correction produces.
func ciLabel(family int) string {
	if family <= 1 {
		return "95%"
	}
	return fmt.Sprintf("%.2f%% (Bonferroni over %d horizons)", 100*(1-0.05/float64(family)), family)
}

// ── statistics ───────────────────────────────────────────────────────────

func field(obs []Obs, f func(Obs) float64) []float64 {
	out := make([]float64, len(obs))
	for i, o := range obs {
		out[i] = f(o)
	}
	return out
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// pearson returns the correlation and whether it was computable (either series
// having zero variance makes it undefined, and returning 0 there would be a lie
// dressed as a measurement).
func pearson(x, y []float64) (float64, bool) {
	if len(x) != len(y) || len(x) < 3 {
		return 0, false
	}
	mx, my := mean(x), mean(y)
	var sxy, sxx, syy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx <= 0 || syy <= 0 {
		return 0, false
	}
	r := sxy / math.Sqrt(sxx*syy)
	if !isFinite(r) {
		return 0, false
	}
	return r, true
}

// spearman is pearson on ranks, with ties averaged.
func spearman(x, y []float64) (float64, bool) {
	if len(x) != len(y) || len(x) < 3 {
		return 0, false
	}
	return pearson(ranks(x), ranks(y))
}

func ranks(x []float64) []float64 {
	type pair struct {
		v float64
		i int
	}
	p := make([]pair, len(x))
	for i, v := range x {
		p[i] = pair{v, i}
	}
	sort.Slice(p, func(a, b int) bool {
		if p[a].v != p[b].v {
			return p[a].v < p[b].v
		}
		return p[a].i < p[b].i
	})
	out := make([]float64, len(x))
	for i := 0; i < len(p); {
		j := i
		for j+1 < len(p) && p[j+1].v == p[i].v {
			j++
		}
		// Average rank across the tie block. Sentiment scores tie constantly
		// (a lexicon has finitely many outputs), so ignoring ties would bias
		// the rank correlation.
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[p[k].i] = avg
		}
		i = j + 1
	}
	return out
}

// residualize returns y with the linear influence of the control columns
// removed, by OLS with an intercept solved through Gaussian elimination on the
// small normal-equations system (3x3 here — no matrix library needed).
func residualize(y []float64, controls ...[]float64) ([]float64, bool) {
	n := len(y)
	k := len(controls) + 1 // + intercept
	if n <= k+1 {
		return nil, false
	}
	// Design matrix rows: [1, c1, c2, ...]
	x := make([][]float64, n)
	for i := 0; i < n; i++ {
		row := make([]float64, k)
		row[0] = 1
		for j, c := range controls {
			if len(c) != n {
				return nil, false
			}
			row[j+1] = c[i]
		}
		x[i] = row
	}
	// Normal equations XtX b = Xty.
	xtx := make([][]float64, k)
	xty := make([]float64, k)
	for a := 0; a < k; a++ {
		xtx[a] = make([]float64, k)
		for b := 0; b < k; b++ {
			var s float64
			for i := 0; i < n; i++ {
				s += x[i][a] * x[i][b]
			}
			xtx[a][b] = s
		}
		var s float64
		for i := 0; i < n; i++ {
			s += x[i][a] * y[i]
		}
		xty[a] = s
	}
	b, ok := solve(xtx, xty)
	if !ok {
		return nil, false
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		fit := 0.0
		for a := 0; a < k; a++ {
			fit += b[a] * x[i][a]
		}
		out[i] = y[i] - fit
	}
	return out, true
}

// solve does Gaussian elimination with partial pivoting on a small system.
// Returns ok=false on a singular system (perfectly collinear controls), which
// must withhold the metric rather than return garbage.
func solve(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	m := make([][]float64, n)
	for i := range a {
		m[i] = append(append([]float64(nil), a[i]...), b[i])
	}
	for col := 0; col < n; col++ {
		piv, best := -1, 0.0
		for r := col; r < n; r++ {
			if v := math.Abs(m[r][col]); v > best {
				piv, best = r, v
			}
		}
		if piv < 0 || best < 1e-12 {
			return nil, false
		}
		m[col], m[piv] = m[piv], m[col]
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := m[r][col] / m[col][col]
			for c := col; c <= n; c++ {
				m[r][c] -= f * m[col][c]
			}
		}
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		if m[i][i] == 0 {
			return nil, false
		}
		out[i] = m[i][n] / m[i][i]
	}
	return out, true
}

// partial is the correlation of x and y after BOTH are residualised on the
// controls — the textbook partial correlation. Residualising only x would leave
// the controls' influence inside y and understate the removal.
func partial(x, y []float64, controls ...[]float64) (float64, bool) {
	rx, ok := residualize(x, controls...)
	if !ok {
		return 0, false
	}
	ry, ok := residualize(y, controls...)
	if !ok {
		return 0, false
	}
	return pearson(rx, ry)
}

// percentiles returns the alpha/2 and 1-alpha/2 quantiles of a SORTED-on-return
// statistic slice (it sorts a copy, so callers can reuse the slice for several
// confidence levels).
func percentiles(stats []float64, alpha float64) (lo, hi float64) {
	s := append([]float64(nil), stats...)
	sort.Float64s(s)
	loIdx := int(alpha / 2 * float64(len(s)))
	hiIdx := int((1 - alpha/2) * float64(len(s)-1))
	if loIdx < 0 {
		loIdx = 0
	}
	if hiIdx >= len(s) {
		hiIdx = len(s) - 1
	}
	return s[loIdx], s[hiIdx]
}

// bootstrapPartialStats resamples CALENDAR MONTHS with replacement and
// recomputes the partial IC on each resample, returning the raw statistic
// distribution so the caller can read several confidence levels off ONE
// bootstrap.
//
// Clustering by month, not by row: headlines are cross-sectionally correlated
// (one Fed day colours every name's news at once) and serially correlated
// within a symbol. A row-level bootstrap assumes away exactly the dependence
// that makes this data thinner than its row count suggests, and would report an
// interval several times too narrow.
func bootstrapPartialStats(obs []Obs, iters int) ([]float64, bool) {
	byMonth := map[string][]Obs{}
	for _, o := range obs {
		if len(o.Day) < 7 {
			continue
		}
		byMonth[o.Day[:7]] = append(byMonth[o.Day[:7]], o)
	}
	keys := make([]string, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) < 4 {
		return nil, false
	}

	rng := newLCG(0x5EED5)
	stats := make([]float64, 0, iters)
	for it := 0; it < iters; it++ {
		samp := make([]Obs, 0, len(obs))
		for j := 0; j < len(keys); j++ {
			samp = append(samp, byMonth[keys[rng.next(len(keys))]]...)
		}
		if len(samp) < 20 {
			continue
		}
		v, ok := partial(
			field(samp, func(o Obs) float64 { return o.Sent }),
			field(samp, func(o Obs) float64 { return o.Fwd }),
			field(samp, func(o Obs) float64 { return o.Ret0 }),
			field(samp, func(o Obs) float64 { return o.RetTrail }),
		)
		if ok {
			stats = append(stats, v)
		}
	}
	if len(stats) < 50 {
		return nil, false
	}
	return stats, true
}

// lcg is a fixed-seed linear congruential generator. Deterministic on purpose:
// the same data must produce the same confidence interval on every run, or a
// borderline result could be re-rolled until it looked significant.
type lcg struct{ s uint64 }

func newLCG(seed uint64) *lcg { return &lcg{s: seed} }

func (r *lcg) next(n int) int {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return int((r.s >> 33) % uint64(n))
}
