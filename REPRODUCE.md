# REPRODUCE — regenerate every published number from this repository alone

The live database (`data/signaldeck.db`) is gitignored, so a checkout used to
contain the grading *code* but none of the grading *inputs* — no published
number was reproducible by anyone else. This file and the committed snapshot in
`repro/` close that gap: the minimal inputs behind every published accuracy
interval ship with the repo, content-hashed so a modified snapshot refuses to
grade.

## One command

```sh
python3 tools/accuracy_registry.py --snapshot repro
```

This regenerates the full accuracy registry — every predictor row, its live n,
accuracy, 95% CI (printed to three decimals), design effect, and verdict — from
the committed CSVs, with no database and no network. The grader first verifies
each CSV against `repro/MANIFEST.json`; a single edited cell aborts the run.

Append `--json out.json` for the full-precision rows. Against the same
snapshot, output is bit-identical to a database grade with one pinned
exception: `claimed` backtest accuracies round-trip at 10 significant digits
(`%.10g`, the repo-wide canonical float format — see
`daemon/internal/datasetver`), which cannot move any printed value or verdict.

## What is in the snapshot

| file | contents |
|---|---|
| `repro/directional_days.csv` | per-(horizon, UTC-day) tallies behind the directional-ensemble rows: n, correct, up-days, plus the high-conviction (\|p−0.5\| ≥ 0.15) slice |
| `repro/structural_days.csv` | per-(kind, horizon, call-day) resolved tallies behind the structural rows |
| `repro/structural_claims.csv` | each structural predictor's frozen backtest claim, forecast count, and first-call timestamp (drives the PENDING dates) |
| `repro/pairs_inputs.csv` | dataset **versions** (not data) of every per-symbol series the pairs study consumes: bounds, row count, SHA-256 |
| `repro/xsfactor_inputs.csv` | dataset **versions** of every per-symbol series the cross-sectional factor derivation consumes: active flag, day bounds, row count, SHA-256 |
| `repro/grading_protocol.csv` | the newest pre-registered **grading protocol**: the grader's pinned SHA-256 and commit, and the frozen evidence floors (`minIndependentN`, `minDistinctDays`, `minDistinctBlocks`) |
| `repro/MANIFEST.json` | hash per CSV, generation time, and the git commit the snapshot was cut at |

These are day-level tallies and hashes — dozens of rows per predictor, **no
OHLCV bars** — so nothing here redistributes licensed market data (the A10
data-license guard is not implicated).

The tallies are produced by the *same* functions the database grade uses
(`fetch_directional_days` / `fetch_structural` in `tools/accuracy_registry.py`),
so the snapshot cannot drift from the SQL that defines the published numbers,
including the independence dedup (one observation per symbol, horizon, UTC-day)
and the 2026-07-24 survivorship boundary. `TestSnapshotRoundTrip` in
`tools/test_accuracy_registry.py` pins DB-grade ≡ snapshot-grade.

### The snapshot refuses to publish a verdict from an unregistered grader

The database grade has always refused to compute a single number unless the
pre-registration chain names *that exact grader file* and *those exact evidence
floors* (`require_registered_grader`). That gate used to exist only on the
database path — the path nobody outside this machine can run — while
`--snapshot`, the path this document hands you, checked nothing and printed
verdict strings anyway. The chain also lived only in the gitignored database, so
no outside party could verify the pin from any artifact at all.

`repro/grading_protocol.csv` now carries that pin inside the bundle, hashed in
`MANIFEST.json` like every other file, and `--snapshot` runs the identical
check against it:

