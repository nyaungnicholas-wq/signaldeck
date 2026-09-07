package papertrade

import (
	"fmt"
	"math"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── EXECUTION MODEL ─────────────────────────────────────────────────────────
//
// WHAT THIS REPLACES, AND WHY. The engine used to fill at a bar open with a
// flat per-side cost in basis points, and record only the price. Two things
// went wrong with that, both measured against the live database:
//
//   - AUDITABILITY. 21 of 44 stored fills do not reproduce from the bar they
//     name (max 90.3 bps, mean 11.8 bps). The bars table is written INSERT OR
//     REPLACE, so a provider revision rewrites the reference a past fill priced
//     off and nothing detects it. The fix is to make the recorded price BE the
//     stored bar's open, exactly, and to check the whole log against the bars on
//     every read (CheckFillFidelity) instead of trusting it.
//   - REALISM. The flat 7.5 bps assumption was smaller than the 11.8 bps average
//     by which the fills already differed from their own bars, and the book paid
//     no spread beyond it, no market impact, and had no capacity limit — an
//     unlimited order at the open, always. A paper book whose fills beat its own
//     cost model is a backtest with extra steps.
//
// The model now charges, per side:
//
//	cost = notional * (halfSpread + impact)
//	impact = ImpactCoef * sigma * sqrt(notional / ADV$)      [square-root law]
//	sigma  = ln(High/Low) / (2*sqrt(ln 2))                   [Parkinson, 1980]
//
// and caps a fill at MaxParticipation of the name's average daily dollar
// volume, filling SHORT (a partial fill) rather than pretending the rest
// executed. The price recorded is the stored bar's open; the execution cost is
// carried in Cost, so the trade log stays reconcilable against the bars while
// the cash math reflects what the fill really cost.
//
// WHAT IS STILL NOT MODELLED (say it, do not bury it): no order book, no queue
// position, no intrabar path — the reference price is the open regardless of
// when in the bar the order would have worked. A multi-bar liquidation is
// charged at the participation cap but its PRICE DRIFT across those bars is not
// modelled, so a forced exit from an illiquid name is still optimistic.

// MaxParticipation is the largest share of a name's average daily dollar volume
// one simulated fill may take. 5% is the conventional ceiling above which the
// square-root impact law itself stops being trustworthy — beyond it you are no
// longer taking liquidity, you are the market. Env-overridable.
func MaxParticipation() float64 {
	v := envFloat("SIGNALDECK_PAPER_MAX_PARTICIPATION", 0.05)
	if v <= 0 || v > 1 {
		return 0.05
	}
	return v
}

// ImpactCoef is the Y of the square-root impact law, impact = Y*sigma*sqrt(Q/V).
// Y is empirically near 1 across markets and horizons (Torre/BARRA; Almgren et
// al. 2005 report 0.9-1.1 for US equities), so 1.0 is the neutral choice rather
// than a tuned one. Env-overridable so the assumption stays explicit.
func ImpactCoef() float64 { return envFloat("SIGNALDECK_PAPER_IMPACT_COEF", 1.0) }

// ExecInputs is everything the execution model needs to price one side: the
// STORED bar the fill references, the market whose spread applies, and the
// name's average daily dollar volume.
//
// ADVUSD is required. Without it neither the impact nor the capacity of a fill
// can be computed, and the silent default for both — zero impact, infinite
// capacity — is the most optimistic possible answer. A fill with no liquidity
// estimate is WITHHELD, with a reason.
type ExecInputs struct {
	Bar    md.Bar
	Market md.Market
	ADVUSD float64
	// Live paper fills use the last completed bar for volatility. The fill
	// bar's high/low are not known at its open. Nil retains the explicit
	// same-bar assumption used by synthetic stress scenarios.
	VolatilityBar *md.Bar
}

// ImpactSigma keeps the EV decision and the charged fill on the same inputs.
func (in ExecInputs) ImpactSigma() (float64, bool) {
	if in.VolatilityBar != nil {
		if in.VolatilityBar.Ts >= in.Bar.Ts {
			return 0, false
		}
		return parkinsonSigma(*in.VolatilityBar)
	}
	return parkinsonSigma(in.Bar)
}

// Fill is the result of executing one transition at a bar open. It is a pure
// value: the caller applies it to the persisted book (cash/position/trade log).
//
// Px is the STORED BAR'S OPEN, unmodified, so a fill remains reconcilable
// against the bar it names for as long as both exist. The execution charge
// lives in Cost, decomposed into SpreadBps + ImpactBps so a reader can see what
// the fill was assumed to cost and why.
//
// When ok is false the zero-ish Fill still carries Reason, explaining what was
// missing. Callers must skip such a fill, never treat it as free.
type Fill struct {
	Side      string  // "buy" | "sell"
	Qty       float64 // units transacted (>0)
	Px        float64 // fill price = the stored bar's OPEN, exactly
	Cost      float64 // dollar execution cost charged on this fill (>=0)
	CashDelta float64 // signed change to cash after notional + cost
	Reason    string  // human-readable why (set by caller/worker, or the refusal)

	// Execution diagnostics — the audit trail for Cost.
	RefPx            float64 // the stored bar open Px was taken from
	SpreadBps        float64 // half-spread crossed, per side
	ImpactBps        float64 // modelled market impact of THIS size
	Participation    float64 // executed notional / ADVUSD (capped)
	Capped           bool    // the ADV participation limit bound this fill
	UnfilledNotional float64 // entry only: the part of the order that did not fill
	LiquidationBars  int     // exit only: bars needed at the participation cap
}

// parkinsonSigma estimates the bar's return volatility from its high/low range
// (Parkinson 1980): sigma = ln(H/L) / (2*sqrt(ln 2)). The caller supplies
// the completed prior bar for live paper fills. ok is false for an unusable range.
func parkinsonSigma(bar md.Bar) (float64, bool) {
	if bar.High <= 0 || bar.Low <= 0 || bar.High < bar.Low {
		return 0, false
	}
	const k = 1.6651092223153954 // 2*sqrt(ln 2)
	s := math.Log(bar.High/bar.Low) / k
	if s < 0 || math.IsNaN(s) || math.IsInf(s, 0) {
		return 0, false
	}
	return s, true
}

// validate checks the bar and the liquidity estimate, returning the reference
// price, the bar's volatility, the half-spread fraction and the one-bar
// notional cap. A refusal reason means the caller must not fill.
func (in ExecInputs) validate() (refPx, sigma, spreadFrac, capNotional float64, reason string) {
	if in.Bar.Open <= 0 || math.IsNaN(in.Bar.Open) || math.IsInf(in.Bar.Open, 0) {
		return 0, 0, 0, 0, "the stored bar has no usable open price"
	}
	s, ok := in.ImpactSigma()
	if !ok {
		return 0, 0, 0, 0, "the stored bar's high/low range is unusable, so market impact cannot be estimated"
	}
	if in.ADVUSD <= 0 || math.IsNaN(in.ADVUSD) || math.IsInf(in.ADVUSD, 0) {
		return 0, 0, 0, 0, "no average daily dollar volume for this symbol: impact and capacity are both unknowable, and assuming zero impact is the most optimistic answer available"
	}
	return in.Bar.Open, s, CostBpsFor(in.Market) / 1e4, MaxParticipation() * in.ADVUSD, ""
}

// impactFrac is the square-root-law impact of taking `notional` against ADVUSD,
// as a fraction of notional.
func impactFrac(notional, adv, sigma float64) float64 {
	if notional <= 0 || adv <= 0 {
		return 0
	}
	return ImpactCoef() * sigma * math.Sqrt(notional/adv)
}

// EnterLong sizes and prices a buy-to-open against the stored bar, deploying a
// target dollar BUDGET (inclusive of all execution cost) so multiple positions
// can coexist and the book never goes negative. budget must already be clamped
// to the cash on hand by the caller (see PositionBudget). It returns the fill
// and the resulting position quantity + average price.
//
// Sizing: the notional N must satisfy N*(1 + spread + impact(N)) = budget, and
// impact depends on N, so the equation is solved by fixed-point iteration from
// N = budget/(1+spread). The map is a contraction (impact grows like sqrt(N)),
// so a handful of passes converge to machine precision and the result is fully
// deterministic.
//
// If N would exceed MaxParticipation of the name's daily dollar volume the fill
// is CAPPED — a genuine partial fill. Qty is the capped size, UnfilledNotional
// reports what did not execute, and the leftover budget stays as cash.
//
// ok=false (with Reason set) for a non-positive budget, an unusable bar, or a
// missing dollar-volume estimate.
func EnterLong(budget float64, in ExecInputs) (Fill, float64, float64, bool) {
	refPx, sigma, spreadFrac, capNotional, reason := in.validate()
	if reason != "" {
		return Fill{Side: "buy", Reason: reason}, 0, 0, false
	}
	if budget <= 0 || math.IsNaN(budget) || math.IsInf(budget, 0) {
		return Fill{Side: "buy", Reason: "no cash slice to deploy"}, 0, 0, false
	}

	notional := budget / (1 + spreadFrac)
	for i := 0; i < 8; i++ {
		notional = budget / (1 + spreadFrac + impactFrac(notional, in.ADVUSD, sigma))
	}
	uncapped := notional
	capped := false
	if notional > capNotional {
		notional = capNotional
		capped = true
	}
	if notional <= 0 {
		return Fill{Side: "buy", Reason: "capacity at this participation limit rounds to zero"}, 0, 0, false
	}

	imp := impactFrac(notional, in.ADVUSD, sigma)
	cost := notional * (spreadFrac + imp)
	qty := notional / refPx
	if qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		return Fill{Side: "buy", Reason: "budget too small to buy any units at this price"}, 0, 0, false
	}

	f := Fill{
		Side:          "buy",
		Qty:           qty,
		Px:            refPx,
		Cost:          cost,
		CashDelta:     -(notional + cost),
		RefPx:         refPx,
		SpreadBps:     spreadFrac * 1e4,
		ImpactBps:     imp * 1e4,
		Participation: notional / in.ADVUSD,
		Capped:        capped,
	}
	if capped {
		f.UnfilledNotional = uncapped - notional
		f.Reason = fmt.Sprintf("partial fill: capped at %.1f%% of $%.0f average daily dollar volume",
			MaxParticipation()*100, in.ADVUSD)
	}
	return f, qty, refPx, true
}

