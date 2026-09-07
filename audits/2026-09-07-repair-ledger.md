# SignalDeck repair ledger - 2026-09-07 pass
repo C:\Users\Nicholas_N\Desktop\claude code\signaldeck, branch public-launch, baseline HEAD ecc73d219bcd50192e74d1209d905085646dd66c; every item records problem, evidence, impact, root cause, repair, regression check, and status. Statuses: FIXED (code changed, tests pass, not yet deployed), VERIFIED (deployed and observed), BLOCKED (needs an action only the operator can take), NOT-A-DEFECT, OPEN.
| id | area | problem | root cause | repair | regression check | status |
|---|---|---|---|---|---|---|
| R9 | security | SIGNALDECK_LOCAL_ONLY_PROXY=1 made route.ts delete X-Forwarded-For, allowing remote raw-bar access via non-loopback Next | exception keyed on header absence | shared key SIGNALDECK_LOCAL_PROXY_KEY; daemon checks X-Signaldeck-Local only on loopback RemoteAddr; proxy sends key only in local-only mode; X-Forwarded-For no longer deleted | daemon/internal/api/localproxy_test.go (keyed+XFF+loopback→200; wrong key→451; non-loopback→451; unset key→451) | FIXED |
| R10 | paper | paper trader as-of clock used max(latest daily bar) and advanced onto still-forming daily bar, causing equity marks on intraday price labelled close | as-of clock logic | DailyBarSettled (stocks ts+22h, crypto ts+24h); pipeline/paper.go falls back to previous bar when newest unsettled and bounds future-dated vendor bars | marketdata/settled_test.go, pipeline/paper_settled_test.go | FIXED |
| R11 | paper | /api/paper returned only book facts; flagship-1d abstained correctly but empty state urged user to add bars | missing abstention stats and UI text | store/paperprocess.go (LastWorkerRun, PaperAbstentionStats); api/paperprocess.go verdict abstaining, active, stalled, failing, unknown with numbers; web SimulatorStatus panel on /lab/paper; empty-state text corrected | none automated yet (browser verification pending) | FIXED |
| R12 | paper | GET /api/paper?strategy= accepted any string and served replay/reconstruction or empty book as live | missing strategy validation | return 404 unless strategy is flagship-1d or flagship-1w | (none mentioned) | FIXED |
| R13 | forecast/resolver | outcome-resolver head-of-line blocked; 1d/1w queues stalled at 3644 due to stale EA/MVO rows voiding only at 30 days, blocking ~1M resolvable rows | rows not voided when base window closed >3 horizons before score and no forward bar | maintain.go voids row immediately when base window closed >3 horizons before score and no forward bar; pipeline.go signal-runner skips stocks with no daily bar in 10 days and reports count | maintain/resolver_headofline_test.go (void-immediately, still-waits-for-live-row) | FIXED |
| R14 | forecast/resolver | maintain.go score resolver lacked 6h DST stamp slack, causing 1w outcomes to grade 8-session move as one week | missing DST slack for daily-bar horizons | applied resolverDSTSlackSecs for daily-bar horizons | TestOutcomeResolverOneWeekAcrossDSTStamp | FIXED |
| R15 | forecast | prediction runner built features on forming daily bar while resolver graded from settled close, causing mismatch | runner used forming bar | pipeline/settledbase.go trims forming bar (trimFormingDaily); rows frozen at/after settledBaseSinceTs=1788807600 graded from newest settled bar; earlier rows keep rule | covered by settled_test.go; runner-level test OPEN | FIXED (partial: legs that read bars directly from store not covered) |
| R16 | forecast monitor | cross-section gate path wrote no evidence row, collapsing coverage denominator (0 of 42 then 0 of 1 for 329 symbols) and blinding starvation check under 30 symbols | missing evidence row write | pipeline/predict_evidence.go writeEvidenceRow (n_used=0 evidence row, one per symbol per trading day) used by both no-leg and gated paths | OPEN (unit test pending) | FIXED |
| R17 | forecast display | forecast-trainer stamped forecasts.ts with wall-clock, showing "trained just now" on closed market; ModelRaceCard chips lacked multiplicity caveat while /accuracy refused | timestamp used wall-clock instead of data-as-of | forecasts.ts set to newest settled bar ts (data-as-of); card label "data as of"; chip text states per-symbol, uncorrected for ~2,500-way selection and overlapping windows | (none mentioned) | FIXED |
| R18 | web auth checks | eight sites checked e.message for "API 401"/"401" but get()/post() replaced message with daemon's sentence, breaking login control and 401 fallbacks | message-based error check | isAuthError(e) status-based in lib/api.ts; post() throws ApiError; all eight sites switched; /health added to PUBLIC_ROUTES; dashboard hint distinguishes 401 from outage; track-record hint no longer asserts outage; chart effect honours retryTick; watchlist shows real error text and failed removal reloads row; market/unusual 404 detection by status | Playwright smoke suite (37/38 before; failing "/" case was stale assertion that public landing page redirects to /login, corrected) | FIXED |
| R19 | ingestion | fleet-health dataAgeSeconds used cross-market max, hiding dead stock feed (crypto nightly bar masked stock age 304,523s on Labor Day) | aggregation across markets | per-market newest bar; stalest market wins | (none mentioned) | FIXED |
| R20 | ingestion (latent) | 1h->1d rollup buckets to UTC midnight while stock daily bars are ET-midnight stamped, causing duplicate daily rows after 1h history ages past keep1h | timezone mismatch in rollup | rollup runs for crypto only | (none mentioned) | FIXED |
| R21 | ops | ops/signaldeck-ctl.sh deploy only verified /api/version; CLAUDE.md requires worker_runs.revision == HEAD | missing verification of worker_runs stamp | step 6 waits up to 180s for a stamped worker_runs row | (none mentioned) | FIXED (exercised on the next deploy) |
| R22 | ops | store.Close never checkpointed WAL despite run.go and market-close.sh claiming shutdown does | missing WAL checkpoint on Close | PRAGMA wal_checkpoint(TRUNCATE) on Close | (none mentioned) | FIXED |
| R23 | ops | logs/daemon-guard.log grew unbounded (2 MB every 5 min) | no log rotation | keep last 2000 lines when over 5 MB | (none mentioned) | FIXED |
| R24 | ops/web process | port 8323 held by node orphan from session-0 S4U logon; registered SignalDeck Web action lacks -H and binds all interfaces; cannot modify or stop unelevated (Access denied) | orphan process and missing -H flag | ops/start-local-workspace.ps1 now takes -Port for loopback-only keyed launcher on 8323; operator must run elevated ops/restart-web.ps1 to free port and re-point task action to launcher with -Port 8323 | (none mentioned) | BLOCKED (administrator action) |
| R25 | ops/backup | no genuine off-machine backup for 23 days; OneDrive fallback on same volume refused; restore rehearsals only local copies | missing offsite destination config | set SIGNALDECK_OFFSITE_DIR (path on another physical volume) or SIGNALDECK_OFFSITE_S3 in daemon/.env; lib-offsite-env.sh already loads it | (none mentioned) | BLOCKED (destination is the operator's decision) |
| R26 | congress feeds | both Stock Watcher S3 mirrors return HTTP 403 (verified 2026-09-07); worker degrades honestly serving stored history | upstream mirrors unavailable | SIGNALDECK_SENATE_TRADES_URL / SIGNALDECK_HOUSE_TRADES_URL env overrides accept any mirror in same JSON shape; Senate eFD / House Clerk publish PDFs, not that shape, needing new parser | (none mentioned) | BLOCKED (no available upstream) |
| R27 | evidence | corrected paper simulator (epoch 4, boundary 2026-09-07T00:56:46Z) has zero marks and zero fills; nothing before it is record of corrected model; with R10 first mark lands on first settled daily bar after deploy | (none, it's expected) | (none needed) | (none) | NOT-A-DEFECT (needs forward observations) |
| R28 | evidence | directional forecasts retired/withheld; accuracy publication gate refuses due to newest-28-session window containing collapsed days 2026-07-31..08-06 (isotonic map emitted 5-33 distinct probs for ~329 symbols); collapse not recurring but detector cannot confirm below 30 symbols/day; clears as resolved days accrue | insufficient daily symbols for detector | (none, by design) | (none) | NOT-A-DEFECT (thresholds untouched by design) |
| R29 | manual paper trading | manual portfolio (positions table) lacks cash accounting or costs; flagship books model-driven only; no manual all-horizon paper book implemented | missing feature | (none) | (none) | OPEN |
| R30 | tests | 1h score voids are time-of-day censored (1.23M voids, 67% of terminal 1h rows, concentrated outside RTH) and nothing records the conditioning | missing conditioning recording | (none) | (none) | OPEN (documented; no code change) |
## Details
### R9
Evidence: route.ts deleted X-Forwarded-For when SIGNALDECK_LOCAL_ONLY_PROXY=1, leaving daemon unable to detect proxy. Impact: remote users could read licensed raw bars if Next bound to non-loopback. Remaining limitation: none noted.
### R10
Evidence: DB showed today's BTC/USD 1d close equaled newest 1m close. Impact: equity marks, barrier exits, cursor advanced on intraday price labelled close. Remaining limitation: none.
### R11
Evidence: /api/paper returned only book facts; flagship-1d abstained correctly but empty state urged adding bars. Impact: user confusion. Remaining limitation: none automated verification pending.
### R12
Evidence: GET /api/paper?strategy= accepted any string and served replay/reconstruction or empty book as live. Impact: potential misuse. Remaining limitation: none.
### R13
Evidence: 1d/1w queues stalled at 3644 due to stale EA/MVO rows voiding only at 30 days, blocking ~1M resolvable rows. Impact: no progress in outcome resolution. Remaining limitation: none.
### R14
Evidence: maintain.go lacked 6h DST stamp slack, causing 1w outcomes to grade 8-session move as one week. Impact: inaccurate weekly outcomes. Remaining limitation: none.
### R15
Evidence: prediction runner used forming daily bar while resolver used settled close. Impact: feature/target mismatch. Remaining limitation: legs that read bars directly from store not covered.
### R16
Evidence: cross-section gate path wrote no evidence row, collapsing coverage denominator (0 of 42 then 0 of 1 for 329 symbols) and blinding starvation check under 30 symbols. Impact: monitor reported zero coverage. Remaining limitation: unit test pending.
### R17
Evidence: forecast-trainer stamped forecasts.ts with wall-clock, showing "trained just now" on closed market; ModelRaceCard chips lacked multiplicity caveat while /accuracy refused. Impact: misleading freshness and overstated significance. Remaining limitation: none.
### R18
Evidence: eight sites checked e.message for "API 401"/"401" but get()/post() replaced message. Impact: login control never rendered for signed-out visitors, 401 fallbacks never fired. Remaining limitation: none.
### R19
Evidence: fleet-health dataAgeSeconds used cross-market max, hiding dead stock feed (crypto nightly bar masked stock age 304,523s on Labor Day). Impact: stale health metric. Remaining limitation: none.
### R20
Evidence: 1h->1d rollup buckets to UTC midnight while stock daily bars are ET-midnight stamped, causing duplicate daily rows after 1h history ages past keep1h. Impact: potential duplicate bars. Remaining limitation: none.
### R21
Evidence: ops/signaldeck-ctl.sh deploy only verified /api/version; CLAUDE.md requires worker_runs.revision == HEAD. Impact: deploy could pass without correct revision. Remaining limitation: none.
### R22
Evidence: store.Close never checkpointed WAL despite claims. Impact: possible WAL growth on shutdown. Remaining limitation: none.
### R23
Evidence: logs/daemon-guard.log grew unbounded (2 MB every 5 min). Impact: disk usage increase. Remaining limitation: none.
### R24
Evidence: port 8323 held by node orphan from session-0 S4U logon; registered SignalDeck Web action lacks -H and binds all interfaces; cannot modify or stop unelevated. Impact: stale bundle served, port blocked. Remaining limitation: requires administrator action.
### R25
Evidence: no genuine off-machine backup for 23 days; OneDrive fallback on same volume refused; restore rehearsals only local copies. Impact: risk of data loss. Remaining limitation: operator must decide destination.
### R26
Evidence: both Stock Watcher S3 mirrors return HTTP 403. Impact: inability to fetch live congress trades. Remaining limitation: no available upstream mirror of required JSON shape.
### R27
Evidence: corrected paper simulator epoch 4 boundary 2026-09-07T00:56:46Z has zero marks and zero fills. Impact: no historical record before correction. Remaining limitation: needs forward observations to validate.
### R28
Evidence: directional forecasts retired/withheld; accuracy publication gate refuses due to newest-28-session window containing collapsed days 2026-07-31..08-06. Impact: no accuracy publication. Remaining limitation: detector cannot confirm below 30 symbols/day.
### R29
Evidence: manual portfolio lacks cash accounting or costs; flagship books model-driven only; no manual all-horizon paper book. Impact: limited manual trading realism. Remaining limitation: feature not implemented.
### R30
Evidence: 1h score voids are time-of-day censored (1.23M voids, 67% of terminal 1h rows, concentrated outside RTH) and nothing records conditioning. Impact: missing insight into void causes. Remaining limitation: documented but no code change.
## Status update after deployment (2026-09-07, commits ab715e1 / 44b8281 / 5b88097 / 2ae15b1)
- R9 VERIFIED live: via port 3000 with the key, GET /api/bars -> 200; direct to the daemon with X-Forwarded-For and no key -> 451; wrong key -> 451; unauthenticated -> 401.
- R11 VERIFIED live: /api/paper now carries `process` (verdict, last run age, 6 of 10,786 forecasts >= 0.60 in 30 days, 655 of 695 EV decisions DO_NOTHING); SIMULATOR STATUS panel renders on /lab/paper.
- R12 VERIFIED live: /api/paper?strategy=flagship-1d-replay -> 404.
- R13 VERIFIED live: outcome-resolver runs after deploy report "resolved 3088, voided 1062, waiting 350" then "resolved 3101, voided 1049, waiting 350" (was pinned at waiting 3644 with zero 1d/1w progress).
- R15 observed live: prediction-runner reports "7 symbol(s) scored on settled bars only - newest daily bar still forming" on the holiday (the 7 crypto symbols).
- R16 observed live: 43 symbols carry a 1d evidence row (n_used=0) within 3h of deploy where gated days previously left 0-1.
- R18 VERIFIED in browser: the landing page shows the "Open workspace" control to a signed-out visitor; /health is public; Playwright 38/38.
- R19 VERIFIED live: fleet-health dataAgeSeconds = 310,958 (stocks since Friday) instead of the crypto-masked 59,464.
- R10 note: the pre-fix code had already advanced the flagship cursors onto the forming 2026-09-07 crypto bar (1788739200), so the settled clock reports asof=1788652800 < cursor and the books no-op until a newer SETTLED bar exists: the 2026-09-08 crypto bar settles at 2026-09-09T00:00Z and the 2026-09-08 stock bars at 2026-09-09T02:00Z. Epoch 4 stays empty until then; that is the first corrected observation, not a stall.
- R24: ops/fix-web-task.ps1 (elevated) re-points the SignalDeck Web task at the loopback launcher with -Port 8323; ops/restart-web.ps1 (elevated) frees the orphaned port. Still BLOCKED on an administrator run.
- R29 VERIFIED live (daemon e38b39f): BTC/USD buy via port 3000 filled at the 1m bar open 79166.5 with 3.5 bps spread + impact (cost 0.0554), cash 100000 -> 99841.667 reconciles; SPY on the holiday refused "no fresh quote"; oversell refused; Playwright paper-manual.spec (2 tests) passes after dismissing the first-run tour like the other specs. Personalization specs flaked once in the full run right after the daemon restart and pass on rerun.
- R21: first run exposed a defect in the new step itself (a file: URI cannot hold the space in the repo path) - fixed in 44b8281 (relative URI); the check then counted 55 stamped rows.

## Verification commands
daemon `go test ./...` (all packages ok, 2026-09-07), `npx tsc --noEmit` (exit 0), `npx playwright test` (to be re-run after build), `ops/signaldeck-ctl.sh deploy` (verifies /api/version and worker_runs.revision)