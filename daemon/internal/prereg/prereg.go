// Package prereg freezes what each structural predictor CLAIMS, before any of
// its forecasts resolve.
//
// # Why this exists
//
// Six structural predictors are carrying thousands of outstanding forecasts and
// none of them has a single resolved observation yet. Their advertised accuracy
// numbers — trend21 at 97.2% in the top band, vol21 at 72.0%, and so on — are
// backtest results. Nothing stops those numbers from being edited after the
// live record starts arriving, and nothing would prove they hadn't been. A
// claim that can be revised once its outcomes are visible is not a prediction;
// it is a description written afterwards.
//
// So the claims are recorded here, hashed, chained, and timestamped BEFORE the
// evidence exists. After the first grade lands on 2026-08-07 the comparison is
// no longer "does the live record roughly match what the code says today" but
// "does the live record match what was committed to on a specific date, with a
// hash that proves nothing moved". That is the difference between a measurement
// and a story, and it is the entire reason the platform's other honesty
// machinery exists.
//
// # Why it is chained
//
// A single stored record could be replaced wholesale. Each record links to the
// previous one by hash, exactly like the prediction ledger: rewriting an old
// claim breaks every link after it, and the break is detectable by recomputation
// rather than by trust. The chain is the proof; the timestamp alone is not.
//
// # What this package deliberately does NOT do
//
// It does not re-derive the accuracy numbers, and it does not judge them. It
// copies what the predictor code says today and freezes it. If a number here is
// wrong, the live record will say so on its own — which is the point.
package prereg

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Band is one conviction band's claim: the floor conviction and the accuracy
// the predictor advertises for calls in that band.
type Band struct {
	MinConviction float64 `json:"minConviction"`
	Claimed       float64 `json:"claimedAccuracy"`
}

// Spec is the frozen, falsifiable claim for ONE predictor.
//
// Every field is something the live record can contradict. Prose that cannot be
// contradicted does not belong here — it belongs in the API payload, where it
// already is.
type Spec struct {
	Kind string `json:"kind"`
	// Question is the exact yes/no the predictor answers, stated so a reader
	// can check the resolution rule actually answers it.
	Question string `json:"question"`
	// HorizonDays is the forward window in TRADING sessions.
	HorizonDays int `json:"horizonDays"`
	// Resolution is the rule that decides correct/incorrect, named precisely
	// enough to be re-implemented from this text alone.
	Resolution string `json:"resolution"`
	// Bands are the per-conviction claims, ascending by floor.
	Bands []Band `json:"bands"`
	// Baseline names what the accuracy must beat to mean anything. For a
	// persistence-style call the honest null is "the regime simply continued",
	// NOT 50% — several of these predictors score high precisely because the
	// underlying state is sticky.
	Baseline string `json:"baseline"`
	// KnownWeakness is the caveat already measured and published for this
	// predictor. Recorded so it cannot quietly disappear once results arrive.
	KnownWeakness string `json:"knownWeakness"`
}

// canonical renders a Spec byte-stably. Field order is fixed and floats use a
// single formatting rule, because a hash over encoding/json output would depend
// on map iteration and struct-tag details rather than on the content.
func (s Spec) canonical() string {
	var b strings.Builder
	b.WriteString("kind=")
	b.WriteString(s.Kind)
	b.WriteString("|question=")
	b.WriteString(s.Question)
	b.WriteString("|horizonDays=")
	b.WriteString(strconv.Itoa(s.HorizonDays))
	b.WriteString("|resolution=")
	b.WriteString(s.Resolution)
	b.WriteString("|baseline=")
	b.WriteString(s.Baseline)
	b.WriteString("|knownWeakness=")
	b.WriteString(s.KnownWeakness)
	b.WriteString("|bands=")
	for i, band := range s.Bands {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(band.MinConviction, 'g', -1, 64))
		b.WriteByte(':')
		b.WriteString(strconv.FormatFloat(band.Claimed, 'g', -1, 64))
	}
	return b.String()
}

// Hash is the content hash of one spec — stable across processes and machines.
func (s Spec) Hash() string {
	h := sha256.Sum256([]byte(s.canonical()))
	return hex.EncodeToString(h[:])
}

// Record is one pre-registration entry in the chain.
type Record struct {
	Seq       int64  `json:"seq"`
	Ts        int64  `json:"ts"`
	Kind      string `json:"kind"`
	SpecJSON  string `json:"specJson"`
	SpecHash  string `json:"specHash"`
	PrevHash  string `json:"prevHash"`
	EntryHash string `json:"entryHash"`
	// Note carries why this record was written (initial registration, or an
	// AMENDMENT — amendments are appended, never overwritten, so a changed
	// claim is visible as a change rather than as the truth).
	Note string `json:"note"`
}

// HashEntry computes entry_hash = sha256(prev_hash ‖ payload), matching the
// prediction ledger's construction so both chains verify the same way.
func HashEntry(prevHash string, r Record) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte("\x1e")) // record separator, as in the prediction ledger
	h.Write([]byte("ts=" + strconv.FormatInt(r.Ts, 10) +
		"|kind=" + r.Kind + "|specHash=" + r.SpecHash + "|note=" + r.Note))
	return hex.EncodeToString(h.Sum(nil))
}

// Specs is the frozen claim set for every structural predictor with outstanding
// forecasts. The numbers mirror structregime.accuracyFor exactly; a test asserts
// that, so the two cannot drift apart silently.
//
// FirstGradableOn is the date the earliest of these can produce a graded
// verdict. Before it, every number below is a backtest claim and the platform
// says so everywhere it displays one.
const FirstGradableOn = "2026-08-07"

