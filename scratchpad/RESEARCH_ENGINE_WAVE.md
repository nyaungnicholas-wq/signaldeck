# RESEARCH DISCOVERY ENGINE WAVE — locked build spec (2026-07-16)

Turns the Bayesian Research Ledger from a live-only validator (blocked at 2
independent market-weeks) into a true quantitative research discovery system:
2020→2026 historical backfill, extension-conditioned hypothesis testing
(H002-R1), interaction/counterfactual analysis, regime-survival testing,
leakage sentinel, hypothesis decay tracking, auto-discovery, evidence graph.

Module: `github.com/nyaungnicholas-wq/signaldeck`, daemon root
`/Users/natalienyaung/claude code/signaldeck/daemon`.

HOUSE RULES (non-negotiable):
- Pure packages: no I/O, no clock, no RNG. Workers get injectable `Now func() time.Time`.
- Honesty: absent data = absent key / honest empty state, never fabricated 0.
  Backtest evidence is labeled backtest. Survivorship bias is penalized, not hidden.
- Store: writes via `s.w`, reads via `s.db`. Tests: `store.Open(filepath.Join(t.TempDir(),"x.db"))`.
- Each agent touches ONLY the files it owns below. Shared files (schema.sql,
  store.go, run.go, api.go) are handled by the coordinator, NOT by agents.
- Comments: only constraints code can't show. Match surrounding density/idiom.
- `gofmt` clean; `go vet ./...` clean; every exported symbol documented.

## Existing facts you build against (verified)

- `signals.ComputeScores(daily, nil, signals.MicroInputs{})[md.H1w]` is PURE:
  returns `md.Score{Score float64, Components []md.ScoreComponent{Name, Value,
  Norm, Weight, Contrib}}`. Feature keys are `"pressure_score"` = Score and
  `"comp_"+Name` = Contrib. 1w weights: trend_sma .45, momentum_roc .25 (n=20,
  scale .05), rsi .10, macd .15, rvol_confirm .05; missing components drop and
  weights renormalize over survivors. Trend needs SMA200 → pass the trailing
  ≤500 bars ending at the anchor (live uses LastBars(TF1d,500)).
- Outcome geometry (must replicate EXACTLY): anchor = the daily bar itself;
  target = anchor.Ts + 7*86400; fwd = first daily bar with ts >= target; VOID
  (skip) if none or fwd.Ts - target > 3*7*86400; fwd_return =
  fwd.Close/anchor.Close - 1; up = fwd_return > 0 (strictly).
- `md` = internal/marketdata: `md.Bar{Ts,Open,High,Low,Close,Volume}` (+SymbolID,
  Tf on store reads), `md.Horizon` ("1h","1d","1w"), `md.TF1d`.
- researchledger (rl): see internal/researchledger/researchledger.go. Week-trial
  discipline: ONE Bernoulli trial per calendar week (week = ts/604800); win =
  week's cross-sectional win rate strictly beats max(upRate, 1-upRate);
  BF = rl.BayesFactorAbove(k, n, 0.5, rl.WeekTrialMaxEdge).
- Store researchledger fns exist: UpsertLedgerHypothesis, InsertLedgerEvidence,
  UpdateLedgerDerived, LedgerHypotheses, LedgerEvidence, LedgerEvidenceMaxWindow.
- VIX: `store.MacroSeries(ctx, "VIXCLS", 0)` → full history ([]MacroPoint{Series,Ts,Value}, oldest-first).
- Alpaca deep fetch: `alpaca.Client.BackfillDailyMulti(ctx, st, symbols []string,
  resolve func(sym string)(int64,bool), start time.Time) (map[string]int, error)`
  — feed=sip, adjustment=split, batches of 100, self-paced.
- Universe: `st.ActiveStockSymbols(ctx, streamed *bool)` (nil = all active stocks).
- `regime` labels not needed here; market regime comes from SPY features below.
- macrofeat: `macrofeat.FromVIX(v float64) Features` with `.Map()` giving
  vix_level, vix_regime, vix_high_vol (>=25 ⇒ 1).

## Eras (fixed calendar, exported from histfeat)

