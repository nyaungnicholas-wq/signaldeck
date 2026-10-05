// REGIMES caveat (2026-10-02). Every regime row the daemon serves carries
// evidenceCaveat, the sentence that says its historicalAccuracy is a backtest
// lookup and when it can first be graded, and it must be shown verbatim beside
// the accuracy it qualifies. No member surface rendered it.
//   node --test src/lib/regimeCaveat.test.mjs
// node --test cannot load JSX, so the render checks read the component sources.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { liveResolvedNote, regimeCaveats } from "./regimeCaveat.ts";

const src = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("regimeCaveats returns the server's text verbatim, once, in row order", () => {
  const a = "BACKTEST CLAIM, not a live measurement: x — y.  ";
  const b = "Another caveat.";
  assert.deepEqual(regimeCaveats([{ evidenceCaveat: a }, { evidenceCaveat: b }, { evidenceCaveat: a }]), [a, b]);
  assert.equal(regimeCaveats([{ evidenceCaveat: a }])[0], a);
  assert.deepEqual(regimeCaveats([{}, { evidenceCaveat: "" }, { evidenceCaveat: undefined }]), []);
  assert.deepEqual(regimeCaveats([]), []);
});

test("every member surface renders its rows' caveat in a HelpTip", () => {
  const surfaces = [
    ["../components/home/TodaysRead.tsx", "regimeCaveats([best.fc])"],
    ["../components/symbol/ValidatedSignalsPanel.tsx", "regimeCaveats([r])"],
    ["../components/MemberWatchlist.tsx", "regimeCaveats(["],
    ["../app/market/regimes/page.tsx", "regimeCaveats(list)"],
    ["../app/market/breadth/page.tsx", "regimeCaveats(rows)"]
  ];
  for (const [file, rowsExpr] of surfaces) {
    const c = src(file);
    assert.ok(c.includes('from "@/lib/regimeCaveat"'), `${file} imports regimeCaveats`);
    assert.ok(c.includes(rowsExpr), `${file} feeds its rows to regimeCaveats`);
    assert.ok(c.includes('import HelpTip from "@/components/HelpTip"'), `${file} imports HelpTip`);
    if (rowsExpr === "regimeCaveats(list)" || rowsExpr === "regimeCaveats([" || rowsExpr === "regimeCaveats(rows)") {
      // One tip for a list of rows: each distinct caveat, as served.
      assert.match(c, /<HelpTip label="what these accuracies are">\s*\{caveats\.map\(\(c\) => \(\s*<span key=\{c\}[^>]*>\s*\{c\}\s*<\/span>/, `${file} renders each caveat as served`);
    } else {
      // One tip per row, beside that row's accuracy.
      assert.match(c, /\.map\(\(c\) => \(\s*<HelpTip key=\{c\} label="what this accuracy is">\s*\{c\}\s*<\/HelpTip>/, `${file} renders the caveat in a HelpTip`);
    }
  }
});

test("ValidatedSignalsPanel carries the caveat through its row model", () => {
  const c = src("../components/symbol/ValidatedSignalsPanel.tsx");
  assert.equal((c.match(/evidenceCaveat: f\.evidenceCaveat/g) ?? []).length, 2, "toRow and volToRow both keep evidenceCaveat");
});

test("no surface writes its own version of the caveat", () => {
  const surfaces = [
    "../components/home/TodaysRead.tsx",
    "../components/symbol/ValidatedSignalsPanel.tsx",
    "../components/MemberWatchlist.tsx",
    "../app/market/regimes/page.tsx",
    "../app/market/breadth/page.tsx"
  ];
  for (const file of surfaces) {
    assert.doesNotMatch(src(file), /BACKTEST CLAIM|not a live measurement/, `${file}: the caveat must come from the server, never a paraphrase in the page`);
  }
});

// E-CAVEAT-DATE (2026-10-02): /proof's "first gradable 2026-08-07" read as "not
// graded yet" long after the date passed. The chip now carries the daemon's
// live count beside it: how many resolved, never how they scored.
test("the /proof first-gradable chip states the live count", () => {
  assert.equal(liveResolvedNote(undefined), "");
  assert.equal(liveResolvedNote(null), "");
  assert.equal(liveResolvedNote(0), " · none resolved live yet");
  assert.equal(liveResolvedNote(1), " · 1 call resolved live");
  assert.equal(liveResolvedNote(42333), " · 42,333 calls resolved live");
  const c = src("../components/proof/Registrations.tsx");
  assert.match(c, /first gradable \{data\.firstGradableOn\}[\s\S]{0,300}\{liveResolvedNote\(data\.liveResolved\)\}/);
});
