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
				"NEGATIVE mean forward 21d return (-0.76%, 95% CI [-1.19%, -0.32%], n=18,850 non-overlapping, " +
				"corrected 2026-07-26 from the originally registered -0.39% — see the AMENDMENT record in this " +
				"chain): high conviction means price is already far from its " +
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

// ProtocolKind is the chain kind under which the GRADING PROTOCOL itself is
// registered. Freezing the claims is only half the pre-registration: if the
// grader can change between now and 2026-08-07 — a looser refusal rule, a
// friendlier verdict boundary — the frozen claims get graded by a moving
// target and the commitment leaks out through the back door. So the grader's
// exact version and its decision rules are chained too, and a changed grader
// shows up as an AMENDMENT record instead of silently regrading history.
const ProtocolKind = "grading-protocol"

// GraderRel is the repo-relative path of the grading script the protocol pins.
const GraderRel = "tools/accuracy_registry.py"

// Protocol is the frozen description of HOW the 2026-08-07 grading will be
// decided, pinned to the exact grader version that will decide it.
type Protocol struct {
	// Grader is the repo-relative path of the grading script.
	Grader string `json:"grader"`
	// GraderCommit is the last commit that touched the grader at registration
	// time. Git history makes any later edit to the pinned version visible.
	GraderCommit string `json:"graderCommit"`
	// GraderSHA256 is the content hash of the grader file, computed at
	// REGISTRATION time and frozen into the chain — checkable without git. A
	// later edit to the grader produces a different digest, a different
	// protocol hash, and therefore an automatic AMENDMENT record.
	GraderSHA256 string `json:"graderSha256"`
	// MinIndependentN mirrors MIN_INDEPENDENT_N: below this many independent
	// observations no verdict is claimed either way.
	MinIndependentN int `json:"minIndependentN"`
	// MinDistinctDays mirrors MIN_DISTINCT_DAYS: below this many distinct UTC
	// days no interval is published for a horizon-1 directional row, and no
	// interval means no verdict.
	MinDistinctDays int `json:"minDistinctDays"`
	// MinDistinctBlocks mirrors MIN_DISTINCT_BLOCKS: the floor that actually
	// gates every STRUCTURAL verdict. Clustering 21-day-horizon calls on the
	// call DAY pseudo-replicates (21 consecutive call days share at least 17 of
	// their 21 forward sessions), so the grader clusters on non-overlapping
	// horizon blocks and refuses below this many of them. Registered here
	// because the chain, not the prose, governs where the two disagree.
	MinDistinctBlocks int `json:"minDistinctBlocks"`
	// ClusterUnit names the unit the block floor counts, per kind family.
	ClusterUnit string `json:"clusterUnit"`
	// MaxAlpha is the FAMILY-WISE error budget the published intervals spend,
	// before it is divided. Frozen here because a hard-coded z in the grader is
	// a coverage claim nobody registered: the registry publishes ~10 rows at
	// once and re-grades them daily, so a nominal 95% per row was never the
	// surface's operating error rate.
	MaxAlpha float64 `json:"maxAlpha"`
	// MultiplicityRule is the frozen divisor rule the grader must run. It is
	// compared BYTE-FOR-BYTE against MULTIPLICITY_RULE in the grader, so the
	// error rate cannot be re-derived after outcomes are visible. The rule is
	// strictly interval-widening: a bigger divisor can only lose verdicts.
	MultiplicityRule string `json:"multiplicityRule"`
	// Independence names the observation unit the counts above refer to.
	Independence string `json:"independence"`
	// Refusal is the rule for when the grader must say nothing at all.
	Refusal string `json:"refusal"`
	// VerdictMap states, before any outcome exists, which verdict each
	// possible live record maps to — so no outcome can be renamed afterwards.
	VerdictMap string `json:"verdictMap"`
}

