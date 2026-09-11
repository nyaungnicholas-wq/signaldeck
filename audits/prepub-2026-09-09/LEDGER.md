# Pre-publication audit ledger — SignalDeck, 2026-09-10

Branch public-launch. Findings below survived an adversarial verification pass.
Full evidence per finding: verified-findings.json (survived=31, refuted=27).

| # | Sev | Blocking | Dimension | Finding |
|---|---|---|---|---|
| 1 | blocker | YES | security | Shipped fly.toml demo posture publishes the entire non-user API anonymously — including /api/hud, a real brokerage account summary |
| 2 | blocker | YES | frontend-code | /lab/track-record renders the publication REFUSAL as "TOO EARLY TO GRADE" and promises the numbers unlock in ~0 trading days |
| 3 | high | - | config-env | fly.toml — the tracked deploy recipe — leaves open registration ENABLED on a public deploy, and the first registrant becomes admin |
| 4 | high | YES | security | Open registration is ON by default in the shipped container topology — SIGNALDECK_OPEN_SIGNUP is unset in fly.toml and its default evaluates true there |
| 5 | high | YES | publication-gate | The public copy promises the accuracy refusal will clear on its own; the shipped gate makes that impossible |
| 6 | high | YES | frontend-code | /lab/honesty hero tiles render mean forward return 100x too small and label the score-ordered buckets "Best"/"Worst" |
| 7 | medium | - | runtime-workers | The VACUUM off-hours gate is permanently bypassed: a 32-minute whole-file rewrite of a 5.2 GB SQLite ran mid-fleet at 01:25 ET and took 13 worker runs and the daemon's own HTTP API down with it |
| 8 | medium | - | runtime-workers | The Agents page shows 34 of 103 workers and renders "Errors 0" while the daemon's own /api/ready says a worker is erroring - /api/agents returns the newest 200 rows fleet-wide, the exact defect already fixed for /api/ready in August |
| 9 | medium | - | security | Session cookie can never carry Secure behind the documented TLS deployment, and no HSTS header is sent |
| 10 | medium | - | security | Cross-tenant leak: the shared "Risk watcher" insight is built from ALL users' open paper positions and republished market-wide |
| 11 | medium | - | publication-gate | The refusal reason published on every surface was computed by superseded gate code and names days the shipped gate excludes |
| 12 | medium | - | publication-gate | The always-on CI honesty gate (FC1 markdown scan) is red on the release branch, on false positives |
| 13 | low | - | config-env | Both .env.example files instruct the operator to set SIGNALDECK_API_TOKEN for the web proxy, which the proxy deliberately refuses to use |
| 14 | low | - | config-env | DEPLOY.md's 'Known deployment gaps' names a hardcoded path in ops/signaldeck-ctl.sh line 163 that does not exist |
| 15 | low | - | runtime-workers | /api/ready returns 503 for up to an hour on a transient upstream LLM 503, because ai-analyst does not handle llm.ErrTransient - the sibling worker on the same client already does |
| 16 | low | - | runtime-workers | /api/fleet-health judges calendar workers against Interval(), which for a ScheduledWorker is not the cadence - it reports the two weekly workers stale ~99% of the time and contradicts data/health.json in the same instant |
| 17 | low | - | runtime-workers | Ledger revision b84670c9 is unreachable from every ref and carries 266 post-epoch verdict rows that the next git gc will strip permanently; the guard that reports it has never actually executed |
| 18 | low | - | db-schema | DEPLOY.md tells an operator the first boot creates 97 tables; it creates 105 |
| 19 | low | - | security | Rate limiting collapses to one shared bucket for every anonymous visitor on the container topology, and the documented fix (TRUST_PROXY) makes the key spoofable |
| 20 | low | - | security | The go-live runbook's ALLOWED_HOSTS instruction would 403 every request in the container topology |
| 21 | low | - | publication-gate | The README accuracy-freshness guard cannot run during a refusal, and nothing runs it automatically anywhere |
| 22 | low | - | publication-gate | The 24.7pp / 97.6% / 54,969 trend21 claim is hand-typed in the shipped MCP surface and is now stale against the repo's own standing measurement |
| 23 | low | - | ingestion-providers | 36 permanently corrupt 1h bars remain in the served record from the partial-bucket rollup defect; Rollup's open/close subqueries are still unbounded |
| 24 | low | - | ingestion-providers | BackfillReconciler re-enqueues 12 permanently-under-covered symbols every 20 minutes forever; minCoverage assumes an untrue fact about new listings |
| 25 | low | - | dead-code-placeholders | /market/activity page metadata promises a rule-citation and a measured-outcomes table that the page does not render; the components that did are dead code and /api/alert-outcomes has no consumer |
| 26 | low | - | dead-code-placeholders | /market/unusual metadata promises compound signals; the word appears nowhere in the page or the API client, and the whole components/signals/unusual/ directory (7 files) is dead |
| 27 | info | - | runtime-workers | logs/daemon-provenance.log contains torn and interleaved writes - truncated line fragments and untimestamped duplicate blocks - so the provenance audit trail is not reliably readable |
| 28 | info | - | security | The bootstrap admin password cannot be rotated through the application and the runbook tells you to read it out of host log retention |
| 29 | info | - | security | Security documentation drift: three statements about the security posture are stale or absent |
| 30 | info | - | publication-gate | ops/check-grader-health.ps1 no longer surfaces an ongoing publication refusal |
| 31 | info | - | dead-code-placeholders | Six additional React components are dead (zero references), including the whole predict/ explainer pair |