* **pin names different code** (`graderSha256` ≠ the running file's digest) or a
  different floor → the grade **aborts**, exactly as the database path aborts;
* **no record in the bundle** (an empty or legacy file) → the grade runs but the
  `verdict` field is **dropped from every row**, the same treatment a missing
  frozen target already gets. An absent verdict cannot be quoted as one.

If a grade refuses, the drift is the finding. The fix is to re-register the
grader through the normal pre-registration process and re-cut the snapshot —
**never** to hand-edit `graderSha256` to the drifted digest, and never to append
a chain record for the purpose of silencing the refusal.

## Hash scheme

Every hash is the canonical scheme from `daemon/internal/datasetver`:

```
sha256( "v1|{name}|{kind}|{N}\n"  +  for each record: f1|f2|…|fk|\n )
```

with floats pinned to `%.10g`. `datasetver.HashRecords` (Go) and
`hash_records` in `tools/make_repro_snapshot.py` (Python) implement it byte for
byte; `TestHashRecordsMatchesPythonImplementation` in
`daemon/internal/datasetver/datasetver_test.go` holds a shared vector so the
two cannot drift silently.

## Pairs study (tools/pairs_trading.py)

The pairs study consumes raw daily bars, which are licensed and cannot be
committed. `repro/pairs_inputs.csv` instead versions every input series: one
row per symbol with the SHA-256 of its canonical `(ts, close, volume)`
serialization (kind `pairs-1d-close-volume`, same filter the study applies:
`close` present and > 0, ordered by ts). To reproduce the study:

1. Obtain your own licensed daily bars and load them into the schema.
2. Hash your series with `hash_records` and compare against
   `repro/pairs_inputs.csv`. Matching hashes mean you hold the identical
   dataset; only then are result differences attributable to code.
3. Run `python3 tools/pairs_trading.py` (deterministic: fixed seed 12345,
   block bootstrap included). The shipped result is embedded at
   `daemon/internal/pairsstudy/result.json`.

## Cross-sectional factor result (tools/xsfactor_edge.py)

The factor-edge constants in `daemon/internal/xsfactor/edge.go` are re-derived
by `tools/xsfactor_edge.py`, and the full derivation ships at
`daemon/internal/xsfactor/derivation.json`. Like the pairs study, its input is
raw daily bars, which are licensed and cannot be committed —
`repro/xsfactor_inputs.csv` versions every input series instead: one row per
symbol with the SHA-256 of its canonical `(UTC-day, close, volume)`
serialization (kind `xsfactor-1d-day-close-volume`), under exactly the study's
universe (`--universe all`: every stock ever tracked, including delisted names;
series shorter than 22 bars are never constructed) and the `active` flag that
drives its `--universe active` split. `TestXsfactorSnapshotRoundTrip` in
`tools/test_accuracy_registry.py` pins the snapshot writer against the study's
own loader — same universe, same floor, same bytes — so the two cannot drift
silently. To reproduce the result:

1. Obtain your own licensed daily bars and load them into the schema.
2. Hash your series with `hash_records` and compare against
   `repro/xsfactor_inputs.csv`. Matching hashes mean you hold the identical
   dataset; only then are result differences attributable to code.
3. Run one command (deterministic: seeded day-clustered bootstrap, seed 12345):

```sh
python3 tools/xsfactor_edge.py --out derivation.json
```

The output must match `daemon/internal/xsfactor/derivation.json`, and the
report prints measured-vs-shipped verdicts for every constant in `edge.go`.

## Verify the anchors — the third-party check

Everything above proves the published numbers follow mechanically from the
committed inputs. It does not prove those inputs and claims existed when the
operator says they did — that is what the public anchors repository is for,
and external timestamping only counts as credibility if a stranger can
actually run the check. This section is that check. It needs a checkout of
this repo, a clone of the anchors repo, `python3`, `jq`, and `shasum` — no
database, no running daemon.

### 0. Clone the public anchors repo

> **NOT YET TRUE, as of 2026-07-27.** No public anchors repository exists, and
> no ledger anchor or pre-registration chain head has ever been externally
> timestamped. `ops/anchor-publish.sh` is built and exercised end-to-end
> against a local clone, but every scheduled run to date published nothing:
> the first four failed to obtain an anchor line at all (fixed 2026-07-27 by a
> SQLite fallback that recomputes the publish line with the daemon down), and
> there is no remote to push to. Until a third party's git history holds these
> files, treat every anteriority claim in this repo — including the
> pre-registration in `PREREGISTRATION.md` — as **operator-asserted and
> unverifiable**, and this entire section as a description of what the script
> would produce rather than of evidence that exists. Everything in sections 1
> through 4 below stands on its own; only the external-timestamp step is
> missing.

Once configured, `ops/anchor-publish.sh` pushes five artifacts to a public git
repository on every run (the remote is operator-configured via
`SIGNALDECK_ANCHOR_REPO`; no URL can be cited here because no such repository
has been created):

| file | contents |
|---|---|
| `anchors.log` | append-only signed ledger-anchor publish lines |
| `prereg.log` | append-only pre-registration chain heads |
| `accuracy_registry.json` | the full registry regenerated at each publish — FAILED verdicts included, nothing filtered |
| `PREREGISTRATION.md` | verbatim copy of the frozen 2026-08-14 grading protocol |
| `README.md` | human rendering of the registry JSON, never hand-edited |

The evidence is not the files — it is their **git history on a host the
operator does not control**. Take commit dates from the hosting provider's
push metadata (e.g. the GitHub commits API), not from committer timestamps
alone, which the pusher sets.

### 1. Recompute the registry from the committed snapshot

```sh
python3 tools/accuracy_registry.py --snapshot repro --json /tmp/local.json
```

Then diff it against the published registry at the anchors-repo commit
contemporaneous with the snapshot cut (`repro/MANIFEST.json` records the
snapshot's `generated` time and `git_commit`):

```sh
cd path/to/anchors-repo
git log --format='%H %cI' -- accuracy_registry.json    # pick the publish nearest MANIFEST's "generated"
git show <commit>:accuracy_registry.json > /tmp/published.json
jq -S 'del(.generated, .survivorship_bound)' /tmp/local.json     > /tmp/a.json
jq -S 'del(.generated, .survivorship_bound)' /tmp/published.json > /tmp/b.json
diff /tmp/a.json /tmp/b.json
```

`generated` is wall-clock and `survivorship_bound` is owned by
`tools/backfill_delistings.py` (carried forward at publish, absent from a
fresh snapshot grade); every other field must match. Reading a non-empty
diff: **frozen fields** (`claimed`, first-call timestamps, the survivorship
epoch) must never differ at ANY pair of commits — a difference there is
rewriting, not drift. **Live tallies** (`live_n`, accuracy, CI, verdict) may
differ only when outcomes resolved between the snapshot cut and the publish,
and `live_n` only grows.

### 2. Check the ledger-anchor line in `anchors.log`

Each line is

```
SIGNALDECK-LEDGER-ANCHOR v1 seq=<S> count=<N> ts=<unix> digest=<64 hex>
```

with `digest = sha256(message ‖ 0x1E ‖ sig ‖ 0x1E ‖ pubkey)` over the signed
message
`signaldeck-ledger-anchor|v1|alg=ed25519|created_at=…|ledger_seq=…|ledger_count=…|head=…`.
The exact byte layout is `Message` / `Digest` / `PublishLine` in
`daemon/internal/ledgeranchor/ledgeranchor.go` — a few dozen lines of stdlib
crypto, re-implementable from the file alone. A stranger verifies two things:

- **Anteriority.** `git log -S 'digest=<hex>' --format='%H %cI' -- anchors.log`
  dates the line's first appearance; that date (cross-checked against push
  metadata) upper-bounds when the ledger head existed.
- **Binding.** Given the full anchor record — `GET /api/ledger/anchors` on a
  running daemon, or an export the operator provides on request — recompute
  `Digest()` and verify the Ed25519 signature over `Message()`. A recomputed
  digest equal to the published line means the record shown is the record
  that was timestamped; a swapped-in record cannot match.

What this cannot prove without the database: that the head hash summarizes
honest ledger rows. The anchor pins WHEN the chain head existed; step 1 pins
WHAT the committed tallies imply. Anteriority does not extend before the
first anchor, and the `/api/ledger/anchors` payload says so itself.

### 3. Check the pre-registration chain head in `prereg.log`

Each line is `prereg seq=<S> head=<64 hex>`. Per `PREREGISTRATION.md` §7:

1. `shasum -a 256 PREREGISTRATION.md` in the anchors repo must equal the
   `specHash` of the `prereg-document` record in `GET /api/prereg` (or an
   operator-provided chain export) — and the file must be byte-identical to
   this repo's `PREREGISTRATION.md`.
2. Recompute the chain: `entry_hash = sha256(prev_hash ‖ 0x1E ‖
   "ts=…|kind=…|specHash=…|note=…")` over every record; the head must equal
   the newest `prereg.log` line.
3. The commit that introduced that line must predate **2026-08-14**, the
   frozen first grading date. A claim timestamped after its outcomes exist is
   not a pre-registration.

## Maintainers — cutting a new snapshot

```sh
python3 tools/make_repro_snapshot.py            # regenerate repro/ from the live DB
python3 tools/make_repro_snapshot.py --verify   # committed snapshot vs DB right now
```

Re-cut and commit the snapshot whenever new outcomes resolve. `--verify`
reporting a changed hash **with no new rows** means history was rewritten
inside a graded range — regrade before republishing any number, exactly as
`datasetver.Compare` treats a revised bar range.
