# Public Release Plan — SignalDeck

## Summary
The codebase, container image, backup discipline, and anonymous public surface are implemented and tested locally. A public deployment requires the owner to approve paid hosting, a public hostname, a separate public source repository, and whether the anchors repository becomes public.

## Architecture of a public deployment
Two processes in one container image (daemon `signaldeckd` on port 8322, Next.js web on port 8323), a persistent volume for SQLite (`data/signaldeck.db`), and a reverse proxy terminating TLS. The daemon binds loopback only; the proxy exposes 443. `SIGNALDECK_PUBLIC_SURFACE=1` and `NEXT_PUBLIC_SIGNALDECK_PUBLIC=1` activate the anonymous read allowlist and redirect anonymous visitors to `/` instead of `/login`.

| Surface | Who can read | How enforced |
|---------|--------------|--------------|
| Public API routes (accuracy, track-record, honesty, calibration, model-health, canary, postmortems, self-audit, lineage, quality, dataset-versions, evidence, research-loop, research-ledger, prereg, ledger, ledger/verify, ledger/anchors, vol-forecast/record, health, ready, version, waitlist POST) | Anonymous | `SIGNALDECK_PUBLIC_SURFACE=1` flips anonymous rule to allowlist (`api.publicRoutes`) |
| Vendor reads (bars, snaps, news, tv-*, exports) | Authenticated sessions only | Session cookie (bcrypt, 30-day) + bearer token for scripts; never open anonymously |
| User-scoped routes, LLM-spending routes | Authenticated user only | Session required; `SIGNALDECK_OPEN_SIGNUP` defaults closed when public host allowlisted |
| Licensed raw market data (Alpaca IEX, Kraken, TradingView, StockTwits) | Never redistributed | Internal `/datalicense` returns HTTP 451 for anonymous requests |

## Data boundaries
- **Public demo data**: Aggregated accuracy, calibration, honesty metrics, ledger proofs, volatility forecasts, model health, canary results, postmortems, lineage, quality reports, dataset versions, evidence, research loop/ledger, preregistration, ledger verification, anchors. No raw bars, no user identifiers, no predictions tied to accounts.
- **Private user data**: Sessions, API tokens, notification preferences, paper-trading state, waitlist entries, any LLM usage logs. Never exposed on public surface.
- **Licensed vendor data**: Alpaca IEX bars, Kraken, TradingView scanner, StockTwits. Daemon ingests with keys from `../stock-trader/.env` (cross-project dependency). Anonymous requests for raw bars receive HTTP 451. Redistribution is prohibited by license.

## Option A: no public host (record the demo locally)
1. Run the private workspace on the Windows 11 machine (daemon via Task Scheduler, web on 127.0.0.1:8323/3000).
2. Sign in locally; navigate public pages (`/`, `/accuracy`, `/proof`, `/volatility`, `/glossary`, `/health`).
3. Record a screen capture demonstrating anonymous access and authenticated features.
4. Share the video with judges.
- Cost: $0.
- Risks: Judges cannot interact live; no independent verification of uptime or latency; single-machine SQLite contention visible under load.

## Option B: hosted public read-only surface

**Verified 2026-09-08 (ledger F20):** a throwaway daemon started with `SIGNALDECK_PUBLIC_SURFACE=1`, `SIGNALDECK_PUBLIC_READS=false` and `SIGNALDECK_OPEN_SIGNUP=false` answered the honesty routes anonymously, 401 on every workspace, vendor-data and AI route, 403 on registration and on a foreign Host header.
Steps:
1. Build the Docker image with mandatory `GIT_REV` build arg (`docker build --build-arg GIT_REV=<commit> -t signaldeck .`).
2. Provision a persistent volume (10 GB minimum) on the chosen host (Fly.io, Railway, Render, or VPS).
3. Set secrets in the host environment: all entries from `daemon/.env` plus `SIGNALDECK_PUBLIC_SURFACE=1`, `NEXT_PUBLIC_SIGNALDECK_PUBLIC=1`, `SIGNALDECK_ALLOWED_HOSTS=<public-hostname>`, `SIGNALDECK_OPEN_SIGNUP=0`.
4. Seed the volume with a reviewed database backup (from `data/backups/` with matching sha256) or start empty (daemon will ingest once Alpaca keys are present).
5. Deploy the container; verify `/api/health` (200) and `/api/ready` (200 once workers healthy).
6. From a fresh anonymous browser: confirm `/` loads, `/accuracy` returns data, `/api/bars` returns 451, `/api/symbol` requires auth, `/api/health` shows `degraded` only under worker load, `/api/ready` is 200.
- Estimated monthly cost: $5–15 (2 GB RAM, 10 GB volume).
- **Not decided — needs approval** and a payment method attached to a hosting account.