## Refuted by verification (27) — recorded so they are not re-raised

- **NEXT_PUBLIC_SIGNALDECK_PUBLIC and NEXT_PUBLIC_SITE_URL are build-time-only, the Dockerfile web build declares ** — The finding's mechanism is half-real, but its central user-impact claim is contradicted by a file the auditor imported and then did not read, and half its file evidence no longer matches the tree. Severity is inflated fr
- **SIGNALDECK_ASSUME_TUNNEL fails OPEN on an unrecognised value, contradicting the codebase's own fail-closed boo** — The CODE claim is accurate — I confirmed the exact text at daemon/internal/config/config.go:479-482 (`if v := strings.TrimSpace(os.Getenv("SIGNALDECK_ASSUME_TUNNEL")); v != "" { return v == "1" || strings.EqualFold(v, "t
- **SIGNALDECK_ALLOW_DIRTY_BUILD is tested by presence, so setting it to 0 or false DISABLES the provenance gate** — The auditor read the code correctly — main.go:87 and :110 do presence-test, and daemon/.env values do reach os.Environ before the gate, so `SIGNALDECK_ALLOW_DIRTY_BUILD=0` would indeed enable the override. That mechanica
- **web/.env.example is gitignored and untracked — the web tier's env template does not exist in any clone, and th** — The git mechanics are real but the defect is not. Three of the finding's load-bearing claims are false, and the remedy it implies would make the release worse.

