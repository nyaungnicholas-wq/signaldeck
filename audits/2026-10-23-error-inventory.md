# SignalDeck error inventory, 23 October 2026
Generated: 2026-10-23T05:08:04.6879687-07:00

This is the public copy of the inventory. Evidence figures derived from licensed market data (Alpaca) or from forecasts graded on it, such as accuracies, percentage-point effects, return figures, prices and counts of graded or resolved predictions and outcomes, are withheld and replaced with a neutral phrase. Row ids, statuses, release states, fix SHAs and owners are unchanged. The owner holds the full copy privately.

Phase 0 error inventory for the SignalDeck public-launch brief of 23 October 2026. It merges the 157 items from 8 read-only Phase 0 investigations (gates, logs-runtime, routes, congress, survivorship, kit-claims, prod-walk, local-walk) into deduplicated rows, keeping the strongest evidence and listing every contributing source, and adds rows from the data-rights research only where a licence or competition rule needs action. Statuses follow the 23 October brief's vocabulary (OPEN, IN PROGRESS, FIXED, BLOCKED, DEFERRED, NOT REPRODUCED). Release state (NOT SHIPPED, STAGED, DEPLOYED, ROLLED BACK, N/A) is tracked separately from status; a row is marked FIXED only with a recorded validation, and DEPLOYED only with a fix SHA. Counts are computed by the renderer from the structured status and release_state fields, not from text search.

Baseline facts and expected states (not rows). At f37c9595 every CI job passes locally and on GitHub run 39906199230 (go test 134 packages ok, coverage 73.0% against a 70.1 floor, web 150/150, Python 484 tests OK, docs-gate strict clean), and production runs daemon 2ddff1a2 (300/300 newest worker_runs) with web build 2yUqzpcRlvKGJgtYWIyRK. Authorization holds in both probed postures: anonymous callers get 401 on member and operator APIs, members get 403 on operator APIs, member responses strip licensed price fields, and the 26 verified live page loads had no page errors or horizontal overflow. Scientific refusals and withholdings are expected results, not defects: calibration refusals (rank-collapsing maps; count withheld under the data licence), benched anti-predictive models, the retired 1d model's coverage starvation, volatility verdicts INSUFFICIENT (fewer graded days than the 60 required), the 1d track record WITHHELD (SD-30), and REFUSED_STALE on a stale backup copy. Open crypto prediction_outcomes are 0 as required while open regime and score outcomes drain by design (counts withheld under the data licence), the delisting detector's 25-session wait is by design, and the read-only replica of check-grader-health passes (heartbeat fresh, WAL 65.6 MB, a small fraction of forecasts unledgered (counts withheld under the data licence)).

## Counts

| Status | Rows |
|---|---:|
| OPEN | 0 |
| IN PROGRESS | 3 |
| FIXED | 46 |
| BLOCKED | 28 |
| DEFERRED | 55 |
| NOT REPRODUCED | 11 |
| **Total** | **143** |

| Release state | Rows |
|---|---:|
| NOT SHIPPED | 56 |
| STAGED | 0 |
| DEPLOYED | 39 |
| ROLLED BACK | 0 |
| N/A | 48 |
| **Total** | **143** |

## Rows