// canonical mirrors Spec.canonical: fixed field order, byte-stable.
func (p Protocol) canonical() string {
	var b strings.Builder
	b.WriteString("grader=")
	b.WriteString(p.Grader)
	b.WriteString("|graderCommit=")
	b.WriteString(p.GraderCommit)
	b.WriteString("|graderSha256=")
	b.WriteString(p.GraderSHA256)
	b.WriteString("|minIndependentN=")
	b.WriteString(strconv.Itoa(p.MinIndependentN))
	b.WriteString("|minDistinctDays=")
	b.WriteString(strconv.Itoa(p.MinDistinctDays))
	b.WriteString("|minDistinctBlocks=")
	b.WriteString(strconv.Itoa(p.MinDistinctBlocks))
	b.WriteString("|clusterUnit=")
	b.WriteString(p.ClusterUnit)
	b.WriteString("|maxAlpha=")
	b.WriteString(strconv.FormatFloat(p.MaxAlpha, 'g', -1, 64))
	b.WriteString("|multiplicityRule=")
	b.WriteString(p.MultiplicityRule)
	b.WriteString("|independence=")
	b.WriteString(p.Independence)
	b.WriteString("|refusal=")
	b.WriteString(p.Refusal)
	b.WriteString("|verdictMap=")
	b.WriteString(p.VerdictMap)
	return b.String()
}

// Hash is the content hash of the protocol, stable across processes.
func (p Protocol) Hash() string {
	h := sha256.Sum256([]byte(p.canonical()))
	return hex.EncodeToString(h[:])
}

// RetireRuleKind is the chain kind under which the directional AUTO-RETIRE
// rule is registered. Registered 2026-07-26, while both live directional rows
// were still INSUFFICIENT (1d: 42.9% vs a 75.0% prequential null over 18 obs;
// 1w high conviction: 33.3% vs 83.3%) — negative skill either way, but
// unfalsifiable until the evidence floors are met. That is precisely when a
// kill criterion must be committed: written now it is a pre-registration,
// written after n=30/10 days it would be a reaction wearing one's clothes.
const RetireRuleKind = "auto-retire-rule"

// RetireRule is the frozen FAILED-forward kill criterion for the directional
// ensemble. Every field is enforceable: the registry grader applies the
// criterion, the model-health worker applies the action, and the chain proves
// neither was written after the data arrived.
type RetireRule struct {
	Model           string `json:"model"`
	MinIndependentN int    `json:"minIndependentN"`
	MinDistinctDays int    `json:"minDistinctDays"`
	Criterion       string `json:"criterion"`
	Action          string `json:"action"`
	Registered      string `json:"registered"`
}

// canonical mirrors Spec.canonical: fixed field order, byte-stable — and
// byte-identical to auto_retire_rule_digest() in tools/accuracy_registry.py,
// so the rule the grader enforces and the rule this chain froze are provably
// one rule. Tests on both sides pin the same digest constant.
func (r RetireRule) canonical() string {
	var b strings.Builder
	b.WriteString("model=")
	b.WriteString(r.Model)
	b.WriteString("|minIndependentN=")
	b.WriteString(strconv.Itoa(r.MinIndependentN))
	b.WriteString("|minDistinctDays=")
	b.WriteString(strconv.Itoa(r.MinDistinctDays))
	b.WriteString("|criterion=")
	b.WriteString(r.Criterion)
	b.WriteString("|action=")
	b.WriteString(r.Action)
	b.WriteString("|registered=")
	b.WriteString(r.Registered)
	return b.String()
}

// Hash is the content hash of the rule, stable across processes and languages.
func (r RetireRule) Hash() string {
	h := sha256.Sum256([]byte(r.canonical()))
	return hex.EncodeToString(h[:])
}

