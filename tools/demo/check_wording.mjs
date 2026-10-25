// What a RUNNING web build renders: a demo build (NEXT_PUBLIC_DEMO_DATA=1) must show the
// demo wording and none of the live-only lines; a normal build the released wording and
// none of the demo lines. The rendered half of web/src/lib/demoCopy.test.mjs, which pins the strings.
// usage: node tools/demo/check_wording.mjs <baseUrl> <demo|live>
// The member pages render client-side behind the stub's demo-member session, so they are
// read from a headless browser (web/node_modules/playwright), not from the server HTML.
import { createRequire } from "node:module";
import { siteCopy } from "../../web/src/lib/demoCopy.ts";
const { chromium } = createRequire(new URL("../../web/package.json", import.meta.url))("playwright");

const [base, mode] = process.argv.slice(2);
if (!base || !["demo", "live"].includes(mode)) {
	console.error("usage: node tools/demo/check_wording.mjs <baseUrl> <demo|live>");
	process.exit(2);
}
const LIVE = siteCopy("");
const DEMO = siteCopy("demo");
const CHECKS = [
	["/", LIVE.skillLine, DEMO.skillLine],
	["/", LIVE.receiptsLead, DEMO.receiptsLead],
	["/", LIVE.gradesCard, DEMO.gradesCard],
	["/", LIVE.volatilityCard, DEMO.volatilityCard],
	["/", LIVE.signupBody, DEMO.signupBody],
	["/proof", LIVE.proofSource, DEMO.proofSource],
	["/volatility", LIVE.volRules, DEMO.volRules],
	["/accuracy", "past the 2026-07-27..08-06 collapsed cross-sections", "every row here is a synthetic demo record"],
	["/accuracy", "FLAGSHIP RETIRED 2026-07-24", null],
	["/", LIVE.recordTitle, DEMO.recordTitle],
	["/accuracy", LIVE.accuracyLead, DEMO.accuracyLead],
];
// member pages (review R1), compared case-blind: panel headings are CSS upper-cased
const MEMBER = [
	["/watchlist", "watchFooter"], ["/watchlist", "watchFooterEnd"], ["/watchlist", "hypoShort"],
	["/s/stocks/DEMOA", "signalsTitle"], ["/s/stocks/DEMOA", "signalsSub"], ["/s/stocks/DEMOA", "symbolFooter"],
	["/s/stocks/DEMOA", "symbolSources"], ["/s/stocks/DEMOA", "financialsSource"], ["/s/stocks/DEMOA", "filingsSource"],
	["/journal", "journalFooter"],
	["/today", "todayLead"], ["/today", "whatSignalDeckIs"], ["/today", "riskLead"], ["/today", "todaysAcc"],
	["/today", "volLeadTested"], ["/today", "hypoNote"],
];
// /proof's pre-registration chain renders client-side too; its count is read off the page (v3 review B3-1)
const PROOF = "/proof";

const ENT = { amp: "&", lt: "<", gt: ">", quot: '"', "#39": "'", "#x27": "'", nbsp: " " };
function text(html) {
	return html
		.replace(/<(script|style)\b[\s\S]*?<\/\1>/gi, " ")
		.replace(/<[^>]*>/g, " ")
		.replace(/&(amp|lt|gt|quot|#39|#x27|nbsp);/g, (_, e) => ENT[e])
		.replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)))
		.replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(Number(d)))
		.replace(/\s+/g, " ");
}

const pages = {};
let n = 0, bad = 0;
const check = (ok, line) => { n++; if (!ok) bad++; console.log(`  ${ok ? "ok  " : "FAIL"} ${line}`); };
for (const p of ["/", "/accuracy", "/volatility", "/proof"]) {
	try {
		const res = await fetch(base + p, { signal: AbortSignal.timeout(30_000) });
		pages[p] = res.status === 200 ? text(await res.text()) : null;
		check(res.status === 200, `${p} HTTP ${res.status}`);
	} catch (e) {
		pages[p] = null;
		check(false, `${p} fetch failed: ${e.message}`);
	}
}
for (const [p, live, demo] of CHECKS) {
	const want = mode === "demo" ? demo : live;
	const not = mode === "demo" ? live : demo;
	const t = pages[p] ?? "";
	if (want) check(t.includes(want), `${p} present: ${want.slice(0, 70)}`);
	if (not) check(!t.includes(not), `${p} absent:  ${not.slice(0, 70)}`);
}
const browser = await chromium.launch();
const member = {};
for (const p of new Set([...MEMBER.map(([p]) => p), PROOF])) {
	const page = await (await browser.newContext({ viewport: { width: 1280, height: 720 } })).newPage();
	try {
		await page.goto(base + p, { waitUntil: "networkidle", timeout: 60_000 });
		await page.waitForTimeout(1500);
		const h = await page.evaluate(() => document.documentElement.scrollHeight);
		for (let y = 0; y < h; y += 400) await page.evaluate((y) => scrollTo(0, y), y);
		await page.waitForTimeout(800);
		member[p] = (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ").toLowerCase();
		check(!member[p].includes("not demo data"), `${p} rendered (no "not demo data" refusal)`);
	} catch (e) {
		member[p] = "";
		check(false, `${p} browser load failed: ${e.message}`);
	}
}
await browser.close();
const low = (s) => s.replace(/\s+/g, " ").trim().toLowerCase();
for (const [p, key] of MEMBER) {
	const [want, not] = mode === "demo" ? [DEMO[key], LIVE[key]] : [LIVE[key], DEMO[key]];
	check(member[p].includes(low(want)), `${p} present: ${key}: ${want.trim().slice(0, 60)}`);
	check(!member[p].includes(low(not)), `${p} absent:  ${key}: ${not.trim().slice(0, 60)}`);
}
{
	const m = member[PROOF].match(/first gradable \d{4}-\d{2}-\d{2} · ([\d,]+) /);
	const cnt = m ? Number(m[1].replace(/,/g, "")) : null;
	check(cnt != null, `${PROOF} pre-registration chain shows a resolved count (${m ? m[1] : "none"})`);
	const [want, not] = mode === "demo" ? [DEMO.resolvedNote(cnt), LIVE.resolvedNote(cnt)] : [LIVE.resolvedNote(cnt), DEMO.resolvedNote(cnt)];
	check(cnt != null && member[PROOF].includes(low(want)), `${PROOF} present: resolvedNote: ${want.trim()}`);
	check(cnt != null && !member[PROOF].includes(low(not)), `${PROOF} absent:  resolvedNote: ${not.trim()}`);
}
if (mode === "demo") check((pages["/"] ?? "").includes("DEMO DATA"), "/ carries the DEMO DATA banner");
else check(!Object.values(pages).some((t) => t?.includes("synthetic values for illustration")), "no page carries the demo banner phrase");
console.log(bad ? `WORDING CHECK FAILED (${bad} of ${n})` : `WORDING CHECK OK (${mode}, ${n} checks)`);
process.exit(bad ? 1 : 0);
