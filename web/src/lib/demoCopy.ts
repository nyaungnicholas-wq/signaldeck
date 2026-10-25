// Live-site wording that would be false next to a demo build's synthetic figures
// (review of the option-B capture, 2026-10-25), and what a demo build says
// instead. A demo build (NEXT_PUBLIC_DEMO_DATA=1, next.config.ts) inlines a
// non-empty SIGNALDECK_DEMO_BANNER; every other build gets "" and LIVE, word for
// word as released, which demoCopy.test.mjs pins.
// This module ships in every build, so no DEMO line may contain the banner phrase
// ops/web-release.ps1 scans a release candidate for.
// Member pages too (v2 review R1): a demo's watchlist, symbol page, signal panels,
// journal and /today must not call the stub's random figures SignalDeck's measured ones.
// Relative with the extension so node --test can load this module.
import { HYPOTHETICAL_NOTE, HYPOTHETICAL_SHORT, LIVE_GRADED_NOTE, WHAT_SIGNALDECK_IS } from "./hypothetical.ts";
import { JOURNAL_FOOTER } from "./journal.ts";
import { liveResolvedNote } from "./regimeCaveat.ts";

type Footer = { generated: string; min_independent_n: number; grading_epoch?: string; survivorship_epoch: string };

const LIVE = {
  demo: false,
  // home, under the live-record table
  skillLine: "Beating it is the only thing that counts as skill, and on this record nothing has.",
  // home, under the newest links of the chain
  receiptsLead: "These are the real, current records.",
  // home, Verification cards
  gradesCard:
    "Every predictor with its sample size, its baseline, its interval and its verdict, failures first. The figures are withheld from the public for now, and the page says why.",
  volatilityCard:
    "How the volatility forecast scores against two simple baselines, day by day. Not a per-symbol risk lookup: it is the scoreboard for one pre-registered estimate. Its figures are withheld from the public for now.",
  // home, Free account
  signupBody:
    "Make an account to keep your own watchlist. While the market-data licence is unresolved, members see the same withheld notice as visitors in place of the live grades, and the call journal and the daily digest are paused. We confirm your email and never sell it, and we send only account emails (confirmation, password reset, or a notice that an address already has an account).",
  // /proof, the page's own description
  proofSource: "This page is that record, recomputed every few minutes from the live ledger.",
  // /volatility: RiskMetrics is a company's name (now part of MSCI) as well as a method's
  volRules: "The two rules are “tomorrow will be like today” and RiskMetrics, an industry-standard weighted average.",
  ewmaRow: "RiskMetrics EWMA",
  ewmaDiff: "Difference against RiskMetrics:",
  // /accuracy: the real flagship's dated retirement and the real grading window
  flagshipRetirement: true,
  accuracyFooter: (r: Footer) =>
    `Regenerated ${r.generated} · minimum ${r.min_independent_n} independent observations for any verdict · directional window starts ${r.grading_epoch ?? r.survivorship_epoch} (re-registered on the publication chain, past the 2026-07-27..08-06 collapsed cross-sections and the SD-30 label leak) · survivorship epoch ${r.survivorship_epoch} (earlier rows were graded against a survivor-seeded universe and are excluded) · intervals resample days, not rows.`,
  // home and /accuracy headings that call the rows a live, measured record (review R2)
  recordEyebrow: "The live record",
  recordTitle: "What this platform has measured about itself",
  statGraded: "predictors graded live",
  statDays: "distinct graded days (longest record)",
  accuracyLead:
    "Every predictor, its claim, and what the live record actually supports — regraded daily against the naive baseline on independent (symbol, horizon, trading day) observations. The failures lead. Descriptive, not advice.",
  // components/HypotheticalNote, on every page that shows a backtest or paper figure
  hypoShort: HYPOTHETICAL_SHORT,
  hypoNote: HYPOTHETICAL_NOTE,
  liveGradedNote: LIVE_GRADED_NOTE,
  // /watchlist: each chip's suffix, and the footer around its "record" link
  watchChip: "backtest",
  watchFooter:
    "Readings are SignalDeck’s validated regime forecasts with their backtest accuracy; how they fare live is graded on the",
  watchFooterEnd: ". Prices are not shown: the market data is licensed and not redistributed. Not financial advice.",
  // symbol page footer, around its "record" link (FINRA's clause stays in the page)
  symbolFooter:
    "Regime reads are SignalDeck’s own forecasts, each shown with the accuracy it measured in backtests; how they fare live is graded on the",
  symbolSources: ". Filings, insider and holder data are public SEC records",
  // symbol page: the validated-signals panel, and the financials and filings panels' sources
  signalsTitle: "BACKTEST-VALIDATED SIGNALS",
  signalsSub: "measured walk-forward · per-band accuracy",
  signalsTipLabel: "why these are the validated ones",
  signalsTip:
    "These are structure calls — which side of a moving average price sits on, whether volume and volatility stay in their current regime — and each reports the MEASURED walk-forward accuracy of its own conviction band. The volatility regime is the replicated, significant one and leads on purpose. None of them is a price-direction call; the experimental P(up) read is kept in its own section below precisely because its live skill measured negative.",
  financialsSource: "SEC EDGAR XBRL, as filed · swept ~daily",
  filingsSource: "EDGAR · lags by law (Form 4 ~2bd, 13F ≤45d)",
  // /journal footer
  journalFooter: JOURNAL_FOOTER,
  // /today: the lead, what SignalDeck is, the risk headline, today's read, the volatility lead
  todayLead:
    "SignalDeck’s regime reads, each with the accuracy it measured in backtests, and the live record that grades every call. What it cannot predict, it says so.",
  whatSignalDeckIs: WHAT_SIGNALDECK_IS,
  riskLead: "SignalDeck’s volatility forecast is being graded live against a pre-registered test: ",
  riskLeadAccruing:
    "SignalDeck’s volatility forecast has reached its live evidence floor and awaits its pre-registered grade: ",
  todaysChip: "backtest-validated signal",
  todaysAcc: "Backtested accuracy in this conviction band:",
  todaysSkill: "measured-skill forecast",
  volLeadChip: "the backtest-validated forecast",
  volLeadTested: "It is our most-tested call — checked against history, then checked again by a second build.",
  // /proof, beside the pre-registration chain's "first gradable" date (v3 review B3-1)
  resolvedNote: liveResolvedNote,
};

