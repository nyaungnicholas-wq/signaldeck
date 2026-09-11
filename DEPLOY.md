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
ops/docker-build.sh signaldeck:demo
docker run --rm -p 8080:8080 \
  -v signaldeck_data:/data \
  -e ALPACA_KEY=... -e ALPACA_SECRET=... \
  signaldeck:demo
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

The app gates on sign-in by default. For a public demo, `fly.toml` sets:

```
SIGNALDECK_PUBLIC_READS = "1"
```

Read-only endpoints then answer without a session, so a visitor sees the deck
rather than a login wall. **Every mutating route still requires authentication** —
this widens reads, it does not disable auth.

Decide deliberately whether you want that on. It is the difference between a
demo people can look at and a private workspace.

---

## Restoring real data

A fresh deploy starts empty and backfills ~2 years. To demo against the full
13.4M-bar history instead, copy the database onto the volume:

```bash
fly ssh console -C "mkdir -p /data"
fly sftp shell
put data/signaldeck.db /data/signaldeck.db
```

The file is ~2.5 GB, so size the volume accordingly and expect a slow transfer.
Stop the machine first — copying a SQLite file out from under a running writer
produces a corrupt database.

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

Schedule it daily. The shipped launchd plists in `ops/` are macOS-only and carry
paths from the machine this was developed on — on Linux use cron:

```cron
15 6 * * * cd /app && bash ops/accuracy-registry.sh
```

Check it is actually running. This script was silently dead for five days
because it had a hardcoded path to a machine it was no longer on, and the README
kept serving a stale refusal that looked exactly like the honesty machinery
working correctly.

---

## Known deployment gaps

- The `ops/*.plist` scheduling files are macOS launchd and several still contain
  absolute paths from the original development machine. They need porting to
  cron or systemd timers for a Linux host.
- `ops/signaldeck-ctl.sh` no longer hardcodes a dev-machine path (its paths are
  now `$REPO`-relative); this gap is closed. The `ops/*.plist` files above are not.
- There is no reverse-proxy or rate-limit config here. `SIGNALDECK_RATE_RPS` and
  `SIGNALDECK_RATE_BURST` exist in the daemon but I have not verified they are
  active under load.
- No CI. Builds and tests are run locally.

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
  the running process must all report the same 40-character sha, and the
  container must still report `resolvable:false`. If it ever claims otherwise,
  something is asserting provenance it could not have observed, and the script
  fails.

    ops/docker-build.sh signaldeck
    ops/oracle-verify.sh signaldeck http://127.0.0.1:8080

Never run a bare `docker build`. The wrapper is what refuses a dirty tree and
proves the revision resolves; a raw invocation stamps an image with a claim
nobody checked.

The grader runs in the container via `ops/grade.sh`, NOT
`ops/accuracy-registry.sh` -- that script needs git, a checkout and a README to
rewrite, none of which exist in the image. Schedule it from the host:

    docker exec signaldeck /usr/local/bin/grade.sh

`GraderMaxAge` is 26h, so a daily timer tolerates one missed run; two look
identical to a broken schedule, which is why `systemctl list-timers` beats a
cron loop inside the container.
