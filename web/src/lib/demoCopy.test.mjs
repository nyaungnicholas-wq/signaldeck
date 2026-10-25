// these tests pin lib/demoCopy.ts
// a build without the demo banner must render the released wording unchanged
// and a demo build (NEXT_PUBLIC_DEMO_DATA=1) must render the demo wording instead
//
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { siteCopy } from "./demoCopy.ts";

const RELEASED = {
	skillLine: "Beating it is the only thing that counts as skill, and on this record nothing has.",
	receiptsLead: "These are the real, current records.",
	gradesCard: "Every predictor with its sample size, its baseline, its interval and its verdict, failures first. The figures are withheld from the public for now, and the page says why.",
	volatilityCard: "How the volatility forecast scores against two simple baselines, day by day. Not a per-symbol risk lookup: it is the scoreboard for one pre-registered estimate. Its figures are withheld from the public for now.",
	signupBody: "Make an account to keep your own watchlist. While the market-data licence is unresolved, members see the same withheld notice as visitors in place of the live grades, and the call journal and the daily digest are paused. We confirm your email and never sell it, and we send only account emails (confirmation, password reset, or a notice that an address already has an account).",
	proofSource: "This page is that record, recomputed every few minutes from the live ledger.",
	volRules: "The two rules are “tomorrow will be like today” and RiskMetrics, an industry-standard weighted average.",
	ewmaRow: "RiskMetrics EWMA",
	ewmaDiff: "Difference against RiskMetrics:",
	// headings (review R2) and the member pages (review R1), as released
	recordEyebrow: "The live record",
	recordTitle: "What this platform has measured about itself",
	statGraded: "predictors graded live",
	statDays: "distinct graded days (longest record)",
	accuracyLead: "Every predictor, its claim, and what the live record actually supports — regraded daily against the naive baseline on independent (symbol, horizon, trading day) observations. The failures lead. Descriptive, not advice.",
	hypoShort: "Backtest accuracy is hypothetical: measured on past data, not trades in a real account, and it does not predict future results.",
	hypoNote: "Hypothetical performance. Simulated paper results, not trades in a real account; costs are estimated. Hypothetical results have inherent limitations and do not predict future results.",
	liveGradedNote: "Live graded record: forecasts frozen before the outcome and scored against it, not a backtest. Past accuracy does not guarantee future results.",
	watchChip: "backtest",
	watchFooter: "Readings are SignalDeck’s validated regime forecasts with their backtest accuracy; how they fare live is graded on the",
	watchFooterEnd: ". Prices are not shown: the market data is licensed and not redistributed. Not financial advice.",
	symbolFooter: "Regime reads are SignalDeck’s own forecasts, each shown with the accuracy it measured in backtests; how they fare live is graded on the",
	symbolSources: ". Filings, insider and holder data are public SEC records",
	signalsTitle: "BACKTEST-VALIDATED SIGNALS",
	signalsSub: "measured walk-forward · per-band accuracy",
	signalsTipLabel: "why these are the validated ones",
	signalsTip: "These are structure calls — which side of a moving average price sits on, whether volume and volatility stay in their current regime — and each reports the MEASURED walk-forward accuracy of its own conviction band. The volatility regime is the replicated, significant one and leads on purpose. None of them is a price-direction call; the experimental P(up) read is kept in its own section below precisely because its live skill measured negative.",
	financialsSource: "SEC EDGAR XBRL, as filed · swept ~daily",
	filingsSource: "EDGAR · lags by law (Form 4 ~2bd, 13F ≤45d)",
	journalFooter: "Your calls, graded by the same rules SignalDeck grades itself with. Prices are not shown: the market data is licensed. Not financial advice.",
	todayLead: "SignalDeck’s regime reads, each with the accuracy it measured in backtests, and the live record that grades every call. What it cannot predict, it says so.",
	whatSignalDeckIs: "SignalDeck does not tailor its statistical forecasts to anyone, and while the market-data licence is unresolved they are shown only to the operator. It does not know your portfolio and does not tell you what to buy or sell.",
	riskLead: "SignalDeck’s volatility forecast is being graded live against a pre-registered test: ",
	riskLeadAccruing: "SignalDeck’s volatility forecast has reached its live evidence floor and awaits its pre-registered grade: ",
	todaysChip: "backtest-validated signal",
	todaysAcc: "Backtested accuracy in this conviction band:",
	todaysSkill: "measured-skill forecast",
	volLeadChip: "the backtest-validated forecast",
	volLeadTested: "It is our most-tested call — checked against history, then checked again by a second build.",
};

// The member pages' demo wording must say its figures are synthetic, not measured (review R1).
const MEMBER_KEYS = ["hypoShort", "hypoNote", "liveGradedNote", "watchFooter", "symbolFooter", "signalsTitle", "signalsSub", "signalsTip", "journalFooter", "todayLead", "whatSignalDeckIs", "riskLead", "todaysAcc", "volLeadTested"];

const REG = {
	generated: "2026-01-02T06:00:00Z",
	min_independent_n: 30,
	grading_epoch: "2026-01-05",
	survivorship_epoch: "2026-01-01",
};