// AutoRetireRule is the frozen rule. The strings must stay byte-identical to
// AUTO_RETIRE_CRITERION / AUTO_RETIRE_ACTION in tools/accuracy_registry.py;
// changing either side changes its digest, breaks the pinned-digest tests, and
// appends an AMENDMENT on the chain — which is the visibility the freeze buys.
func AutoRetireRule() RetireRule {
	return RetireRule{
		Model:           "directional-ensemble",
		MinIndependentN: 30,
		MinDistinctDays: 10,
		Criterion: "The first time a directional row reaches 30 independent (symbol, horizon, " +
			"UTC-day) observations spread over 10 distinct UTC days, if the upper bound of its " +
			"effective-N day-clustered Wilson 95% interval is below the prequential-majority null, " +
			"the verdict is FAILED and the row carries retire=true. No grace period, no re-window, " +
			"no threshold revision after the evidence arrives.",
		Action: "The daemon's model-health worker reads retire from data/accuracy_registry.json " +
			"and stops publishing the flagged horizon's predictions. The flag is recomputed on " +
			"every grade under these same frozen thresholds; only a record whose interval clears " +
			"the null lifts it.",
		Registered: "2026-07-26",
	}
}

// GradingProtocol is the frozen protocol for the first structural grading.
// Registered 2026-07-26, twelve days before the first forecast can resolve.
//
// graderSHA256 and graderCommit are both measured by the caller at
// registration time rather than hard-coded here, because the registrar re-runs
// every 12h: a grader edited after registration is re-digested, its protocol
// hash changes, and the chain gains an AMENDMENT record automatically — the
// exact discipline the claim specs already follow. The commit is measured too
// because a constant drifts: it named 04395a2 long after the file had moved on
// to 82ef494, so the chain pinned a version whose digest it had never taken.
// The registrar refuses to register at all when the grader has uncommitted
// changes, since a working-tree digest names a version that exists nowhere but
// one machine's disk.
func GradingProtocol(graderSHA256, graderCommit string) Protocol {
	return Protocol{
		Grader:            GraderRel,
		GraderCommit:      graderCommit,
		GraderSHA256:      graderSHA256,
		MinIndependentN:   30,
		MinDistinctDays:   10,
		MinDistinctBlocks: 10,
		MaxAlpha:          MaxAlpha,
		MultiplicityRule:  MultiplicityRule,
		ClusterUnit: "non-overlapping horizon blocks (anchored at the first call day) for structural " +
			"kinds; UTC day for horizon-1 directional kinds",
		Independence: "One observation per (symbol, horizon, cluster unit). For STRUCTURAL kinds the " +
			"cluster unit is the non-overlapping horizon BLOCK (call day // horizon_days, anchored at " +
			"the first call day), not the call day: at horizon 21 any 21 consecutive call days share at " +
			"least 17 of their forward sessions, so day-clustering still pseudo-replicates. For " +
			"horizon-1 directional kinds a block IS a UTC day, so those rows stay day-clustered. Either " +
			"way, 408 forecasts resolving in one cluster are ONE market observation, however many " +
			"symbols they cover, and the 95% interval is Wilson on the effective sample, matching " +
			"clusterstat in the Go daemon.",
		Refusal: "Fewer than 30 independent observations: INSUFFICIENT, no verdict claimed either way. " +
			"Fewer than 10 distinct NON-OVERLAPPING HORIZON BLOCKS (MIN_DISTINCT_BLOCKS, which for " +
			"horizon-1 directional rows is 10 distinct UTC days): no interval is published at all, and " +
			"no interval means no verdict — reading a verdict off the point estimate there is exactly " +
			"the failure the interval discipline exists to prevent. The block floor is the STRICTER " +
			"gate: it takes 10 x horizon_days of calls to clear, not 10 days.",
		VerdictMap: "Structural kinds are graded against the claim FROZEN in this chain, not against " +
			"whatever the code says on grading day. With [lo,hi] the day-clustered 95% interval on live " +
			"accuracy and C the frozen band claim: hi < C-0.05 maps to DECAYED (the advertised table is " +
			"retired from display and the failure publishes unfiltered); lo >= C-0.05 maps to HOLDING " +
			"(the claim may keep rendering, now citing the live record); anything else maps to WIDE " +
			"(still experimental, no promotion). Every verdict, including DECAYED, ships to the public " +
			"anchor repo — a pre-registered negative is publishable evidence; a post-hoc one is not.",
	}
}