// ExitLong prices a sell-to-close of the whole position against the stored bar.
// Cash received = notional - cost, where cost is the same half-spread plus
// square-root impact charged on entry.
//
// A position larger than one bar's participation cap cannot be exited in one
// bar. Rather than refuse (which would strand the book) or pretend (which is
// what the old model did), the fill is charged at exactly the participation
// cap — the best impact rate achievable — and LiquidationBars reports how many
// bars the exit would really take. The PRICE DRIFT across those bars is NOT
// modelled, so a forced exit from an illiquid name remains optimistic; that
// limitation is stated here rather than discovered later.
//
// ok=false (with Reason set) for a non-positive qty, an unusable bar, or a
// missing dollar-volume estimate.
func ExitLong(qty float64, in ExecInputs) (Fill, bool) {
	refPx, sigma, spreadFrac, capNotional, reason := in.validate()
	if reason != "" {
		return Fill{Side: "sell", Reason: reason}, false
	}
	if qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		return Fill{Side: "sell", Reason: "no position to close"}, false
	}

	notional := qty * refPx
	if math.IsNaN(notional) || math.IsInf(notional, 0) {
		return Fill{Side: "sell", Reason: "position notional is not finite"}, false
	}
	participation := notional / in.ADVUSD
	bars := 1
	capped := false
	if notional > capNotional {
		capped = true
		participation = MaxParticipation()
		bars = int(math.Ceil(notional / capNotional))
	}

	// Impact is charged at the achieved participation rate: a slice that has to
	// be spread over several bars pays the capped rate, not the rate its full
	// size would imply.
	imp := ImpactCoef() * sigma * math.Sqrt(participation)
	cost := notional * (spreadFrac + imp)

	f := Fill{
		Side:            "sell",
		Qty:             qty,
		Px:              refPx,
		Cost:            cost,
		CashDelta:       notional - cost,
		RefPx:           refPx,
		SpreadBps:       spreadFrac * 1e4,
		ImpactBps:       imp * 1e4,
		Participation:   participation,
		Capped:          capped,
		LiquidationBars: bars,
	}
	if capped {
		f.Reason = fmt.Sprintf("position is %.1fx one bar's capacity: modelled as a %d-bar liquidation at the %.1f%% participation cap (price drift across those bars is not modelled)",
			notional/capNotional, bars, MaxParticipation()*100)
	}
	return f, true
}

