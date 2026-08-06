<!--
TARGETS (nothing here has been applied):
  ops/docs-registry.json   <- ops-docs-registry.patch in this directory
  partials/controls_evidence.md, partials/deck_facts.md   <- created by §4 commands
  STRATEGY_DECK.md         <- rewritten in place by §4 commands (BLOCKED-4: human only)
HOW TO APPLY: read §3 first, then run §4 verbatim, then §6.
NOT EXECUTED by the agent that wrote this: STRATEGY_DECK.md, ops/docs-registry.json
and partials/ are byte-identical to HEAD's working tree. `git status --porcelain --
STRATEGY_DECK.md ops/docs-registry.json partials/` was empty after every step below.
-->

# BLOCKED-4 — docs gate: two generated regions earn no exemption

## 1. The failure, verified

```
$ python tools/docs_gate.py check
docs-gate: 2 violation(s)
docs-gate: single-source-of-truth: STRATEGY_DECK.md:126: Generated region 'controls_evidence' ... is not a declared partial (ops/docs-registry.json `partials` / `external_partials`).
docs-gate: single-source-of-truth: STRATEGY_DECK.md:199: Generated region 'deck_facts' ... is not a declared partial ...
```

`tools/docs_gate.py:347` `_region_is_anchored` grants a region its exemption only when
**all three** hold:

| # | Condition | Source | Today |
|---|---|---|---|
| 1 | the name is in `partials` or `external_partials` | `docs_gate.py:330-344`, `:362` | **fails** — `external_partials` is `["live_accuracy", "live_accuracy.md"]` |
| 2 | `partials/<name>` or `partials/<name>.md` exists | `docs_gate.py:301-321`, `:365` | **fails** — `partials/` holds only `INCLUDES.txt` and `live_accuracy.md` |
| 3 | the document's region equals that file | `docs_gate.py:369-377` | **would fail** — both regions have drifted, see §5 |

All three are broken. Fixing one is not a fix — `audits/completion-2026-08-06/completion_state.json`
`repairs_attempted_and_reverted[0]` records a prior attempt that declared the names and
stopped, which "only changed the gate's failure message". Condition 3 is the one nobody
looked at: **both regions are stale against their own generators right now.**

## 2. Is the exemption legitimate? Yes — and it is the weakest exemption in the file

The exemption is from `no-hardcoded-live-accuracy` only. What it would cover:

**`tools/controls_evidence.py`** — reads the Go tree and nothing else.
`count_tests` (`:32-44`) counts `func Test|Fuzz|Example` lines in each package's
`*_test.go`; `count_wired` (`:46-67`) walks `daemon/` and counts distinct non-test `.go`
files importing the package. The only hardcoded strings are the 12-row `CONTROLS`
manifest (`:14-27` — *what to measure*, not what the measurement is) and the two
explanatory sentences in `render` (`:107-115`). Every cell is measured.

**`tools/deck_facts.py`** — reads the database and nothing else, at
`sqlite3.connect("file:%s?mode=ro", uri=True)` (`:195`). `measure` (`:49-91`) is SQL
aggregates over `universe_membership` and `symbols`, plus `bars_completeness.measure`.
`render` (`:98-152`) explicitly refuses the clock — "every date comes from the data,
never the clock" (`:99-100`) — so the block is deterministic given the database. Every
figure is measured; the surrounding paragraph is generator-owned prose, not per-run text.

Neither generator writes a partial file today. Both write the *space*-form markers the
gate parses (`docs_gate.py:57-67`), and both are already wired into CI —
`.github/workflows/ci.yml:238` (`controls_evidence.py --check`), `:241-243`
(`--inject` + `git diff --exit-code`), `:284` (`deck_facts.py --check --inject`). The
missing piece is only the `partials/` copy the gate anchors against.

**The decisive point: neither block contains a live-accuracy claim at all.** Both regions
are currently NOT exempt, so `check_no_hardcoded_live_accuracy` scanned every one of their
lines — and reported nothing but the two anchor violations. `controls_evidence` has no
percentages; `deck_facts`'s percentages are bar-coverage, not directional accuracy. So the
exemption removes a false alarm and hides nothing. That is the opposite of an unearned
exemption: exempting hand-written prose would be a hole, and this is machine-derived
content whose numbers would pass the check even unexempted.

### 2b. Removing the markers instead — do not

It would break `ci.yml:241` and `ci.yml:284`: both call `--inject STRATEGY_DECK.md`, and
`inject()` exits 1 with "no BEGIN/END marker pair" when the markers are gone
(`controls_evidence.py:127-130,171-172`; `deck_facts.py:167-170,224-225`). It would also
move ~20 measured figures back into hand-typed prose — the exact FC1/§8 failure both
generators were written to close (`deck_facts.py:4-10`). Marker removal trades a
bookkeeping violation for a correctness regression.

## 3. Preconditions

- STRATEGY_DECK.md is `ACTIVE` in the registry; §4 rewrites it. **BLOCKED-4: a human runs this.**
- Run on the machine holding `data/signaldeck.db`. `deck_facts.py` opens it read-only.
- `.gitattributes` already carries `partials/* -text`, so the two new partials stay LF
  on every platform. No `.gitattributes` change is needed.
- `--inject` rewrites the whole document LF (it reads with universal newlines and writes
  `newline=""`). With `core.autocrlf=true` the index is already LF, so `git diff` shows
  only the content hunks — but your editor may briefly report a line-ending change on the
  worktree file. Harmless; a later `git checkout` restores CRLF.

## 4. The commands

```bash
cd "C:/Users/Nicholas_N/Desktop/claude code/signaldeck"

# (a) declare both names as external partials
git apply drafts/pending-approval/docs-gate/ops-docs-registry.patch
python -c "import json;json.load(open('ops/docs-registry.json',encoding='utf-8'));print('registry parses')"

# (b) sanity: no control may be published with zero tests or zero importers
python tools/controls_evidence.py --check

# (c) ONE invocation each — renders the block once and writes the SAME bytes to
#     the partial and to the deck. Never split these into two runs: two renders
#     of deck_facts seconds apart can differ (see §5), and the gate compares them.
python tools/controls_evidence.py --write partials/controls_evidence.md --inject STRATEGY_DECK.md
python tools/deck_facts.py        --write partials/deck_facts.md        --inject STRATEGY_DECK.md
```

`--write` + `--inject` in one process is load-bearing: `controls_evidence.py:166-172` and
`deck_facts.py:199,221-225` both compute `block` once and then write it to both targets.

**`external_partials`, never `partials`.** `check_single_source_of_truth`
(`docs_gate.py:581-601`) regenerates every name in `partials` with docs_gate's own
`generate_partial` stub (`:566-580`) and reports it stale when it does not match. Adding
these two names there would make the gate demand that a stub replace a real measurement.
`_anchorable_names` (`:330-344`) reads both lists identically for the anchor check, so
`external_partials` costs nothing and keeps `docs_gate.py build` out of the way.

## 5. Expected result

**`ops/docs-registry.json`** — exactly `ops-docs-registry.patch` in this directory:
`external_partials` grows from 2 names to 6, and `_why_external_partials` is extended to
name all three sibling generators and to state that declaring a name without writing the
file is not a fix.

**`partials/controls_evidence.md`** and **`partials/deck_facts.md`** — new, ~1.3 KB and
~1.8 KB, each the full marker-delimited block, LF, trailing newline.

**`STRATEGY_DECK.md`** — 7 lines changed, in two hunks. See `STRATEGY_DECK.expected.diff`.

*controls_evidence, exact and stable* (line 139) — `internal/maintain` **29 → 30 tests**.
A test was added to `daemon/internal/maintain` since the last injection; `ci.yml:241` is
failing on this today.

*deck_facts, six cells* — measured 2026-08-06T22:14Z:

| Cell | In the deck | Measured |
|---|---|---|
| `symbols.delisted_at` stamps | 1,886 | 1,868 |
| Daily-bar calendar sessions | 1,908 | 1,909 |
| Stock bar coverage | 90.65% — 2,637,147 / 2,909,163 | 90.66% — 2,644,647 / 2,917,106 |
| still-listed names only | 99.66% over 1,054 | 99.61% over 1,072 |
| `delisted_at` names only | 79.36% over 1,886 | 79.02% over 1,868 |
| stop printing, no `delisted_at` | 10 | 28 |

18 symbols moved from the delisted cohort to the still-listed one (1,886→1,868 and
1,054→1,072), and the survivorship-relevant "stops printing with no `delisted_at`" count
nearly tripled, 10 → 28. **Read that row before committing** — it is the deck's own
survivorship disclosure and it moved against the deck.

**The deck_facts numbers you get will not be these.** The database ingests continuously:
two runs 80 s apart already differed —

```
- | Stock bar coverage | 90.66% — 2,644,647 of 2,917,106 symbol-days over 2,940 symbols |
+ | Stock bar coverage | 90.64% — 2,649,864 of 2,923,420 symbol-days over 2,940 symbols |
```

Regenerate, read what you get, commit that. No figure in STRATEGY_DECK.md's prose restates
any of these cells (`grep -F` for each: every hit is inside the block), so no prose edit is
required. `proofs/P3D_DELISTING_GAP_2023_2025.md:147` carries the old **1,886** but it is a
dated proof artefact, not a live-claim document — leave it.

## 6. Verify

```bash
python tools/docs_gate.py check          # expect: docs-gate: clean
python tools/check_strategy_deck.py      # expect: PASS (~36,900 chars)
python tools/deck_facts.py --check --inject STRATEGY_DECK.md   # expect: exit 0
git diff --stat -- STRATEGY_DECK.md ops/docs-registry.json
git status --porcelain -- partials/      # expect: two new untracked files, add them
```

`check_strategy_deck.py` strips all three generated blocks before its number allowlist
(`:17-34`), so re-injection cannot break it — confirmed by running it against the
regenerated deck: `PASS (36925 chars)`.

## 7. Rollback

```bash
git checkout -- ops/docs-registry.json STRATEGY_DECK.md
rm -f partials/controls_evidence.md partials/deck_facts.md
```

## 8. What this does not fix

1. **docs_gate proves consistency, not freshness.** It compares the document's region to
   the partial file. Both are committed, so they agree forever until someone regenerates
   them. Freshness is proved only by `ci.yml:284`, which self-skips on any checkout
   without `data/` (`ci.yml:281`). This is pre-existing; the fix adds a second committed
   copy of whatever the numbers are at commit time.
2. **`deck_facts.py --check` cannot stay green on a live database** — §5 shows the block
   changing within 80 seconds. The operator gate at `ci.yml:284` therefore fails on almost
   any run. That is a separate design question (pin the block to a dated snapshot, or
   round the volatile cells) and it does not block this fix.
3. **A human could still copy the stale region into the partial by hand** and get a green
   `docs_gate`. The only defence is `ci.yml:241`/`:284` re-deriving from source on the
   operator's machine. Declaring the names does not weaken that; it is the same standing
   the `live_accuracy` exemption has today.

## 9. Evidence

`dryrun_mirror.py` in this directory reproduces the proof without touching the repo:
it mirrors the deck, applies the whole fix in the mirror, and asserts docs_gate's own
`check_no_hardcoded_live_accuracy` returns zero — plus two negative controls so a green
result cannot be a harness artefact.

```
$ python drafts/pending-approval/docs-gate/dryrun_mirror.py --db data/backups/signaldeck-20260806-131007.db
PASS  full fix -> 0 violations; registry-only -> 2; partials-only -> 2
 STRATEGY_DECK.md | 14 +++++++-------
 1 file changed, 7 insertions(+), 7 deletions(-)
```
