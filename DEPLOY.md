# Deploying the SignalDeck demo

Everything here is reproducible from the repo. The one step I cannot do for you
is create the hosting account and attach a card.

---

## What you are deploying

One container running two processes: the Go daemon (owns the SQLite database and
the worker fleet) and the Next.js app (proxies `/api/*` to the daemon over
loopback). They share a container because SQLite permits exactly one writer and
sharing that file across containers over a network volume is the one thing
SQLite is genuinely bad at.

**This needs a host with a persistent volume and an always-on process.** Vercel
cannot run it — its functions are request-scoped with no persistent disk, and the
half of SignalDeck that does the work is a long-running fleet on a schedule.

Suitable: Fly.io, Railway, Render, or any small VPS. Budget ~$5–15/month for
2 GB RAM and a 10 GB volume.

---

## Build and run locally first

```bash
SIGNALDECK_ALLOW_LOCALHOST_SITE_URL=1 ops/docker-build.sh signaldeck:demo
docker run --rm -p 8080:8080 \
  -v signaldeck_data:/data \
  -e ALPACA_KEY=... -e ALPACA_SECRET=... \
  signaldeck:demo
```

`NEXT_PUBLIC_SITE_URL` is inlined into the web bundle at BUILD time and cannot
be set from the container environment afterwards. Unset, the image serves a
`robots.txt`, a `sitemap.xml` and og:/twitter: cards all pointing at
`http://localhost:8323` -- invisible until someone shares a link, so the build
REFUSES without it. The `SIGNALDECK_ALLOW_LOCALHOST_SITE_URL=1` above is the
explicit opt-out for a LOCAL image and is wrong for anything you will publish:

```bash
NEXT_PUBLIC_SITE_URL=https://<your-host> ops/docker-build.sh signaldeck
```

Then `http://localhost:8080`.

**`GIT_REV` is not optional.** The daemon refuses to start from a build it cannot
attribute to a commit, because rows it writes could not then be graded. Omit it
and the container builds fine and then exits with:

```
refusing to start: build is unattributable — rows it writes cannot be graded
```

That is the gate working, not a build error.

---

## Fly.io

```bash
fly launch --no-deploy --name <your-app-name>
fly volumes create signaldeck_data --size 10 --region iad
fly secrets set ALPACA_KEY=... ALPACA_SECRET=...
fly deploy --build-arg GIT_REV=$(git rev-parse HEAD)
```

`fly.toml` is committed. Two settings there are deliberate:

- **`auto_stop_machines = false`.** The worker fleet *is* the product — it
  ingests bars and freezes pre-registered forecasts on a schedule. A machine
  that sleeps between visitors stops recording, and the honesty page would then
  grade gaps it caused itself.
- **`grace_period = "120s"`** on the healthcheck. On a cold volume the daemon
  creates 105 tables and backfills bars before it listens.

---

## First boot

On an empty volume the daemon will:

1. Create the schema (105 tables)
2. Seed a watchlist (BTC/USD, SPY, QQQ, AAPL, NVDA, TSLA)
3. Backfill a broad daily universe — ~800k bars, a few minutes
4. **Print an admin password to stderr, once**

That last one matters. Capture it from the deploy logs immediately:

```bash
fly logs | grep "created initial admin user"
```

It goes to stderr only and is never written to the log file, so if you miss it
you will need to reset the user rather than recover it.

---

## Demo access
The public surface is controlled by `SIGNALDECK_PUBLIC_SURFACE=1` (an allowlist of public read routes in `daemon/internal/api/security.go`), not `PUBLIC_READS`. `SIGNALDECK_PUBLIC_READS` must remain `false` on any public host. The web build must be done with `NEXT_PUBLIC_SIGNALDECK_PUBLIC=1` and `NEXT_PUBLIC_SITE_URL=https://<host>` at BUILD time or every anonymous visitor is redirected to `/login`. `SIGNALDECK_ASSUME_TUNNEL=1` must be set whenever the daemon binds loopback behind a tunnel, otherwise `reachablePrivately()` reads the deployment as private and opens signup, anonymous reads, and disables the 451 licence guard. `SIGNALDECK_ALLOWED_HOSTS` must include the public host AND `127.0.0.1:8322,localhost:8322` (the web proxy does not forward Host). `SIGNALDECK_WEB_ORIGINS` is a separate Origin list and the daemon refuses to boot without it.

