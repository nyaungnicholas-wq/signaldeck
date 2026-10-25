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
};

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
	];
	for (const rel of files) {
		const content = readFileSync(join(srcDir, rel), "utf8");
		assert.ok(content.includes('from "@/lib/demoCopy"'), rel);
	}
	const liveStrings = [
		...Object.values(RELEASED),
		"past the 2026-07-27..08-06 collapsed",
	];
	for (const rel of files) {
		const content = readFileSync(join(srcDir, rel), "utf8");
		for (const s of liveStrings) {
			assert.ok(!content.includes(s), `${s} in ${rel}`);
		}
	}
	const accuracyContent = readFileSync(join(srcDir, "app/accuracy/page.tsx"), "utf8");
	assert.ok(accuracyContent.includes("COPY.flagshipRetirement &&"), "app/accuracy/page.tsx");
});