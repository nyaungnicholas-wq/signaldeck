"""Objective check on the generated STRATEGY_DECK.md.

The check that matters is the number allowlist: a grep cannot catch a fabricated
statistic, but an allowlist of every numeric token I supplied can.
"""
import re, sys, pathlib

DECK = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "STRATEGY_DECK.md")
raw = DECK.read_text(encoding="utf-8")

# The live-record block is generated from data/accuracy_registry.json by
# tools/live_accuracy.py and guarded by its own --check in CI. Its figures and its
# verdicts are the registry's, not the author's, so the checks below -- which exist
# to police hand-written prose -- must not run over it. Checking it here would also
# be wrong in substance: the ban on interval verdicts protects against asserting one
# where none is published, and inside this block the interval IS the published thing.
t = re.sub(r"<!-- BEGIN GENERATED live_accuracy -->.*?<!-- END GENERATED live_accuracy -->",
           "", raw, flags=re.S)
# Same reasoning for §8's data measurements, generated from data/signaldeck.db by
# tools/deck_facts.py and guarded by its own --check. The allowlist below is the
# wrong instrument for them twice over: the figures are the database's rather than
# the author's, and no hand-maintained list of numeric tokens can stay current with
# a database that moves every day -- the previous attempt to keep one ("measured at
# the close of P6") was stale within 24 hours.
t = re.sub(r"<!-- BEGIN GENERATED deck_facts -->.*?<!-- END GENERATED deck_facts -->",
           "", t, flags=re.S)
# Third generated block, same reasoning as the two above and the same defect it
# was meant to prevent: §7's evidence table is INJECTED by
# tools/controls_evidence.py --inject, whose CI step re-derives it from the
# source and fails on drift. Policing those cells against a hand-typed allowlist
# means every package that gains a test breaks this checker for a number no
# human wrote — `internal/riskgate` reaching 39 tests is exactly what happened.
t = re.sub(r"<!-- BEGIN GENERATED controls_evidence -->.*?<!-- END GENERATED controls_evidence -->",
           "", t, flags=re.S)
low = t.lower()
fail = []

if "<!-- BEGIN GENERATED live_accuracy -->" not in raw:
    fail.append("deck no longer includes the generated live-record block")

HEADINGS = [
    "# SignalDeck — Strategy Deck",
    "## 1. What the system is, and what it is not",
    "## 2. Current grader status",
    "## 3. The one thesis",
    "## 4. Directional family — retired",
    "## 5. Structural family — pending and weakened",
    "## 6. Pre-registration rules",
    "## 7. Bias controls actually in force",
    "## 8. Open data defects",
    "## 9. Risk policy specification",
    "## 10. Execution specification",
    "## 11. Negative studies",
    "## 12. Honest capability assessment",
    "## 13. Governance and limitations",
    "## 14. Next actions",
]
for h in HEADINGS:
    if h not in t:
        fail.append(f"missing heading: {h}")

# Whole words. A substring test flagged "ledger-provenance" as the marketing word
# "proven", which is the failure mode that gets a checker switched off: it was
# right about the letters and wrong about the claim, and the only fix available
# to the author would have been to rename a real thing.
BANNED_WORDS = ["verified", "guaranteed", "proven", "moat", "best-in-class",
                "world-class", "revolutionary", "cutting-edge", "seamless",
                "state-of-the-art", "unlock", "industry-leading"]
# Prefixes, where the tail genuinely varies ("game-changing", "game-changer").
BANNED_PREFIXES = ["game-chang"]

for w in BANNED_WORDS:
    pat = re.compile(r"\b" + re.escape(w) + r"\b")
    for n, line in enumerate(t.split("\n"), 1):
        if pat.search(line.lower()):
            fail.append(f"banned word {w!r} at line {n}: {line.strip()[:90]}")
            break
for w in BANNED_PREFIXES:
    pat = re.compile(r"\b" + re.escape(w))
    for n, line in enumerate(t.split("\n"), 1):
        if pat.search(line.lower()):
            fail.append(f"banned word {w!r} at line {n}: {line.strip()[:90]}")
            break