// MaxAlpha is the family-wise error budget every published registry interval
// spends. It is divided, never spent per row: see MultiplicityRule.
const MaxAlpha = 0.05

// MultiplicityRule must stay BYTE-IDENTICAL to MULTIPLICITY_RULE in
// tools/accuracy_registry.py. The grader compares the chained string to its own
// and refuses to grade on any difference, so the rule pricing the published
// error rate cannot drift away from the rule that was registered.
const MultiplicityRule = "Published intervals are Bonferroni-corrected for both multiplicities this " +
	"surface pays: FAMILY (rows published in the same grading cycle) and LOOKS " +
	"(grading cycles taken over the same accruing rows). " +
	"divisor = family_size * looks; corrected_alpha = maxAlpha / divisor; " +
	"z = probit(1 - corrected_alpha/2), floored at the uncorrected two-sided z " +
	"so the correction can only ever WIDEN an interval, never narrow one. " +
	"family_size is the number of rows the cycle actually publishes, measured " +
	"from the rows themselves, then folded with max() against the largest " +
	"family ever published on the grading-look chain and the family the last " +
	"published registry JSON declared, so publishing fewer rows can never " +
	"refund multiplicity a wider family already spent. " +
	"looks is the monotone count of 'grading-look' " +
	"records on the pre-registration chain, folded with max() over the record " +
	"count, the counters those records carry, and the looks already published " +
	"in the registry JSON, so neither log rotation nor a re-cut snapshot can " +
	"refund a look already taken."

// LookKind is the chain kind under which each GRADING LOOK is recorded.
//
// The registry is re-graded daily by ops/com.signaldeck.accuracy.plist over the
// same accruing rows. Re-testing an accruing sample and reading the verdict off
// whichever look crosses the bar is optional stopping, and the only defence is
// to charge for every look taken — which requires counting them somewhere that
// cannot be rewound. The chain is append-only and pruned by nothing, so the
// count lives here rather than in a log or a mutable meta cell, exactly as
// ResearchLoop's durable search count does for the discovery grid.
const LookKind = "grading-look"

// Look is one grading cycle, charged whether or not it changed a verdict.
// Counting only the cycles that moved something would make a null grade a free
// look, which is the very thing the counter exists to price.
type Look struct {
	// Counter is the monotone look number, one higher than the highest already
	// on the chain. Carried IN the record as well as implied by the record
	// count so a truncated chain still cannot refund looks already taken.
	Counter int `json:"counter"`
	// GradedAt is the graded_at stamp of the registry this look observed. It is
	// the de-duplication key: a registrar pass that sees no new grade appends
	// nothing, so the counter tracks GRADES, not registrar wakeups.
	GradedAt string `json:"gradedAt"`
	// Registry is the artifact the look was read from.
	Registry string `json:"registry"`
	// Family is the family_size the observed grade published. It rides the
	// record for the same reason Counter does: the family term of the divisor
	// is monotone, so the widest family already charged has to survive a
	// truncated chain or a re-cut snapshot. Zero means a record written before
	// the family was carried, which can only fail to raise the floor.
	Family int `json:"family"`
}

func (l Look) canonical() string {
	var b strings.Builder
	b.WriteString("counter=")
	b.WriteString(strconv.Itoa(l.Counter))
	b.WriteString("|gradedAt=")
	b.WriteString(l.GradedAt)
	b.WriteString("|registry=")
	b.WriteString(l.Registry)
	b.WriteString("|family=")
	b.WriteString(strconv.Itoa(l.Family))
	return b.String()
}

// Hash is the content hash of the look record.
func (l Look) Hash() string {
	h := sha256.Sum256([]byte(l.canonical()))
	return hex.EncodeToString(h[:])
}