const LIVE_FOOTER = "Regenerated 2026-01-02T06:00:00Z · minimum 30 independent observations for any verdict · directional window starts 2026-01-05 (re-registered on the publication chain, past the 2026-07-27..08-06 collapsed cross-sections and the SD-30 label leak) · survivorship epoch 2026-01-01 (earlier rows were graded against a survivor-seeded universe and are excluded) · intervals resample days, not rows.";
const LIVE_FOOTER_NO_GRADING = "Regenerated 2026-01-02T06:00:00Z · minimum 30 independent observations for any verdict · directional window starts 2026-01-01 (re-registered on the publication chain, past the 2026-07-27..08-06 collapsed cross-sections and the SD-30 label leak) · survivorship epoch 2026-01-01 (earlier rows were graded against a survivor-seeded universe and are excluded) · intervals resample days, not rows.";

test("a normal build keeps the released wording, word for word", () => {
	for (const banner of ["", undefined]) {
		const c = siteCopy(banner);
		assert.equal(c.demo, false);
		assert.equal(c.flagshipRetirement, true);
		for (const key of Object.keys(RELEASED)) {
			assert.equal(c[key], RELEASED[key], key);
		}
		assert.equal(c.accuracyFooter(REG), LIVE_FOOTER);
		assert.equal(c.accuracyFooter({ ...REG, grading_epoch: undefined }), LIVE_FOOTER_NO_GRADING);
	}
});

test("a demo build swaps every live-only line", () => {
	const d = siteCopy("DEMO DATA banner");
	assert.equal(d.demo, true);
	assert.equal(d.flagshipRetirement, false);
	for (const key of Object.keys(RELEASED)) {
		assert.ok(typeof d[key] === "string" && d[key].length > 0, `${key} non-empty string`);
		assert.notEqual(d[key], RELEASED[key], key);
	}
	assert.ok(d.receiptsLead.includes("synthetic"));
	assert.ok(d.gradesCard.includes("synthetic figures"));
	assert.ok(d.signupBody.includes("demo member"));
	assert.ok(d.proofSource.includes("synthetic ledger"));
	assert.ok(d.volRules.includes("EWMA"));
	const demoFooter = d.accuracyFooter(REG);
	assert.ok(demoFooter.includes("synthetic demo record"));
	assert.ok(demoFooter.includes("minimum 30 independent observations"));
	assert.ok(!demoFooter.includes("2026-07-27"));
	assert.ok(!demoFooter.includes("SD-30"));
	assert.ok(!demoFooter.includes("survivor"));
	for (const key of MEMBER_KEYS) assert.ok(/synthetic|demo/i.test(d[key]), `${key} says demo or synthetic`);
	for (const key of MEMBER_KEYS) assert.ok(!/measured walk-forward|validated regime forecasts with|measured-skill/.test(d[key]), `${key} claims a measurement`);
	assert.equal(d.recordEyebrow, "The demo record");
	assert.ok(d.accuracyLead.includes("synthetic record"));
});

test("no demo line repeats a real-record claim or the release guard's marker", () => {
	const d = siteCopy("x");
	const allDemo = [
		...Object.values(d),
		d.accuracyFooter(REG),
	].filter((v) => typeof v === "string");
	const forbidden = [
		"RiskMetrics",
		"2026-07-24",
		"real, current",
		"withheld from the public for now",
		"on this record nothing has",
	];
	for (const s of allDemo) {
		for (const f of forbidden) {
			assert.ok(!s.includes(f), `demo string contains forbidden: ${f}`);
		}
	}
	for (const banner of ["", "x"]) {
		const c = siteCopy(banner);
		const all = [
			...Object.values(c),
			c.accuracyFooter(REG),
		].filter((v) => typeof v === "string");
		for (const s of all) {
			assert.ok(
				!s.includes("synthetic values for illustration, not market data"),
				`build string contains release-guard phrase: ${banner ? "demo" : "live"}`
			);
		}
	}
});

test("every page that showed a live-only line reads it from demoCopy", () => {
	const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..");
	const files = [
		"app/page.tsx",
		"app/accuracy/page.tsx",
		"app/volatility/page.tsx",
		"app/proof/page.tsx",
		"components/landing/SignupCTA.tsx",
		// member pages (review R1)
		"components/HypotheticalNote.tsx",
		"components/MemberWatchlist.tsx",
		"components/symbol/MemberSymbolView.tsx",
		"components/symbol/ValidatedSignalsPanel.tsx",
		"components/symbol/FinancialsPanel.tsx",
		"components/symbol/FilingsIntelPanel.tsx",
		"components/MemberJournal.tsx",
		"app/today/page.tsx",
		"components/home/TodaysRead.tsx",
		"components/home/VolRegimeLead.tsx",
		"lib/riskHeadline.ts",
	];
	for (const rel of files) {
		const content = readFileSync(join(srcDir, rel), "utf8");
		assert.ok(content.includes('from "@/lib/demoCopy"') || content.includes('from "./demoCopy.ts"'), rel);
	}
	const liveStrings = [
		...Object.values(RELEASED).filter((s) => s.length >= 20), // short ones ("backtest") also sit in comments
		"past the 2026-07-27..08-06 collapsed",
	];
	// JSX writes ’ and — as entities and wraps long text over lines
	const norm = (s) => s.replace(/&rsquo;/g, "’").replace(/&mdash;/g, "—").replace(/\s+/g, " ");
	for (const rel of files) {
		const content = norm(readFileSync(join(srcDir, rel), "utf8"));
		for (const s of liveStrings) {
			assert.ok(!content.includes(norm(s).trim()), `${s} in ${rel}`);
		}
	}
	const accuracyContent = readFileSync(join(srcDir, "app/accuracy/page.tsx"), "utf8");
	assert.ok(accuracyContent.includes("COPY.flagshipRetirement &&"), "app/accuracy/page.tsx");
});