// ── FILL FIDELITY ───────────────────────────────────────────────────────────

// FillFidelityToleranceBps is how far a logged fill may sit from the bar open
// it claims and still count as reproducing it. It is a float round-trip
// tolerance (SQLite REAL), not a modelling allowance: fills are written AT the
// open, so anything above this means the bar was revised after the fact, or the
// fill never came from that bar.
const FillFidelityToleranceBps = 0.01

// FillVsBar pairs one logged fill with the stored bar it claims to have priced
// off. HasBar is false when no bar exists at that timestamp any more.
type FillVsBar struct {
	Px      float64
	BarOpen float64
	HasBar  bool
	// Unchecked means the bar LOOKUP failed — the store errored, so we do not
	// know whether a bar exists. Distinct from HasBar=false, which is the
	// positive finding that no bar is stored. Folding the two together reports
	// an outage as a data gap and sends the reader hunting missing bars that are
	// not missing.
	Unchecked bool
}

// Fidelity is the verdict on whether a trade log still reconciles against the
// bars it was built from. It exists because the alternative — trusting the log
// — is what let 21 unreproducible fills sit in a "track record" for weeks.
type Fidelity struct {
	Fills      int `json:"fills"`
	Matched    int `json:"matched"`
	Mismatched int `json:"mismatched"`
	NoBar      int `json:"noBar"`
	// Unchecked counts fills whose bar lookup ERRORED. It is reported apart from
	// NoBar on the same principle the empty-log case above rests on: "we could
	// not look" and "we looked and found nothing" are different statements, and
	// reporting the first as the second turns a store outage into a fabricated
	// finding about the book.
	Unchecked  int     `json:"unchecked"`
	MaxAbsBps  float64 `json:"maxAbsBps"`
	MeanAbsBps float64 `json:"meanAbsBps"`

	ToleranceBps float64 `json:"toleranceBps"`
	Verified     bool    `json:"verified"`
	Reason       string  `json:"reason,omitempty"`
}

