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