| ID | Title | Severity | Phase | Owner | Status | Release | Fix SHA | Dependency |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| SD23-105 | Production host memory/CPU starvation from other workloads on the shared PC stalls the daemon and web origin (Cloudflare 524s, timeouts) | blocker | ops/host | owner | BLOCKED | N/A |  | owner: Owner keeps heavy jobs off through Mon 26 Oct 09:00 PT. Engineering re-probes Sat 03:00 PT. |
| SD23-015 | Public /accuracy shows 'Temporarily unavailable - UNREACHABLE': /api/accuracy 503 or timeout for most requests since 22 Oct 22:00 PT | high | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-016 | Daemon overloaded since 22 Oct 22:00 PT: prediction-runner and composite-scorer hit the 30-minute deadline, 6 workers stale, multi-second write-lock holds | high | 1 repairs | engineering | NOT REPRODUCED | N/A |  | Sat 24 Oct 03:00 PT: re-read data/health.json and worker_runs (mode=ro). If stale workers recur with the host idle, open a post-deadline investigation of the lock-hold sources (SectorRotator SetMeta, news ingest). |
| SD23-043 | Alpaca licence bars public display of its market data, news and derivations; no written consent obtained | high | 2 price rights | owner | BLOCKED | N/A |  | owner: Owner decides whether to send (owner step 7). Save any reply in 3-legal. |
| SD23-044 | Price-feed replacement: no free source clears the public-display bar | high | 2 price rights | owner | BLOCKED | N/A |  | no free consolidated source is permitted for public display; IEX HIST needs owner acceptance of its ToU and is IEX-only/T+1; no full regular session possible before the 25 Oct freeze |
| SD23-045 | Anonymous bulk download of every model forecast (Alpaca-derived, plus Kraken-derived crypto) via /api/ledger/range | high | 2 price rights | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-066 | Member tier is effectively public: open sign-up is ON, so anyone with a verified email reaches per-symbol Alpaca-derived forecasts and congressional trades | high | 5 public access | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-076 | Published survivorship_clean depends on whether the 14:05 grade overlaps the daily sweep, not on listing status (today's grade will publish false again) | high | 6 survivorship | engineering | IN PROGRESS | DEPLOYED | 73e69248 | The pinned registry flag stays timing-dependent by design (accuracy_registry.py is sha-pinned); survivorship-status v1 (shipped 73e69248) classifies the same symbols; first scheduled run Sat 24 Oct 14:05 PT |
| SD23-077 | Registry's 'inactive with no delisted_at' counts (direction and structure) are lean-mode watch exclusion, but public docs present them as unknown listing status | high | 6 survivorship | owner | FIXED | DEPLOYED | 73e69248 | none |
| SD23-078 | Survivorship bound is measured every grade cycle (10-22: an accuracy-inflation bound in percentage points, figure withheld under the data licence) but dropped from the published registry | high | 6 survivorship | owner | BLOCKED | NOT SHIPPED |  | owner: Unblocks only with Alpaca written consent (SD23-043). Then add a new versioned measure. |
| SD23-086 | Cloud migration: Oracle Always Free account creation is owner-only and age-gated; cutover deferred | high | 8 cloud | owner | DEFERRED | N/A |  | owner/age gate: an eligible adult must hold the account; cutover deferred past the freeze |
| SD23-087 | Ops layer is Windows-only: service control and deploy are schtasks-only, release/guard/health/alerting are PowerShell, the schedule is Task Scheduler XML | high | 8 cloud | engineering | DEFERRED | NOT SHIPPED |  | engineering: Address with SD23-086. |
| SD23-091 | Narration is scheduled before the public-launch phases ship, and those phases will make several kit claims false | high | 9 handoff | owner | FIXED | N/A |  | none |
| SD23-106 | Production host powered off by the user 3 times in 7 days (~49 h down, including market sessions on 10-19, 10-20, 10-21) | high | ops/host | owner | BLOCKED | N/A |  | owner: Owner keeps the PC on through the freeze and judging (owner step 14). |
| SD23-107 | Member digests and alerts can carry Alpaca-derived content to non-operators by email/Telegram | high | 5 public access | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-108 | Web pages must show an honest withheld state (not "unreachable") when content is withheld for licence reasons | high | 5 public access | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-109 | Slow market-close backup overlapped the 13:15 refresh sweep and left the daemon down from 13:30:50 (public site degraded) | high | ops/host | engineering | DEFERRED | NOT SHIPPED |  | Daily-Refresh and Market-Close run weekdays only: next possible overlap Mon 26 Oct 13:10, after the deadline; fix = the sweep defers while the market-close maintenance lock is held (or market-close-restart restarts after a concurrent sweep) |
| SD23-141 | R2 heads on a stale base would have reverted R1 (NVIDIA) | high | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-001 | Daemon toolchain go1.26.6 carries 10 reachable Go stdlib vulnerabilities (fixed in go1.26.9/go1.26.10) | medium | 0 baseline | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-002 | CI has no Go vulnerability gate (npm audit gate exists, govulncheck does not) | medium | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: Tue 27 Oct: add a pinned govulncheck step to the daemon job. Run it report-only for one week, then make it blocking. |
| SD23-003 | Operator health views unchecked: /api/source-health, /api/version and the degraded/ready reasons need the API token | medium | 0 baseline | owner | BLOCKED | N/A |  | an operator run with the token through an owner-approved mechanism |
| SD23-017 | Public default branch main lags public-launch by 2 commits and still pins vulnerable next 16.3.8 | medium | 1 repairs | owner | FIXED | N/A | 28449999 | none |
| SD23-018 | Retention prunes keep skipping on read timeouts, so the hot DB is over its size budget and gap-fill is paused | medium | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Tue 27 Oct, once the host is relieved: re-measure prune skips. Then either give archive reads their own deadline or raise the budget with owner sign-off, and VACUUM in a deploy window. |
| SD23-019 | Public ledger proof /api/ledger/verify returns 503 'warming'; /proof hash-chain and pre-registration cards stay skeletons | medium | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Sat 24 Oct 03:00 PT: probe /api/ledger/verify 5 times, 60 s apart (read-only), to size it. Post-deadline (by 2026-11-06): serve the last completed verification stale-while-revalidate keyed on the ledger head, with a test. |
| SD23-020 | DST: every SignalDeck task trigger has a fixed -07:00 StartBoundary, so after 2026-11-01 tasks fire one hour earlier local time | medium | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: By Thu 29 Oct: regenerate ops/tasks/*.xml with offset-free local StartBoundary and update ops/install-windows-tasks.ps1. The owner runs it elevated before Sun 1 Nov 02:00 PT. Fold in SD23-027 and WakeToRun (SD23-083). |
| SD23-021 | Two demo-noted UI layout defects still unfixed in the repo (home live-record table width; /accuracy STRUCTURAL CLAIMS column clipped) | medium | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: fix the CSS on the operator view and verify by screenshot. |
| SD23-046 | Anonymous Alpaca-derived aggregates served and rendered on public pages (/api/track-record, /api/vol-forecast/record, /api/accuracy) | medium | 2 price rights | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-047 | IEX HIST terms need owner acceptance (browse-wrap with an indemnity) before HIST can be ingested or shown | medium | 2 price rights | owner | BLOCKED | N/A |  | owner: Owner decision, recorded after the deadline. |
| SD23-048 | Crypto (Kraken-derived) content remains on anonymous and member surfaces through publicRoutes despite 'crypto is not part of the member product' | medium | 2 price rights | counsel | FIXED | DEPLOYED | 73e69248 | none |
| SD23-050 | Kadoa licence RESTRICTED: keyless automated fetch continues and /api/congress serves Kadoa-compiled rows to members; the Restricted class governs no route | medium | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | Kadoa written permission (owner) to resume; House/Senate replacement not built (rights UNCLEAR) |
| SD23-051 | Terms page and public docs say older congress rows came from House/Senate Stock Watcher; the evidence says none did | medium | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | Kadoa written permission (owner) to resume; House/Senate replacement not built (rights UNCLEAR) |
| SD23-052 | Members are told congress data comes from 'free Stock Watcher community mirrors' while Kadoa serves it; chamber status reads ok:true with no source field | medium | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-053 | Official House Clerk / Senate eFD ingest does not exist; Kadoa is the only live congress upstream | medium | 3 congress | owner | BLOCKED | NOT SHIPPED |  | owner: Owner or counsel decides; build only after that, with an offline backfill. |
| SD23-054 | House Clerk disclosures rated UNCLEAR for public display: 5 U.S.C. 13107 'commercial purpose' is undefined | medium | 3 congress | counsel | BLOCKED | N/A |  | counsel: Owner sends the counsel packet (owner step 20) if wanted. |
| SD23-055 | Senate eFD access needs a per-session click-through acknowledgment; automated acquisition rated UNCLEAR | medium | 3 congress | owner | BLOCKED | N/A |  | owner: Owner decision (owner step 9). |
| SD23-056 | Congress dedup ID is wrong both ways: one filing from two upstreams gets two IDs, and distinct line items collapse to one | medium | 3 congress | engineering | DEFERRED | NOT SHIPPED |  | engineering: Address in the replacement-source design. |
| SD23-057 | Senate coverage from Kadoa is thin and shrinking (5 disclosures in October); one filer is 75% of senate rows; filer names arrive truncated | medium | 3 congress | external | DEFERRED | N/A |  | external: Revisit only if Kadoa or an official source is re-enabled. |
| SD23-064 | NVIDIA trial API used in production: 448 LLM calls in 24 h (sentiment tagger, risk watcher, market analyst, briefing); trial terms bar production use | medium | 4 NVIDIA | engineering | FIXED | DEPLOYED | 2970a6a8 | none (observation through 13:30 PT pending) |
| SD23-065 | NVIDIA-LLM sentiment over Alpaca news can be an input to the forecasts served anonymously | medium | 4 NVIDIA | engineering | FIXED | DEPLOYED | 2970a6a8 | none (observation through 13:30 PT pending) |
| SD23-067 | Dormant ngrok tunnel task is a second public ingress straight to the daemon port; the kit's hosting claim omits it | medium | 5 public access | owner | BLOCKED | N/A |  | owner: Owner decides by Sun 25 Oct 12:00 PT. Then FIX_KIT SUBMISSION.md:47 to name the real ingress (Cloudflare tunnel only, or plus ngrok for alerts). |
| SD23-068 | Latent config hazard: SIGNALDECK_PUBLIC_READS=true on this published deployment would open about 120 GET routes, including close-carrying ones, to anonymous visitors | medium | 5 public access | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-069 | Member-QA recipe tests a different anonymous surface from production (PUBLIC_SURFACE=1 vs absent) and omits SIGNALDECK_ALLOWED_HOSTS | medium | 5 public access | engineering | DEFERRED | N/A |  | engineering: Before the next member QA (post-deadline): update memory project_signaldeck_members_proof. No PUBLIC_SURFACE; add SIGNALDECK_ALLOWED_HOSTS=127.0.0.1:<port>; set SIGNALDECK_PUBLIC_URL so published() is true. |
| SD23-079 | Sweep reactivation bypasses the delisted_at guard and reactivates all 1,902 stamped stocks every weekday | medium | 6 survivorship | engineering | DEFERRED | N/A |  | investigated 23 Oct: the sweep reactivation is the only path that re-checks stamped stocks (guarding it would make every false stamp permanent; BURU evidence); documented in ops/signaldeck-refresh.sh comment (73e69248); narrowing it is a design call |
| SD23-080 | Revalidation 'active-only vs survivorship-clean' percentage-point effect splits on the watch flag, so lean-excluded live names are labelled delisted/inactive | medium | 6 survivorship | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-081 | universe_membership (the point-in-time denominator) has not been rebuilt since 2026-08-22 and nothing schedules it | medium | 6 survivorship | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: schedule `sdmaint build-universe` weekly and add a freshness check to docs_gate. |
| SD23-088 | No SignalDeck cloud kit exists: the Docker path is a Fly.io-era demo without the production schedule, and the memory's Ubuntu kit belongs to stock-trader | medium | 8 cloud | engineering | DEFERRED | NOT SHIPPED |  | engineering: Address with SD23-086. |
| SD23-092 | Authorship statement in public docs/COMPETITION.md reversed on 22 Oct; the earlier wording stays readable in public history | medium | 9 handoff | owner | BLOCKED | N/A |  | owner: Owner prepares to explain if asked (CHECKLIST a.3) before submitting. |
| SD23-093 | CHECKLIST.md line 29 still describes the old shared-authorship COMPETITION.md wording and contradicts lines 31-32 | medium | 9 handoff | engineering | FIXED | N/A |  | none |
| SD23-094 | SUBMISSION lists 'the operator's own TradingView alerts' as a data source in use; since 07ace1b2 they are retired as a model input | medium | 9 handoff | engineering | FIXED | N/A |  | none |
| SD23-095 | Congressional district and state not recorded in the owner kit; eligibility unconfirmed | medium | 9 handoff | owner | BLOCKED | N/A |  | owner registration on congressionalappchallenge.us confirms the district |
| SD23-096 | Silent demo capture never watched end to end; it predates later web commits and shows /accuracy, which is UNREACHABLE live | medium | 9 handoff | owner | BLOCKED | N/A |  | owner: Owner chooses option (a) or (b) of owner step 1 by Sat 24 Oct 10:00 PT. Under (b), re-record with the demo mode (DEMO_MODE.md), and Claude aligns DEMO_SCRIPT (SD23-091). |
| SD23-097 | Five plaintext off-site backup releases remain; the backup key's offline copy is not confirmed | medium | 9 handoff | owner | BLOCKED | N/A |  | owner: Owner copies backup.key to USB by 27 Oct, and either deletes the plaintext releases or lets them rotate out about 28-29 Oct. |
| SD23-098 | CAC rules require the demo video to be set to 'public' on YouTube or Vimeo | medium | 9 handoff | owner | BLOCKED | N/A |  | owner: Owner uploads as PUBLIC and checks it in a signed-out private window by Sun 25 Oct 12:00 PT. |
| SD23-099 | CAC IP rule vs Alpaca's claim over derivations of its data: compliance rated UNCLEAR | medium | 9 handoff | counsel | BLOCKED | N/A |  | counsel: Owner asks the district office or counsel before submitting (CHECKLIST line 25). |
| SD23-114 | check_strategy_deck grace ends; CI red on pushes from 2026-11-03 | medium | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: By 2026-11-02: re-read STRATEGY_DECK.md, update 'Last reviewed' and 'Revalidate by', and settle section 8. Do not move the date without re-reading. |
| SD23-123 | Public anchors repo still carries live accuracies and registry copies | medium | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides on step 2a by Sun 25 Oct 12:00 PT. |
| SD23-124 | Alpaca-derived figures still committed in the main repo tree and in public git history | medium | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides on step 2a. If withdrawn, engineering prepares registered amendments after the deadline. |
| SD23-125 | STRATEGY_DECK section 8 still carries pre-23-Oct database measurements | medium | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides with step 2a. If withhold, engineering builds deck_facts --withheld by 2026-11-02 (with NEW-4). |
| SD23-126 | Legal review of /terms and /privacy (draft) and the Alpaca/Kadoa emails not done | medium | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides whether to send the drafts and the counsel packet. |
| SD23-136 | Licence switch did not cover the refusal path, data-integrity, revalidation or loose switch values | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-137 | research/dirfix/paper_ledger.jsonl carried raw Alpaca closes; scan ignored JSON; wording said derived analytics are free | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-138 | Descriptive and legal text contradicted the operator-only gate | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-139 | Non-operator prereg view showed invented 'horizon days 0' and empty resolution | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-142 | Public verifier reported HTTP 451 from /api/ledger/range as FAIL (reads as tampering) | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-143 | Web promised features R2 withholds (prereg 'null (frozen empty)', digest, journal, watchlist, front-door/OG copy, /today, terms crypto/congress) | medium | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-004 | CI toolchain provenance ambiguous: setup-go installs go1.26.0 while production is built with go1.26.6 | low | 0 baseline | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-005 | Shared golangci-lint cache on this host reports 52 phantom issues from deleted worktrees (false FAIL; could equally mask) | low | 0 baseline | engineering | DEFERRED | N/A |  | engineering: Tue 27 Oct, after the freeze: run `golangci-lint cache clean` on the shared cache, or set a per-worktree GOLANGCI_LINT_CACHE in the lint wrapper. |
| SD23-006 | Dev-only npm tree: 5 high (braces GHSA-vfj7-8cjw-p6xm) via eslint-config-next; no patched braces exists | low | 0 baseline | external | DEFERRED | N/A |  | upstream braces/fast-glob release |
| SD23-007 | Committed gate snapshots expire: docs-gate goes red after 2026-10-29T00:35Z, revalidation after 2026-11-05T10:20Z | low | 0 baseline | engineering | IN PROGRESS | NOT SHIPPED |  | After the Sat 24 Oct 14:05 PT grade succeeds, commit and push by Sun 25 Oct 17:00 PT. Fallback: Sun's 14:05 grade, at the latest by Wed 28 Oct 17:00 PT. For revalidation: after the 2026-11-01 monthly run (needs NEW-2 deployed), commit the new figure-free stamp by 2026-11-04. |
| SD23-008 | ops/test-lib-portable.sh kills every 'nosleep.keeper' PowerShell on the host by command line, not by its own PIDs | low | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: Standing rule until fixed: never run ops/test-lib-portable.sh or test-market-close-keeps-web.sh on the production host. Tue 27 Oct: kill only the PIDs the test spawned, and verify on a non-production Windows runner. |
| SD23-009 | CI runner deprecations: Node 20 actions forced to Node 24; ubuntu-latest moving to Ubuntu 26 | low | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: By 2026-11-15: bump actions/checkout@v5, setup-go@v6, setup-node@v5, setup-python@v6, and pin runs-on: ubuntu-24.04. |
| SD23-010 | Prior open item: TradingView attribution on member chart pages still not seen live (unit-tested only) | low | 0 baseline | owner | BLOCKED | N/A |  | needs a signed-in member view of a member chart page on the live build; production sign-in by Claude is not allowed: owner checks once (TradingView attribution under the widget) and notes the result |
| SD23-022 | Production web server output is not logged anywhere; no 7-day web error coverage | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: redirect next start output in ops/start-local-workspace.ps1 to rotated logs/web.*.log. |
| SD23-023 | Ledger manifest stale: ops/ledger-revisions.txt lacks c8e18a2c, 7907f070 and 2ddff1a2 | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Tue 27 Oct, from a worktree: `SIGNALDECK_DB=<live db> LEDGER_MANIFEST=<worktree>/ops/ledger-revisions.txt bash ops/ledger-provenance.sh --write`, then --check. Commit the manifest only. |
| SD23-024 | worker_runs keeps no complete 7-day failure history; old failures are pruned regardless of status | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: exempt non-ok rows younger than 7 days in PruneWorkerRuns, with a store test. |
| SD23-025 | ERROR 'request failed: sql: database is closed' (14 in 7 d) at daemon shutdowns: in-flight requests are not drained | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: call http.Server.Shutdown before store close, and add a test. |
| SD23-026 | Daemon log rotation set inconsistent: .1/.2 missing, orphan .3.gz from August (08-17..10-05 history absent) | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: check logrotate numbering. Delete no log without owner OK. |
| SD23-027 | Cleanup and Accuracy tasks both trigger at 14:05 (possible I/O contention during the grade) | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Move Cleanup to 14:35 in the SD23-020 re-registration, by 29 Oct. |
| SD23-028 | STORAGE.md hot-DB size figure is stale (says 4.8 GB; actual 10.83 GB, over the 10,240 MB budget) | low | 1 repairs | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-029 | Live checkout carries 10 scheduler-regenerated tracked docs as uncommitted changes (they repeat the misleading survivorship line) | low | 1 repairs | engineering | IN PROGRESS | DEPLOYED | 73e69248 | R2 makes the grader regenerate tracked docs in withheld mode (SIGNALDECK_PUBLISH_DERIVED unset); confirm at the Sat 24 Oct 14:05 grade whether the regenerated files still differ from HEAD |
| SD23-030 | Next.js ?_rsc= prefetch ERR_ABORTED fires during page load (the 10-22 report's 'walker closes a page' explanation is wrong); not confirmed benign | low | 1 repairs | engineering | FIXED | N/A |  | none |
| SD23-031 | Signup username hint text overlaps the input's bottom border | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Ride along only if R4 or a later release builds web anyway; otherwise after the deadline. |
| SD23-032 | Unknown URLs show anonymous visitors the session gate, not the 404 page; 404 title repeats the brand | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline. The title fix (not-found.tsx:8 title 'Not found') can ride with any web build. |
| SD23-033 | Landing page prints the raw fetch error text to anonymous visitors | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: render a fixed sentence instead of e.message. |
| SD23-034 | ModelHealthWorker ignores SIGNALDECK_REGISTRY while the API honours it, so the kill switch is unreadable where only that variable locates the registry | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: First daemon release after the deadline. |
| SD23-035 | Member symbol page links to operator-only /intel/congress, so a member who clicks it is bounced to /today | low | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-036 | Public pages show 'Sign in / Sign up' to a signed-in member, with no session indicator or link back to /today | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline. |
| SD23-037 | Shell renders operator chrome before auth resolves (anonymous operator fetches 401, transient /dashboard logo link); some member pages lack an h1 | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: gate operator fetches on known && isAdmin, and add h1s. |
| SD23-038 | First UI sign-in in each new browser context returned POST /api/auth/login 400; retry after hydration returned 200 | low | 1 repairs | engineering | NOT REPRODUCED | N/A |  | check production logs for intermittent login 400s |
| SD23-039 | rv-forecast-runner failed on every isolated start ('refusing an incomplete forecast row', AAL h=1); likely an artefact of the no-Alpaca copy | low | 1 repairs | engineering | NOT REPRODUCED | N/A |  | production daemon log check for the same worker error |
| SD23-049 | Recurring upstream errors over 7 days: Alpaca rate-limit backoffs (135), news 429s, websocket drops | low | 2 price rights | engineering | DEFERRED | NOT SHIPPED |  | engineering: Retune backoff after a source decision exists. |
| SD23-058 | Congress schema drops owner, official filing link, asset type, amount bounds and late flags: rows look duplicated, T-bills/options stored as stock trades, late filings hidden | low | 3 congress | engineering | DEFERRED | NOT SHIPPED |  | engineering: Replacement-source design. |
| SD23-059 | Kadoa parse path has no offline unit test; only live tests, skipped by default | low | 3 congress | engineering | DEFERRED | NOT SHIPPED |  | engineering: Delete the dead path or add offline fixtures when a replacement lands. |
| SD23-060 | Congress data has no freshness monitor; a silently stale upstream reads as 'ok' | low | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-061 | /intel/congress default view hides most recent disclosures of older trades (344 of 562 last-30-day disclosures) | low | 3 congress | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: order the request by disclosed_ts. |
| SD23-062 | Smart Money page claims to include congress data, but none is used | low | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-063 | Each congress run polls the dead Stock Watcher S3 mirrors (403) first and downloads the whole Kadoa file twice | low | 3 congress | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-070 | Live daemon runs with PublicSurface off: the 'skeptic with curl' publicRoutes and /api/version answer 401 anonymously, contrary to the published-record design | low | 5 public access | owner | BLOCKED | N/A |  | owner: Owner decides after the deadline. |
| SD23-071 | Stale comment in next.config.ts (and route.ts:1-4) says the /api proxy attaches SIGNALDECK_API_TOKEN; restoring it would hand anonymous visitors admin | low | 5 public access | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-072 | /sitemap.xml is an empty urlset because NEXT_PUBLIC_SITE_URL is unset at build | low | 5 public access | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-073 | Response headers: CSP allows 'unsafe-inline' scripts; HSTS lacks includeSubDomains; no build-identifying header | low | 5 public access | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline. |
| SD23-074 | Posture-A public routes are expensive on first touch (model-health 18 s, quality 10.8 s, honesty 504 at 20 s); production answers them 401 | low | 5 public access | engineering | NOT REPRODUCED | N/A |  | production timing once Phase 5 decides the posture |
| SD23-082 | 44 delisted_at stamps conflict with SEC companies still listing the ticker on NYSE/Nasdaq (not adjudicated) | low | 6 survivorship | external | DEFERRED | N/A |  | external: Adjudicate after the deadline. |
| SD23-083 | Grade times are published without a timezone, and catch-up runs drift late (17:03-20:28 on 8 days) | low | 6 survivorship | engineering | DEFERRED | NOT SHIPPED |  | engineering: Versioned registry change after the deadline. WakeToRun goes in the SD23-020 re-registration. |
| SD23-084 | Phase 7 free sources (Treasury yields, BLS, Fed H.15, EDGAR facts) not added | low | 7 optional data | engineering | DEFERRED | NOT SHIPPED |  | deferred: lowest-priority phase; adding sources now threatens the 25 Oct freeze |
| SD23-085 | BEA Data API needs an owner click-through ToS acceptance (with indemnity) before any use | low | 7 optional data | owner | DEFERRED | N/A |  | owner-only click-through acceptance; Phase 7 deferred |
| SD23-100 | Final report's 'CI green on every pushed commit' is false: the run for 5083f8f6 was red | low | 9 handoff | engineering | FIXED | N/A |  | none |
| SD23-101 | Ledger row P2-SURV-LIVE is stale: it expected the 14:05 grade to stay survivorship-clean, but it published not clean | low | 9 handoff | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-102 | Kit and judge-facing docs still carry unresolved placeholders, including the already-decided solo-entry confirmation and the AI-disclosure answer | low | 9 handoff | owner | FIXED | N/A |  | none |
| SD23-103 | CHECKLIST owner-only DB-copy list has a wrong path and misses several full-DB copies | low | 9 handoff | owner | FIXED | N/A |  | none |
| SD23-104 | CAC deadline time inconsistent across kit documents and official pages (all kit docs adopt 09:00 PT Mon 26 Oct) | low | 9 handoff | owner | BLOCKED | N/A |  | owner: Owner asks the district office by Sat 24 Oct. |
| SD23-110 | CFTC COT source reads stale every Friday: 10-day budget vs weekly Saturday 06:00 poll of the Tuesday report | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | none; clears at Sat 06:00 poll; fix = budget >= 11.5 d or a Friday-evening poll |
| SD23-111 | pre-publish-scan price rule misses Alpaca short keys (c,o,l,vw,ap,bp) | low | 1 repairs | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-112 | revalidate-structural.sh stages full figures at ops/revalidation-status.json.tmp (not gitignored) | low | 1 repairs | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-113 | docs_gate withhold_integrity splits the refusal reason on the first ':' | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: keep a known class prefix, else write 'refusal: detail withheld (licence)'. Test with a Windows path and with a reason that has no colon. |
| SD23-115 | prereg claimText turns JSON null into "" / 0 for non-operators | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Next daemon release after the deadline. |
| SD23-116 | Home page fetches /api/accuracy without forwarding the cookie (operator sees withheld on /) | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline, if wanted: forward cookies() as accuracy/page.tsx:375 does. The page is force-dynamic (line 42). |
| SD23-117 | Every member page calls GET /api/ask, which always answers 451 (console error) | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: skip askStatus unless the viewer is the admin, or while the AI layer is retired. Add a release-smoke assertion that members make no /api/ask request. |
| SD23-118 | Member nav still lists withheld sections | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: consider hiding or labelling the items while withheld. |
| SD23-119 | /account link row renders run together | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Ride along with any web build (R4 or later); otherwise after the deadline. |
| SD23-120 | Operator gets the raw-row 451 on the published deployment | low | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides after the deadline. Engineering then sets AllowRawExport or adds a narrower operator exemption, with tests. |
| SD23-121 | GO-LIVE runbook expects 200 from anonymous /api/accuracy; MCP closure undocumented | low | 1 repairs | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-122 | Operator notifier sends Alpaca-derived content to a configured Discord webhook | low | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner confirms the Discord channel is private to them by Sun 25 Oct. Engineering may then note it in DATA_SOURCES.md. |
| SD23-127 | 23 Oct 14:05 grade ran the pre-R2 script and could have regenerated figure-bearing docs | low | 1 repairs | engineering | NOT REPRODUCED | N/A |  | None. The next anchor-publish (about 19:30 PT) runs the R2-gated script. |
| SD23-128 | Live posture not confirmed published() (gate could be inert) | low | 1 repairs | engineering | NOT REPRODUCED | N/A |  | None. |
| SD23-129 | Operator step: restore the dirfix paper ledger into data/ after 1c0b8188 | low | 1 repairs | engineering | FIXED | N/A |  | none |
| SD23-130 | Release checks not run: local -race, Playwright e2e against a real daemon, docker build, cold-clone repro, data-dependent checks | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Tue 27 Oct: docker build and cold-clone-repro-check in a worktree. Run live_accuracy --check and deck_facts --check against a read-only DB copy when the host has headroom. |
| SD23-131 | Residual public copy over-promises beside withheld panels | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Bundle with the next web build: R4 if it builds web, else after the deadline. |
| SD23-132 | PRIVACY_NOTICE_FACTS section header dated 2026-10-22 over 23 Oct content | low | 1 repairs | engineering | FIXED | DEPLOYED | d93e6f55 | none |
| SD23-133 | 451 Cache-Control: no-store not pinned by a test | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: assert Cache-Control == no-store in checkWithheld, mutation-check, and regenerate controls evidence. |
| SD23-134 | Flaky TestSWRBodyCache_PersistedBodyServesWithoutAnInlineBuild under load | low | 1 repairs | engineering | DEFERRED | NOT SHIPPED |  | engineering: Watch CI -race. If it recurs, give the persist wait a deterministic hook after the deadline. |
| SD23-135 | Members lose the whole call journal (cannot file calls); MCP closed to the operator's key | low | 1 repairs | owner | BLOCKED | NOT SHIPPED |  | owner: Owner decides after the deadline. If yes, allowlist POST /api/journal with outcomes stripped, plus a test. |
| SD23-140 | Raw-row 451 fallback told the operator data 'is shown only to the operator' | low | 1 repairs | engineering | FIXED | DEPLOYED | 73e69248 | none |
| SD23-011 | pre-publish-scan advisory: 10 tracked data CSVs to eyeball against DATA_SOURCES.md; history scan ignores NUL bytes | info | 0 baseline | owner | BLOCKED | N/A |  | owner: Owner records the step-2a decision (OWNER-STEPS-2026-10-23.md) by Sun 25 Oct 12:00 PT. If withdrawn, engineering needs a registered amendment, not a hand edit. |
| SD23-012 | web eslint: 2 react-hooks/exhaustive-deps warnings (triaged benign) | info | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: add the deps or a justified eslint-disable-next-line. |
| SD23-013 | next build Turbopack warning: dynamic fs.readFile in /accuracy traces the whole project into server output | info | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: narrow loadRegistry's path resolution so Turbopack can trace it statically. |
| SD23-014 | Test-output noise: MODULE_TYPELESS_PACKAGE_JSON warnings, unclosed-file ResourceWarnings, simulated-lock retries with real sleeps | info | 0 baseline | engineering | DEFERRED | NOT SHIPPED |  | engineering: After the deadline: close the two files with `with`, and inject a no-op sleep into test_forward_test_store. |
| SD23-040 | Deployment-drift refusal of today's 14:05 grade (HEAD f37c9595 vs running 2ddff1a2) does not reproduce | info | 1 repairs | engineering | NOT REPRODUCED | N/A |  | keep daemon commits out of the live checkout until deployed |
| SD23-041 | Retired-source errors (FRED VIXCLS parse, Kraken TLS) stopped after the 10-22 deploy | info | 1 repairs | engineering | NOT REPRODUCED | N/A |  | none |
| SD23-042 | Landing full-page screenshot blank band is a scroll-reveal capture artefact, not a defect | info | 1 repairs | engineering | NOT REPRODUCED | N/A |  | none |
| SD23-075 | 11 operator and member pages first appeared not to redirect; a 60 s recheck showed all redirect correctly (dev-server latency) | info | 5 public access | engineering | NOT REPRODUCED | N/A |  | none |
| SD23-089 | Web has never been built on linux/arm64 (CI builds ubuntu x64; lockfile carries arm64 native packages) | info | 8 cloud | engineering | DEFERRED | N/A |  | engineering: Add an arm64 build job when SD23-086 resumes. |
| SD23-090 | ARM64/Linux daemon build infeasibility does not reproduce: pure-Go SQLite, no cgo, cross-compile and vet pass for linux/arm64 | info | 8 cloud | engineering | NOT REPRODUCED | N/A |  | none |

## Evidence

### SD23-105: Production host memory/CPU starvation from other workloads on the shared PC stalls the daemon and web origin (Cloudflare 524s, timeouts)
- Source: prod-walk#0 (Node fetch probes (http.json), Playwright pass 1 (walk.json), loopback curl, Get-Process/Get-Counter); congress#20 (curl / Invoke-WebRequest probes); logs-runtime#5 (curl probes); routes#4 (live probe + logs/signaldeckd.log access log)
- Evidence: Prod walk 01:14-01:22 PT: /accuracy, /proof and /volatility returned Cloudflare 524 after ~125 s; all 13 w375 loads timed out at 30 s; loopback :8323 and :8322 answered nothing in 40 s. signaldeckd and node working sets 4 MB (paged out); free RAM 767 of 63,127 MB; commit 146.4 of 151.7 GB; CPU 100%; 13.7k pages/s. Top: AfterFX ~33 GB, vmmemWSL ~25 GB, 7 python processes ~1.5 GB each (STEM Racing CFD job, 98-100% CPU). Logs-runtime saw :8323 stall 01:16-01:21; congress saw /api/health time out at 45-60 s. Recovered 01:24, unstable again 01:28.
- Validation: 

### SD23-015: Public /accuracy shows 'Temporarily unavailable - UNREACHABLE': /api/accuracy 503 or timeout for most requests since 22 Oct 22:00 PT
- Source: logs-runtime#0 (logs/signaldeckd.log + public site GET); kit-claims#4 (logs/signaldeckd.log; curl to 127.0.0.1:8322 and :8323); prod-walk#1 (Playwright w1280 pass 1 and w375 retry; screenshots w1280_accuracy.png, w375_accuracy.png); survivorship#14 (Invoke-WebRequest GET on 127.0.0.1:8322); routes#4 (live probe + logs/signaldeckd.log access log); logs-runtime#23 (final-report-2026-10-22.md:64 + curl)
- Evidence: Live /accuracy rendered 'TEMPORARILY UNAVAILABLE - UNREACHABLE' on every verified load (w1280 08:11Z, w375 08:25Z); its SSR fetch of /api/accuracy uses AbortSignal.timeout(8_000) (accuracy/page.tsx:372-377). /api/accuracy by hour: 10-22 03h-21h all 200; 22h 43x200/10x503; 23h 18/6; 10-23 00h 8/16; 01h 3/18; first 503 at 22:00:57. Log: 'accuracy refused: a read failed err=context canceled', status=503 uid=0 ms=148060. The landing page showed 'The grading service is unreachable right now'. DEMO_SCRIPT scene 4 depends on this page.
- Validation: Re-verified 23 Oct ~15:05 PT: 23 Oct 15:05 PT: anonymous GET 127.0.0.1:8322/api/accuracy returned 451 {"error":"withheld","reason":"licence","source":"alpaca",...} in 0.005 s. GET 127.0.0.1:8323/accuracy returned 200 in 0.036 s with data-testid="withheld" data-source="alpaca" and no 'UNREACHABLE'. GET / renders 'The live record: withheld'.

### SD23-016: Daemon overloaded since 22 Oct 22:00 PT: prediction-runner and composite-scorer hit the 30-minute deadline, 6 workers stale, multi-second write-lock holds
- Source: logs-runtime#1 (worker_runs (mode=ro), data/health.json, logs/signaldeckd.log); kit-claims#4 (logs/signaldeckd.log; curl to 127.0.0.1:8322 and :8323); routes#4 (live probe + logs/signaldeckd.log access log)
- Evidence: worker_runs: prediction-runner and composite-scorer timed out at 00:50 ('exceeded run deadline 30m0s'); runs at 00:00-00:20 took 390-1048 s vs 10-65 s healthy; downsampler timed out 4x. health.json ok:false, stale: composite-scorer, composite-scorer-1w, downsampler, outcome-resolver, prediction-runner, sentiment-tagger; cache-warmer failing. 'long write-lock hold' 854 in 7 d (median 5.65 s, max 56.6 s), e.g. SectorRotator SetMeta 11.953 s at 01:30:49, news ingest and Alpaca backfill 4.2-4.5 s; dq wal_checkpoint_busy 147.
- Validation: Re-checked 23 Oct: data/health.json at 15:06 PT: {"ok":true,"staleWorkers":[]}. The daemon log since the 14:42 PT restart (325 lines) has 0 'exceeded run deadline', 0 'long write-lock hold' and 0 level=ERROR. It does have cache-warmer 'warm pass cut short: context deadline exceeded' at 14:57. Host: 6,243 MB free, AfterFX not running.

### SD23-043: Alpaca licence bars public display of its market data, news and derivations; no written consent obtained
- Source: rights/alpaca: Alpaca market data + News API research and review (accessed 2026-10-23)
- Evidence: Rights review: RESTRICTED for a public site, for market data and the Benzinga-sourced News API. Alpaca T&C (https://files.alpaca.markets/disclosures/library/TermsAndConditions.pdf, paraphrase): personal, non-commercial use only; no public display or distribution without express prior written consent; a User Application needs 30 days' written notice. Alpaca staff (https://forum.alpaca.markets/t/use-sip-data-feed-for-display/19641): "You cannot provide the data, or derivations of that data, to third parties". Internal storage, backfill and training: confirmed fine (staff, 2026-10-15).
- Validation: 

### SD23-044: Price-feed replacement: no free source clears the public-display bar
- Source: rights/exchange-free, av-tiingo, polygon-twelve, ndl-edgar: free price-source verdicts (accessed 2026-10-23)
- Evidence: Public-site verdicts (accessed 2026-10-23): Stooq, Alpha Vantage free, Tiingo free, Massive (Polygon) Basic, Twelve Data Basic and Nasdaq Data Link premium/sample sets RESTRICTED; WIKI Prices UNAVAILABLE; IEX real-time/delayed RESTRICTED; SEC EDGAR has no prices. Only IEX HIST is PERMITTED FOR SPECIFIED USE, IEX-only and T+1 (https://www.iex.io/products/equities/market-data-connectivity: "Free historical data downloads from IEX equities available on a T+1 basis."). Brief Phase 2 completion needs one full trading day of ingest.
- Validation: 

### SD23-045: Anonymous bulk download of every model forecast (Alpaca-derived, plus Kraken-derived crypto) via /api/ledger/range
- Source: routes#0 (live anonymous probe + security.go + ledger.go + read-only DB)
- Evidence: Probe: GET https://signaldeck.nicholasnyaung.com/api/ledger/range?from=1&limit=3 returned 200 no-store with entries containing symbolId, horizon, barTs, rawProb, calProb, featureHash, modelVersion. security.go:475-477 exempts the path anonymously. ledger.go:94 caps a request at 5,000 rows. DB (read-only): MAX(seq) read (the forecast count is withheld under the data licence); symbols id1=BTC/USD (crypto), id2=SPY, id3=QQQ. The owner kit's final-report-2026-10-22.md records that Alpaca staff say its data 'or derivations' may not be provided to third parties.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-066: Member tier is effectively public: open sign-up is ON, so anyone with a verified email reaches per-symbol Alpaca-derived forecasts and congressional trades
- Source: routes#1 (anonymous /api/health body + accounts.go + security.go)
- Evidence: Anonymous GET /api/health returned {"openSignup":true,"signupPaused":false,...}, so any stranger with a verified email becomes a member. accounts.go:60-97 memberRoutes include /api/regimes, /api/vol-regime, /api/market-regimes, /api/vol-forecast/latest, /api/symbol-agent and /api/congress; accounts.go:251-262 also admits every publicRoutes entry (per-symbol /api/ledger, /api/honesty, /api/regime-postmortems). security.go:152-155 is the only member gate. users: 5 total, 1 admin, 1 verified non-admin.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-076: Published survivorship_clean depends on whether the 14:05 grade overlaps the daily sweep, not on listing status (today's grade will publish false again)
- Source: survivorship#0 (logs/accuracy-registry.log, logs/refresh.log, Task Scheduler); logs-runtime#9 (symbols table (mode=ro), data/accuracy_registry.json, accuracy-registry.log); survivorship#6 (live DB sqlite_master and filings; tools/alpha/fetch_delisted.py:30; cmd/signaldeckd/run.go:789;); survivorship#8 (live DB; daemon/cmd/sdmaint/voidretiredcrypto.go:13-17, 422; cmd/signaldeckd/run.go:2149-2157); survivorship#9 (tools/accuracy_registry.py:915; daemon/internal/store/store.go:900, survivorship.go:213/226); survivorship#10 (daemon/internal/pipeline/delisting.go:57-75, 113-155; universe/poller.go:138-141; live DB); survivorship#12 (logs/refresh.log; live DB; cmd/signaldeckd/run.go:802/818; universe/tape.go:119); survivorship#13 (live DB; ops/signaldeck-refresh.sh:79; store.go:937-941)
- Evidence: accuracy-registry.log, structural family (graded count withheld under the data licence): survivorship_clean on all 10 runs inside a sweep window (10-05..09, 12, 13, 19, 20, 21) and NOT CLEAN on all 9 runs outside one (10-04, 10, 11, 14-18, 22). The 13:15 PT sweep sets active=1 on all 2,943 stocks; the prune cuts back to ~320, leaving 721 inactive stocks with no delisted_at (the 10-22 grade's registry undated count is withheld under the data licence; every registry row false). No listing-status source is persisted: no Alpaca asset table, no Form 25 rows in filings, no corporate-actions table.
- Validation: 

### SD23-077: Registry's 'inactive with no delisted_at' counts (direction and structure) are lean-mode watch exclusion, but public docs present them as unknown listing status
- Source: survivorship#1 (live DB (mode=ro) running the registry SQL; data/accuracy_registry.json; partials/live_accuracy.md)
- Evidence: Registry SQL run now returns prediction_outcomes and regime_outcomes triples equal to the published survivorship_completeness (counts withheld under the data licence). Of the directional undated, nearly all have a 1d bar within 5 days and a few at 5-42 days. Of all 721 inactive-undated stocks, nearly all have a last-bar date of 2026-10-22, and SEC companies.exchange shows NYSE 337 / Nasdaq 300. store.go:991 describes SetSymbolActive as 'toggles the live subscription flag'. survivorship.go:207-210: 'Deactivating is a SUBSCRIPTION decision... delisting is a MARKET fact'.
- Validation: tools/live_accuracy.py (generator) no longer presents the registry undated-inactive count as delistings; DATA_SOURCES.md explains the flag and the separate survivorship-status v1 measure; under licence withholding the generated block prints no figures. Shipped in 73e69248 (PR #39 + #41), CI green, docs gates clean.

### SD23-078: Survivorship bound is measured every grade cycle (10-22: an accuracy-inflation bound in percentage points, figure withheld under the data licence) but dropped from the published registry
- Source: survivorship#2 (logs/accuracy-registry.log; ops/accuracy-registry.sh:339-343; tools/accuracy_registry.py:2748, 3078-3096;)
- Evidence: In run 2026-10-22T14:05:05, log line 16882 reads 'survivorship bound: 43 symbol(s) left the universe since 2026-07-24 vs [graded count] graded -> at most [bound] pp accuracy inflation. (graded count and bound withheld under the data licence) Written to ...\data\accuracy_registry.json' ('as_of': '2026-10-22T21:05:14Z'). Log line 17043 in the same run reads 'Survivorship bound: NOT YET MEASURED this cycle', and line 17058 reads 'published: staged registry -> ...accuracy_registry.json (atomic rename)'. `grep -c survivorship_bound data/accuracy_registry.json` returns 0, and the same for accuracy_registry.prev.json. The bound is written to OUT before grading.
- Validation: 

### SD23-086: Cloud migration: Oracle Always Free account creation is owner-only and age-gated; cutover deferred
- Source: rights/oracle: OCI Always Free research and review (accessed 2026-10-23)
- Evidence: Oracle.com Terms of Use s.18 (https://www.oracle.com/legal/terms/): "not of the age of majority are not eligible to use the Site"; the CSA s.6.1 capacity representation, card, phone and one-account-per-person rules make account creation owner-only. Review: RESTRICTED for this owner; uses are permitted only with an eligible adult as the contracting customer. The brief assumes A1 with 4 OCPU/24 GB, but the current Always Free allotment is "equivalent to 2 OCPUs and 12 GB of memory" (docs.oracle.com Always Free Resources).
- Validation: 

### SD23-087: Ops layer is Windows-only: service control and deploy are schtasks-only, release/guard/health/alerting are PowerShell, the schedule is Task Scheduler XML
- Source: kit-claims#14 (ops/lib-portable.sh:304-346; ops/signaldeck-ctl.sh:217-292; market-close.sh:132; anchor-publish.sh:304); kit-claims#15 (git ls-files ops; Get-ScheduledTask SignalDeck*; ops/tasks/*.xml)
- Evidence: lib-portable.sh:304-346 sd_svc_start/stop are schtasks-only with no systemctl branch; signaldeck-ctl.sh deploy ignores sd_svc_restart's result, then runs .venv/Scripts/python.exe (l.284), also hardcoded in market-close.sh:132 and anchor-publish.sh:304, so a Linux deploy ends 'deploy UNVERIFIED'. 34 ops scripts are PowerShell; web-release, web-assets-check, the daemon/web/tunnel guards, check-grader-health, check-task-health, lib-notify/setup-alerts, restart-web and start-local-workspace have no bash twin. The schedule is 23 Task Scheduler XMLs with fixed -07:00 triggers.
- Validation: 

### SD23-091: Narration is scheduled before the public-launch phases ship, and those phases will make several kit claims false
- Source: kit-claims#7 (CHECKLIST.md (d)-(g); brief-public-launch-2026-10-23.md phases 2-5, 8, freeze)
- Evidence: CHECKLIST schedules narration for Sat 24 Oct, upload by Sun 25 12:00 PT and submit by 20:00 PT. The brief ships phases 2-5 and 8 Sat/Sun, with the freeze at 18:00 PT Sun. Claims at risk: SUBMISSION.md:10 'Free member accounts add ... symbol pages' (Phase 5 opens them); :27 'NVIDIA-hosted language models tag news sentiment' (Phase 4, branch pl/r1-nvidia-off in progress); :36 'Alpaca (stock market data and news)' and 'congressional STOCK Act trade disclosures (via third-party datasets)' (Phases 2-3); :47 'single Windows PC at home' (Phase 8); Q6 2.0 items.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. In C:/Users/Nicholas_N/Desktop/claude code/signaldeck-owner-kit/cac-2026/SUBMISSION.md:
- :10: say forecasts are hash-chained and graded, the grades and statistics are shown only to the operator while Alpaca's licence question is open, and the public

### SD23-106: Production host powered off by the user 3 times in 7 days (~49 h down, including market sessions on 10-19, 10-20, 10-21)
- Source: logs-runtime#2 (System event log 1074/6005/6006 + daemon log gaps)
- Evidence: gaps.py: 2026-10-18 22:59:17 -> 10-19 17:45:39 (18.8 h); 10-20 06:21:03 -> 18:40:27 (12.3 h); 10-20 23:17:26 -> 10-21 17:26:51 (18.2 h). Event 1074 at 10-18 22:59:11, 10-20 06:22:02 and 10-20 23:17:26: 'StartMenuExperienceHost.exe ... has initiated the power off of computer NICHOLAS_NYAUNG on behalf of user Nicholas_Nyaung'. Effects: grades ran as StartWhenAvailable catch-ups (grader_heartbeats 10-20T00:55Z, 10-21T01:45Z, 10-22T00:33Z); 9 orphaned runs swept at boot (crypto-live, backfiller, stock-streamer x3 each).
- Validation: 

### SD23-107: Member digests and alerts can carry Alpaca-derived content to non-operators by email/Telegram
- Source: R2 design review (route map: member daily digest, alert-runner deliveries)
- Evidence: route map: memberRoutes include alert-prefs and telegram-link; steps5-6 member daily digest (email). Alpaca T&C bar transmitting Content to others without consent.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-108: Web pages must show an honest withheld state (not "unreachable") when content is withheld for licence reasons
- Source: R2 design (brief: honest unavailable states)
- Evidence: public /accuracy currently renders "Temporarily unavailable - UNREACHABLE" for any non-200; a 451 would be mislabelled the same way.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-109: Slow market-close backup overlapped the 13:15 refresh sweep and left the daemon down from 13:30:50 (public site degraded)
- Source: live observation 23 Oct 13:31-14:05 PT (refresh.log, backup-offline.log, daemon-guard.log, Task Scheduler)
- Evidence: 23 Oct: market-close backup started 13:10:53 and its DB phase finished 13:49:24 (8209 MB; 22 Oct took ~6 min). The 13:15 sweep ran meanwhile, stopped the daemon 13:30:50 'for a clean prune' and, because the backup had already stopped it, did not restart it (step 8 restarts only if it was up before the sweep). daemon-guard: 'maintenance lock held by pid 69324 (35 min): leaving daemon down'. /api/health 000 from 13:31 to at least 14:03. Cause of the slowness: host CPU 96-100% from unrelated jobs and a 2.4 GB WAL.
- Validation: 

### SD23-141: R2 heads on a stale base would have reverted R1 (NVIDIA)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-31)
- Evidence: origin/public-launch log contains 7c5e0707. Live log 14:42:01: 'AI layer retired: NVIDIA trial API terms do not permit production use'. Sources: r2_result reviews[0].G1 and reviews[2].G6.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch log contains 7c5e0707. Live log 14:42:01: 'AI layer retired: NVIDIA trial API terms do not permit production use'. Sources: r2_result reviews[0].G1 and reviews[2].G6.

### SD23-001: Daemon toolchain go1.26.6 carries 10 reachable Go stdlib vulnerabilities (fixed in go1.26.9/go1.26.10)
- Source: gates#0 (govulncheck v1.8.0 -show verbose ./... (daemon, GOOS=windows and GOOS=linux);); gates#6 (govulncheck -show verbose (Package and Module Results))
- Evidence: govulncheck v1.8.0 exits 3 for GOOS=windows and linux: 'Your code is affected by 10 vulnerabilities from the Go standard library' in go1.26.6 (the go.mod toolchain and bin/signaldeckd.exe). E.g. GO-2026-6809 crypto/x509 (fixed go1.26.10) reached via internal/notify/slack_smtp.go:261; GO-2026-6617, 6612, 6611, 6603 HTTP/2 (fixed go1.26.9). Imported but not called: net/http Range and os Root.Mkdir junctions (go1.26.9), golang.org/x/crypto v0.53.0 ssh advisories (fixed v0.56.0).
- Validation: go.mod toolchain go1.26.10 (PR #40, merged in release 73e69248); govulncheck v1.9.0: 0 called vulnerabilities on GOOS=windows and linux (10 before); deployed binary: go version -m bin/signaldeckd.exe = go1.26.10, ldflagsRev 73e69248; ctl deploy VERIFIED 14:42 PT.

### SD23-002: CI has no Go vulnerability gate (npm audit gate exists, govulncheck does not)
- Source: gates#1 (.github/workflows/ci.yml)
- Evidence: `grep -c govulncheck .github/workflows/ci.yml` printed 0. ci.yml:224-239 adds npm audit with the comment "CI had NO such gate in either language", but only the npm half was wired. The 10 reachable stdlib vulnerabilities above went unreported by CI: run 39906199230 is green on all 6 jobs.
- Validation: 

### SD23-003: Operator health views unchecked: /api/source-health, /api/version and the degraded/ready reasons need the API token
- Source: logs-runtime#13 (curl + ops scripts); prod-walk#3 (Node fetch probe /api/health, /api/ready (http.json)); prod-walk#16 (Get-CimInstance Win32_Process)
- Evidence: 127.0.0.1:8322/api/source-health and /api/version -> 401 {"error":"authentication required"}; the ops mechanism greps SIGNALDECK_API_TOKEN out of daemon/.env (signaldeck-ctl.sh:41), which audits may not read. Anonymous /api/health 200 {"degraded":true,"detail":"sign in or send the API token for the full breakdown"}; /api/ready 503 ready:false. Daemon PID 42496 started 2026-10-22 13:30:04 PT, a restart the 10-22 final report does not record (line 63: 'Daemon unchanged'). Revision 2ddff1a2 is known only from worker_runs (300/300 newest runs).
- Validation: 

### SD23-017: Public default branch main lags public-launch by 2 commits and still pins vulnerable next 16.3.8
- Source: gates#2 (git rev-list/diff; gh repo view); logs-runtime#25 (git (read-only))
- Evidence: Commit counts: main-only 25 (all merges), public-launch-only 2 (5083f8f6, f37c9595). `git diff --stat 2ddff1a2 origin/main` is empty, so main adds no content. `git diff --stat origin/main origin/public-launch` touches docs/COMPETITION.md, web/package.json (+2/-2) and web/package-lock.json (102 lines). gh repo view reports defaultBranchRef main, isPrivate false. The final-report says f37c9595 fixed 3 next advisories, including a critical RCE. Production is not affected (live node_modules next 16.4.2).
- Validation: PR #42 (main <- public-launch at deployed 73e69248) merged 23 Oct ~15:20 PT as merge commit 28449999 with --match-head-commit, CI 12/12 green; git diff origin/main origin/public-launch = 0 lines at that time. R4 (d93e6f55) landed later on public-launch; main lags it by docs/ops-only commits until the next sync PR.

### SD23-018: Retention prunes keep skipping on read timeouts, so the hot DB is over its size budget and gap-fill is paused
- Source: logs-runtime#3 (logs/signaldeckd.log, worker_runs detail, dq_events)
- Evidence: 'retention prune skipped (fail-safe)' 293 in 7 d (10-16..23: 88,131,57,3,0,0,4,10): table=1h 76, 1m 65, anomalies 76, snapshots_1s 76, research_weeks 1; reason 'read for archive failed: context deadline exceeded'. downsampler detail 00:45 'archived+pruned 0 1m, 0 1h bars, 0 snaps, 0 anomalies'. backfill-reconciler latest 'gap-fill paused: db 10326 MB of 10240 MB budget; 35 streamed with a session gap' (was 10091/10240 at the 10-22 deploy, 10163 at 13:30, 10309 at 21:50). data/signaldeck.db = 10,826,637,312 bytes. dq vacuum_skip 'only 0.0MB of 10324.1MB is reclaimable'.
- Validation: 

### SD23-019: Public ledger proof /api/ledger/verify returns 503 'warming'; /proof hash-chain and pre-registration cards stay skeletons
- Source: logs-runtime#4 (curl + logs/signaldeckd.log); prod-walk#2 (Playwright w1280 pass 1 and w375 retry; screenshots w1280_proof.png, w375_proof.png)
- Evidence: curl -H 'X-Signaldeck: 1' 127.0.0.1:8322/api/ledger/verify -> 503 {"error":"warming"} after 20.95 s (01:10:07). Log: 'cache warm step skipped: no build slot, a verification already running, or a ledger key read that timed out' 65x 22:00-01:09; cache-warmer 'ledger-verify: context deadline exceeded' 22x in 7 d. Live /proof at both widths: the verify and PRE-REGISTRATION CHAIN cards stayed skeletons with /api/ledger/verify 503 (proof/page.tsx:278-311 retries on 503).
- Validation: 

### SD23-020: DST: every SignalDeck task trigger has a fixed -07:00 StartBoundary, so after 2026-11-01 tasks fire one hour earlier local time
- Source: logs-runtime#7 (Export-ScheduledTask + ops/tasks/*.xml)
- Evidence: SignalDeck Accuracy <StartBoundary>2026-08-12T14:05:00-07:00</StartBoundary>. Market-Close 5x 13:10:00-07:00, Daily-Refresh 13:15-07:00, Market-Open 06:20-07:00, Cleanup 14:05-07:00 and others. Only Revalidation (2026-08-31T03:20:00) has no offset. A StartBoundary with an offset is the 'Synchronize across time zones' form, an absolute UTC instant (21:05Z for Accuracy). After the PST change: Accuracy 13:05 PST; Market-Close 12:10 PST, before the 13:00 PST close (daemon stop + backup + forward-test mid-session); Daily-Refresh 12:15 PST; Market-Open 05:20 PST.
- Validation: 

### SD23-021: Two demo-noted UI layout defects still unfixed in the repo (home live-record table width; /accuracy STRUCTURAL CLAIMS column clipped)
- Source: kit-claims#10 (demo-capture-notes.md scenes 3-4; git diff --stat 7907f070 HEAD -- web/src)
- Evidence: Notes: the home table sits in a ~3,380 px horizontal scroll box, so 'Accuracy, Baseline and Skill are off-screen' at 1280 px; the /accuracy STRUCTURAL CLAIMS status column is 'clipped ... ("no interval, so no verdic")'. The 11 web/src files changed since 7907f070 (intel/shorts, intel/smart-money, lab/system/agents, market/macro, privacy, terms, CalendarsCard, TradingViewChart, tradingviewEmbed) include neither web/src/app/page.tsx nor web/src/app/accuracy. The investigator did not re-check live.
- Validation: 

### SD23-046: Anonymous Alpaca-derived aggregates served and rendered on public pages (/api/track-record, /api/vol-forecast/record, /api/accuracy)
- Source: routes#2 (live anonymous probe + web server pages)
- Evidence: Probe: /api/track-record returned 200 with byMarket entries for crypto and stocks, each carrying a mean forward return (meanFwd) and a graded count (n) (figures withheld under the data licence). /api/vol-forecast/record returned 200 (QLIKE grades; verdict INSUFFICIENT is expected, not a defect). /api/accuracy is code-open (security.go:463-466). page.tsx:90-97 fetches /api/accuracy server-side for every visitor; volatility/page.tsx:65 does the same for the record. By the repo's own licence line these are DERIVED (datalicense.go:179-201); under Alpaca's stated 'derivations' reading they are in scope.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-047: IEX HIST terms need owner acceptance (browse-wrap with an indemnity) before HIST can be ingested or shown
- Source: rights/exchange-free: IEX HIST research and review (accessed 2026-10-23)
- Evidence: Downloading accepts the terms: https://iextrading.com/trading/market-data/ "By accessing or using IEX Historical Data, you agree"; HIST ToU s.07 (https://www.iex.io/products/market-data-connectivity/hist-terms): "You agree to indemnify and hold harmless IEX"; the iex.io website terms stack on top. If accepted: the exact HIST citation and link wherever values are shown (including to an LLM provider), labelled IEX-only, T+1, not consolidated. Review: PERMITTED FOR SPECIFIED USE, on implied permission, not an express licence.
- Validation: 

### SD23-048: Crypto (Kraken-derived) content remains on anonymous and member surfaces through publicRoutes despite 'crypto is not part of the member product'
- Source: routes#6 (accounts.go, probe bodies, read-only DB)
- Evidence: accounts.go:103-108 says Kraken's terms restrict derived works and member routes drop crypto, while 'the public proof record (publicRoutes) are unchanged'. Anonymous /api/track-record byMarket contains a crypto entry (its graded count is withheld under the data licence). Anonymous /api/ledger/range starts with symbolId 1 = BTC/USD forecasts. regime_postmortems holds 'liquidity21-crypto' rows (count withheld under the data licence), and /api/regime-postmortems (member-reachable) does not filter crypto (regimepostmortems.go handler, no filter).
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created).

### SD23-050: Kadoa licence RESTRICTED: keyless automated fetch continues and /api/congress serves Kadoa-compiled rows to members; the Restricted class governs no route
- Source: congress#2 (internal/datalicense/datalicense.go, internal/api/accounts.go, owner kit); routes#3 (api/congress.go, pipeline/congress.go, run.go, datalicense.go, read-only DB); congress#17 (congress.go:83, edgar.go:92-101); rights/congress: Kadoa research and review (accessed 2026-10-23)
- Evidence: datalicense.go:108-111 marks kadoa Restricted (Redistrib:false) but RestrictedRoutes (:205-248) has no /api/congress entry, and accounts.go:75 puts /api/congress in memberRoutes. pipeline/congress.go:160-166 falls back to Kadoa whenever the Stock Watcher mirrors fail (403 today) and run.go:1153 sets KadoaFallback:true, so every served row is Kadoa-compiled. Review: RESTRICTED pending Kadoa's written permission; https://www.kadoa.com/terms s.4: "access the Services via any automated system"; s.8.2 bars systematic retrieval.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created). Kadoa fetch removed (kadoa.go deleted), /api/congress note corrected, rows withheld from non-operators.

### SD23-051: Terms page and public docs say older congress rows came from House/Senate Stock Watcher; the evidence says none did
- Source: congress#1 (web/src/app/terms/page.tsx, README.md, DATA_SOURCES.md, docs/PRIVACY_NOTICE_FACTS.md vs DB)
- Evidence: terms/page.tsx:202-206 says "Kadoa's public congress-trades file, and for older rows the House and Senate Stock Watcher datasets". README.md:199 and :247, DATA_SOURCES.md:44 and PRIVACY_NOTICE_FACTS.md:62 say the same. But the oldest stored disclosure is 2026-07-01 (`SELECT MIN(NULLIF(disclosed_ts,0))`), while the Stock Watcher dumps are cumulative multi-year files, so one successful ingest would have stored years of history. dq congress_mirror_error ran 362 times from 2026-07-04 to 09-07, every one a 403. workers.go:566-568 records that congress_trades held ZERO rows.
- Validation: Release R2 (PR #41, 73e69248; PRs #39/#40 merged with it) deployed 2026-10-23: ctl deploy 'deploy VERIFIED: daemon is running commit 73e69248 (resolvable)' 14:42 PT; web build uGRCWaJdwrnd4LPlobZGS promoted 14:48 after asset gate + browser gate 18/18 (previous build kept at web/.next-prev-20261023-144748). CI 6/6 green on 73e69248 (daemon job with -race). Reviews: 3 fresh-context lenses + real-daemon isolated walk on 0e872123 (bypass GO, walk GO; honesty must-fixes fixed), delta review of the fix round (GO after one spec fix). Live loopback probes after deploy: anonymous /api/accuracy, /api/track-record, /api/vol-forecast/record, /api/ledger/range -> 451 with the exact contract body and Cache-Control no-store; operator token -> 200 on /api/accuracy, /api/ledger/range, /api/congress; source-health congress_trades + news_sentiment_llm paused; 0 congress-poller runs on the new revision; congress_trades 2,705 rows kept. Member behaviour verified in the isolated real-daemon walk (no production accounts created). Kadoa fetch removed (kadoa.go deleted), /api/congress note corrected, rows withheld from non-operators.

### SD23-052: Members are told congress data comes from 'free Stock Watcher community mirrors' while Kadoa serves it; chamber status reads ok:true with no source field
- Source: congress#0 (internal/api/congress.go, internal/pipeline/congress.go, DB meta)
- Evidence: api/congress.go:18 congressNote = "...ingested via the free Stock Watcher community mirrors...". pipeline/congress.go:160-166 swaps in Kadoa trades and then records CongressChamberStatus{OK:true} (:193), with no field saying which source served them. meta congress_mirror_status = {"senate":{"ok":true,"fetched":80},"house":{"ok":true,"fetched":1368}}, while `curl -I` on both S3 mirrors returns 403 today. The 80 and 1368 counts fit the Kadoa window (97 senate / 1349 house usable rows), not a full mirror dump. intel/congress/page.tsx:151 renders the note to members.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:daemon/internal/api/congress.go:22 congressNote says no stored row records its source, all are treated as Kadoa's compilation, and collection stopped 23 Oct 2026. Lines 31-33 serve senate/house {"ok": false, ... "detail": congressRetiredDetail} instead of the frozen meta. run.go:1144 `func congressWorkers(...) { return nil }`.

### SD23-053: Official House Clerk / Senate eFD ingest does not exist; Kadoa is the only live congress upstream
- Source: congress#5 (Kadoa dump analysis plus DB id match); congress#8 (kadoa.go, congress.go, Kadoa dump); congress#13 (internal/api/congress.go, pipeline/congress.go, kadoa.go); congress#18 (daemon-wide grep, gbm/selfref.go)
- Evidence: Congress investigator: no House Clerk or Senate eFD code exists; the only traces are Stock Watcher-shaped fixtures linking disclosures-clerk.house.gov and efdsearch.senate.gov. Kadoa's dump covers ~11 weeks (filing dates 2026-08-05 to 10-21) and 1,266 of 2,688 stored rows now exist only in SignalDeck, so a replacement needs its own backfill. No amendment handling anywhere. Retirement trap: unwiring the poller freezes congress_mirror_status at ok:true; SIGNALDECK_CONGRESS_KADOA_URL=off yields DEGRADED runs. Congress feeds no model feature.
- Validation: 

### SD23-054: House Clerk disclosures rated UNCLEAR for public display: 5 U.S.C. 13107 'commercial purpose' is undefined
- Source: rights/congress: House Clerk Financial Disclosure research and review (accessed 2026-10-23)
- Evidence: Review: UNCLEAR for public display, derived outputs, model input and LLM processing, leaning permitted while SignalDeck stays free with no ads, fees or donations; acquisition, storage and backfill PERMITTED FOR SPECIFIED USE. 5 U.S.C. 13107(c)(1)(B) (https://www.govinfo.gov/content/pkg/USCODE-2024-title5/html/USCODE-2024-title5-partIV-chap131-subchapI-sec13107.htm): "for any commercial purpose, other than by news and communications media". No official interpretation of 'commercial purpose' was found.
- Validation: 

### SD23-055: Senate eFD access needs a per-session click-through acknowledgment; automated acquisition rated UNCLEAR
- Source: rights/congress: Senate eFD research and review (accessed 2026-10-23)
- Evidence: https://efdsearch.senate.gov/search/home/: "You must agree to the following to be able to search reports." Report pages redirect (302) to the agreement until it is accepted in that session, so automated fetching means a bot accepting a click-through on the owner's behalf; eFD also lets filers see who viewed their reports. Review: automated acquisition and backfill UNCLEAR, manual access PERMITTED, public display UNCLEAR ('commercial purpose' undefined, 5 U.S.C. 13107(c)).
- Validation: 

### SD23-056: Congress dedup ID is wrong both ways: one filing from two upstreams gets two IDs, and distinct line items collapse to one
- Source: congress#3 (internal/ingest/congress/congress.go, kadoa.go); congress#6 (kadoa.go:71, store INSERT OR IGNORE)
- Evidence: TradeID (congress.go:316-323) hashes raw strings: the Senate path uses MM/DD/YYYY dates and owner text ('Self', 'Spouse') (congress.go:259-260) while kadoa.go:71 uses ISO dates and Kadoa owner codes, so one filing from two upstreams gives two rows. Conversely, over the current Kadoa dump 7 pairs of distinct Kadoa ids (e.g. house_20035350_g0/g1) map to one TradeID and lose a row under INSERT OR IGNORE; Kadoa's unique id is never parsed (kadoa.go:20).
- Validation: 

### SD23-057: Senate coverage from Kadoa is thin and shrinking (5 disclosures in October); one filer is 75% of senate rows; filer names arrive truncated
- Source: congress#4 (DB read-only plus the Kadoa dump)
- Evidence: Senate disclosures by month: 2026-07 732, 08 182, 09 24, 10 5 (house 299/453/519/474). Senate rows fetched per run fell 199 (10-15), 175, 166, 80 (10-22). Kadoa's window holds only 187 senate_efd rows from 17 filers, and a single filer accounts for 706 of the 943 stored senate rows (Kadoa's chamber label not independently verified). Two stored filer names are malformed (one surname lost, one trailing comma); no name normalization exists.
- Validation: 

### SD23-064: NVIDIA trial API used in production: 448 LLM calls in 24 h (sentiment tagger, risk watcher, market analyst, briefing); trial terms bar production use
- Source: logs-runtime#11 (logs/signaldeckd.log); rights/nvidia-local: NVIDIA API Trial Terms research and review (accessed 2026-10-23)
- Evidence: 'llm call' since 10-22 01:15: 448, all nvidia/nemotron-3-nano-omni-30b-a3b-reasoning: news sentiment tagger 323, Risk & Anomaly Watcher 97, Market Analyst 27, morning-briefing rewrite 1; last 2026-10-23T01:08:41. NVIDIA API Trial Terms s.1.4 (https://assets.ngc.nvidia.com/products/api-catalog/legal/NVIDIA%20API%20Trial%20Terms%20of%20Service.pdf): "only use the API Service for internal testing and evaluation purposes, not in production". Review: RESTRICTED; sending Alpaca/Benzinga headlines to NVIDIA is also RESTRICTED.
- Validation: R1 deployed 2026-10-23 05:13 PT via ops/signaldeck-ctl.sh deploy: 'deploy VERIFIED: daemon is running commit 2970a6a8 (resolvable)'; CI PR #38 6/6 green (daemon job with -race); public-launch fast-forwarded to 2970a6a8. Post-deploy: startup log 'AI layer retired: NVIDIA trial API terms do not permit production use (2026-10-23)', 0 'llm call' log lines and 0 sentiment-tagger runs since the 05:13:31 boot, /api/source-health news_sentiment_llm paused with reason. Transport-counting unit tests prove zero requests (mutation-checked). Network corroboration: per-process TCP sampler (60 s) caught the OLD daemon pid 42496 connected to ai.api.nvidia.com at 05:10:38 and 05:11:40 (positive control); new pid shows 0 NVIDIA connections so far. Observation runs through 13:30 PT today (one full regular session); the brief's one-day observation target stays pending until it completes.

### SD23-065: NVIDIA-LLM sentiment over Alpaca news can be an input to the forecasts served anonymously
- Source: routes#7 (store/news.go, pipeline/predict.go, ensemble.go)
- Evidence: store/news.go:118-123 NewsSentimentAgg averages news.score over rated rows; the LLM tagger rates them (newssent.go:3-6 names internal/aiagents/sentiment as the LLM path). pipeline/predict.go:897-915 passes sentScore as SentimentScore. ensemble.go:418-419 includes LegSentiment when admitsLeg is true. Those predictions are ledgered and served anonymously by /api/ledger/range. The investigator did not check whether the leg is currently admitted. The final report already lists the NVIDIA trial-terms issue as open.
- Validation: R1 deployed 2026-10-23 05:13 PT via ops/signaldeck-ctl.sh deploy: 'deploy VERIFIED: daemon is running commit 2970a6a8 (resolvable)'; CI PR #38 6/6 green (daemon job with -race); public-launch fast-forwarded to 2970a6a8. Post-deploy: startup log 'AI layer retired: NVIDIA trial API terms do not permit production use (2026-10-23)', 0 'llm call' log lines and 0 sentiment-tagger runs since the 05:13:31 boot, /api/source-health news_sentiment_llm paused with reason. Transport-counting unit tests prove zero requests (mutation-checked). Network corroboration: per-process TCP sampler (60 s) caught the OLD daemon pid 42496 connected to ai.api.nvidia.com at 05:10:38 and 05:11:40 (positive control); new pid shows 0 NVIDIA connections so far. Observation runs through 13:30 PT today (one full regular session); the brief's one-day observation target stays pending until it completes.

### SD23-067: Dormant ngrok tunnel task is a second public ingress straight to the daemon port; the kit's hosting claim omits it
- Source: gates#17 (Get-ScheduledTask (read-only)); kit-claims#6 (Get-ScheduledTask; Get-Service; ops/signaldeck-ctl.sh header)
- Evidence: Task 'SignalDeck Tunnel' (Ready; last run 2026-10-22 06:20, rc=267014) runs ngrok.exe http 8322 to an ngrok-free.dev domain, and 'SignalDeck Tunnel Keepalive' (tunnel-guard.ps1) is also Ready. No ngrok process runs now; cloudflared runs as a service. If started, the public reaches the daemon's :8322 listener without the Next proxy, widening exposure to the reachable stdlib HTTP issues (SD23-001). SUBMISSION.md:47 and COMPETITION.md describe one home PC behind a Cloudflare tunnel; the signaldeck-ctl.sh header says the tunnel 'is not registered on this machine'.
- Validation: 

### SD23-068: Latent config hazard: SIGNALDECK_PUBLIC_READS=true on this published deployment would open about 120 GET routes, including close-carrying ones, to anonymous visitors
- Source: routes#5 (security.go, config.go)
- Evidence: security.go:531 `return !d.Cfg.PublicReads` is the default for every path not in the always-open or always-auth lists (403-530). The PublicSurface allowlist is not in force: the key is absent from .env and config.go:300 defaults it to false. The member gate (security.go:152) applies only to uid!=0, so anonymous callers would exceed members. MIXED routes carrying Alpaca closes (/api/screener, /api/symbol, /api/dashboard) and /api/congress and /api/paper are not in datalicense.RestrictedRoutes, so the 451 guard would not catch them.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:daemon/internal/api/security.go:158-169 `if d.withheldFrom(r) { if src, cleared := datalicense.NonOperatorSource(r.URL.Path); !cleared { writeWithheld(w, src); return } }`. :196-201 `withheldFrom = d.published() && !d.isOperator(r)`. Live: anonymous /api/accuracy 451 and /mcp 451 at 15:05 PT, so published() is true. The SIGNALDECK_PUBLIC_READS key is present in .env (value not read); anonymous /api/version still answers 401.

### SD23-069: Member-QA recipe tests a different anonymous surface from production (PUBLIC_SURFACE=1 vs absent) and omits SIGNALDECK_ALLOWED_HOSTS
- Source: local-walk#0 (memory project_signaldeck_members_proof recipe; daemon/.env key names; api/security.go requiresAuth;); local-walk#1 (isolated daemon posture A first start)
- Evidence: daemon/.env key names include PUBLIC_READS, PUBLIC_URL and TUNNEL_LOG but no SIGNALDECK_PUBLIC_SURFACE; `grep -r SIGNALDECK_PUBLIC_SURFACE ops/` finds only message text. security.go requiresAuth applies the publicRoutes allowlist only `if d.Cfg.PublicSurface`, so the recipe's posture A (PUBLIC_SURFACE=1) does not match production's anonymous surface. The recipe also omits SIGNALDECK_ALLOWED_HOSTS: an isolated daemon on :18422 answered 403 'forbidden host: 127.0.0.1:18422 is not in the daemon's allowed-hosts list' until it was added.
- Validation: 

### SD23-079: Sweep reactivation bypasses the delisted_at guard and reactivates all 1,902 stamped stocks every weekday
- Source: survivorship#3 (ops/signaldeck-refresh.sh:192-197; logs/refresh.log; live DB)
- Evidence: refresh.sh:196 `UPDATE symbols SET active=1 WHERE market='stocks';` has no delisted_at predicate. Compare store.go:883-900 ('A row carrying delisted_at is NOT reactivated... ATC... splices two securities into one price series') and universe.go:27-30. refresh.log 2026-10-22 13:15:05: 'reactivated full universe: 2943 active'. The live DB holds 2,943 stocks in total, 1,902 of them stamped. Inside the window, poller.go:138-141 (ActiveStockSymbols stream=0) requests 1d, 1h and 1m bars for the dead tickers, which reopens the recycled-ticker splice path.
- Validation: 

### SD23-080: Revalidation 'active-only vs survivorship-clean' percentage-point effect splits on the watch flag, so lean-excluded live names are labelled delisted/inactive
- Source: survivorship#4 (tools/revalidate_structural.py:384-419; ops/revalidation-status.json; partials/live_accuracy.md:49)
- Evidence: revalidate_structural.py:384 runs `SELECT id, symbol, active FROM symbols WHERE market='stocks'`. Line 396: `(live_samples if active else dead_samples)`. Line 402 prints 'active: ... delisted/inactive: ...'. revalidation-status.json (2026-10-01 03:20) has symbols_active=293, symbols_inactive=1682. live_accuracy.md publishes 'measured effect [effect] (active-only [accuracy] minus survivorship-clean [accuracy] ...) - POSITIVE means the active-only figure is INFLATED by excluding dead names' (the effect and both accuracies are withheld under the data licence). The inactive arm includes still-trading names: about 721 such stocks exist today.
- Validation: Generator wording now says the active-only vs survivorship-clean gap mixes watch-set selection with survivorship; committed ops/revalidation-status.json reduced to a figure-free stamp (figures kept privately in data/). Shipped 73e69248, CI green.

### SD23-081: universe_membership (the point-in-time denominator) has not been rebuilt since 2026-08-22 and nothing schedules it
- Source: survivorship#5 (live DB; daemon/internal/store/pituniverse.go:75-114; grep of ops, cmd/signaldeckd and pipeline)
- Evidence: `SELECT date(day,'unixepoch'), COUNT(*) FROM universe_membership GROUP BY day ORDER BY day DESC LIMIT 5` returns 2026-08-22 with 7 rows, then 08-21 with 624 and 08-20 with 626. The only caller of RebuildUniverseMembership is the manual `sdmaint build-universe`; it appears nowhere in ops/* or in daemon worker registration. paperreplaydriver.go:94-100 refuses outside coverage. docs_gate.py:718-723 checks only that rows > 0, not freshness.
- Validation: 

### SD23-088: No SignalDeck cloud kit exists: the Docker path is a Fly.io-era demo without the production schedule, and the memory's Ubuntu kit belongs to stock-trader
- Source: kit-claims#16 (Dockerfile; ops/docker-entrypoint.sh; ops/docker-build.sh:167,210; fly.toml; .dockerignore); kit-claims#19 (memory project_push20_cloud_and_leverage_2026_09_22.md; stock-trader/deploy/)
- Evidence: The Docker runtime (ops/docker-entrypoint.sh) runs only signaldeckd, next start and a 6-hourly grade loop; it lacks anchor-publish, market-close backup/VACUUM, daily-refresh, liveness checks, restore rehearsal, cleanup, revalidation and the .venv deps, builds 3 of 18 cmds, writes into the repo root, and fly.toml is app 'signaldeck-demo' (shared-cpu-2x, 2 GB). The memory's 'existing Ubuntu cloud kit' (stock-trader/deploy/) is the PUSH-20 kit: Python cron jobs only, nothing for Go, Node/Next, cloudflared or SQLite.
- Validation: 

### SD23-092: Authorship statement in public docs/COMPETITION.md reversed on 22 Oct; the earlier wording stays readable in public history
- Source: kit-claims#1 (git log -- docs/COMPETITION.md; gh repo view); kit-claims#0 (git show 5083f8f6; git log/shortlog); rights/cac: Team vs solo rules
- Evidence: Commits 1249c436 (2026-10-04) through 1e75274d said the 51 July commits made under a second family member's git identity were partly that person's work; 5083f8f6 (22 Oct 20:55) changed this to 'all of the work in them is Nicholas's' and 'Nicholas Nyaung is the sole entrant'. The repo is PUBLIC and history is deliberately not rewritten, so judges can read both. Git-checkable facts hold (51 commits 07-01..07-15; Nicholas's commits from 07-13); work ownership is an owner attestation. CAC Rules: "The app must be original and solely created by the contestant."
- Validation: 

### SD23-093: CHECKLIST.md line 29 still describes the old shared-authorship COMPETITION.md wording and contradicts lines 31-32
- Source: kit-claims#2 (signaldeck-owner-kit/cac-2026/CHECKLIST.md:29 vs docs/COMPETITION.md @5083f8f6)
- Evidence: CHECKLIST.md:29 still says docs/COMPETITION.md describes the July commits as partly the other family member's work and partly yours, 'so this is a real question'; lines 31-32 of the same file record 'DECIDED 22 October 2026: solo entry' and cite 5083f8f6, which removed that wording. The file contradicts itself.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. Rewrite line 29: 'The repository has 51 commits from 1 to 15 July 2026 under a second family member's git identity. Until 22 October docs/COMPETITION.md said that work was partly theirs; commit 5083f8f6 changed it to say all the work is Nicholas's an

### SD23-094: SUBMISSION lists 'the operator's own TradingView alerts' as a data source in use; since 07ace1b2 they are retired as a model input
- Source: kit-claims#5 (SUBMISSION.md:36; git show 07ace1b2)
- Evidence: SUBMISSION.md was written 2026-10-22 04:12. Commit 07ace1b2 (04:29, an ancestor of the live daemon 2ddff1a2) says: 'TradingView ToS s.3 licenses content display-only and bars non-display use ... stop building tv_webhook_signal ... tv_webhook_signal joins RetiredSourceKey ... alerts are stored for the record'. SUBMISSION says none of this. Alerts also arrive only through the ngrok task: the last 'POST path=/api/tv-webhook status=200' was 2026-10-22T06:29:56.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. Remove it from the in-use list. Add to the retired line: 'the operator's TradingView alerts are kept for the record but are no longer a model input (22 October 2026, TradingView terms)'.

### SD23-095: Congressional district and state not recorded in the owner kit; eligibility unconfirmed
- Source: kit-claims#12 (grep of signaldeck-owner-kit (district, Member of Congress, state names)); rights/cac: Participant's district and Member participation (review: UNCLEAR)
- Evidence: A grep of signaldeck-owner-kit finds no district or state value: CHECKLIST.md:18 '[NICHOLAS: district office contact]', :39 '[ ] Your district is hosting the 2026 Challenge', :49-50 ask for the district and Member of Congress. The official list (https://www.congressionalappchallenge.us/students/participating-districts/: "participating in the 2026 Congressional App Challenge below") was final after the 15 Oct Member sign-up deadline. Review: district UNCLEAR (not confirmed).
- Validation: 

### SD23-096: Silent demo capture never watched end to end; it predates later web commits and shows /accuracy, which is UNREACHABLE live
- Source: logs-runtime#22 (final-report-2026-10-22.md:38); kit-claims#11 (demo-capture-notes.md:5; git log 7907f070..HEAD -- web/src; git worktree list)
- Evidence: signaldeck-owner-kit/cac-2026/demo-capture-silent.mp4 exists (39,317,547 bytes, 22 Oct 04:12) and nothing records a human viewing it. The capture ran 22 Oct 04:00-04:15 PT, before 1e75274d (04:16; terms, privacy, retired feeds, authorship) and 07ace1b2 (04:29; TradingView attribution in the AAPL chart widget); the sd-demo worktree used for scene 7 no longer exists. Its /accuracy scene currently renders UNREACHABLE live (SD23-015).
- Validation: 

### SD23-097: Five plaintext off-site backup releases remain; the backup key's offline copy is not confirmed
- Source: logs-runtime#24 (gh release list (read-only), final-report-2026-10-22.md:46-50); kit-claims#9 (gh release list/view on SIGNALDECK_OFFSITE_GH_REPO (name not printed); ops/lib-offsite-gh.sh:165-200)
- Evidence: gh release list on the private off-site repo: plaintext-era backup-20261015-131021, 20261016-131012, 20261019-175426, 20261020-184335 and 20261021-173308 remain beside the sealed 20261022-030637 and 20261022-131018; gh_offsite_prune keeps the newest 7 carrying .db.gz or .db.gz.enc, so they rotate out only as sealed backups land. The 10-22 13:10 backup verified (1,311,130,899 bytes). %USERPROFILE%\.signaldeck\backup.key exists; its offline copy is not confirmed.
- Validation: 

### SD23-098: CAC rules require the demo video to be set to 'public' on YouTube or Vimeo
- Source: rights/cac: Demonstration video requirements (2026 CAC Rules PDF)
- Evidence: 2026 CAC Rules (https://www.congressionalappchallenge.us/wp-content/uploads/2026/05/2026-CAC-Rules.pdf): "The video must be set to "public"." and "Upload the completed video to YouTube or Vimeo." Unlisted or private does not meet the rule. DEMO_SCRIPT.md (2:49) plans a public YouTube or Vimeo upload within the 1-3 minute range; CHECKLIST schedules the upload by Sun 25 Oct 12:00 PT.
- Validation: 

### SD23-099: CAC IP rule vs Alpaca's claim over derivations of its data: compliance rated UNCLEAR
- Source: rights/cac: Third-party IP, open-source and data rights (review: UNCLEAR)
- Evidence: 2026 CAC Rules s.3 (same PDF): "the app must not violate any Intellectual Property, common law, or privacy rights" and "No other party should have any rights or interest in the app". Review: UNCLEAR, not PERMITTED: open-source licensors keep copyright, the TradingView widget is proprietary, and Alpaca staff claim control over derivations of its data. The kit's licence audit records Alpaca consent and Kadoa permission as still open; the rules give no exception for unresolved data permissions.
- Validation: 

### SD23-114: check_strategy_deck grace ends; CI red on pushes from 2026-11-03
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-4)
- Evidence: origin/public-launch:STRATEGY_DECK.md:4 'Revalidate by: 2026-10-19'. tools/check_strategy_deck.py GRACE_DAYS = 14 and fails when over > 14 (UTC), i.e. from 2026-11-03.
- Validation: 

### SD23-123: Public anchors repo still carries live accuracies and registry copies
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-13)
- Evidence: `gh api repos/nyaungnicholas-wq/signaldeck-anchors`: private false, visibility public. Last commit 12a70fb6 at 2026-10-23T02:30:23Z, 'anchor + accuracy registry + statement' (pre-R2). The anchor-publish gate leaves committed copies ('the copies already committed stay put'). README.md:169 names the repo without linking it (47f27418).
- Validation: 

### SD23-124: Alpaca-derived figures still committed in the main repo tree and in public git history
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-14)
- Evidence: r2final fix.not_done: the paper_ledger closes remain in history at 2970a6a8 and earlier. Still in the tree: PREREGISTRATION.md:37-42 bands, repro/*_days.csv, research/dirfix/ablation.csv, and older docs' percentages.
- Validation: 

### SD23-125: STRATEGY_DECK section 8 still carries pre-23-Oct database measurements
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-15)
- Evidence: r2final verify[1].G2 and fix.not_done: the generated deck_facts block (universe rows, stamp counts, bar-coverage %) remains. 47f27418 narrowed README to name section 8 as the exception.
- Validation: 

### SD23-126: Legal review of /terms and /privacy (draft) and the Alpaca/Kadoa emails not done
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-16)
- Evidence: r2final fix.not_done. SUBMISSION.md:46 says the notices are drafts pending legal review.
- Validation: 

### SD23-136: Licence switch did not cover the refusal path, data-integrity, revalidation or loose switch values
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-26)
- Evidence: origin/public-launch:ops/data-integrity.json carries a 'withheld' key and only pass/fail facts. ops/revalidation-status.json is a stamp. 5fdc7aa6 adds fail-closed cases 0/true/yes/' 1'/'1 '/'' and an end-to-end refusal-path test. Sources: r2final verify[0].G1/G2 and verify[1].G3/G4/G5/G6.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:ops/data-integrity.json carries a 'withheld' key and only pass/fail facts. ops/revalidation-status.json is a stamp. 5fdc7aa6 adds fail-closed cases 0/true/yes/' 1'/'1 '/'' and an end-to-end refusal-path test. Sources: r2final verify[0].G1/G2 and verify[1].G3/G4/G5/G6.

### SD23-137: research/dirfix/paper_ledger.jsonl carried raw Alpaca closes; scan ignored JSON; wording said derived analytics are free
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-27)
- Evidence: `git show 1c0b8188 --stat` removes research/dirfix/paper_ledger.jsonl. pre-publish-scan.sh:223 checks .json/.jsonl/.ndjson. Test case 7 is mutation-checked. Sources: r2final verify[0].G3 and verify[2].G12.
- Validation: Re-verified 23 Oct ~15:05 PT: `git show 1c0b8188 --stat` removes research/dirfix/paper_ledger.jsonl. pre-publish-scan.sh:223 checks .json/.jsonl/.ndjson. Test case 7 is mutation-checked. Sources: r2final verify[0].G3 and verify[2].G12.

### SD23-138: Descriptive and legal text contradicted the operator-only gate
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-28)
- Evidence: `git grep -c 'same for every reader' origin/public-launch:web/src/app/terms/page.tsx` gives 0. `git grep -i 'graded in public|grades itself in public' -- web/src` gives no match. README.md:169 names the anchors repo without linking it. Sources: r2final verify[1].G1/G2 and verify[2].G2/G3/G4/G7/G8/G9/G10/G11, plus the wording part of verify[0].G4.
- Validation: Re-verified 23 Oct ~15:05 PT: `git grep -c 'same for every reader' origin/public-launch:web/src/app/terms/page.tsx` gives 0. `git grep -i 'graded in public|grades itself in public' -- web/src` gives no match. README.md:169 names the anchors repo without linking it. Sources: r2final verify[1].G1/G2 and verify[2].G2/G3/G4/G7/G8/G9/G10/G11, plus the wording part of verify[0].G4.

### SD23-139: Non-operator prereg view showed invented 'horizon days 0' and empty resolution
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-29)
- Evidence: origin/public-launch:daemon/internal/api/prereg.go:169 claimText copies keys only when present. Test TestClaimTextKeepsOnlyTheFieldsTheSpecFroze. Source: r2final verify[2].G1.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:daemon/internal/api/prereg.go:169 claimText copies keys only when present. Test TestClaimTextKeepsOnlyTheFieldsTheSpecFroze. Source: r2final verify[2].G1.

### SD23-142: Public verifier reported HTTP 451 from /api/ledger/range as FAIL (reads as tampering)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-32)
- Evidence: origin/public-launch:tools/verify_public_record.py:8-12 and :64 define the WITHHELD verdict. Test tools/test_verify_public_record.py. Sources: r2_result reviews[0].G3 and reviews[1].G4.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:tools/verify_public_record.py:8-12 and :64 define the WITHHELD verdict. Test tools/test_verify_public_record.py. Sources: r2_result reviews[0].G3 and reviews[1].G4.

### SD23-143: Web promised features R2 withholds (prereg 'null (frozen empty)', digest, journal, watchlist, front-door/OG copy, /today, terms crypto/congress)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-33)
- Evidence: 4349bdb8 changes FullRecord, AlertsPanel, MemberJournal, MemberWatchlist, og-image, site.ts, today, terms and privacy. 4dff9e55 adds digestWithheld and specWithheld. Live / renders 'The live record: withheld'. Sources: r2_result reviews[1].G1/G2/G3 and reviews[2].G1-G5/G7/G8.
- Validation: Re-verified 23 Oct ~15:05 PT: 4349bdb8 changes FullRecord, AlertsPanel, MemberJournal, MemberWatchlist, og-image, site.ts, today, terms and privacy. 4dff9e55 adds digestWithheld and specWithheld. Live / renders 'The live record: withheld'. Sources: r2_result reviews[1].G1/G2/G3 and reviews[2].G1-G5/G7/G8.

### SD23-004: CI toolchain provenance ambiguous: setup-go installs go1.26.0 while production is built with go1.26.6
- Source: gates#3 (gh run view 39906199230 --job 120258611370 --log)
- Evidence: The log shows "Setup go version spec 1.26.0", "go version go1.26.0 linux/amd64" and GOTOOLCHAIN='auto', from setup-go reading the `go 1.26.0` directive. daemon/go.mod has `toolchain go1.26.6`. The log has no "go: downloading go1.26.6" line and the module cache was restored, so whether CI actually compiled with 1.26.6 is unproven. Production bin/signaldeckd.exe was built with go1.26.6.
- Validation: Release 73e69248 adds a CI step (PR #40) that asserts go.mod toolchain == the Dockerfile builder image and prints the go version used; CI on PR #40 and #41 green with it; deployed binary go1.26.10.

### SD23-005: Shared golangci-lint cache on this host reports 52 phantom issues from deleted worktrees (false FAIL; could equally mask)
- Source: gates#4 (golangci-lint run (default cache) vs GOLANGCI_LINT_CACHE=<fresh scratch dir>)
- Evidence: Default cache: "52 issues: errcheck 50, staticcheck 2", EXIT=1. Every path is under ..\..\sd-h1-tvhook\daemon\... or ..\..\sd-sources\daemon\..., with "level=warning msg=[runner/source_code] Failed to get line 45 for file ...sd-h1-tvhook\daemon\internal\store\signalreport.go ... The system cannot find the path specified." Same tree with a fresh cache: "0 issues." EXIT=0, for both windows and GOOS=linux. Hand-run lint results on this machine are unreliable unless the cache is isolated. CI is not affected (fresh runner).
- Validation: 

### SD23-006: Dev-only npm tree: 5 high (braces GHSA-vfj7-8cjw-p6xm) via eslint-config-next; no patched braces exists
- Source: gates#5 (npm audit --audit-level=high; npm audit --json; npm ls braces)
- Evidence: Full audit: 5 high, exit 1. Chain from npm ls: eslint-config-next@16.4.2 > @next/eslint-plugin-next@16.4.2 > fast-glob@3.3.1 > micromatch@4.0.8 > braces@3.0.3. The advisory range is <=3.0.3 (CVSS 7.5, stack-exhaustion DoS through deeply nested patterns). npm's only "fix" is a breaking downgrade to eslint-config-next 14.2.35. `npm audit --omit=dev --audit-level=moderate` prints "found 0 vulnerabilities" with exit 0. Effect: lint-time only, with repo-controlled glob patterns and nothing shipped, so production effect is nil. CI reports this without failing (ci.yml:239 `|| true`).
- Validation: 

### SD23-007: Committed gate snapshots expire: docs-gate goes red after 2026-10-29T00:35Z, revalidation after 2026-11-05T10:20Z
- Source: gates#8 (ops/data-integrity.json, tools/docs_gate.py:219, tools/check_revalidation.py:49, check_revalidation output)
- Evidence: data-integrity.json has "generated": "2026-10-22T00:34:57.039207Z" and the bound is `int(os.getenv("SIGNALDECK_INTEGRITY_MAX_AGE_DAYS") or 7)`. check_revalidation prints "OK: revalidation 22.0d old (2026-10-01T10:20:22+00:00)" with MAX_AGE_DAYS = 35. This is by design (a stale snapshot is a violation), but any push after those instants goes red until someone runs `docs_gate.py write-integrity` / revalidate-structural on the DB host and commits the result. History shows this chore recurs: run 38195806959 "ops: refresh data-integrity snapshot (docs-gate 7-day bound)".
- Validation: 

### SD23-008: ops/test-lib-portable.sh kills every 'nosleep.keeper' PowerShell on the host by command line, not by its own PIDs
- Source: gates#10 (ops/test-lib-portable.sh (read; deliberately not executed))
- Evidence: sd_kill_stray_keepers (about line 34): `Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" | Where-Object { $_.CommandLine -match '-File' -and $_.CommandLine -match 'nosleep.keeper' ... } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`. It matches by command line, not by the PIDs the test started. Run on the production Windows box during a backup or refresh, it would also kill the real job's wake lock. On the ubuntu CI runner it is a no-op. The investigator did not run it (or ops/test-market-close-keeps-web.sh, which shims schtasks/powershell) here.
- Validation: 

### SD23-009: CI runner deprecations: Node 20 actions forced to Node 24; ubuntu-latest moving to Ubuntu 26
- Source: gates#13 (gh run view 39906199230 annotations)
- Evidence: "Node.js 20 is deprecated. The following actions target Node.js 20 but are being forced to run on Node.js 24: actions/checkout@v4, actions/setup-python@v5" (also setup-node@v4 in web). "The ubuntu-latest label will migrate to Ubuntu 26 beginning October 19, 2026". These appear on every job.
- Validation: 

### SD23-010: Prior open item: TradingView attribution on member chart pages still not seen live (unit-tested only)
- Source: logs-runtime#21 (final-report-2026-10-22.md:37)
- Evidence: The member chart page needs a signed-in session; production sign-in is forbidden in this audit, and there was no local member walk here. Still unit-tested only.
- Validation: 

### SD23-022: Production web server output is not logged anywhere; no 7-day web error coverage
- Source: logs-runtime#6 (ops/start-local-workspace.ps1, logs/web.*.log)
- Evidence: Task 'SignalDeck Web' action: powershell -File ops/start-local-workspace.ps1 -Port 8323. Line 75: & node.exe ...next start -H 127.0.0.1 -p $Port, with no redirection, so output goes to a hidden console. logs/web.out.log / web.err.log mtime 2026-09-06; the tail of web.out.log shows '▲ Next.js 16.3.0' (live is 16.4.2) and web.err.log shows old api-proxy ECONNREFUSED. These are leftovers of the older signaldeck-web-task.ps1 launcher (lines 192-194).
- Validation: 

### SD23-023: Ledger manifest stale: ops/ledger-revisions.txt lacks c8e18a2c, 7907f070 and 2ddff1a2
- Source: logs-runtime#12 (logs/check-grader-health.log + grep)
- Evidence: check-grader-health 10-22 09:20: 'LEDGER MANIFEST STALE: 1 revision(s) cited by the ledger and on the remote for 7+ days are not recorded ... c8e18a2c ... unrecorded age=10d; 2ddff1a2 ... age=0d; 7907f070 ... age=0d' (warning, RESULT ok). grep -c '^<sha>' ops/ledger-revisions.txt = 0 for each of c8e18a2c, 7907f070, 2ddff1a2. Fix: 'bash ops/ledger-provenance.sh --write' + commit.
- Validation: 

### SD23-024: worker_runs keeps no complete 7-day failure history; old failures are pruned regardless of status
- Source: logs-runtime#16 (daemon/internal/store/store.go:1802-1818; DB counts)
- Evidence: PruneWorkerRuns keeps the newest N overall, 20 per worker and 5000 prediction-runner rows; no status exemption. worker_runs count(*)=7,557 while max(id)=845,016. Retained per day: 10-16 183, 10-17 175, 10-18 198, 10-19 158, 10-20 228, 10-21 218 vs 10-22 2,469. 7-day retained failures: downsampler timeout 4; crypto-live/backfiller/stock-streamer orphaned 3 each; fred-poller error 3; stock-streamer error 3; crypto-bars 1; prediction-runner timeout 1; api-probe 1. The log shows many more (e.g. 'worker failed' 103, 'worker run timed out' 119).
- Validation: 

### SD23-025: ERROR 'request failed: sql: database is closed' (14 in 7 d) at daemon shutdowns: in-flight requests are not drained
- Source: logs-runtime#19 (logs/signaldeckd.log)
- Evidence: Group: ERROR 'request failed' 34 in 7 d. err 'context deadline exceeded' 16; 'sql: database is closed' 14 (first 10-16 13:29:52; e.g. 2026-10-22T03:06:25 'datastats symbols: sql: database is closed'), which line up with 'signaldeckd stopped' lines. In-flight requests are not drained before the store closes.
- Validation: 

### SD23-026: Daemon log rotation set inconsistent: .1/.2 missing, orphan .3.gz from August (08-17..10-05 history absent)
- Source: logs-runtime#27 (logs/ listing)
- Evidence: logs/signaldeckd.log starts 2026-10-05T17:03:12; no signaldeckd.log.1 or .2 present; signaldeckd.log.3.gz dated 2026-08-17 (651,145 bytes). The 7-day window is fully covered (no gap), but history from 08-17 to 10-05 is absent. logrotate keep=3 (main.go:58).
- Validation: 

### SD23-027: Cleanup and Accuracy tasks both trigger at 14:05 (possible I/O contention during the grade)
- Source: logs-runtime#28 (Get-ScheduledTask)
- Evidence: SignalDeck Cleanup trigger Daily 2026-08-12T14:05:00-07:00, last 10-22 14:05 res 0x0; SignalDeck Accuracy same time, last 10-22 14:05 res 0x0 (cleanup.sched.log 14:05:12, registry generated 14:05:37). No failure observed; possible I/O contention during the grade.
- Validation: 

### SD23-028: STORAGE.md hot-DB size figure is stale (says 4.8 GB; actual 10.83 GB, over the 10,240 MB budget)
- Source: logs-runtime#29 (STORAGE.md:130)
- Evidence: STORAGE.md:130 says hot data/signaldeck.db is '4.8 GB, still growing'; the actual file is 10,826,637,312 bytes, over the 10,240 MB daemon budget.
- Validation: SD23-028: STORAGE.md:130: replace '**4.8 GB**, still growing' with '**10.8 GB** on 2026-10-23, over the 10,240 MB daemon budget (gap-fill paused)'. Run `python tools/docs_gate.py check`; it must exit 0. Stage only STORAGE.md. | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-029: Live checkout carries 10 scheduler-regenerated tracked docs as uncommitted changes (they repeat the misleading survivorship line)
- Source: logs-runtime#26 (git status --porcelain (read-only), logs/accuracy.out.log); survivorship#15 (git status --porcelain (read-only); logs/accuracy.out.log:881-895)
- Evidence: M CASE_STUDY.md, HOW_PREDICTORS_WORK.md, INSTITUTIONAL_GAP.md, PREDICTION_PROCESS.md, README.md, SHIP_READINESS.md, STRATEGY_DECK.md, ops/data-integrity.json, partials/deck_facts.md, partials/live_accuracy.md. accuracy.out.log 10-22 14:05:47: 'NOTE: this run regenerated tracked documents and left them UNCOMMITTED ... Commit them when convenient.' This is by design, but any ff-merge in the live checkout must back them up and restore them first (as P3-DEPLOY did).
- Validation: 

### SD23-030: Next.js ?_rsc= prefetch ERR_ABORTED fires during page load (the 10-22 report's 'walker closes a page' explanation is wrong); not confirmed benign
- Source: prod-walk#4 (Playwright requestfailed events tagged by phase (walk.json, walk-retry.json))
- Evidence: Across the 26 verified loads there were 82 ?_rsc= aborts. All 95 request failures carry phase 'loading', meaning they fired before networkidle+2.5 s and before page.close; 0 were 'settled' or 'closing'. 23 of 26 loads prefetched the same path under two different _rsc keys, e.g. /glossary: '/accuracy?_rsc=7ebrWl8dkHoUlw7t' and '/accuracy?_rsc=jcoGI19LwHfgmxAx', both net::ERR_ABORTED. This contradicts final-report-2026-10-22.md:64 ('logs cancelled link-prefetch requests ... when the walker closes a page'). No visible effect was seen.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. In C:/Users/Nicholas_N/Desktop/claude code/signaldeck-owner-kit/final-report-2026-10-22.md add a '| Correction |' row in line 20's format: 'Line 64's cause is wrong: the ?_rsc= ERR_ABORTED prefetches fire during page load, before networkidle, not whe

### SD23-031: Signup username hint text overlaps the input's bottom border
- Source: prod-walk#5 (Screenshot w1280_signup.png (crop crop_signup_user.png) plus source)
- Evidence: The enlarged crop shows '3-32 letters, digits, . _ -' drawn across the username input's bottom border, while 'Gmail addresses only' sits cleanly below its input. Source: web/src/app/signup/page.tsx:205 '<p className="-mt-3 mb-4 text-[10px] ...">' (negative top margin), versus :225 '<p className="mb-4 text-[10px] ...">'.
- Validation: 

### SD23-032: Unknown URLs show anonymous visitors the session gate, not the 404 page; 404 title repeats the brand
- Source: prod-walk#8 (Node fetch /no-such-page-xyz plus source)
- Evidence: HTTP 404 whose body contains 'checking session' (the gate shell), with title 'Not found - SignalDeck — SignalDeck'. AuthGate wraps all children (web/src/app/layout.tsx:70). isPublicRoute('/no-such-page-xyz') is false, so a 401 from /api/auth/me triggers window.location.replace('/login?next=...') (AuthGate.tsx:62-66). The title comes from web/src/app/not-found.tsx:8 (title 'Not found - SignalDeck') combined with layout.tsx:43 (template '%s — SignalDeck'). The redirect was not exercised in the browser; it is inferred from code and the HTTP shell.
- Validation: 

### SD23-033: Landing page prints the raw fetch error text to anonymous visitors
- Source: routes#10 (web/src/app/page.tsx + live render)
- Evidence: page.tsx:100 returns detail: e.message. Live / HTML contained 'The grading service is unreachable right now ... The operation was aborted due to timeout'.
- Validation: 

### SD23-034: ModelHealthWorker ignores SIGNALDECK_REGISTRY while the API honours it, so the kill switch is unreadable where only that variable locates the registry
- Source: local-walk#2 (isolated daemon log; cmd/signaldeckd/run.go)
- Evidence: run.go:374 `fleet = append(fleet, &pipeline.ModelHealthWorker{St: st})` sets no RegistryPath. run.go:633 gives the API `RegistryPath: os.Getenv("SIGNALDECK_REGISTRY")`, and pipeline/modelhealth.go:31 has a RegistryPath field. The run.go comment says the variable is set explicitly in the container deployment. Isolated log with SIGNALDECK_REGISTRY set: `worker degraded worker=model-health err="kill switch unreadable, no model graded: no accuracy registry found at any candidate path"`, while /api/accuracy read the same file fine.
- Validation: 

### SD23-035: Member symbol page links to operator-only /intel/congress, so a member who clicks it is bounced to /today
- Source: local-walk#3 (walk link scan (member-1280 /s/stocks/MSFT, member-375 /s/stocks/AAPL))
- Evidence: web/src/components/symbol/CongressChip.tsx:37 `href="/intel/congress"` is rendered by web/src/components/symbol/MemberSymbolView.tsx:117 `<CongressChip symbol={symbol} />`. web/src/lib/memberPages.ts MEMBER_PAGES does not include /intel/*, and Shell.tsx:416-422 router.replace('/today') when offLimits. The walk captured the link '/intel/congress' on member pages at both widths.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:web/src/components/symbol/CongressChip.tsx:38-39 `if (withheld) return <Withheld compact title="Congressional trades" .../>`; the /intel/congress href at :45 is on the data branch. Commit 062c4ff3 per `git log -S withheldOf`.

### SD23-036: Public pages show 'Sign in / Sign up' to a signed-in member, with no session indicator or link back to /today
- Source: local-walk#4 (walk member-1280/375 on /, /accuracy, /proof, /signup)
- Evidence: web/src/components/PublicNav.tsx:45 'Sign in' and :50 'Sign up' are static; the file has no auth or me check. Walk member-1280 '/' bodyHead: 'SIGNALDECK Grades Volatility record Receipts Glossary Sign in Sign up OPEN RESEARCH INSTRUMENT ...', with the session valid (/api/auth/me 200, uid 8).
- Validation: 

### SD23-037: Shell renders operator chrome before auth resolves (anonymous operator fetches 401, transient /dashboard logo link); some member pages lack an h1
- Source: local-walk#5 (isolated web proxy log + walk anon-1280/375); local-walk#13 (walk link/h1 capture)
- Evidence: Isolated web proxy tally: 4 GET /api/signal-report 401 and 2 GET /api/alerts 401; anonymous /signals/report/stocks/AAPL fetched /api/signal-report and /api/alerts before redirecting to /login. Shell.tsx:420 `const operatorChrome = known && !member;` treats an anonymous visitor as non-member. Shell.tsx:540 `href={member ? "/today" : "/dashboard"}` rendered /dashboard on member /market/regimes while membership was unknown. No h1 on member /market/regimes (1280 and 375) and /s/crypto/BTC%2FUSD.
- Validation: 

### SD23-038: First UI sign-in in each new browser context returned POST /api/auth/login 400; retry after hydration returned 200
- Source: local-walk#7 (isolated daemon log, walk run 1)
- Evidence: Daemon log `time=01:15:30.356 msg=request method=POST path=/api/auth/login status=400 uid=0 ms=10` (x2, one per member context), with fields filled 800 ms after #login-username appeared. The only 400 in authLogin is the JSON decode (auth.go:399-401, 'bad json: '). With the login page waited to networkidle plus 2 s and typed with pressSequentially, both contexts logged in at the first attempt: `{status:200, landed:'/today'}`. The suspected cause is a dev-server first-compile race on the /api/[...path] proxy route; it was not reproduced, and production-build behaviour was not tested.
- Validation: 

### SD23-039: rv-forecast-runner failed on every isolated start ('refusing an incomplete forecast row', AAL h=1); likely an artefact of the no-Alpaca copy
- Source: local-walk#15 (isolated daemon log (both starts))
- Evidence: `level=WARN msg="worker failed" worker=rv-forecast-runner err="store AAL h=1: rv_forecasts: refusing an incomplete forecast row"` at 01:11:56 and 01:12:45. The isolated instance has no Alpaca ingestion (alpaca=false), so this may be an artefact of the stale or absent bars; not checked against production logs.
- Validation: 

### SD23-049: Recurring upstream errors over 7 days: Alpaca rate-limit backoffs (135), news 429s, websocket drops
- Source: logs-runtime#17 (logs/signaldeckd.log)
- Evidence: 'alpaca rate limited, backing off' 135 (10-16 26, 10-19 54, 10-20 40, 10-21 15). news-fetcher 'status N: too many requests' on 10-19, 10-20 and 10-21 plus timeouts (8). stock-streamer websocket read/dial failures 4 in log and 3 errors in worker_runs, the latest at 2026-10-23T01:00:18 (TLS handshake timeout). 'alpaca: dropping symbol the vendor rejects' 15.
- Validation: 

### SD23-058: Congress schema drops owner, official filing link, asset type, amount bounds and late flags: rows look duplicated, T-bills/options stored as stock trades, late filings hidden
- Source: congress#7 (store/congress.go, schema.sql, DB); congress#15 (api/congress.go:17, DB); congress#16 (kadoa.go, DB)
- Evidence: store/congress.go:15-25 and schema.sql:794-804 keep only chamber, member, symbol, symbol_id, tx_type, amount_range, tx_ts, disclosed_ts; Kadoa's owner, asset_type, amount bounds, days_to_file, is_late, filer_id and doc_url (the official filing link) are dropped. Effects: 45 groups (90 rows) identical on every stored field; 'US-TBILL' has 19 rows and options are stored as stock trades; the 'lag 30-45 days by law' note (api/congress.go:17) sits beside 984 rows lagging over 90 days with is_late discarded.
- Validation: 

### SD23-059: Kadoa parse path has no offline unit test; only live tests, skipped by default
- Source: congress#9 (daemon tests)
- Evidence: Grep for ParseKadoa/FetchKadoa/KadoaFallback: the only tests are kadoa_live_test.go:11-13 and pipeline/congress_kadoa_live_test.go:12-15, both of which `t.Skip` unless SIGNALDECK_LIVE_KADOA=1. testdata has only house_transactions.json and senate_transactions.json (Stock Watcher shape).
- Validation: 

### SD23-060: Congress data has no freshness monitor; a silently stale upstream reads as 'ok'
- Source: congress#10 (internal/srchealth/srchealth.go)
- Evidence: The srchealth registry (srchealth.go:61-121) has no congress_trades spec. The poller reports status ok with '0 new' (e.g. run 836889 on 2026-10-22: 'senate 0 new/80 fetched'), while senate disclosures fell to 24 in September and 5 in October with no alert.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:daemon/internal/srchealth/srchealth.go:122 `{source: "congress_trades", query: "SELECT MAX(disclosed_ts) FROM congress_trades", ... note: "... (congress-poller, retired)"}`. `git log -S` points to 1568e12f.

### SD23-061: /intel/congress default view hides most recent disclosures of older trades (344 of 562 last-30-day disclosures)
- Source: congress#11 (web/src/app/intel/congress/page.tsx, store/congress.go)
- Evidence: The page requests limit=200 (page.tsx:57). The API orders by tx_ts DESC (store/congress.go:67). The page then re-sorts by disclosedTs (page.tsx:94). The 200th-newest tx_ts is 2026-09-17. Of 562 rows disclosed in the last 30 days, 344 fall outside the default page (`SELECT COUNT(*) FROM congress_trades WHERE disclosed_ts >= now-30d AND tx_ts < <200th tx_ts>` returned 344).
- Validation: 

### SD23-062: Smart Money page claims to include congress data, but none is used
- Source: congress#12 (web/src/app/intel/smart-money/page.tsx)
- Evidence: smart-money/page.tsx:384 subtitle: "insiders, institutions and congress in one view". A daemon-wide grep for 'congress' returns no smart-money, composite or feature file, and intel/layout.tsx:40-42 describes Smart Money as "insider/short/13F/funding data".
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:web/src/app/intel/smart-money/page.tsx:384: '...insider buying and institutional holdings in one view. Congressional trades are not part of it.' Commit 062c4ff3.

### SD23-063: Each congress run polls the dead Stock Watcher S3 mirrors (403) first and downloads the whole Kadoa file twice
- Source: congress#14 (pipeline/congress.go, kadoa.go); congress#21 (curl -I)
- Evidence: ingestChamber (pipeline/congress.go:150-166) calls FetchSenate/FetchHouse on the Stock Watcher S3 mirrors (curl -I: 403 for both today) and then FetchKadoa per chamber; FetchKadoa (kadoa.go:95) GETs the whole file each time (bytes=4329412), so each run fetches ~4.3 MB twice.
- Validation: Re-verified 23 Oct ~15:05 PT: origin/public-launch:daemon/cmd/signaldeckd/run.go:1144 `func congressWorkers(st *store.Store) []workers.Worker { return nil }`, with the comment 'congress-poller removed 23 Oct 2026'.

### SD23-070: Live daemon runs with PublicSurface off: the 'skeptic with curl' publicRoutes and /api/version answer 401 anonymously, contrary to the published-record design
- Source: routes#8 (security.go comments vs live probe); prod-walk#7 (Node fetch /api/version plus daemon source)
- Evidence: security.go:291-319 describes /api/model-health, /api/canary, /api/postmortems, /api/lineage, /api/dataset-versions, /api/evidence and /api/research-loop as the machine-readable half of the published record for a skeptic, but the anonymous probe got 401 on each, and on /api/version (46 ms), which security.go:287 lists in publicRoutes. publicRoutes apply only when PublicSurface is true (security.go:345-353); daemon/.env has no SIGNALDECK_PUBLIC_SURFACE, so the live daemon runs the legacy allowlist.
- Validation: 

### SD23-071: Stale comment in next.config.ts (and route.ts:1-4) says the /api proxy attaches SIGNALDECK_API_TOKEN; restoring it would hand anonymous visitors admin
- Source: routes#9 (web/next.config.ts vs route.ts); local-walk#12 (web source read)
- Evidence: next.config.ts:5-7: 'the route handler ... forwards an allowlist of headers, attaches the server-only SIGNALDECK_API_TOKEN when set'. route.ts:95-103 deliberately does NOT attach it, because the token maps to the admin and attaching it would hand anonymous visitors admin. route.ts:1-4 also still says 'the server can attach a bearer token'. A reader trusting the stale comment could restore it.
- Validation: SD23-071: web/next.config.ts:5-7: replace 'attaches the server-only SIGNALDECK_API_TOKEN when set' with 'and deliberately never attaches SIGNALDECK_API_TOKEN (it maps to the admin; see route.ts)'. web/src/app/api/[...path]/route.ts:1-4: replace 'and the server can attach a bearer token (SIGNALDECK_API_TOKEN — | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-072: /sitemap.xml is an empty urlset because NEXT_PUBLIC_SITE_URL is unset at build
- Source: prod-walk#6 (Node fetch /sitemap.xml)
- Evidence: 200, 110 bytes: '<?xml version="1.0" encoding="UTF-8"?> <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"> </urlset>'. web/src/app/sitemap.ts returns [] when siteUrl() is null. web/src/lib/site.ts says that when NEXT_PUBLIC_SITE_URL is unset there is 'no metadataBase, no og:image, an empty sitemap'. This is documented behaviour when unset. Whether the public build should set it is an owner decision.
- Validation: Web release 23 Oct 14:46-14:48 PT ran with NEXT_PUBLIC_SITE_URL=https://signaldeck.nicholasnyaung.com (the established ops/CLOUDFLARE_TUNNEL.md invocation); build uGRCWaJdwrnd4LPlobZGS; GET http://127.0.0.1:8323/sitemap.xml at ~20:55 PT returns 11 <loc> entries starting https://signaldeck.nicholasnyaung.com; public walk 14:48-14:52 got sitemap.xml 200.

### SD23-073: Response headers: CSP allows 'unsafe-inline' scripts; HSTS lacks includeSubDomains; no build-identifying header
- Source: prod-walk#15 (Response headers (privacy.h, http.json))
- Evidence: content-security-policy: script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com https://accounts.google.com/gsi/client https://s3.tradingview.com; style-src 'self' 'unsafe-inline' .... strict-transport-security: max-age=31536000. No x-powered-by and no build or version header. The build was identified only from the RSC payload: "b":"2yUqzpcRlvKGJgtYWIyRK" matches web/.next/BUILD_ID. Prerendered pages send cache-control: s-maxage=31536000 with cf-cache-status DYNAMIC, so the edge does not cache them.
- Validation: 

### SD23-074: Posture-A public routes are expensive on first touch (model-health 18 s, quality 10.8 s, honesty 504 at 20 s); production answers them 401
- Source: local-walk#6 (isolated probe + curl timings, daemon-A log)
- Evidence: Daemon log, first touch: `path=/api/honesty status=504 uid=0 ms=19995`, `path=/api/quality status=499 uid=0 ms=20005`, `path=/api/model-health status=200 uid=0 ms=20067`, `path=/api/research-ledger status=503 ms=5000`, `/api/calibration 503 warming at 5 s`. Retimed: model-health 18.1 s, quality 10.8 s, research-ledger 9.5 s. Third call: model-health 4.6 s and quality 4.4 s, not cached; honesty and research-ledger were about 0 ms once cached. Under posture A these are anonymous, so each call is an unauthenticated multi-second compute, bounded only by the per-IP rate limit.
- Validation: 

### SD23-082: 44 delisted_at stamps conflict with SEC companies still listing the ticker on NYSE/Nasdaq (not adjudicated)
- Source: survivorship#7 (live DB symbols JOIN companies)
- Evidence: Stamped stocks with companies.exchange in (NYSE, Nasdaq): 29 Nasdaq and 13 NYSE among the stamped set, and the listing shows 44 rows. Examples: EA 2026-08-04, EQR 2026-08-04 (last print 08-17, a few post-stamp prints), AVB 2026-08-14, NSA 2026-07-21, JHG 2026-06-30, SEM 2026-06-30, CRNX 2026-08-31 (still active and in user_symbols). Bar evidence (no prints since, despite the sweep re-requesting daily) supports real exits. SEC company_tickers can lag, and some are renamed issuers (SBEV->Endovia, TOI->Starling, BLD->QXO Insulation). The investigator did not verify against a corporate-actions source.
- Validation: 

### SD23-083: Grade times are published without a timezone, and catch-up runs drift late (17:03-20:28 on 8 days)
- Source: survivorship#11 (data/accuracy_registry.json; README.md:42; Task 'SignalDeck Accuracy' settings)
- Evidence: "graded_at": "2026-10-22T14:05:37" has no offset (dt.datetime.now().isoformat()). README.md:42 reads '_Regenerated 2026-10-22T14:05:37'. The task is scheduled at 14:05 local Pacific (PDT now, UTC-7). After 2026-11-01 PST it moves to 22:05Z. StartWhenAvailable=True with WakeToRun=False gave late runs at 17:03 to 20:28 on 10-05, 06, 07, 08, 10, 19, 20, 21.
- Validation: 

### SD23-084: Phase 7 free sources (Treasury yields, BLS, Fed H.15, EDGAR facts) not added
- Source: rights/optional-data: Treasury, BLS, BEA, H.15 verdicts; rights/ndl-edgar: EDGAR (accessed 2026-10-23)
- Evidence: Rights verdicts (accessed 2026-10-23): US Treasury par yield curve, BLS Public Data API and Federal Reserve H.15 PERMITTED FOR SPECIFIED USE (H.15 with the Board citation and no implied endorsement; BLS with its 'cannot vouch' statement and retrieval date); SEC EDGAR PERMITTED for facts, filing rows and links; BEA UNAVAILABLE until a UserID exists. None is ingested; each needs an audit row, a source-health entry and ingest tests (brief Phase 7).
- Validation: 

### SD23-085: BEA Data API needs an owner click-through ToS acceptance (with indemnity) before any use
- Source: rights/optional-data: BEA Data API research and review (accessed 2026-10-23)
- Evidence: No BEA UserID exists. https://apps.bea.gov/api/signup/ requires users to "agree to the published terms of service"; the ToS (https://apps.bea.gov/API/_pdf/bea_api_tos.pdf) says "You agree to indemnify and hold harmless the BEA". Review: UNAVAILABLE today; after registration, uses 2-7 are PERMITTED FOR SPECIFIED USE with the 'not endorsed or certified by BEA' notice, within 100 requests, 100 MB and 30 errors per minute.
- Validation: 

### SD23-100: Final report's 'CI green on every pushed commit' is false: the run for 5083f8f6 was red
- Source: gates#9 (gh run list / gh run view 39897541383; signaldeck-owner-kit/final-report-2026-10-22.md:27); kit-claims#20 (gh run view 39897541383 --log-failed; gh run list)
- Evidence: signaldeck-owner-kit/final-report-2026-10-22.md:27 says 'CI green on every pushed commit', but run 39897541383 (5083f8f6, public-launch push, 2026-10-23T03:55Z) failed in the web job's 'Dependency audit - production tree must be clean' step on the next 16.3.8 advisories (critical GHSA-2464-fhr4-w9xw and two more). f37c9595 (run 39906199230) is green on all 6 jobs; pre-publish-scan passed on every listed sha.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. Add a '| Correction |' row in line 20's format: 'Line 27 overstated: CI run 39897541383 for 5083f8f6 (public-launch push, 2026-10-23T03:55Z) failed the web dependency audit on the next 16.3.8 advisories; f37c9595 (run 39906199230) fixed it and was gr

### SD23-101: Ledger row P2-SURV-LIVE is stale: it expected the 14:05 grade to stay survivorship-clean, but it published not clean
- Source: logs-runtime#10 (audits/2026-10-22-full-fix-ledger.md:41 vs registry)
- Evidence: Ledger row text: 'Follow-up: check the 14:05 PT grade publishes survivorship_clean as before' (status VERIFIED). The final report 14:37 follow-up and registry JSON show every row false. The brief's Phase 1 already lists this correction.
- Validation: SD23-101: Append after the table: '**Correction (2026-10-23):** P2-SURV-LIVE's follow-up did not hold. The 22 Oct 14:05 PT grade published no row as survivorship-clean (see SD23-076; survivorship-status v1, 196d4869). The VERIFIED status covers only the crypto-exclusion measurement.' Do not edit row 41 and ad | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-102: Kit and judge-facing docs still carry unresolved placeholders, including the already-decided solo-entry confirmation and the AI-disclosure answer
- Source: kit-claims#3 (SUBMISSION.md:4,13,26,30,56-72; DEMO_SCRIPT.md:31; docs/COMPETITION.md)
- Evidence: SUBMISSION.md:4 still reads '[NICHOLAS: confirm this is an individual entry. If anyone else wrote code, they must be listed as a team member...]', and there are about 9 more [NICHOLAS: ...] markers. DEMO_SCRIPT.md:31 still offers the TEAM variant. The public docs/COMPETITION.md keeps 'Student name: [NICHOLAS TO CONFIRM]', '3. What inspired it [NICHOLAS TO CONFIRM]', '5. ... [NICHOLAS TO CONFIRM]' and the AI-disclosure '[NICHOLAS TO CONFIRM: which tools you actually used...]', inside sections marked 'Superseded ... kept as history'.
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. SUBMISSION.md:4: 'Entrant: Nicholas Nyaung, individual entry (decided 22 October 2026; see CHECKLIST.md step 3).' DEMO_SCRIPT.md: delete the line-31 TEAM bullet. Touch no other marker and not the public docs/COMPETITION.md.

### SD23-103: CHECKLIST owner-only DB-copy list has a wrong path and misses several full-DB copies
- Source: kit-claims#8 (CHECKLIST.md:113-119; ls/stat/find in %TEMP%\claude)
- Evidence: All listed copies exist (.Trash 8 entries; OneDrive/SignalDeckBackups 6 db-like; both freeze frozen.db 4.27/4.26 GB; scratchpad/repair-20260821; %TEMP%/sd-amend-dryrun.db 6.25 GB; reh2.db, rehearsal.db, idxtest.db). Wrong path: pristine.db is at 6cecc134...\scratchpad\iso\pristine.db (7.03 GB), not iso\data\. Unlisted: 6cecc134...\iso\data\backups\signaldeck-20261001-{034022,034904,092715,094733}.db (0.60-3.48 GB) and 1b818d33...\scratchpad\iso\signaldeck\data\signaldeck.db (6.98 GB). (The 23 Oct sdqa copy was deleted by the local-walk teardown.)
- Validation: Owner-kit files (outside git) corrected 23 Oct; originals archived in signaldeck-owner-kit/cac-2026/archive-2026-10-23/; fresh-context kit review NO-GO -> must-fix G1/G2 (no Alpaca-derived verdict in SUBMISSION; option C video avoids files printing figures) and G3/G4 fixed. In cac-2026/CHECKLIST.md, change the 'old Claude session scratch copies' bullet (about line 119) to list: '6cecc134...\scratchpad\iso\data\signaldeck.db', '6cecc134...\scratchpad\iso\pristine.db', '6cecc134...\scratchpad\iso\data\backups\ (5 signalde

### SD23-104: CAC deadline time inconsistent across kit documents and official pages (all kit docs adopt 09:00 PT Mon 26 Oct)
- Source: kit-claims#13 (CHECKLIST.md:3,86; brief-full-fix-2026-10-22.md:10-12; docs/COMPETITION.md Key dates); rights/cac: Submission deadline (review: date clear, time UNCLEAR)
- Evidence: CHECKLIST.md:3 cites 09:00 PT (12:00 pm EDT) from the rulebook and notes the registration page says 9:00 pm ET; COMPETITION.md says 12:00 pm EDT. Official: the 2026 rules PDF "12:00 pm EDT, Monday, October 26th, 2026."; https://www.congressionalappchallenge.us/students/student-registration/ "October 26, 2026 at 9:00 pm ET". Review: date clear, time UNCLEAR; use 12:00 pm EDT / 09:00 PDT, the earliest. Whether districts set earlier deadlines is also UNCLEAR.
- Validation: 

### SD23-110: CFTC COT source reads stale every Friday: 10-day budget vs weekly Saturday 06:00 poll of the Tuesday report
- Source: /api/source-health after R2 deploy
- Evidence: cot_reports age 261.8 h > budget 240 h at 14:50 PT Fri; cot-poller runs ok weekly (10-03, 10-10, 10-17 ~06:00, latest report 10-13); CFTC publishes Tuesday positions on Friday.
- Validation: 

### SD23-111: pre-publish-scan price rule misses Alpaca short keys (c,o,l,vw,ap,bp)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-1)
- Evidence: origin/public-launch:ops/pre-publish-scan.sh:223 key group is (entry_close|exit_close|close|open|high|low|vwap|bid|ask|last_price|price). A read-only scan of all 91 tracked .json/.jsonl/.ndjson files at 73e69248 for short keys with numbers matched only "h": 21 (research/dirfix/best_config.json, a horizon) and "p" p-values (150). There were no c, o, l, vw, ap or bp hits, so nothing is exposed today.
- Validation: NEW-1: 1. Extend the group in ops/pre-publish-scan.sh:223 with |c|o|l|vw|ap|bp. Do not add h or p.
2. Add case 8 to ops/pre-publish-scan-test.sh. A tracked research/x/bars.jsonl holding {"t":"2026-10-01T04:00:00Z","o":101.2,"h":102.0,"l":99.5,"c":100.1,"vw":100.4} must FAIL naming only that file. A tracked | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-112: revalidate-structural.sh stages full figures at ops/revalidation-status.json.tmp (not gitignored)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-2)
- Evidence: origin/public-launch:ops/revalidate-structural.sh:19-20 `OUT="$REPO/ops/revalidation-status.json"`, `TMP="$OUT.tmp"`. The revalidator writes --json "$TMP" and `rm -f "$TMP"` runs only on clean failure exits. .gitignore covers data/ and .tmpverify/ only.
- Validation: NEW-2: 1. Set TMP="$REPO/data/revalidation-status.json.tmp" and move `mkdir -p "$REPO/data"` above the revalidator call.
2. Add `ops/*.tmp` to .gitignore.
3. In tools/test_revalidation_licence.py, have the stub revalidator record its --json argument. Assert it is under data/ and that no ops/*.tmp exists af | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-113: docs_gate withhold_integrity splits the refusal reason on the first ':'
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-3)
- Evidence: origin/public-launch:tools/docs_gate.py:726 `str(reason).split(":", 1)[0].strip() + ": detail withheld (licence)"`. The MISSING reason f"{reg_path} not found" (:1025) becomes 'C: detail withheld (licence)' on Windows. 'grader exited 0 ... (still <ISO ts>)' truncates mid-timestamp.
- Validation: 

### SD23-115: prereg claimText turns JSON null into "" / 0 for non-operators
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-5)
- Evidence: origin/public-launch:daemon/internal/api/prereg.go claimText: json.Unmarshal of `null` into string or int succeeds, so a frozen "resolution": null would become "" and "horizonDays": null would become 0. Read-only query of the live prereg_records: 162 rows, 10 with a question, none with a null or wrongly typed kind/question/resolution/horizonDays.
- Validation: 

### SD23-116: Home page fetches /api/accuracy without forwarding the cookie (operator sees withheld on /)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-6)
- Evidence: origin/public-launch:web/src/app/page.tsx:93 fetch has no cookie header. The operator walk showed withheld on / (r2final verify[3].G2).
- Validation: 

### SD23-117: Every member page calls GET /api/ask, which always answers 451 (console error)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-7)
- Evidence: origin/public-launch:web/src/components/Shell.tsx:425-431 calls api.askStatus() (lib/api.ts:665, GET /api/ask). The log shows path=/api/ask after the deploy. Anonymous gets 401.
- Validation: 

### SD23-118: Member nav still lists withheld sections
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-8)
- Evidence: origin/public-launch:web/src/components/Shell.tsx:108-114 MEMBER_NAV still holds REGIMES, MY CALLS and RECORD.
- Validation: 

### SD23-119: /account link row renders run together
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-9)
- Evidence: origin/public-launch:web/src/app/account/page.tsx:70 `<div className="space-y-2">` wraps inline <Link>s (vertical margins do not apply). Screenshot member_account-375.png.
- Validation: 

### SD23-120: Operator gets the raw-row 451 on the published deployment
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-10)
- Evidence: origin/public-launch:daemon/internal/api/security.go:179 `if !d.Cfg.AllowRawExport && d.published()`. web/src/lib/withheld.ts gives raw-row 451s their own notice (47f27418).
- Validation: 

### SD23-121: GO-LIVE runbook expects 200 from anonymous /api/accuracy; MCP closure undocumented
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-11)
- Evidence: origin/public-launch:ops/GO-LIVE.md:76 `curl ... http://127.0.0.1:8322/api/accuracy   # must print 200`. Live at 15:05 PT: anonymous /api/accuracy 451 and anonymous /mcp 451. The R2 review found the operator's MCP key also gets 451.
- Validation: NEW-11: ops/GO-LIVE.md:76: change the expectation to '# must print 451 (published deployment, licence)'. Add the operator variant `curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $SIGNALDECK_API_TOKEN" http://127.0.0.1:8322/api/accuracy   # must print 200 (operator)`, without echoing the  | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-122: Operator notifier sends Alpaca-derived content to a configured Discord webhook
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-12)
- Evidence: daemon/.env key names include SIGNALDECK_DISCORD_WEBHOOK (value not read). Source: r2final verify[0].G7.
- Validation: 

### SD23-127: 23 Oct 14:05 grade ran the pre-R2 script and could have regenerated figure-bearing docs
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-17)
- Evidence: 15:05 PT: live checkout HEAD is 73e69248 and `git --no-optional-locks status --porcelain` is empty. The anchors repo's last commit is 2026-10-23T02:30:23Z, before the 14:05 PT grade. Source: r2final verify[0].G5.
- Validation: Re-checked 23 Oct: 15:05 PT: live checkout HEAD is 73e69248 and `git --no-optional-locks status --porcelain` is empty. The anchors repo's last commit is 2026-10-23T02:30:23Z, before the 14:05 PT grade. Source: r2final verify[0].G5.

### SD23-128: Live posture not confirmed published() (gate could be inert)
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-18)
- Evidence: 15:05 PT: anonymous GET 127.0.0.1:8322/api/accuracy returned 451 {"error":"withheld","reason":"licence","source":"alpaca",...} in 5 ms, and /mcp returned 451. Source: r2_result reviews[0].G6.
- Validation: Re-checked 23 Oct: 15:05 PT: anonymous GET 127.0.0.1:8322/api/accuracy returned 451 {"error":"withheld","reason":"licence","source":"alpaca",...} in 5 ms, and /mcp returned 451. Source: r2_result reviews[0].G6.

### SD23-129: Operator step: restore the dirfix paper ledger into data/ after 1c0b8188
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-19)
- Evidence: data/dirfix_paper_ledger.jsonl exists in the live checkout. `git grep -i dirfix origin/public-launch -- ops/ .github/ daemon/cmd` finds only pre-publish-scan comments.
- Validation: Re-verified 23 Oct ~15:05 PT: data/dirfix_paper_ledger.jsonl exists in the live checkout. `git grep -i dirfix origin/public-launch -- ops/ .github/ daemon/cmd` finds only pre-publish-scan comments.

### SD23-130: Release checks not run: local -race, Playwright e2e against a real daemon, docker build, cold-clone repro, data-dependent checks
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-20)
- Evidence: r2final fix.not_done 'Not run locally'.
- Validation: 

### SD23-131: Residual public copy over-promises beside withheld panels
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-21)
- Evidence: At 73e69248:
- web/src/app/page.tsx:640 '...and how to verify the anchors yourself →', though /proof no longer links the anchors.
- volatility/page.tsx:125-128 'This page reports ... how that estimate has actually scored'.
- proof/page.tsx:484-491 still shows the 'Grade the record over' horizon picker above the withheld track record.
- glossary/page.tsx WITHHELD example names only SD-30.
- AccuracyStatusBanner PRIVATE says 'sign in to read the record'.
- signup/page.tsx:160-165 paused branch lists the accuracy registry and volatility record as public.
'This page is that record' and 'is scoring' were already fixed in 4349bdb8. Sources: r2final verify[2].G13-G15, r2 reviews[2].G9.
- Validation: 

### SD23-132: PRIVACY_NOTICE_FACTS section header dated 2026-10-22 over 23 Oct content
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-22)
- Evidence: origin/public-launch:docs/PRIVACY_NOTICE_FACTS.md:62 '## Data sources (2026-10-22)'. The deploy was daemon 14:42 PT and web 14:48 PT on 23 Oct. Source: r2final verify[2].G17.
- Validation: NEW-22: Change line 62 to '## Data sources (2026-10-23)'. Run `python tools/docs_gate.py check`. | Landed 2026-10-23 20:47 PT: PR #43 (head d93e6f55, CI 6/6 green incl. daemon -race) fast-forwarded to public-launch and the live checkout; no daemon or web restart needed (no Go change; web change is comments only). Fresh-context reviews: code GO (no gaps), docs NO-GO -> must-fix fixed (STORAGE dated cell restored). Local gates on the merge: go test 134 ok, 528 python tests, docs gates clean, pre-publish-scan-test 11/0 (also re-run in the production checkout), deployment_drift 6/6 ok.

### SD23-133: 451 Cache-Control: no-store not pinned by a test
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-23)
- Evidence: grep 'Cache-Control' in daemon/internal/api/operatoronly_test.go and security_test.go at 73e69248 finds no match. Source: r2_result reviews[1].G5.
- Validation: 

### SD23-134: Flaky TestSWRBodyCache_PersistedBodyServesWithoutAnInlineBuild under load
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-24)
- Evidence: r2_result reviews[1].G6: one 30 s failure in a full `go test -p 4` run.
- Validation: 

### SD23-135: Members lose the whole call journal (cannot file calls); MCP closed to the operator's key
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-25)
- Evidence: /api/journal* and /mcp are absent from NonOperatorRoutes; live /mcp gives 451. Source: r2_result reviews[1].G8 and reviews[0].G4.
- Validation: 

### SD23-140: Raw-row 451 fallback told the operator data 'is shown only to the operator'
- Source: 23 Oct review notes (R2 final/delta reviews), triaged 23 Oct (NEW-30)
- Evidence: 47f27418 changes web/src/lib/withheld.ts and withheld.test.mjs. Sources: r2final verify[2].G16 and verify[3].G1, and the step-7 part of r2_result reviews[2].G9.
- Validation: Re-verified 23 Oct ~15:05 PT: 47f27418 changes web/src/lib/withheld.ts and withheld.test.mjs. Sources: r2final verify[2].G16 and verify[3].G1, and the step-7 part of r2_result reviews[2].G9.

### SD23-011: pre-publish-scan advisory: 10 tracked data CSVs to eyeball against DATA_SOURCES.md; history scan ignores NUL bytes
- Source: gates#15 (bash ops/pre-publish-scan.sh (worktree), exit 0)
- Evidence: "! data files are tracked — eyeball each against DATA_SOURCES.md." It lists ops/prune-20260719-181723/deactivated_symbols.csv, repro/{directional_days,grading_protocol,pairs_inputs,prereg_claims,structural_claims,structural_days,structural_naive_days,xsfactor_inputs}.csv and research/dirfix/ablation.csv. It also prints "ops/pre-publish-scan.sh: line 175: warning: command substitution: ignored null byte in input". Final line: "SAFE TO PUBLISH". This is an expected advisory, not a failure.
- Validation: 

### SD23-012: web eslint: 2 react-hooks/exhaustive-deps warnings (triaged benign)
- Source: gates#11 (npm run lint (eslint v9.39.4); CI annotations on run 39906199230)
- Evidence: src/app/account/page.tsx 31:6 warning: React Hook useEffect has a missing dependency: 'router'. src/components/auth/Turnstile.tsx 104:6 warning: missing dependency: 'onToken'. "2 problems (0 errors, 2 warnings)", exit 0. Triage: both callers pass a stable state setter (forgot/page.tsx:50 and signup/page.tsx:288 use `onToken={setTurnstileToken}`) and useRouter is stable, so there is no stale-closure effect.
- Validation: 

### SD23-013: next build Turbopack warning: dynamic fs.readFile in /accuracy traces the whole project into server output
- Source: gates#12 (npm run build (Next.js 16.4.2 Turbopack), web-build.log)
- Evidence: "./src/app/accuracy/page.tsx:116:31 Warning: Dynamic filesystem access causes tracing of the whole project" from loadRegistry() reading path.resolve(cwd,'..','data','accuracy_registry.json') and path.resolve(cwd,'data',...). BUILD_EXIT=0, 70/70 static pages. next.config.ts sets no standalone output and the live web/ holds only .env.example, so no secret is swept in. The effect is build size and speed only.
- Validation: 

### SD23-014: Test-output noise: MODULE_TYPELESS_PACKAGE_JSON warnings, unclosed-file ResourceWarnings, simulated-lock retries with real sleeps
- Source: gates#14 (npm test log; unittest log)
- Evidence: npm test: 19 "[MODULE_TYPELESS_PACKAGE_JSON] Warning: Module type of file:///.../web/src/lib/accuracybinding.ts is not specified" lines (web/package.json has no "type"); 150 pass, 0 fail. unittest ResourceWarnings: tools/verify_public_record.py:160 (unclosed .pem file) and tools/test_accuracy_registry.py:1907 (unclosed ops/accuracy-registry.sh). "store: database is locked - attempt 1/5, retrying in 2s ... 4/5 ... 16s" comes from tools/test_forward_test_store.py, which uses sqlite3.connect(":memory:") (line 27) with an injected OperationalError.
- Validation: 

### SD23-040: Deployment-drift refusal of today's 14:05 grade (HEAD f37c9595 vs running 2ddff1a2) does not reproduce
- Source: logs-runtime#8 (tools/deployment_drift.py (run read-only), ops/accuracy-registry.sh:290-298)
- Evidence: .venv/Scripts/python.exe -B tools/deployment_drift.py --db data/signaldeck.db -> 6/6 [ok], exit=0: 'daemon-code-drift: deployed revision 2ddff1a2 runs current daemon source (no daemon commits between it and HEAD)'; 'row-revision-stamp: ... worker_runs 2770/2770 ...', with every prediction_ledger and regime_outcomes row also stamped (those forecast and outcome counts are withheld under the data licence). The check diffs only DAEMON_BINARY_PATHS (deployment_drift.py:88-93: daemon/internal/, daemon/cmd/signaldeckd/, go.mod, go.sum; *_test.go ignored), so web and docs commits never count.
- Validation: 

### SD23-041: Retired-source errors (FRED VIXCLS parse, Kraken TLS) stopped after the 10-22 deploy
- Source: logs-runtime#18 (logs/signaldeckd.log, worker_runs)
- Evidence: fred-poller 'parse csv for VIXCLS: ... bare " in non-quoted-field' 3x on 10-21; crypto-bars kraken TLS handshake timeout 10-21 22:55. grep in the log since 2026-10-22T03:04:34: fred-poller 0, crypto-bars 0, crypto-live 0, kraken 0, finra 0.
- Validation: 

### SD23-042: Landing full-page screenshot blank band is a scroll-reveal capture artefact, not a defect
- Source: prod-walk#13 (Screenshot w1280_.png plus source)
- Evidence: In the full-page capture, the area between the ticker and 'STAY IN TOUCH' is empty. The DOM nonetheless contains all 5 h2s ('What this platform has measured about itself', 'What this refuses to do', 'How you can check all of this yourself', 'Watch the record get written.', 'Get told when ...'), bodyLen 5075. web/src/app/motion.css:30 '.reveal-pending .reveal-item { animation-play-state: paused; opacity: 0; }' via components/landing/Reveal.tsx, which uses IntersectionObserver. A full-page screenshot does not scroll, so the band is an artifact.
- Validation: 

### SD23-075: 11 operator and member pages first appeared not to redirect; a 60 s recheck showed all redirect correctly (dev-server latency)
- Source: local-walk#8 (walk run 2 + recheck.cjs)
- Evidence: Walk flagged anon /journal, /ask, /watchlist/compare, /lab/signal-backtest, /lab/system and member /advanced, /hud, /welcome, /lab/signal-backtest, /lab/system, /intel/insiders, /lab/debate as stuck on 'checking session…' with no API calls yet. The recheck waited up to 60 s: every anonymous page went to /login?next=<path> with only /api/auth/me 401 and /api/health 200; every member page went to /today in 4-15 s at 1280 and 375 (member /ask correctly stays on /ask). No data rendered while gated.
- Validation: 

### SD23-089: Web has never been built on linux/arm64 (CI builds ubuntu x64; lockfile carries arm64 native packages)
- Source: kit-claims#18 (.github/workflows/ci.yml; gh run list; web/package-lock.json)
- Evidence: ci.yml web job 'runs-on: ubuntu-latest' with 'npm run lint', 'npm test', 'npm run build'. CI for f37c9595 is 'success' (2026-10-23T05:31Z). package-lock contains node_modules/@next/swc-linux-arm64-{gnu,musl}, lightningcss-linux-arm64-{gnu,musl}, @tailwindcss/oxide-linux-arm64-{gnu,musl}, @img/sharp-linux-arm64 and sharp-linuxmusl-arm64. The web deps are next 16.4.2, react 19.2.4 and lightweight-charts. Not built on arm64.
- Validation: 

### SD23-090: ARM64/Linux daemon build infeasibility does not reproduce: pure-Go SQLite, no cgo, cross-compile and vet pass for linux/arm64
- Source: kit-claims#17 (git archive f37c9595 daemon -> scratch; go build/vet; file)
- Evidence: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOFLAGS=-mod=readonly GOPROXY=off GOTOOLCHAIN=local go build -o <scratch> ./cmd/... gave exit=0 in 145 s and produced all 18 binaries. go vet ./... (linux/arm64) gave vet exit=0. file signaldeckd: 'ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked'. go.mod requires modernc.org/sqlite v1.53.0 (19 blank imports, no mattn); no 'import "C"'. OS files are //go:build !windows twins. marketcal.go:15 imports time/tzdata. go.mod has 'toolchain go1.26.6'; local is go1.26.5. The binaries were not run on ARM hardware.
- Validation:
