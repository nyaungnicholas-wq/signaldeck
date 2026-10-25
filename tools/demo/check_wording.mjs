// What a RUNNING web build renders: a demo build (NEXT_PUBLIC_DEMO_DATA=1) must show the
// demo wording and none of the live-only lines; a normal build the released wording and
// none of the demo lines. The rendered half of web/src/lib/demoCopy.test.mjs, which pins the strings.
// usage: node tools/demo/check_wording.mjs <baseUrl> <demo|live>
import { siteCopy } from "../../web/src/lib/demoCopy.ts";

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
];

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
if (mode === "demo") check((pages["/"] ?? "").includes("DEMO DATA"), "/ carries the DEMO DATA banner");
else check(!Object.values(pages).some((t) => t?.includes("synthetic values for illustration")), "no page carries the demo banner phrase");
console.log(bad ? `WORDING CHECK FAILED (${bad} of ${n})` : `WORDING CHECK OK (${mode}, ${n} checks)`);
process.exit(bad ? 1 : 0);
