// Live-site wording that would be false next to a demo build's synthetic figures
// (review of the option-B capture, 2026-10-25), and what a demo build says
// instead. A demo build (NEXT_PUBLIC_DEMO_DATA=1, next.config.ts) inlines a
// non-empty SIGNALDECK_DEMO_BANNER; every other build gets "" and LIVE, word for
// word as released, which demoCopy.test.mjs pins.
// This module ships in every build, so no DEMO line may contain the banner phrase
// ops/web-release.ps1 scans a release candidate for.

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
};

/** The wording for this build: DEMO in a demo build, LIVE (unchanged) in every other. */
export function siteCopy(banner: string | undefined = process.env.SIGNALDECK_DEMO_BANNER): typeof LIVE {
  return banner ? DEMO : LIVE;
}

export const COPY = siteCopy();