---

## Restoring real data
A fresh deploy starts empty and backfills. To seed the full history copy a REVIEWED backup from `data/backups/backup-<ts>.db` with its matching `.sha256` (never the live file out from under a running writer). The file is 6.2 GB as of 2026-09-20 so size the data volume at 40 GB. Transfer with `scp`/`rsync` to the target host's `data` directory. Stop the daemon on the source first if copying the live file. Verify sha256 after transfer before starting the daemon. Then run `ops/restore-rehearsal.sh` style verification (ledger verify must report intact).

---

## Verifying the deployment

```bash
curl https://<app>/api/health     # process is alive
curl https://<app>/api/ready      # can it serve CORRECT answers
curl https://<app>/api/version    # which commit produced these rows
```

`/api/ready` is stricter than `/api/health` on purpose. Health means the process
is up; ready additionally fails when the store is unreachable, workers were
refused at boot, or provider credentials are absent — the states where the app
answers requests but the answers are empty or wrong.

`/api/version` should report `"resolvable": true` and `"modified": false`. If it
reports `+dirty` or an empty revision, the rows being written are ungradable and
the accuracy page will withhold every verdict.

---

## Keeping the accuracy page alive

The grader runs from `ops/accuracy-registry.sh`. It is fail-closed: if it cannot
run, it *removes* the accuracy tables from the README and publishes the refusal
instead of reprinting stale numbers.

Schedule it daily. WHICH script depends on where you are, and the two are not
interchangeable.

**On a dev box / any full checkout**, `ops/accuracy-registry.sh` is the job. The
fleet definitions in `ops/tasks/*.xml` are Windows Scheduled Task documents and
mean nothing off Windows — on Linux use cron:

```cron
15 6 * * * cd /path/to/checkout && bash ops/accuracy-registry.sh
```

**In the container, that script cannot run and never could.** It is 680 lines of
bash that shell out to git, rewrite README.md between markers, inject the
rendered block into eight `partials/` documents and page Telegram. The image has
no `.git`, no checkout, no README to publish, and no bash. This file used to
print `cd /app && bash ops/accuracy-registry.sh` here, which fails on every
container deployment — and the failure is not loud: with no grader running,
`/api/accuracy` stays 503 REFUSED_STALE forever (the handler is fail-closed on a
heartbeat older than `GraderMaxAge`, 26h), which looks exactly like the honesty
machinery working correctly. Use `ops/grade.sh`, the container-sized
replacement, installed by the Dockerfile at `/usr/local/bin/grade.sh`:

```cron
15 6 * * * /usr/local/bin/grade.sh
```

It is POSIX sh, stdlib-only Python, and runs the same registry generation, the
same selection-honesty merge, the same collapse gate the handler runs, and
records the same heartbeat the handler reads.

One gate fewer runs there, deliberately: the deployment-drift check
(`tools/deployment_drift.py`) shells out to git against the deployed revision,
which the image cannot do, so a stale binary the dev-box publish would refuse on
is still graded in the container. `ops/grade.sh` documents this divergence at
its head; it is known, not silent.

Check it is actually running. This script was silently dead for five days
because it had a hardcoded path to a machine it was no longer on, and the README
kept serving a stale refusal that looked exactly like the honesty machinery
working correctly.

---

## Known deployment gaps

- CLOSED 2026-09-19: the `ops/com.signaldeck.*.plist` files are deleted. The fleet
  is defined by `ops/tasks/*.xml`, one lossless `Export-ScheduledTask` document per
  task, registered by `ops/install-windows-tasks.ps1 -Install` (elevated) and
  reconciled by `ops/check-task-health.ps1`, which now reports definition drift
  instead of only checking the task store against itself. `com.stocktrader.hud.plist`
  and `com.tickstream.daemon.plist` remain: they belong to sibling projects.
- A Linux host would still need these ported to cron or systemd timers; the XML is
  Windows-specific by design, which is what makes it lossless here.