| Era const | Range (UTC dates, inclusive) |
|---|---|
| EraPreCovid    "pre_covid"       | ..2020-02-14 |
| EraCovidCrash  "covid_crash"     | 2020-02-15..2020-06-30 |
| EraBull2021    "bull_2020_21"    | 2020-07-01..2021-12-31 |
| EraBear2022    "bear_2022"       | 2022-01-01..2022-12-31 |
| EraAIRally     "ai_rally_2023_25"| 2023-01-01..2025-12-31 |
| EraY2026       "y2026"           | 2026-01-01.. |

`func EraOf(ts int64) string`. Order helper: `func EraOrder() []string` (chronological, excluding pre_covid).

---

## AGENT H — internal/histfeat (NEW package; owns internal/histfeat/*)

Files: `histfeat.go`, `histfeat_test.go`. Pure (imports: math, sort,
internal/marketdata, internal/signals, internal/macrofeat only).

```go
const WeekSecs = 7 * 86400

type Point struct { Ts int64; Value float64 }        // adapter for macro series

type WeekRow struct {
    Ts        int64              // anchor daily-bar ts (last bar of its week bucket)
    Week      int64              // Ts / WeekSecs
    Vec       map[string]float64
    FwdReturn float64
    Up        bool
    Era       string
    HighVol   bool               // vix_high_vol == 1 at anchor
}

type MarketCtx struct { /* opaque; built once, shared across symbols */ }

// BuildMarketCtx precomputes per-week market state from SPY daily bars and the
// VIX series: mkt_trend (+1 bull / -1 bear / 0 sideways: close vs SMA200 and
// SMA50 vs SMA200), mkt_ret_13w (SPY 13-week return), vix_* via macrofeat
// (VIX value = latest obs at-or-before the anchor ts; absent -> vix keys omitted).
func BuildMarketCtx(spyDaily []md.Bar, vix []Point) MarketCtx

// WeeklyRows computes point-in-time weekly research rows for one symbol.
// daily must be ascending TF1d bars. rowsFrom skips anchors before it.
// An anchor is the LAST bar of each calendar-week bucket (ts/WeekSecs).
// Features use ONLY bars[..anchor] (trailing window capped at 500 bars, matching
// live LastBars(TF1d,500)). Anchors with <60 trailing bars are skipped entirely;
// pressure components drop/renormalize per signals semantics beyond that.
// Label per the exact outcome geometry (see spec header); unlabelable anchors
// (no forward bar within 21d of target) are skipped.
func WeeklyRows(daily []md.Bar, mkt MarketCtx, rowsFrom int64) []WeekRow
```

Vec keys (exact; omit a key when uncomputable — never fake 0 except comp_vol_regime
which signals itself reports as 0-contrib):
- From signals 1w score: `pressure_score`, `comp_<Name>` = Contrib for every
  returned component, plus `pressure_abs` = |pressure_score|.
- Extension block (trailing, point-in-time, from bars[..anchor]):
  - `rsi14` (level 0..100), `rsi_pct` = percentile rank (0..1) of current RSI14
    within the trailing 252 RSI14 values (needs ≥60 RSI values, else omit; use
    incremental Wilder RSI series computed once per symbol — O(n) total),
  - `vwap_dist_atr` = (close − VWAP20)/ATR14 (unclamped; omit if ATR<=0),
  - `vwap_dist_pct` = (close − VWAP20)/VWAP20,
  - `atr_ext_20` = (close − SMA20)/ATR14,
  - `ma_dist_20`, `ma_dist_50`, `ma_dist_200` = close/SMA_n − 1,
  - `vol_anomaly` = (vol − mean60(vol))/std60(vol) (omit if std==0 or <60 bars),
  - `vol_pct` = percentile rank (0..1) of realized 20d vol within its trailing
    252-value series (≥60 values else omit),
  - `price_accel` = ROC5(now) − ROC5(5 bars ago),
  - `consec_dir` = signed consecutive same-direction daily closes, capped ±10, /10,
  - `ext_score` = mean of the AVAILABLE members of {rsi_ext=|rsi14−50|/50,
    clamp01(|vwap_dist_atr|/3), clamp01(|atr_ext_20|/3), vol_pct}; omit if none.