func Specs() []Spec {
	return []Spec{
		{
			Kind:        "trend21",
			Question:    "Will the stock still be on its current side of its 200-day moving average in 21 trading sessions?",
			HorizonDays: 21,
			Resolution: "Compare the close 21 sessions after the call against the 200-day SMA computed at that " +
				"later bar. Correct when the side (above/below) matches the call. Ties and degenerate windows " +
				"are NOT graded rather than guessed.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.731},
				{MinConviction: 0.5, Claimed: 0.900},
				{MinConviction: 0.8, Claimed: 0.946},
				{MinConviction: 0.9, Claimed: 0.972},
			},
			Baseline: "Persistence — the share of calls whose side simply did not change. This is NOT 50%: " +
				"SMA200 state is sticky, and any accuracy at or below persistence means the predictor adds nothing.",
			KnownWeakness: "The 2026-07-24 re-validation measured the most ACCURATE band (>=0.9) as carrying a " +
				"NEGATIVE mean forward 21d return (-0.39%): high conviction means price is already far from its " +
				"average, and extended names mean-revert. Accuracy and return are different quantities here. " +
				"Survivorship: the universe is currently-tracked stocks, so downtrend persistence into delisting " +
				"is unobserved.",
		},
		{
			Kind:        "vol21",
			Question:    "Will realized volatility over the next 21 sessions sit in the upper (elevated) or lower (calm) half of its recent range?",
			HorizonDays: 21,
			Resolution: "Compute realized volatility over the 21 sessions after the call and compare it to the " +
				"trailing median of the same measure. Correct when the half matches the call. A tied or NaN " +
				"median is not graded.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.558},
				{MinConviction: 0.5, Claimed: 0.643},
				{MinConviction: 0.8, Claimed: 0.668},
				{MinConviction: 0.9, Claimed: 0.720},
			},
			Baseline: "Persistence of the current volatility regime. Volatility clusters, so a naive 'it stays " +
				"where it is' rule already scores well above 50% and is the number to beat.",
			KnownWeakness: "This is the platform's most re-tested claim and the only one the options surface is " +
				"allowed to build on — but forward RETURN was never measured for this kind, so no return number " +
				"is quoted. A regime call is situational awareness with a hit rate, not a trade.",
		},
		{
			Kind:        "liquidity21",
			Question:    "Will average daily dollar volume over the next 21 sessions be above (active) or below (quiet) its trailing 200-day median?",
			HorizonDays: 21,
			Resolution: "Average dollar volume over the 21 sessions after the call, compared against the " +
				"trailing-200-day median at the call bar. Correct when the side matches.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.595},
				{MinConviction: 0.5, Claimed: 0.739},
				{MinConviction: 0.8, Claimed: 0.801},
				{MinConviction: 0.9, Claimed: 0.876},
			},
			Baseline: "Naive persistence, which scores THE SAME 0.876 in the top band. The skill measured here " +
				"IS liquidity persistence; the accuracy claim holds and a novelty claim would not.",
			KnownWeakness: "Labels are imbalanced by secular volume drift (majority class 56-61% by tier), so the " +
				"headline accuracy overstates how much is being decided.",
		},
		{
			Kind:        "trend63",
			Question:    "Will the stock still be on its current side of its 200-day moving average in 63 trading sessions?",
			HorizonDays: 63,
			Resolution: "Identical to trend21 with a 63-session forward window.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.700},
				{MinConviction: 0.5, Claimed: 0.777},
				{MinConviction: 0.8, Claimed: 0.817},
				{MinConviction: 0.9, Claimed: 0.837},
			},
			Baseline: "Persistence over a quarter — weaker than at 21 sessions, but still far above 50%.",
			KnownWeakness: "These are CUMULATIVE tiers (accuracy of all calls at or above the floor), not the " +
				"per-band decompositions the 21d kinds report, because the 63d loop did not record band shares — " +
				"so a call at the bottom of its band is slightly overstated by its tier number. The " +
				"accuracy/return inversion is SHARPER here: the >=0.9 band measured -1.30% mean forward 63d return.",
		},
		{
			Kind:        "trend21-crypto",
			Question:    "Will the crypto symbol still be on its current side of its 200-day average in 21 sessions?",
			HorizonDays: 21,
			Resolution:  "Identical arithmetic to trend21; only the accuracy table differs, having been measured on crypto.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.934},
				{MinConviction: 0.5, Claimed: 0.985},
			},
			Baseline: "Persistence. The sample is bear-dominated (83% of sampled points below SMA200), so " +
				"persistence is unusually easy to score on.",
			KnownWeakness: "Only ~2 years of history and 4 quarterly clusters back these intervals, against ~900 " +
				"stocks for the equity tables. A genuine bull-flip stress test is ABSENT from the sample.",
		},
		{
			Kind:        "liquidity21-crypto",
			Question:    "Will the crypto symbol's average daily dollar volume over the next 21 sessions be above or below its trailing 200-day median?",
			HorizonDays: 21,
			Resolution:  "Identical arithmetic to liquidity21; accuracy measured on crypto.",
			Bands: []Band{
				{MinConviction: 0.0, Claimed: 0.795},
				{MinConviction: 0.5, Claimed: 0.912},
				{MinConviction: 0.8, Claimed: 0.935},
				{MinConviction: 0.9, Claimed: 0.964},
			},
			Baseline: "Naive persistence scores 0.783 — the predictor agrees with it on 98% of samples.",
			KnownWeakness: "~2 years / 6 quarterly clusters only. The crypto VOL regime did NOT replicate and is " +
				"deliberately not served, which is the honest comparison point for these numbers.",
		},
	}
}

// SpecFor returns the frozen spec for a kind.
func SpecFor(kind string) (Spec, bool) {
	for _, s := range Specs() {
		if s.Kind == kind {
			return s, true
		}
	}
	return Spec{}, false
}
