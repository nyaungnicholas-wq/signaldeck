# P0 — FREEZE & TRUTH STOP

**Filed:** 2026-08-04T17:02:58-07:00
**Phase:** P0 (blocking)
**Status:** COMPLETE
**Authority:** Remediation & Proof Plan, Rule 1 (freeze all external presentation)

---

## 1. Statement of freeze

No document in this repository may be published, presented, distributed, or quoted
externally until Phases P1–P3 are complete and this freeze is explicitly lifted by a
successor proof artifact.

No capital may be attached to any signal, model, or predictor described in this
repository. No model may be promoted. No execution or risk control may be described as
existing unless it lives in *this* repository and has a passing test.

## 2. Frozen claim classes

The following classes of claim are frozen wherever they appear, in every file:

| Class | Reason |
|---|---|
| Live accuracy figures | Four mutually inconsistent versions across four documents |
| Confidence-interval verdicts | Published in two documents while the platform withholds every interval |
| Survivorship control | Asserted closed in three documents; measured open in `ALPHA_WORKFLOW.md` §B2 |
| Point-in-time universe | Asserted verified; `universe_membership` holds zero rows (§B3) |
| Kill switch | Claimed via a file in a **different repository** |
| Position sizing | Marked "Exists"; not bound to any allocation decision |
| "Already built / verified" status | Three rows in one attestation table are contradicted by measurement |

## 3. Documents marked NOT AUTHORITATIVE

| File | SHA-256 after freeze banner |
|---|---|
| `CASE_STUDY.md` | `6231ed19afb0d1957a2d674dba1109fbdef367efcb1a785c87eef1b2c221c52d` |
| `INSTITUTIONAL_GAP.md` | `aa393c41ceff34c7370fcea46028f7cec8e5246f247c1db0c5cc27835dc05c79` |
| `PREDICTION_PROCESS.md` | `13417bc4be9d75327b5d4572250e11cbd7d6f331c83586098c7045820aad0aaa` |
| `HOW_PREDICTORS_WORK.md` | `cff4c3d1e13655f3681568d1485f625f4dc33a76c331fcaa1b0dfd3ccc482892` |

## 4. Documents marked FROZEN (banner applied, authority retained)

| File | SHA-256 after freeze banner |
|---|---|
| `ALPHA_WORKFLOW.md` | `1cf0cb3e34cef461b0a1ae3e54f146390f10244443abb509f3ca3fc0843263f3` |
| `EDGE_PLAN.md` | `45f384f9df8b26e8216e13b92a06b430b4ae38b02c05e9c651d70669814f2bbd` |
| `PAIRS_TRADING.md` | `dbcd4a06d19390c092eb7d029c8a3a57776172f89c23a158bc7e05e0d28d0780` |
| `ARCHITECTURE_EV.md` | `ff89ca7bbf6ba3d664d6ff039aec6399e6be58bd769dada30e777f156e7793c2` |
| `DATA_SOURCES.md` | `8f02b6c5b8d40996a2672f92b29934a93a5f0502836920b7dead22a5dc43907d` |
| `README.md` | `cc4ae4d44c4e05a4fb130ca69faa13a6533b3823468a0a0e4606a2cfcb79af32` |

`ALPHA_WORKFLOW.md` §B2 and §B3 are recorded as the **corroborated authority** on the
two open data-integrity defects. They are what other documents contradict, not the
other way round.

`DATA_SOURCES.md`'s licensing table and the HTTP 451 raw-export guard are **not**
frozen and remain in force.

## 5. Deliberate exclusion — `PREREGISTRATION.md`

**`PREREGISTRATION.md` was NOT given a freeze banner, deliberately.**

That document is digested into the hash-chained pre-registration table as the
`prereg-document` record, and `daemon/internal/prereg/document_claims_test.go` parses
its prose against the chain. Editing it — even to add a freeze banner — changes its
digest, contradicts the chained record, and forces an amendment that no one intended
to file. Adding a truth-stop banner would itself have been a truth-stop violation.

Its SHA-256 is recorded here as evidence it was left untouched during P0:

```
01d6bcdfe1451b6f2c84185478f9c8f3c405ef3ae845a11a937d865eacf2a724
```

The freeze applies to it by reference through this document. Any banner it needs must
be filed through the amendment path in P1, not by direct edit.

## 6. Change proof

```
 ALPHA_WORKFLOW.md      | 15 +++++++++++++++
 ARCHITECTURE_EV.md     |  9 +++++++++
 CASE_STUDY.md          | 14 ++++++++++++++
 DATA_SOURCES.md        |  9 +++++++++
 EDGE_PLAN.md           |  7 +++++++
 HOW_PREDICTORS_WORK.md | 17 +++++++++++++++++
 INSTITUTIONAL_GAP.md   | 17 +++++++++++++++++
 PAIRS_TRADING.md       |  9 +++++++++
 PREDICTION_PROCESS.md  | 17 +++++++++++++++++
 README.md              | 47 +++++++++++++++++++++--------------------------
 10 files changed, 135 insertions(+), 26 deletions(-)
```

(`README.md`'s deletion count includes pre-existing uncommitted changes from the
in-flight session; the P0 contribution to it is additive only.)

## 7. Done-when criteria

| Criterion | Status |
|---|---|
| No document can be mistaken for final | **MET** — every numeric strategy document carries a banner in its first screen |
| All known false/contradictory areas visibly fenced off | **MET** — each banner names the specific contradicting file and section |
| Chain-digested documents not corrupted by the freeze itself | **MET** — see §5 |

## 8. Lift conditions

This freeze lifts only when **all** of the following are true, each with its own proof
artifact:

1. P1 complete — grader re-registered through the amendment path (`proofs/P1_*`).
2. P2 complete — one generated live record, no hardcoded percentages
   (`proofs/P2_*`).
3. P3 complete — survivorship backfilled and `universe_membership` populated, with
   affected backtests re-run (`proofs/P3A_*`, `proofs/P3B_*`).

Partial completion does not lift the freeze on any document.
