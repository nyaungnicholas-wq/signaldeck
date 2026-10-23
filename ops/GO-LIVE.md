# SignalDeck — Go-Live Guide (phone access + remote alerts)

## 0. Deploying code: `ops/signaldeck-ctl.sh deploy` is the ONLY sanctioned path

Do not `go build` in `daemon/` and restart the daemon by hand. That path is how
the running binary came to differ from the reviewed source — mechanisms that
existed in the working tree were never in the process producing rows, and a
review of the source measured an artifact rather than the system.

```
ops/signaldeck-ctl.sh deploy
```

It refuses, in order, and only ever refuses — it can never make a result look
better:

1. **Dirty working tree** → refuse. A deploy must be reproducible from a commit
   anyone else can check out.
2. **Load-bearing path untracked** (`ops/manifest-check.sh`) → refuse.
3. **`go test ./...` in `daemon/` non-zero** → refuse.
4. Builds `signaldeckd` from a `git archive HEAD` extraction in a temp dir —
   **not** from the working tree — and installs it to `bin/signaldeckd`.
5. Restarts `com.signaldeck.daemon`, then `GET /api/version` and requires the
   running `revision` to equal the commit just built with `resolvable: true`.
   Anything else prints `deploy UNVERIFIED` and exits non-zero; do not treat an
   unverified run as deployed.

`/api/version` is the check anyone can repeat at any time: the `rowStamp` it
returns is the exact string the process writes onto every ledger row, so a row
can be tied to a commit without trusting the deployment.

### One time, right after the crypto-pause deploy: `sdmaint void-retired-crypto`

The first deployed build that pauses crypto (no licensed crypto data source,
2026-10-22) sets every crypto symbol `active=0` at boot (`pauseCrypto`) and, on
purpose, leaves `delisted_at` NULL. Until the step below has run, every
`ops/accuracy-registry.sh` run counts those symbols as undated exits and
publishes `survivorship_clean=false`. Run it from the repo root (Git Bash) as
soon as the deploy prints `deploy VERIFIED`, before the next registry run (the
`SignalDeck Accuracy` task runs it daily at 14:05 local). Record each output.

```
# (a) Back up first. The offline backup SKIPs while signaldeckd runs, so stop,
#     back up, restart. With a backup key configured it also makes the
#     encrypted off-site copy. Check logs/backup-offline.log shows the new
#     data/backups/signaldeck-<ts>.db and its sha256 line, not "SKIP".
bash ops/signaldeck-ctl.sh stop && bash ops/signaldeck-backup-offline.sh; bash ops/signaldeck-ctl.sh up
#     If a stop is not acceptable, name the newest backup and its sha instead:
#     ls -t data/backups/signaldeck-*.db* | head -2; cat data/backups/<newest>.sha256
# (b) Build sdmaint from the DEPLOYED HEAD (git rev-parse HEAD must equal the
#     revision in curl -s http://127.0.0.1:8322/api/version).
(cd daemon && go build -o ../bin/sdmaint.exe ./cmd/sdmaint)
# (c) Dry run, read-only: must print active_crypto=0. Record voided/by_horizon/delisted_stamped.
bin/sdmaint.exe void-retired-crypto
# (d) Apply, keeping the CSV of old values.
bin/sdmaint.exe void-retired-crypto -apply -log logs/void-retired-crypto-$(date -u +%Y%m%dT%H%M%SZ).csv
# (e) Dry run again: must print voided=0 ... delisted_stamped=0.
bin/sdmaint.exe void-retired-crypto
```

`-apply` voids the open crypto forecasts the resolver can never settle, stamps
`delisted_at` at each crypto symbol's last stored daily bar, and records one
`dq_events` row (`predictions_voided_source_retired`) naming the CSV of old
values, all in one transaction. It refuses while any crypto symbol is still
`active=1` (the paused build is not running) and refuses a `-log` file that
already exists. Keep the CSV. A second run changes nothing.

The `delisted_at` stamp has one effect outside the CSV: on its next pass,
maintain's OutcomeResolver voids open crypto `score_outcomes` past their target
with no forward bar (the `dq_events` detail says so too). Restoring the stamp
from the CSV does not restore those rows; the backup from (a) does.

```
# (f) Verify, from the repo root:
bin/sdmaint.exe ledger-verify      # must print "chain: intact" and "anchors: N/N verify", exit 0
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8322/api/accuracy   # must print 451 (published deployment, licence)
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $SIGNALDECK_API_TOKEN" http://127.0.0.1:8322/api/accuracy   # must print 200 (operator)
powershell -NoProfile -File ops/check-grader-health.ps1   # last line must be "RESULT: ok"
```