# Numbers I actually supplied, plus structural tokens (section numbers, FC ids,
# ordinals, HTTP codes) that legitimately appear in any rendering of the brief.
ALLOWED = {
    # supplied facts
    "21", "9", "5", "4", "10", "12529", "11853", "2026", "08", "07", "14",
    "1077", "7.5", "451", "0", "429", "48", "018", "63", "1", "2", "3",
    "6", "8", "11", "12", "13", "1.0", "04", "27", "5322",
    # "09" is the month in the header's Revalidate by: 2026-09-04.
    "09", "1.1",
    "01d6bcdfe1451b6f2c84185478f9c8f3c405ef3ae845a11a937d865eacf2a724",
    # structural / ordinal
    "7", "3.6", "256", "128",
    # measured at the close of P6 and re-checked against data/signaldeck.db
    "1854228", "2146", "1777", "28", "17",
    # from proofs/P3A_SURVIVORSHIP_BACKFILL.md: delisted_at stamps 16 -> 716,
    # substantially closed 2019-2022, residual 2023-2025
    "16", "716", "2019", "2022", "2023", "2025",
    # from proofs/P4D_BARRIER_EXITS.md: the triple-barrier envelope —
    # adverse 2.0xATR, favorable 3.0xATR, ATR period 20 bars
    "2.0", "3.0", "20",
    # §7 evidence table, measured 2026-08-04 by `go test -list` and by counting
    # non-test importers of each package. Test counts:
    "26", "29", "31", "255",
    # riskgate sizing envelope (RISK_POLICY.md §1.5): 10% max position weight,
    # 0.5% minimum ticket; drawdown ladder rungs 20% suspend / 25% flatten.
    "0.5", "25",
    # from proofs/P11_BARS_COMPLETENESS.md, measured by tools/bars_completeness.py:
    # 1,907-session SPY calendar; 1,770 stock symbols; 1,916,310 expected
    # symbol-days; 96.49% overall and 97.36% common-stock coverage; 100% crypto;
    # 43,857 missing common-stock symbol-days.
    "1907", "1770", "1916310", "96.49", "97.36", "100", "43857",
    # survivorship residual sized 2026-08-04 against data/signaldeck.db:
    # 600 delistings recorded 2020-2022 vs 94 for 2023-2025 (15.7%);
    # tools/alpha/fetch_form25.py passes 12/12 resolver tests.
    "600", "94", "15.7", "2020", "2024",
    # riskgate defaults, read from internal/riskgate/provenance.go and matching
    # RISK_POLICY.md: SIGNALDECK_RISK_KELLY_FRACTION 0.25 (§169),
    # SIGNALDECK_RISK_MAX_CORR_TO_BOOK 0.80 (§267-268). "01" is the (0,1]
    # fractional clamp those two limits are bounded by — interval notation, not
    # a quantity, and the comma makes it one token to this scan.
    "0.25", "0.80", "01",
    # 95 is the width of the pre-registered auto-retire criterion's Wilson
    # interval (internal/prereg/prereg.go); the published interval is
    # Bonferroni-corrected and wider, which is the reconciliation §3 flags.
    "95",
    # The daily-bar symbol count as the import landed in batches: 1,070 ->
    # 1,770 -> 2,940 over three days. 2,940 is the same figure the generated
    # deck_facts block carries; 1,070 and 1,770 are the earlier snapshots, and
    # the paragraph exists to say why a hand-typed one was wrong.
    "1070", "2940",
    # "36 hand-typed live-record literals across 15 files" — the count
    # --scan-code returned when it was first run over shipped source.
    "15",
}
# Section numbers are structure, not statistics, and the token regex reads them
# as decimals — a cross-reference (§8.1) and a heading's own ordinal ("### 13.4
# Frozen claim set") both arrive here looking like measurements. This is the
# "ledger-provenance"/"proven" failure in numeric form: right about the
# characters, wrong about the claim, and the only fix available to the author
# would have been to stop numbering sections. Stripped for the NUMBER scan only
# — the HEADINGS check above still reads the deck's real headings.
nums_src = re.sub(r"§\s*\d+(?:\.\d+)*", "", t)
nums_src = re.sub(r"(?m)^(#+\s*)\d+(?:\.\d+)*", r"\1", nums_src)

bad = set()
for tok in re.findall(r"[0-9a-f]{16,}|\d+(?:[.,]\d+)*", nums_src):
    norm = tok.replace(",", "")
    if norm in ALLOWED or norm.replace(".", "") in {n.replace(".", "") for n in ALLOWED}:
        continue
    bad.add(tok)
if bad:
    fail.append("numbers not in the supplied facts: " + ", ".join(sorted(bad)[:20]))

# Every interval/significance verdict must be absent (FC2).
for pat in [r"confidence interval below", r"interval below the", r"significant .{0,12}skill",
            r"statistically significant"]:
    if re.search(pat, low):
        fail.append(f"interval/significance verdict present: /{pat}/")

# Historical vs live must be labelled.
if "HISTORICAL" not in t or "LIVE" not in t:
    fail.append("missing explicit HISTORICAL / LIVE labels")

# Status vocabulary must be used.
for s in ["BUILT AND IN FORCE", "BUILT BUT NOT IN FORCE", "NOT BUILT", "RETIRED", "PENDING"]:
    if s not in t:
        fail.append(f"missing status label: {s}")

if len(t) < 4000:
    fail.append(f"too short: {len(t)} chars")

# Remediation phases landed while this deck was being written, and three times a
# sentence saying "artefact X does not exist" was true when typed and false an hour
# later. A claim of absence has to fail the moment the file appears.
proofs = DECK.parent / "proofs"
for line in t.split("\n"):
    if not re.search(r"\b(not filed|is not\b.*filed|do(es)? not exist|are absent|is absent|NOT BUILT as files)\b", line):
        continue
    for name in re.findall(r"`?proofs/([A-Za-z0-9_.-]+\.(?:md|txt|json))`?", line):
        if (proofs / name).exists():
            fail.append(f"claims proofs/{name} is missing, but it exists: {line.strip()[:100]}")

if fail:
    print("FAIL")
    for f in fail:
        print(" -", f)
    sys.exit(1)
print(f"PASS ({len(t)} chars)")