// CheckFillFidelity compares every logged fill against the stored bar it names
// and reports whether the book still reconciles.
//
// An EMPTY log is not verified: "no fills yet" and "every fill checks out" are
// different statements, and reporting the first as the second is how a fresh
// book claims a clean audit it never had.
func CheckFillFidelity(pairs []FillVsBar) Fidelity {
	f := Fidelity{Fills: len(pairs), ToleranceBps: FillFidelityToleranceBps}
	if len(pairs) == 0 {
		f.Reason = "no simulated fills yet — nothing to reconcile against the stored bars"
		return f
	}
	var sumAbs float64
	var compared int
	for _, p := range pairs {
		if p.Unchecked {
			f.Unchecked++
			continue
		}
		if !p.HasBar || p.BarOpen <= 0 {
			f.NoBar++
			continue
		}
		bps := math.Abs(p.Px-p.BarOpen) / p.BarOpen * 1e4
		compared++
		sumAbs += bps
		if bps > f.MaxAbsBps {
			f.MaxAbsBps = bps
		}
		if bps <= FillFidelityToleranceBps {
			f.Matched++
		} else {
			f.Mismatched++
		}
	}
	if compared > 0 {
		f.MeanAbsBps = sumAbs / float64(compared)
	}
	f.Verified = f.Mismatched == 0 && f.NoBar == 0 && f.Unchecked == 0
	switch {
	case f.Verified:
		// nothing to explain
	case f.Mismatched == 0 && f.NoBar == 0:
		// Only lookups that failed. Nothing whatsoever was learned about the
		// book, so say that instead of borrowing the language of a finding.
		f.Reason = fmt.Sprintf(
			"%d of %d fills could not be checked: the bar lookup itself failed, so this is an unavailable store, NOT a finding about the book. Nothing here says the fills are wrong or right — re-run once the store is healthy",
			f.Unchecked, f.Fills)
	default:
		f.Reason = fmt.Sprintf(
			"%d of %d fills do not reproduce from the bar they name (worst %.1f bps, mean %.1f bps) and %d have no stored bar at all. Fills are written at the bar open, so a mismatch means the bar was revised after the fill (the bars table is written INSERT OR REPLACE) — the equity curve derived from those fills cannot be re-derived from the data now in the database",
			f.Mismatched, f.Fills, f.MaxAbsBps, f.MeanAbsBps, f.NoBar)
		if f.Unchecked > 0 {
			f.Reason += fmt.Sprintf(
				". Separately, %d fill(s) could not be checked at all because the bar lookup failed; that count is an unavailable store, not evidence about the book, and the counts above are drawn from the remainder",
				f.Unchecked)
		}
	}
	return f
}