// RegistryRel is the repo-relative path of the graded registry artifact whose
// graded_at stamp tells the registrar a new look was taken.
const RegistryRel = "data/accuracy_registry.json"

// NullQuarantineKind is the chain kind under which the FROZEN unmatched-null
// quarantine manifest is registered.
//
// A silent INSERT-OR-IGNORE no-op in the outcome freeze path wrote 1,157
// post-amendment structural rows with no naive-persistence baseline before the
// write-path guard existed. Those rows cannot be repaired — a persistence label
// computed after the outcome is known is hindsight, not a null — and they cannot
// be waved through either, because the unmatched-null invariant is what makes a
// FAILING structural verdict reachable at all.
//
// So the affected set is frozen exactly as it stood and its digest rides this
// chain. That is the only thing that makes the exemption honest: the exempt rows
// become an externally fixed, publicly countable fact, so any later attempt to
// extend the set re-digests, breaks verification in the daemon, and appears here
// as an AMENDMENT rather than as a quietly larger loophole.
//
// The record raises no number. The quarantined rows keep their NULL baseline,
// keep grading as NO BASELINE, and stay out of every structural denominator.
const NullQuarantineKind = "null-quarantine-manifest"

// NullQuarantine is the frozen manifest as registered on the chain.
type NullQuarantine struct {
	// Digest is the store's sha256 over the canonical membership rendering.
	Digest string `json:"digest"`
	// NRows is how many outcome rows are exempt — the publicly countable number.
	NRows int `json:"nRows"`
	// Epoch is the null-amendment instant the exemption is scoped to.
	Epoch int64 `json:"epoch"`
	// Scope states, in words a reader can check against the code, what the
	// exemption does and does not do.
	Scope string `json:"scope"`
	// Growable is registered explicitly and is always false, so a future change
	// to that answer is a visible amendment rather than a silent policy drift.
	Growable bool `json:"growable"`
}

// canonical mirrors Spec.canonical: fixed field order, byte-stable.
func (q NullQuarantine) canonical() string {
	var b strings.Builder
	b.WriteString("digest=")
	b.WriteString(q.Digest)
	b.WriteString("|nRows=")
	b.WriteString(strconv.Itoa(q.NRows))
	b.WriteString("|epoch=")
	b.WriteString(strconv.FormatInt(q.Epoch, 10))
	b.WriteString("|scope=")
	b.WriteString(q.Scope)
	b.WriteString("|growable=")
	b.WriteString(strconv.FormatBool(q.Growable))
	return b.String()
}

// Hash is the content hash of the manifest record.
func (q NullQuarantine) Hash() string {
	h := sha256.Sum256([]byte(q.canonical()))
	return hex.EncodeToString(h[:])
}

// NullQuarantineScope is the frozen prose describing the exemption. It is a
// constant so the registrar cannot vary it per run, and so changing it is an
// amendment.
const NullQuarantineScope = "These outcome rows predate the write-path guard that requires a frozen " +
	"naive-persistence baseline, and were produced by a silent INSERT-OR-IGNORE no-op in the freeze " +
	"path. They are NOT relabelled and NOT backfilled: a persistence label computed after the outcome " +
	"is known would be a hindsight baseline. They keep a NULL naive_label, grade as NO BASELINE, and " +
	"are excluded from every structural benchmark denominator, so the exemption cannot raise any " +
	"published accuracy. It excuses them from ONE thing only: the startup invariant that every " +
	"post-amendment structural row carries a matched null. The set is frozen at this digest and is " +
	"not growable — every row written after the freeze must satisfy the guard, and any change to the " +
	"exempt set fails digest verification in the daemon and amends this chain."

// NullQuarantineRecord builds the chain payload for a frozen manifest.
func NullQuarantineRecord(digest string, nRows int, epoch int64) NullQuarantine {
	return NullQuarantine{
		Digest: digest, NRows: nRows, Epoch: epoch,
		Scope: NullQuarantineScope, Growable: false,
	}
}
