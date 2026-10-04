# 2026-10-03 signaldeckfix ledger: everything left open after the 10-02 cleanup

Scope: Nicholas asked to "fix everything else" after the 10-02 cleanup close-out. That meant every non-VERIFIED row of
`audits/2026-10-02-cleanup-ledger.md`, the close-out reviewer's notes, and the owner items. The CI race-timeout ceiling
was fixed in a separate session (PRs #18 and #19, 8febb68 and 6a6b255) and is recorded here only as it reached this
trunk.

Decisions are Nicholas's, taken by AskUserQuestion on 2026-10-03. Live revision at close: daemon 7cdfba5; trunk
4827993 plus this ledger.

| ID | Cat | Status | Item | Evidence |
|---|---|---|---|---|
| SD30-LABEL | CODE | VERIFIED | Option (b), the recommended readings: base = the issue day's own bar from 2026-10-04 (crypto and in-session rows included); 1w = calendar week; after-hours residual accepted; build, file, deploy | 38befcb + 49a8cf6. Chain seq 137 label-window-reregistration, filed 02:51Z (guard: 0 resolved, 462 pending per 1d/1w horizon). Registrar seq 138 grading-protocol, 139 prereg-document, 141 grading-protocol (crash fix). PREREGISTRATION.md section 15. Deploy VERIFIED 4441620, then 7cdfba5. Fresh review found two record-text errors, fixed before filing |
| SD30-WITHHOLD | WAIT | WAIT | Lift publication.SD30Withheld | Only after the new 1d window has 10 credible days AND Nicholas says yes. Before lifting, epoch-filter the two all-time readers (api/confidence.go DirectionalRecord call with since=0; api/postmortem.go), at the caller, never inside DirectionalRecord (model health grades lifetime on purpose) |
| SD30-PROBE | WAIT | WAIT | Re-run tools/sd30_label_window_probe.py on new-window rows | About 3 trading days after 10-04 (about 10-08). Expect stock visible-move agreement at or below ~57% (was 93.8%), no visible crypto window, and the 1w agree/disagree split closed. The probe needs the settle_ts-bar update for new rows first (planner step 6) |
| GRADER-EMPTY | CODE | VERIFIED | The first grade after seq 137 crashed: a None excluded_fraction was formatted on an empty window | ba34379. test_report_survives_an_empty_window fails on the unpatched grader with the same TypeError. Re-pinned at seq 141; the 20:03 grade published and /api/accuracy is OK |
| REPRO-RECUT | CODE | VERIFIED | Re-cut repro, re-freeze frozen verdicts | 4827993. --verify identical to the live DB; --verify-complete: seq 141 pins the running grader. Structural first-call timestamps unchanged; directional_days is header-only. Re-freeze with per-row reasons |
| H1-REVOID-APPLY | DATA | VERIFIED | Apply score-1h-revoid (owner yes) | 5,551 stock 1h rows voided in one transaction at 10-02 23:59 PT. Old values in data/backups/score-1h-revoid-20261003.csv (5,551 rows). Re-run finds 0 |
| CRNX-NEVER-SETTLE | CODE | VERIFIED | 30-day void for score rows whose forward bar never settles (owner yes) | 1f73d86 (OutcomeResolver clock; test fails if reverted). Reversal record data/backups/crnx-pending-over30d-20261003.csv (1,485 rows pending over 30 days before deploy): all 1,485 VOID after the first post-deploy pass |
| VACUITY | CODE | VERIFIED | Seeded fallback only while the window is young (owner: accept) | 1f73d86 + 49a8cf6. Young = directional-ensemble (1d) missing or INSUFFICIENT/PENDING. The directional-presence test allows an empty set only within a week of the snapshot's epoch (4827993). Mutations red |
| R5-MCP-SUMMARY | CODE | VERIFIED | MCP theHonestSummary said "tested live and FAILED" beside withheld figures | 1f73d86. The withheld twin swaps it; flag off restores it; mutation red |
| SD30-ANCHORS-JSON | CODE | VERIFIED | Raw registry published unaltered, plus a README note (owner: option a) | 7cdfba5. render_track_record prints the note only under SD-30; test asserts it; mutation red. Takes effect on the next anchor-publish |
| SD30-REASON | CODE | VERIFIED | publication.SD30Reason no longer says the decision is pending | 49a8cf6. Documents regenerate from it at each grade |
| BASIS-EPOCH | CODE | VERIFIED | store.BasisEpoch bumped per its contract (non-comparable label) | 49a8cf6, now 1791072000. Rows written 10-04 00:00Z to deploy carry the old value; the ts cutoff is authoritative |
| OFFSITE-PRUNE | CODE | VERIFIED | gh_offsite_prune never deleted anything (`IFS= read`), so 12 releases (11.8 GB) were kept against KEEP 7 (owner: land it) | 4441620. ops/test-offsite-prune.sh: 8/8 pass, and restoring the bug fails 6; added to CI. First live prune at the next market-close backup (Mon 10-05 13:10 PT) deletes 0916, 0918, 0921, 0922, 0923; the empty 10-01 release is kept and not counted |
| OFFSITE-PRUNE-LIVE | WAIT | WAIT | Confirm the first real prune | After 10-05 13:22 PT: `gh release list --repo <offsite>` should show 7 asset-carrying backups plus the empty 10-01 |
| AUTH-TRADEOFF | DECISION | DECIDED | Sign-up timing oracle and verify-link shape | Nicholas: accept for now; fix with a future auth refactor |
| CRYPTO-PROOF | DECISION | DECIDED | Proof routes keep crypto; member product stays equities | Nicholas: keep both |
| MEMBER-COPILOT | DECISION | DECIDED | SIGNALDECK_MEMBER_COPILOT | Nicholas: keep off until a stable hostname and Turnstile exist |
| MIN-SIZE | DECISION | BLOCKED | Enforce minimum size in code (owner: enforce in code) | Ambiguous on a money path, so not guessed. In the execution-layer repo "min size" is the readiness ladder's small first step ($500 rising to $10k, overridden 09-01 to start at $10k), but the question was put as "minimum order size". These are different guards. Needs: which one, and its value |
| SD57-LIVE | OPS | VERIFIED | Market close restarts the daemon before the offsite upload | Fired 10-02: stopped 13:10:04, restarted 13:14:37 after the DB phase, offsite OK 13:22. Downtime 4m33s (was about 12m) |
| LIVE-TRIGGER-WAIT | WAIT | WAIT | SD-06, 08, 10, 12, 14, 15, 16, 19, 20, 22, 29, 41, 42, 43, 46, 47, 59 | Swept 10-02: none has met its trigger since its fix went live (no sign-up/forgot/reset/verify traffic, no LLM 400, no holiday, and so on); none fired badly. Dates: SD-29 11-25, SD-59 10-05, SD-22 about 10-18, SD-16 2027-03-14 |
| R3-SIGNIN-WATCH | WAIT | WAIT | Sign-in latency under TRUNCATE retries | 0 sign-ins since batch 3 (05:37 10-02); last 10-01 22:49 at 3.5 s. Unmeasurable until someone signs in |
| R4-STAGGER-RESTARTS | OPS | VERIFIED | Heavy workers' first runs after boot | 20:59 boot: all 14 heavy workers ran by +29.5 min |
| TRACKRECORD-CACHE-OVERREFUSE | CODE | NOT APPLICABLE | Gated track record held by the shared cache | Measured bounded: 2-minute TTL plus the 60-second warmer, about 3 minutes in market and off hours. Unbounded only if rebuilds keep failing; uncached refusals are now capped at 10 minutes (d2e9948) |
| WAL-GROWTH | OPS | WAIT | WAL reached 4.19 GB at 20:46 10-02 with TRUNCATE failing | That ran on 4b3ec10, before batch 4. On d2e9948 and later the WAL stays bounded (peak 133 MB, truncates to 0). If an identical-frame stall returns, wal_checkpoint_starved names the running workers; the remaining suspect is a continuous worker holding one snapshot (crypto-live, stock-streamer, backfiller, research-engine, research-loop) |
| DAEMON-DEATH-1945 | OPS | NOT APPLICABLE | No shutdown line at 19:45 10-02 | A Windows restart from the Start menu (System event 1074 at 19:44:59; 7 scheduled tasks terminated in the same 3 ms). Every no-stop-line orphan sweep in 7 days matches a 1074 event. Optional: a shutdown script running `signaldeck-ctl.sh stop` |
| DEAD-REGIME-READER | CODE | NOT APPLICABLE | ResolvedRegimeOutcomes is dead in production | It is the read helper for 9 test files; deleting it would mean rewriting them for nothing |
| GENERATION-ROW | DECISION | NOT APPLICABLE | O(1) collapse fingerprint via a trigger-kept row (schema change) | The exact fingerprint cache already answers in 0.8-1.3 s; a schema change for this would be speculative |
| BARS-REVISED | NOTE | NOTED | 16.8% of 1d stored score labels no longer recompute from today's bars | Data-quality fact. It is why SD-30 does not backfill; no action |

Still owner-only (unchanged from 10-02): ANCHORS-PUBLIC, ANCHOR-6H, TUNNEL-BOOT, HOSTNAME (before the 11-01 price rise), B5,
B6-ENV, B3 host uptime, B7 old backups by hand, SD-27 Turnstile keys, SD-52 SMTP rotation, STEP7-LIVE, COUNSEL, STEP10,
DEPENDABOT, STEP7-PAPER, BEST-CALLS, HOSTING, SD-54 FINRA terms, RV A1-A6.