## Separate public source repository
Create a new public repository (or a reviewed history export) containing:
- Reviewed source code (daemon, web, ops scripts, tests, docs).
- `Dockerfile`, `fly.toml`, `DEPLOY.md`, `ops/pre-publish-scan.sh`, `ops/restore-rehearsal.sh`, `ops/signaldeck-ctl.sh`.
- Documentation (`docs/`, `README.md`, `PREREGISTRATION.md`, `proofs/`, `repro/`). The live registry `data/accuracy_registry.json` is generated and gitignored; the anchors repo carries its published copies.
- Git history preserved for attribution **only after review**.

Never include:
- `daemon/.env`, `web/.env.local`, any `.env*`.
- `data/`, `data/backups/`, `logs/`, `audits/` entries that name people or hold private review notes (review each). `proofs/` and `repro/` are the public evidence trail and DO go in.
- Private audits containing personal details or internal review notes.
- Raw provider data or licensed vendor payloads.

Run `ops/pre-publish-scan.sh` before any push; it scans tree and history for secrets and raw provider data.

## Backups, restore and Rollback

On this machine `ops/web-guard.ps1` ("SignalDeck Web Keepalive", every 5 minutes) restarts a web task whose port stops answering; `ops/daemon-guard.ps1` does the same for the daemon.
- Nightly: `VACUUM INTO data/backups/backup-<timestamp>.db` with sha256 sidecar; gzipped copy uploaded as release asset `backup-<timestamp>` to private GitHub repo `nyaungnicholas-wq/signaldeck` (newest 7 kept).
- Weekly restore rehearsal: `ops/restore-rehearsal.sh --from-github` downloads newest asset, verifies sha256, restores to a test path, runs integrity checks.
- Daemon Rollback: `ops/signaldeck-ctl.sh deploy` builds from a specific commit; redeploy the previous commit to roll back.
- Web Rollback: rebuild the Next.js image from the previous commit and redeploy the container.

## Outstanding approvals

| Item | Why it needs the owner | Default if no decision |
|------|------------------------|------------------------|
| Paid hosting account (Fly.io/Railway/Render/VPS) | Required for Option B; no account exists, no card attached | Option A only (local demo) |
| Make `signaldeck-anchors` repo public | Currently private (GitHub visibility PRIVATE, checked 2026-09-09); README, this plan and ops/anchor-publish.sh now say so | Remains private |
| Create public source repository | Private repo holds backups and must not be flipped public | No public source repo |
| Expose any public hostname | `SIGNALDECK_ALLOWED_HOSTS` must be set; DNS, TLS, proxy config needed | No public hostname |
| Change notification transports (Discord/Telegram/Slack/SMTP) | Secrets required; optional but if used must be configured | Disabled (no secrets set) |

## Costs

| Item | One-time | Monthly |
|------|----------|---------|
| Local-only demo (Option A) | $0 | $0 |
| Hosted public surface (Option B) — 2 GB RAM, 10 GB volume | $0 | $5–15 (estimated) |
| Domain name (if desired) | $10–15/year | — |
| TLS certificate (Let's Encrypt) | $0 | $0 |
| Backup storage (GitHub release assets, private repo) | $0 | $0 (within GitHub limits) |

All figures estimated. No hosting account exists today; nothing is deployed publicly.