- `ops/signaldeck-ctl.sh` no longer hardcodes a dev-machine path (its paths are
  now `$REPO`-relative); this gap is closed.
- There is no reverse-proxy or rate-limit config here. `SIGNALDECK_RATE_RPS` and
  `SIGNALDECK_RATE_BURST` exist in the daemon but I have not verified they are
  active under load.
- **The quarterly second-machine DR drill in `ops/GO-LIVE.md` has never been
  executed.** The box is unticked and no execution date is recorded anywhere. The
  weekly `com.signaldeck.restore` rehearsal proves the *backup file* restores;
  nothing has ever proved the *procedure* works on a machine that is not this one.
  The runbook is also still macOS/launchd-shaped -- it says to restore "on a Mac
  that isn't this one" and "reinstall the launchd fleet" -- so as written it could
  not be executed against the current Windows host even if a second machine were
  available. Treat the recovery time in `ops/DR_RUNBOOK.md` as an estimate that
  has not been measured.

## Container deploys: how provenance is proved

`/api/version` reports `resolvable: false` in a container and always will.
`internal/lineage` shells out to `git`, and the image has no git binary and no
`.git` (`.dockerignore` excludes it). That is the daemon telling the truth, and
it must never be faked -- a platform whose claim is that its rows tie back to
the code that produced them cannot fake exactly that.

So the proof is split across the two places where each half can be established:

* **Build time**, on the build host, in `ops/docker-build.sh`: the tree is
  clean (modulo `ops/generated-docs.txt`), `git cat-file -e <rev>^{commit}`
  proves the revision RESOLVES, and the revision is both baked in via ldflags
  and recorded as an OCI label.
* **Deploy time**, in `ops/oracle-verify.sh`: the checkout, the image label and
  the running process must all report the same 40-character sha.

The container's own answer changed on 2026-09-16, and the reason is worth
reading before you assume it was weakened. `tools/accuracy_registry.py` is
pinned by hash on the pre-registration chain, so it cannot be edited to suit a
container, and it asks `git cat-file -e <rev>^{commit}` of **every revision in
the database**, not just the running one. With no git in the image the answer
was false for all of them, `apply_revision_gate()` stripped the verdict from
every directional and structural row, and a container grade published a
registry with nothing in it. `ops/docker-build.sh` now packs this repository's
**commit objects only** — no trees, no blobs, under a megabyte — and the image
unpacks them into `/app/.git`. Git objects are content-addressed, so the store
cannot affirm a commit that was never made; `oracle-verify.sh` now requires
`resolvable:true` **and** proves the store refuses an all-zero sha. Shipping the
evidence is not the same as asserting the conclusion, which is what the old
`resolvable:false` assertion existed to prevent.

    SIGNALDECK_ALLOW_LOCALHOST_SITE_URL=1 ops/docker-build.sh signaldeck
    ops/oracle-verify.sh signaldeck http://127.0.0.1:8080

Never run a bare `docker build`. The wrapper is what refuses a dirty tree and
proves the revision resolves; a raw invocation stamps an image with a claim
nobody checked.

The grader runs in the container via `ops/grade.sh`, NOT
`ops/accuracy-registry.sh` -- that script needs a checkout and a README to
rewrite, neither of which exists in the image.

**The image schedules it itself** (`ops/docker-entrypoint.sh`), every six hours,
starting as soon as the daemon is healthy. It did not before: `grade.sh` was
copied in and nothing called it, `fly.toml` installed no timer, and this section
told the operator to arrange one from outside -- a prerequisite living outside
the artifact that claims to be self-contained. Deploy just the image and the
honesty page never worked on a fresh volume and went stale a day later on a
seeded one, with `/api/health` answering 200 the whole time.

`GraderMaxAge` is 26h and the interval is 6h, so four attempts fit inside the
window and one refusal or one restart cannot expire the heartbeat. Set
`SIGNALDECK_GRADE_INTERVAL_SEC=0` to turn the in-container schedule off if you
genuinely do drive it from the host:

    docker exec signaldeck /usr/local/bin/grade.sh