- Market block (joined from MarketCtx at the anchor's week): `vix_level`,
  `vix_regime`, `vix_high_vol`, `mkt_trend`, `mkt_ret_13w`.

RSI/VWAP/ATR/SMA/ROC: reuse `signals.RSI/VWAP/ATR/SMA/ROC` on bar-slice windows
where a single value is needed; implement incremental series internally only for
rsi_pct / vol_pct percentile histories.

Tests MUST include:
1. Truncation invariance (the leakage sentinel's structural guarantee): for a
   synthetic 400-bar series, WeeklyRows(bars) restricted to anchors ≤ bars[300].Ts
   equals WeeklyRows(bars[:301]) row-for-row (labels may differ only where the
   forward window crosses the cut — exclude the final 2 weeks from comparison).
2. Label geometry: hand-built bars verifying target/void/up semantics incl. the
   21d void rule and strict >0 up.
3. Pressure parity: WeeklyRows' pressure_score/comp_* at the last anchor equals
   calling signals.ComputeScores directly on the same trailing 500-bar slice.
4. Era mapping boundaries. 5. consec_dir/ext_score math on hand data.

## AGENT X — internal/researchx (NEW package; owns internal/researchx/*)

Files: `researchx.go`, `discover.go`, `decay.go`, `graph.go`, tests per file.
Pure. May import internal/researchledger (rl) for Evidence/Posterior/EffectiveChain
and math helpers ONLY (no store, no I/O). Copy small wilson/probit helpers from
internal/researchlab (unexported there) — keep researchx self-contained.

```go
type Obs struct {
    SymbolID int64
    Week     int64
    Ts       int64
    Vec      map[string]float64
    Up       bool
    FwdRet   float64
    Era      string
    HighVol  bool
}

type Cond struct {
    Key string  `json:"key"`
    Op  string  `json:"op"`  // ">=" | "<="
    Val float64 `json:"val"`
    Pct bool    `json:"pct"` // threshold applies to the WITHIN-WEEK cross-sectional
                             // percentile rank of Key (0..1), not the raw value
}

type Rule struct {
    Conds []Cond `json:"conds"`
    Call  string `json:"call"` // "inverse_pressure" | "follow_pressure" | "long" | "short"
}
// Dir: inverse_pressure = -sign(vec["pressure_score"]) (0 pressure ⇒ no trade);
// follow_pressure = +sign; long=+1; short=-1. A rule matches an obs when ALL
// conds hold (Pct conds evaluated against that week's cross-sectional ranks,
// computed among obs that HAVE the key that week).

type WeekTrial struct { Week int64; N, Wins, Ups, HighVol int; Win bool }
type EraGrade  struct { Era string; Weeks, WinWeeks, Obs int }
type WeekGrade struct {
    Weeks, WinWeeks, TotalObs, HighVolObs int
    Trials []WeekTrial            // chronological
    ByEra  []EraGrade             // chronological era order
}
// GradeWeeks: week-trial discipline exactly as the ledger (win = week win rate
// strictly beats max(upRate,1-upRate)); weeks with n<minWeekObs dropped.
// Directionless obs (Dir==0) are excluded before clustering.
func GradeWeeks(obs []Obs, r Rule, minWeekObs int) WeekGrade

type CFArm struct { Name string; Grade WeekGrade; WinRate float64 }
type CFReport struct {
    Full       CFArm
    Ablations  []CFArm  // drop-one-cond, same call ("without <condKey>")
    Base       CFArm    // no conds, same call
    NullMatched CFArm   // same matched obs as Full, but direction = deterministic
                        // hash parity of (SymbolID,Week) — the random-baseline arm
    AddsValue  bool     // Full beats EVERY ablation AND Base AND NullMatched
    Margin     float64  // Full winrate − best competing arm winrate
}
// AddsValue requires Full.Weeks >= minWeeks and winRate margins > 0.
func Counterfactual(obs []Obs, r Rule, minWeekObs, minWeeks int) CFReport

type SurvivalReport struct {
    Eras        []EraGrade
    PositiveEras int      // eras with >=minWeeksPerEra weeks AND winRate > 0.5
    GradedEras   int      // eras with >=minWeeksPerEra weeks
    TotalWeeks   int
    Survives     bool     // PositiveEras >= 2 AND TotalWeeks >= 30 AND no graded
                          // era has winRate < 0.35 (catastrophic regime failure)
}
func RegimeSurvival(g WeekGrade, minWeeksPerEra int) SurvivalReport

// FragileThreshold perturbs every numeric threshold ±10% (both directions, one
// cond at a time), re-grades, and reports the WORST retained edge fraction.
// fragile = original edge > 0 and worst retained < 0.5 of original.
func FragileThreshold(obs []Obs, r Rule, minWeekObs int) (worstRetained float64, fragile bool)
```

`discover.go`:
```go
type Candidate struct {
    ID    string   // "AD-" + hex(sha1(canonical spec))[:8]
    Rule  Rule
    Desc  string
    Grade WeekGrade
    CF    CFReport
    Survival SurvivalReport
    WilsonLower float64  // Bonferroni-corrected over nTested
    Survives    bool
}
type DiscoverConfig struct {
    MinWeekObs, MinWeeks, MinWeeksPerEra int  // defaults 10, 30, 8
    Alpha float64                              // 0.05
    MaxCandidates int                          // hard cap on grid, default 48
}
// Discover runs a bounded deterministic grid: single conds and pairs over atoms
// {pressure_abs Pct>=.75/.9; rsi_pct >=.8/<=.2; ext_score >=.7; vol_pct >=.8;
//  vol_anomaly Pct>=.85; price_accel Pct>=.85/<=.15; consec_dir >=.4/<=-.4;
//  vix_high_vol >=1} × calls {inverse_pressure, follow_pressure}; pairs only
// with a pressure_abs or ext_score atom (keeps the grid ~<=48). Every candidate
// is judged: week-trial winrate's Bonferroni-corrected (alpha/nTested) Wilson
// lower bound must exceed 0.5, AND Survival.Survives, AND CF.AddsValue (multi-
// cond only), AND !fragile. Deterministic order (sorted by ID).
func Discover(obs []Obs, cfg DiscoverConfig) []Candidate
```

`decay.go`:
```go
type DecayReport struct {
    Peak        float64
    PeakTs      int64
    Current     float64
    LastGradeTs int64   // newest experiment/replication/backtest ts
    EdgeWeakening bool  // Current < Peak-0.15 (only meaningful once Peak>0.5)
    Stale         bool  // nowTs-LastGradeTs > 60d (never graded ⇒ Stale)
}
// Decay replays the chain (through rl.EffectiveChain at each prefix) to find the
// posterior peak, exactly as the ledger would have reported it at the time.
func Decay(prior float64, chain []rl.Evidence, nowTs int64) DecayReport
```

`graph.go`:
```go
type GraphNode struct { ID, Kind, Label string; Posterior float64; Status string }
// Kind: "hypothesis" | "family" | "attack" | "era" | "evidence_kind"
type GraphEdge struct { From, To string; Weight int; AvgBF float64 }
type Graph struct { Nodes []GraphNode; Edges []GraphEdge }
// BuildGraph links each hypothesis to its family node, to attack:<name> nodes
// (name = note prefix before ':'), to era:<era> nodes (evidence notes carrying
// "era=<era>"), and to kind:<kind> nodes. Deterministic ordering.
func BuildGraph(hyps []rl.Hypothesis, evidence []rl.Evidence) Graph
```

Tests: week-trial math vs hand data; counterfactual detects a planted
conditional edge (edge only when cond holds) and rejects a redundant cond;
survival gates; fragile-threshold on a knife-edge rule; Discover finds exactly
the planted rule on synthetic obs and nothing on pure noise (>=200 weeks of
noise, assert zero survivors); decay peak replay; graph shape.

## AGENT L — internal/researchledger core additions (owns researchledger.go + researchledger_test.go EDITS)

Additive only; existing exported behavior unchanged (existing tests keep passing).
1. `const KindBacktest = "backtest"` — a disjoint HISTORICAL-window grade from the
   backfill engine: independent data the discovery never saw, but survivor-universe
   backtest, not a live forward record. Doc comment must say exactly that.
2. Penalties: `PenaltySurvivorship = 0.65` (backfilled universe = today's
   survivors), `PenaltyFragileThreshold = 0.5`, `PenaltyNoIncrementalValue = 0.3`.
3. `Counters`: KindBacktest rows count as replications (and contradictions when
   BF<1) — they ARE disjoint-window grades; the API distinguishes live vs
   backtest separately. Update doc.
4. `EffectiveChain`: static-state attacks are now the SET {"single-regime",
   "survivorship"} — dedupe each to its latest row (refactor to a map).
5. `Hypothesis` struct gains derived fields (JSON tags): `PeakPosterior float64
   json:"peakPosterior"`, `PeakTs int64 json:"peakTs"`, `LastGradeTs int64
   json:"lastGradeTs"`, `Spec string json:"spec,omitempty"` (rule JSON for
   machine-graded hypotheses; "" = prose-only).
6. Tests: backtest counting, static-attack dedupe for survivorship, existing
   suite untouched-green.

## AGENT S — store layer (owns NEW internal/store/researchweeks.go + EDITS to internal/store/researchledger.go + NEW researchweeks_test.go)

Coordinator has ALREADY added to schema.sql the table below and to migrate() the
four new research_ledger_hypotheses columns (peak_posterior REAL NOT NULL DEFAULT 0,
peak_ts INTEGER NOT NULL DEFAULT 0, last_grade_ts INTEGER NOT NULL DEFAULT 0,
spec TEXT NOT NULL DEFAULT '') — build against them.

```sql
CREATE TABLE IF NOT EXISTS research_weeks (
  symbol_id INTEGER NOT NULL, week INTEGER NOT NULL, ts INTEGER NOT NULL,
  vec TEXT NOT NULL, fwd_return REAL NOT NULL, up INTEGER NOT NULL,
  era TEXT NOT NULL, high_vol INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, PRIMARY KEY (symbol_id, week)
) WITHOUT ROWID;
```

researchweeks.go:
```go
type ResearchWeek struct {
    SymbolID int64; Week, Ts int64; Vec map[string]float64
    FwdReturn float64; Up bool; Era string; HighVol bool
}
// UpsertResearchWeeks: one tx, INSERT OR REPLACE, via s.w.
func (s *Store) UpsertResearchWeeks(ctx context.Context, rows []ResearchWeek, now int64) error
// ResearchWeeks: [fromWeek,toWeek] inclusive week-bucket range, ordered week,symbol_id.
// keys != nil ⇒ Vec filtered to those keys after unmarshal (memory discipline).
func (s *Store) ResearchWeeks(ctx context.Context, fromWeek, toWeek int64, keys []string) ([]ResearchWeek, error)
type ResearchWeekStats struct {
    Rows, Symbols, Weeks int; MinTs, MaxTs int64; ByEra map[string]int
}
func (s *Store) ResearchWeeksStats(ctx context.Context) (ResearchWeekStats, error)
// EarliestBarTs: MIN(ts) for (symbol, tf); 0 when none.
func (s *Store) EarliestBarTs(ctx context.Context, symbolID int64, tf md.Timeframe) (int64, error)
```

researchledger.go store edits:
- `LedgerHypotheses` scan + `UpsertLedgerHypothesis` insert now include the four
  new columns (spec written on insert AND updated on conflict alongside
  statement/open_questions; peak/last_grade written by the dedicated updater).
- `func (s *Store) UpdateLedgerDecay(ctx, id string, peakPosterior float64, peakTs, lastGradeTs int64) error`
- `func (s *Store) LedgerEvidenceMaxWindowKinds(ctx, hypID string, kinds []string) (int64, error)`
  (COALESCE(MAX(window_to),0) over the given kinds)
- `func (s *Store) LedgerEvidenceMinWindowKinds(ctx, hypID string, kinds []string) (int64, error)`
  (COALESCE(MIN(window_from),0) — used to keep backtest windows clear of live ones)
Tests: researchweeks round-trip incl. key projection + stats; decay update;
kind-scoped cursors.

## PHASE 2 — AGENT P — pipeline (owns NEW internal/pipeline/histbackfill.go, researchengine.go, histbackfill_test.go, researchengine_test.go)

**HistoryBackfillWorker** (`hist-backfill`, Interval 6h, once/UTC-day meta
`hist_backfill_last_day`, SetMeta on every terminal path):
```go
type HistoryBackfillWorker struct {
    St *store.Store
    Alpaca *alpaca.Client      // nil ⇒ skip fetching, compute rows from bars on disk
    Now func() time.Time
    BarsFrom time.Time         // zero ⇒ 2019-01-01 UTC (warmup year before rows)
    RowsFrom time.Time         // zero ⇒ 2020-01-01 UTC
}
```
Run: (1) list all active stocks (streamed *bool = nil); those with
EarliestBarTs(1d) == 0 or > BarsFrom+45d are "shallow" → fetch via
BackfillDailyMulti(ctx, st, shallowNames, resolver, BarsFrom) (resolver maps
symbol→id from the listed set). Per-symbol fetch failures: dq kind
`hist_backfill_error`, continue. Ensure SPY exists (UpsertDailyUniverseSymbol +
fetch) — it anchors MarketCtx. (2) Build MarketCtx ONCE from SPY 1d bars +
MacroSeries("VIXCLS",0) (adapt to histfeat.Point). (3) For every active stock
with ≥260 daily bars: load Bars(TF1d, 0, now, 0), histfeat.WeeklyRows(daily,
mkt, RowsFrom.Unix()), UpsertResearchWeeks in ≤2000-row batches. Crypto is
SKIPPED with an honest detail note (Kraken depth ~2y — no fake history).
Detail: "bars: deepened X/Y symbols; weeks: N rows / M symbols / K weeks; eras: ...".
Tests: synthetic-store test with Alpaca nil — seed symbols+bars via UpsertBars,
run, assert research_weeks rows + labels + once-per-day gate + shallow detection.

**ResearchEngineWorker** (`research-engine`, Interval 6h, once/UTC-day meta
`research_engine_last_day`):
```go
type ResearchEngineWorker struct {
    St *store.Store
    Now func() time.Time
    MinWeekObs, MinWeeks, MinWeeksPerEra int // 10, 30, 8
    MaxNewHyps int                           // 6 per run
}
```
Steps (each independent, continue on failure with dq `research_engine_error`):
A. Stats gate: ResearchWeeksStats; if Rows==0 → "no historical weeks yet" (honest
   empty, still set day meta).
B. Load obs ONCE with key projection (the union the graders+discovery need):
   pressure_score, pressure_abs, comp_rsi, rsi_pct, rsi14, ext_score, vol_pct,
   vol_anomaly, price_accel, consec_dir, vwap_dist_atr, atr_ext_20, vix_high_vol,
   mkt_trend. Sentinel STRUCTURAL pre-checks: rows ordered, no |fwdRet|>1.5
   (else dq `research_sentinel` + drop row), weeks monotone per symbol.
C. HISTORICAL ERA GRADING of ledger-registered machine hypotheses. Registry in
   code maps hyp ID → researchx.Rule:
   - H002:    Rule{Conds: nil, Call: "inverse_pressure"}
   - H008:    Rule{Conds: [{Key:"comp_rsi(abs)"...}]} — implement as Cond on a
     derived key: engine adds `comp_rsi_abs` to each obs vec at load (=|comp_rsi|);
     H008 = Conds:[{Key:"comp_rsi_abs",Op:">=",Val:0.02}], Call: inverse_pressure.
   - H002-R1: Conds:[{Key:"pressure_abs",Op:">=",Pct:true,Val:0.5},
                     {Key:"ext_score",Op:">=",Val:0.7}], Call: inverse_pressure.
   For each hyp × each era (chronological via histfeat.EraOrder, skipping eras
   whose end >= the hyp's live-evidence min window — LedgerEvidenceMinWindowKinds
   over experiment/replication — minus 2*WeekSecs; and eras whose start <= the
   backtest cursor LedgerEvidenceMaxWindowKinds(hyp,[backtest])): grade era obs
   with researchx.GradeWeeks. Era with Weeks < MinWeeksPerEra → skip (no row).
   Evidence: kind=backtest, K=WinWeeks, N=Weeks, P0=0.5,
   BF=rl.BayesFactorAbove(K,N,0.5,rl.WeekTrialMaxEdge), WindowFrom/To = era obs
   span, note MUST embed "era=<era>" plus "<k>/<n> winning weeks (<obs> obs) —
   historical backfill, survivor universe".
   Attack battery per era grade (insert attacks BEFORE the grade row, same
   crash-bias discipline as the ledger): time-split within era (PenaltyTimeSplitFail),
   suspicious-edge (PenaltySuspiciousEdge), survivorship (ALWAYS the static
   PenaltySurvivorship row w/ note "survivorship: backfilled universe is today's
   survivor set..."), and for multi-cond rules: counterfactual
   (PenaltyNoIncrementalValue when !AddsValue, note embeds arm winrates) +
   fragile-threshold (PenaltyFragileThreshold when fragile). Passed attacks BF=1.
   Regime coverage: era grades accumulate HighVolObs; >=30 across graded eras ⇒
   regimes=2 for the recompute (same rule as the live grader).
   After each hyp: recompute posterior over rl.EffectiveChain(chain), status via
   rl.Status(post, reps, regimes), UpdateLedgerDerived + decay via
   researchx.Decay → UpdateLedgerDecay.
D. SEED v2 (meta `research_ledger_seed_v2`, crash-idempotent like seedOnce):
   H002-R1 hypothesis: family meanrev, horizon 1w, prior 0.20, maxEdge 0.20,
   statement "Pressure×Extension: the weekly inverse edge concentrates where
   |pressure| is cross-sectionally high AND extension is extreme, and adds
   incremental value over pressure alone", Spec = its Rule JSON, OpenQuestions:
   ["Does the conditioned edge beat unconditioned inverse-pressure after costs?",
    "Is the driver extension level or extension CHANGE?"].
E. AUTO-DISCOVERY: researchx.Discover over the full obs (cfg defaults). For each
   survivor not already a ledger hypothesis (cap MaxNewHyps/run): Upsert as
   id=Candidate.ID, family "auto", prior 0.15, maxEdge 0.20, Spec=rule JSON,
   statement = generated Desc; evidence: attacks first, then ONE
   kind=experiment row (BF capped rl.DiscoveryMaxBF, note "auto-discovered on
   this very window — in-sample; disjoint-era/live replications are the real
   test" + era=<all>); recompute + decay. Auto hyps with Spec join the era-
   grading registry on FUTURE runs (fresh eras/live data only, cursor-guarded).
F. DECAY sweep over ALL ledger hypotheses (even prose-only) → UpdateLedgerDecay.
G. Detail: "graded E era-grades over H hyps; discovered D; decayed/stale: ...".
Tests: planted-edge synthetic (inverse-pressure edge across 2 eras w/ >=8 weeks
each, >=10 obs/week → H002 gains backtest evidence, posterior rises, regimes=2
when high-vol seeded); cursor prevents re-grading; live-window guard respected;
discovery inserts capped, idempotent next run; decay fields written; honest
empty on zero rows.

## PHASE 2 — AGENT A — API (owns EDIT internal/api/researchledger.go + NEW internal/api/researchgraph.go + researchgraph_test.go + EDIT researchledger_test.go)

1. Extend researchLedger handler payload: per-hyp fields now flow automatically
   from rl.Hypothesis (peak/lastGrade/spec); ADD `"weeks": <ResearchWeeksStats>`,
   `"evidenceKinds": map[kind]count` (from the loaded evidence),
   `"liveVsBacktest": {"live": n_replication, "backtest": n_backtest}`, and a
   `"decay"` array {id, peak, current, edgeWeakening, stale} via researchx.Decay
   per hypothesis (chain from the already-loaded evidence, grouped in Go).
   Discipline line: append "; historical era grades are BACKTEST evidence on a
   survivor universe — penalized as such, never presented as live."
2. NEW handler `researchGraph`: GET /api/research-graph →
   {"graph": researchx.BuildGraph(hyps, evidence)}. Route registration is done
   by the coordinator in api.go — do NOT touch api.go; just define
   `func (d Deps) researchGraph(w, r)`.
3. httptest tests for both handlers (seed via store like researchledger_test.go).

## Coordinator-owned wiring (NOT agents)
- schema.sql: research_weeks block (DONE before agents start).
- store.go migrate(): 4 column adds (DONE before agents start).
- run.go: `discoveryEngineWorkers(st, alpacaClient)` appended wave:
  HistoryBackfillWorker{St, Alpaca} + ResearchEngineWorker{St}.
- api.go: `mux.HandleFunc("GET /api/research-graph", d.researchGraph)` at END.
- Full build + vet + test, deploy, live run, results report.