const DEMO: typeof LIVE = {
  demo: true,
  skillLine:
    "Beating it is the only thing that counts as skill. Every row here is a synthetic demo record, so it says nothing about how SignalDeck has performed.",
  receiptsLead: "These are synthetic demo records, not the real chain.",
  gradesCard:
    "Every predictor with its sample size, its baseline, its interval and its verdict, failures first. This demo shows synthetic figures; the live site withholds the real ones until its data provider gives permission.",
  volatilityCard:
    "How the volatility forecast scores against two simple baselines, day by day. Not a per-symbol risk lookup: it is the scoreboard for one pre-registered estimate. This demo shows synthetic counts; the live site withholds the real ones for now.",
  signupBody:
    "Make an account to keep your own watchlist. In this demo the member pages run as a demo member on synthetic data. On the live site, while the market-data licence is unresolved, members see the same withheld notice as visitors in place of the live grades, and the call journal and the daily digest are paused. We confirm your email and never sell it, and we send only account emails (confirmation, password reset, or a notice that an address already has an account).",
  proofSource: "This demo shows the page over a synthetic ledger, not the live one.",
  volRules:
    "The two rules are “tomorrow will be like today” and an exponentially weighted moving average (EWMA), a standard industry method.",
  ewmaRow: "EWMA",
  ewmaDiff: "Difference against the EWMA:",
  flagshipRetirement: false,
  accuracyFooter: (r: Footer) =>
    `Regenerated ${r.generated} · minimum ${r.min_independent_n} independent observations for any verdict · directional window starts ${r.grading_epoch ?? r.survivorship_epoch} · every row here is a synthetic demo record · intervals resample days, not rows.`,
  recordEyebrow: "The demo record",
  recordTitle: "A synthetic record, laid out like the live one",
  statGraded: "demo predictors graded (synthetic)",
  statDays: "synthetic graded days (longest record)",
  accuracyLead:
    "Every predictor, its claim, and what its record supports, graded against the naive baseline on independent (symbol, horizon, trading day) observations. In this demo every row is a synthetic record, not measured on any data. The failures lead. Descriptive, not advice.",
  hypoShort:
    "Demo accuracy is synthetic: a random stand-in for a backtest figure, not measured on any data, and it predicts nothing.",
  hypoNote:
    "Demo figures. Synthetic stand-ins for simulated paper results: not trades, not measured on any data, and they predict nothing.",
  liveGradedNote: "Demo record: synthetic counts standing in for a live graded record. Nothing here was forecast or scored.",
  watchChip: "synthetic",
  watchFooter:
    "Readings here are synthetic demo reads, each with a random accuracy standing in for the backtest accuracy of SignalDeck’s validated regime forecasts, not measured on any data; this demo’s",
  watchFooterEnd: " is synthetic too. The tickers are fictional and no market data is shown. Not financial advice.",
  symbolFooter:
    "Regime reads here are synthetic demo reads, each with a random accuracy standing in for one measured in backtests, not measured on any data; this demo’s",
  symbolSources: " is synthetic too. Filings, insider and holder rows are fictional demo records, not SEC data",
  signalsTitle: "SYNTHETIC DEMO SIGNALS",
  signalsSub: "synthetic stand-ins · not measured on any data",
  signalsTipLabel: "what these demo rows are",
  signalsTip:
    "In this demo every row is synthetic: a random accuracy and sample size for a fictional ticker, standing in for SignalDeck’s backtest-validated structure calls. Nothing here was measured.",
  financialsSource: "demo: fictional figures, not SEC filings",
  filingsSource: "demo: fictional filings, not EDGAR",
  journalFooter:
    "Demo calls on fictional tickers, graded by the demo stub on synthetic closes. On the live site, members’ calls are graded by the same rules SignalDeck grades itself with. Not financial advice.",
  todayLead:
    "Synthetic demo reads for fictional tickers, standing in for SignalDeck’s regime reads: each accuracy is a random stand-in, not measured on any data, and the record that grades the calls is synthetic too.",
  whatSignalDeckIs:
    "SignalDeck does not tailor its statistical forecasts to anyone. This demo shows synthetic reads for fictional tickers to a demo member; on the live site, while the market-data licence is unresolved, the real forecasts are shown only to the operator. It does not know your portfolio and does not tell you what to buy or sell.",
  riskLead: "In this demo a synthetic volatility record stands in for SignalDeck’s live, pre-registered test: ",
  riskLeadAccruing:
    "In this demo a synthetic volatility record has reached its evidence floor and awaits its pre-registered grade: ",
  todaysChip: "synthetic demo signal",
  todaysAcc: "Synthetic accuracy for this conviction band (a random stand-in):",
  todaysSkill: "synthetic demo read",
  volLeadChip: "synthetic demo rows",
  volLeadTested: "In this demo every row is synthetic, for fictional tickers, and nothing was tested.",
  resolvedNote: (n: number | null | undefined) =>
    n == null
      ? ""
      : n <= 0
        ? " · no synthetic calls resolved yet"
        : n === 1
          ? " · 1 synthetic call resolved"
          : ` · ${n.toLocaleString("en-US")} synthetic calls resolved`,
};

/** The wording for this build: DEMO in a demo build, LIVE (unchanged) in every other. */
export function siteCopy(banner: string | undefined = process.env.SIGNALDECK_DEMO_BANNER): typeof LIVE {
  return banner ? DEMO : LIVE;
}

export const COPY = siteCopy();