1. "the web tier's two server-side variables" — FALSE. `gre
- **82 of 128 daemon environment variables are undocumented, including every retention key the code itself classif** — The headline claim — "82 of 128 daemon environment variables are undocumented, including every retention key ... and SIGNALDECK_PUBLIC_SURFACE" — is false. The repro measures absence from ONE file (.env.example) and the 
- **The fleet-quiesce drain has NEVER succeeded: 724 of 724 recorded attempts timed out, so every WAL checkpoint p** — The mechanical observations are real, but the statistic that carries the finding is invalid and the user-impact conclusion is inverted. What survives is a low-severity tuning nit, not a high-severity defect.

CONFIRMED. 
- **/api/health - the endpoint both the Fly check and the Docker HEALTHCHECK probe - does a per-worker query over ** — The mechanism is real but the causal claim is falsified by the auditor's own evidence source.

WHAT IS TRUE: /api/health does two SQLite reads before branching on the caller. daemon/internal/api/api.go:485 (GetMeta for s
- **ops/check-grader-health.ps1 invokes bare `python` instead of the repo venv interpreter, so the ledger guard ru** — The two cited lines exist verbatim, but every load-bearing claim built on top of them is wrong or overstated, and the residual is a style nit in an ops-only script with zero user-visible surface.

1. "the interpreter eve
- **Five tables and four indexes exist in the live database but are declared nowhere the Go schema contract can se** — The raw count (105 declared / 111 live) is real and I reproduced it byte-for-byte. Every claimed consequence is wrong.

1) "Four indexes declared in no source file at all" is factually false for one of them. tools/valida
- **regime_changes has no dedup constraint and its guard read runs outside the write transaction: 217 (symbol_id, ** — The observable facts reproduce, but the finding's root cause, its implied remedy, and its severity are all wrong.

WHAT SURVIVES (verified myself):
- daemon/internal/store/schema.sql:184-191 — regime_changes has only `id
- **publication_verdicts is empty and its sticky-retirement trigger has never fired, because the only production w** — The auditor's raw observations are true, but the finding is a mischaracterization on three counts, and its stated user impact is already handled by a different, populated belt.

1. THE QUOTED COMMENT IS THE FIX'S OWN CHA
- **Timestamps are unix INTEGER everywhere except two tables that use ISO-8601 TEXT, and the day key is INTEGER in** — Refuted as a defect, though the census itself is accurate and reproducible.

The auditor did honest work and stated the limit themselves: "No incorrect value observed today." What they reported is a schema-shape observat
- **The datalicense 451 guard covers only 4 of the 10 routes its own table governs, and its middleware backstop is** — The mechanical sub-claims are true, but the headline is inverted and the user-impact claim is unevidenced.

WHAT SURVIVES (I verified each):
- datalicense.RestrictedRoutes governs 10 paths (daemon/internal/datalicense/da
- **isAdmin is stored and reported but never authorizes anything; any authenticated user can drive the operator's ** — The bare code observation is accurate — `IsAdmin` genuinely authorizes nothing — but the finding's security claim, its root cause, and two of its three cited attack surfaces are all wrong.

1. THE CODE FACT HOLDS. My own
- **SHIP_READINESS.md publishes a bare uncaveated 'trend21 82%' two lines under the GRADING REFUSED block, plus se** — The finding's load-bearing claim — "an accuracy figure that no registered claim or measurement supports" — is false, and three of its four supporting sub-claims are wrong or selectively framed. What survives is ordinary 
- **Document-control metadata disagrees between the file headers and ops/docs-registry.json, and both gates report** — The finding's quoted lines are accurate, but its root cause and user impact are both wrong, and the one true residue is editorial hygiene on an internal file.

1) The claimed missing enforcement EXISTS and is green. The 
- **Streamed hot set lost an entire NYSE session of 1m/1h bars and gap-fill cannot heal it: the 24h cooldown is wr** — The symptom is real; the root cause, the "cannot heal" conclusion, and the high/blocking severity are all refuted by the timestamps.

WHAT IS TRUE
1. The code line is as quoted. daemon/internal/pipeline/backfill.go:180-1
- **Freshness registry uses MAX(ts) over mixed populations, so a dead stream reads fresh — bars_1m_hot and tv_quot** — The SQL quotes are accurate but the finding's evidence, root cause and impact are all wrong. What survives is a narrow monitoring-precision gap in an operator-only board, not a high-severity defect.