On a published deployment /mcp answers 451 to every caller, the operator's MCP key included, until the data licence is resolved.

**Rollback means rolling forward.** `ops/signaldeck-ctl.sh` keeps the previous
binary, but that build (c8e18a2c) re-wires every retired source (Kraken,
TickStream, TradingView, StockTwits, FINRA, Cboe, FRED) and, before the void has
stamped `delisted_at`, re-activates BTC/USD through `UpsertSymbol`. Do not swap
it in: fix forward and redeploy HEAD. If an emergency rollback happens anyway,
stop it as soon as the incident is contained and redeploy HEAD.


Everything below is prepared; the two ★ steps need YOUR accounts/credentials —
Claude cannot (and should not) do them for you.

## 1. Phone access via Tailscale (~30 min)

1. ★ Install Tailscale on this Mac (`brew install --cask tailscale`) and on
   your phone (App Store), sign both into the SAME tailnet (free personal
   plan), and note the Mac's tailnet name, e.g. `nicholas-mac.tailXXXX.ts.net`.
2. Harden the daemon before exposure — in `daemon/.env` set:
   - `SIGNALDECK_PUBLIC_READS=false`  (reads require login)
   - `SIGNALDECK_API_TOKEN=<long random string>`  (`openssl rand -hex 32`)
   - `SIGNALDECK_TRUST_PROXY=false`
   The CSRF/origin/Host allowlists are already built; add your tailnet host to
   the allowlists per the README section "Exposing SignalDeck remotely".
3. Restart both services (Windows):
   `bash ops/signaldeck-ctl.sh restart`
   The `launchctl kickstart` commands this step used to give are macOS-only and
   do nothing here; the Mac they named is retired.
4. On the phone: open `http://<mac-tailnet-name>:8323`, log in with your
   SignalDeck account ("nicholas"). Tailscale encrypts the path; nothing is on
   the public internet.
5. Optional: Tailscale "MagicDNS + HTTPS certs" gives you a proper
   `https://` URL (`tailscale cert`).

## 2. Remote alerts via Telegram (~10 min, zero code)

The delivery layer (`daemon/internal/notify`) is built and dormant — env vars
switch it on.

1. ★ In Telegram, talk to @BotFather → `/newbot` → name it (e.g.
   "SignalDeck Alerts") → copy the BOT TOKEN.
2. ★ Message your new bot once (any text), then get your chat id: open
   `https://api.telegram.org/bot<TOKEN>/getUpdates` and read
   `message.chat.id`.
3. In `daemon/.env` add:
   - `SIGNALDECK_TELEGRAM_BOT_TOKEN=<token>`
   - `SIGNALDECK_TELEGRAM_CHAT_ID=<chat id>`
   (Discord instead: `SIGNALDECK_DISCORD_WEBHOOK=<webhook url>`.)
4. Restart the daemon (kickstart command above). Alert sweeps now send ONE
   batched Telegram message per pass (30-min cooldown), and health-watchdog
   warnings ride the same channel. Failures appear as `notify_failed` dq
   events — secrets are redacted everywhere.
5. **Confirm it actually delivers, immediately:**

   ```
   curl -X POST -H 'X-Signaldeck: 1' http://127.0.0.1:8322/api/notify/test
   ```

   This sends a real message through every configured transport and reports
   each one's status *after* the attempt. Do not skip it. Delivery is
   deliberately best-effort, so a mistyped token raises no error — it fails
   quietly into a dq event, and without this call the first thing you learn
   is that an alert you needed never arrived. With nothing configured, the
   response names the exact env vars to set.

## 3. PWA (after Tailscale)

The web app ships a manifest + icon; on the phone open the tailnet URL in
Safari/Chrome → Share → "Add to Home Screen". It launches standalone like a
native app.

## Order of operations
Tailscale (1) → open on phone → PWA install (3) → Telegram (2).

## Recurring

- [ ] **Quarterly: execute `ops/DR_RUNBOOK.md` on a second machine** — full
      drill, not a read-through: restore the newest offsite backup on a machine
      that isn't this one, re-register the fleet with
      `ops/install-windows-tasks.ps1 -Install` (elevated) from `ops/tasks/*.xml`,
      and verify the
      restored ledger reproduces the last published external anchor. The
      weekly `com.signaldeck.restore` rehearsal proves the *backup* restores;
      only this drill proves the *procedure* (and the operator) can bring
      SignalDeck back when this machine is gone.