CONFIRMED (code read
- **No price validation at the ingest→store boundary: +Inf, zero and inverted-OHLC bars can be written; Kraken's p** — The literal code claims are accurate — I read every cited line myself and they say what is claimed. UpsertBars (daemon/internal/store/store.go:845-867) really is a bare INSERT OR REPLACE loop; schema.sql:15-22 really has
- **Ten of thirteen external HTTP clients have no 429 / rate-limit handling, including the crypto price source** — The factual SURVEY is accurate, but the USER IMPACT that carries the "medium" severity is wrong, and I disproved it directly.

What holds: daemon/internal/ingest/cryptohist/kraken.go:152-153 does read `if resp.StatusCode
- **Second-source price validation — the only check that can catch a quietly-wrong provider — is unconfigured, and** — The finding has two halves. Half one is factually true but is not a defect; half two is factually false, and I disproved it against the live system.

HALF ONE — "pricecheck is unconfigured". True, and I reproduced it. Bu
- **ValidateSymbol bypasses the shared 429 back-off path** — The one-line code observation is literally true — daemon/internal/ingest/alpaca/client.go:287 is `resp, err := c.httpClient().Do(req)`, and fetchBarsPage (client.go:262) / fetchMultiBarsPage (client.go:540) do call `c.ge
- **Nothing in the frontend reads /api/health.degraded or /api/ready — the header shows a green "daemon live" dot ** — Refuted as framed. Three of the finding's four pillars fail:

1. "No surface can say degraded" — false. /api/agents carries status "degraded" plus a plain-English reason, and /lab/system/agents prints that status word ve
- **/api/fleet-health publishes "this list cannot go stale the way a design document does" while 7 of its 9 live=t** — The observable facts hold - seven of nine live flags are Go literals, and the served note is exactly as quoted - but they do not add up to the defect claimed.

The note says Live is asserted alongside the worker or route
- **The entire Playwright e2e suite — including the publication-refusal honesty contract — is executed by no autom** — The mechanical fact is right (CI's `web` job stops at build; nothing in .github/ or ops/ invokes `npm run e2e`), but the finding's headline claim — "the automated guard on the single claim the product is built around is 
- **Two maintained gates (audit_register.py, schema_contract_check.py) are invoked by nothing — only their tests r** — REFUTED. The finding's premise ("CI verifies that the checkers work while never running them against the repo") is false. Both gates ARE run against the real tree on every CI run — not through a workflow step naming the 
- **MCP server construction error is discarded with no log line** — The quoted source is accurate, but there is no defect to fix — the branch is doubly unreachable in the release configuration, and the exact failure mode the auditor speculates about is already covered by a live, passing 

## Addendum — prediction_ledger freeze-once (verified, deliberately NOT fixed)

Verified directly against the live DB 2026-09-10: 335 identities in prediction_ledger carry two entries; 15 disagree on the published probability; 124 on feature_hash. idx_prediction_ledger_ident is a plain CREATE INDEX, not UNIQUE, and pipeline/predict.go appends unconditionally, so nothing enforces the freeze-once guarantee this file header asserts ("appends exactly one ledger row"). VerifyLedger cannot detect it: both rows append validly.

SCOPE, measured: the duplicates stop at 2026-08-04 while the ledger runs to 2026-09-10 (five clean weeks). Post-epoch (>= survivorship epoch 2026-07-24) there are 87 duplicate identities and ZERO with differing cal_prob. So no published or gradeable figure is affected; all 15 conflicting-probability cases are pre-epoch.

A freeze-once guard in Store.AppendLedger was implemented and then REVERTED. It works and its test passes, but it contradicts an explicit existing spec: TestLedger_AppendOnly_NoUpdatePath deliberately appends an identical-identity entry and asserts a SECOND ROW appears ("an upsert would collapse it; an append-only ledger creates a second row"). Refusing the append also means the tamper-evident log stops recording something the system actually did.

This is a design decision, not a repair: (a) refuse the second freeze in AppendLedger and amend that test, or (b) keep append-only and make every reader deterministically take the FIRST entry per identity. (b) changes no ledger semantics. Nicholas decides.

