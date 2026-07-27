# SignalDeck — Disaster-Recovery Runbook (clean second Mac)

Institutional-readiness gap this closes: **"no path off this Mac."** Every
launchd job, the database, and the daemon live on one machine. This runbook is
the exact sequence to bring SignalDeck back up on a clean Mac from the newest
offsite backup, ending with cryptographic proof that the restored track record
is the same one that was published externally before the disaster.

Read it once now, not during the incident. The quarterly rehearsal line in
`ops/GO-LIVE.md` exists because a runbook nobody has executed on a real second
machine is a hypothesis, not a recovery plan — the same reasoning that created
`ops/restore-rehearsal.sh` (H8).

## What survives the Mac, and what does not

Survives (recovery inputs):

| artifact | where |
|---|---|
| Database backups (nightly `VACUUM INTO`, newest 2 plain `.db`, older `.zst`/`.gz`) | iCloud Drive: `~/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups` |
| Source, ops scripts, launchd plists | GitHub: `nyaungnicholas-wq/signaldeck` |
| External anchors, prereg chain head, accuracy registry | the public anchors repo (`anchors.log`, `prereg.log`, `accuracy_registry.json` in its git history) |

Does NOT survive automatically — keep offline copies (password manager or
encrypted disk), or accept the stated consequence:

| artifact | consequence if lost |
|---|---|
| `daemon/.env` (gitignored: API token, data-source keys, Telegram/Discord transports, allowlists) | re-enter every secret by hand; nothing else breaks |
| `~/.signaldeck/ledger_anchor.key` (Ed25519 signing key — deliberately OUTSIDE `data/`, so it is NOT in any backup) | survivable but publicly visible: old anchors still verify (each `Record` embeds its `pubKey`), but the daemon generates a fresh key and every NEW anchor signs with it — external observers see a key rotation they are entitled to question |
| everything written after the last nightly backup | gone. RPO = last offsite backup (≤ ~24h + iCloud sync lag); backfill workers close price-data gaps on their own, but resolved outcomes emitted in the lost window are not reconstructable |

## 0. Prerequisites on the clean Mac (~30 min)

1. Sign into the same iCloud account and wait for
   `SignalDeckBackups` to actually download (Files can show placeholders —
   `brctl download` or open the folder and check sizes).
2. Xcode CLT (`xcode-select --install`), Go toolchain, Node.js 20+,
   `brew install zstd jq`. `sqlite3` ships with macOS. `gh auth login` (or an
   SSH key) for the two clones and the anchor push.
3. **Path warning:** every plist and ops script hard-codes
   `/Users/natalienyaung/claude code/signaldeck`. Recreate the same username +
   path if at all possible; otherwise
   `sed -i '' 's|/Users/natalienyaung|/Users/<you>|g'` across `ops/*.plist`
   and `ops/*.sh` after cloning, and know you are now running an edited fleet.

## 1. Clone and build (~15 min)

```bash
mkdir -p ~/claude\ code && cd ~/claude\ code
git clone https://github.com/nyaungnicholas-wq/signaldeck.git
cd signaldeck/daemon
go build -o ../bin/signaldeckd ./cmd/signaldeckd
go build -o ../bin/sdmaint    ./cmd/sdmaint
cd ../web && npm ci && npm run build
```

The committed `bin/` binaries are arm64-macOS from the dead machine — rebuild
rather than trust them on new hardware.

## 2. Restore the database — the rehearsed path

This is the SAME decompress-and-verify sequence `ops/restore-rehearsal.sh`
exercises weekly, pointed at the live path instead of a temp dir. Run the
rehearsal script first if it can see the offsite dir — it proves the chosen
backup restores clean before you commit to it:

```bash
cd ~/claude\ code/signaldeck
bash ops/restore-rehearsal.sh        # exit 0 = newest backup provably restorable
```

Then restore for real:

```bash
OFFSITE="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
SRC=$(ls -t "$OFFSITE"/signaldeck-*.db "$OFFSITE"/signaldeck-*.db.zst \
            "$OFFSITE"/signaldeck-*.db.gz 2>/dev/null | head -n1)
mkdir -p data
case "$SRC" in
  *.zst) zstd -d -q -f -o data/signaldeck.db "$SRC" ;;
  *.gz)  gzip -dc "$SRC" > data/signaldeck.db ;;
  *)     cp "$SRC" data/signaldeck.db ;;
esac
sqlite3 data/signaldeck.db "PRAGMA quick_check;"          # must print: ok
sqlite3 data/signaldeck.db "SELECT COUNT(*) FROM bars;"   # must be ≥ 1000
```

Do not skip the two checks — an openable-but-empty copy is the exact failure
mode the rehearsal exists to catch.

## 3. Recreate secrets and the signing key

1. `daemon/.env` — restore from your offline copy, or re-enter. The daemon
   starts without it; what you lose until it's back: remote alerts
   (`SIGNALDECK_TELEGRAM_*` / `SIGNALDECK_DISCORD_WEBHOOK` /
   `SIGNALDECK_WEBHOOK_URL`), auth hardening (`SIGNALDECK_API_TOKEN`,
   `SIGNALDECK_PUBLIC_READS`), data-source keys, host allowlists.
2. `~/.signaldeck/ledger_anchor.key` — restore your offline copy to exactly
   that path, `chmod 700 ~/.signaldeck && chmod 600` the file (the daemon
   REFUSES a wider-permissioned key rather than tightening it). Override path
   via `SIGNALDECK_LEDGER_ANCHOR_KEY` if you must. If the key is gone, let the
   daemon generate a new one and note the rotation publicly in the anchors
   repo — silence about a key change is worse than the change.

## 4. Re-point SIGNALDECK_ANCHOR_REPO

`ops/anchor-publish.sh` never creates or re-points a remote by design; it
quiet-skips until a clone exists:

```bash
git clone <public-anchors-repo-url> ~/.signaldeck/anchor-publish
```

That default path needs no env var. A different location requires
`SIGNALDECK_ANCHOR_REPO=<path>` in the environment of whatever invokes
`ops/anchor-publish.sh`. Confirm push access (`git push` a no-op) — a publish
that fails only shows up as `PUSH FAILED` in `logs/anchor-publish.log`.

## 5. Reinstall the launchd fleet

Install the SignalDeck plists from `ops/` (SKIP `com.stocktrader.hud.plist`
and `com.tickstream.daemon.plist` — different projects that shared the dead
Mac):

```bash
cd ~/claude\ code/signaldeck/ops
for p in com.signaldeck.*.plist; do
  cp "$p" ~/Library/LaunchAgents/
  launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/"$p"
done
bash signaldeck-ctl.sh up     # daemon :8322, web :8323, warms the dashboard
```

The fleet: `daemon` (signaldeckd), `web` (Next.js :8323), `market-open` /
`market-close` (session gating; market-close chains the nightly offline
backup — which re-establishes the offsite flow from the NEW machine),
`daily-refresh`, `accuracy`, `bias`, `restore` (weekly restore rehearsal),
`awake`, and `tunnel` only if ngrok is installed and wanted.

## 6. Verify the restored ledger against the last published external anchor

This is the step that turns "a database that opens" into "the same track
record that existed before the disaster." The daemon's verify surface is the
API (`GET /api/ledger/verify` for chain integrity, `GET /api/ledger/anchors`
for anchor standing — there is no sdmaint subcommand for this):

```bash
# 1. Chain is internally intact:
curl -sf -H 'X-Signaldeck: 1' http://127.0.0.1:8322/api/ledger/verify | jq .

# 2. Newest anchor the restored chain still reproduces:
curl -sf -H 'X-Signaldeck: 1' 'http://127.0.0.1:8322/api/ledger/anchors?limit=1' \
  | jq -r '.anchors[0] | {ok, reason, publish}'

# 3. The externally published line it must match:
tail -1 ~/.signaldeck/anchor-publish/anchors.log
```

Interpretation — be exact about what each outcome proves:

- **`publish` line matches the tail of the public `anchors.log`
  (`ok: true`)** — the restored history reproduces, byte for byte, a head that
  was timestamped in a third party's git history before the disaster.
  Anteriority holds up to that anchor. Done.
- **The restored chain reproduces an OLDER published line but not the newest**
  — the backup predates the last anchor. Everything up to the matching anchor
  is proven; the gap between that anchor and the disaster is attested only by
  the backup file itself. Say so wherever the track record is presented.
- **No published line reproduces** — stop. Either the wrong backup was
  restored or the restored history is not the published history. Do not
  publish new anchors on top of it; that would anchor a discontinuity.

Then the ordinary health checks: `/api/health`, dashboard at
`http://127.0.0.1:8323`, and
`curl -X POST -H 'X-Signaldeck: 1' http://127.0.0.1:8322/api/notify/test`
(per GO-LIVE — delivery is best-effort, so an untested transport is an
unconfigured one).

## Recovery objectives

- **RPO:** the newest offsite backup — nominally ≤ 24h behind (nightly,
  post-close), plus iCloud sync lag. Price bars backfill; lost-window ledger
  emissions do not.
- **RTO:** ~1–2 hours on prepared hardware following this document, dominated
  by iCloud download and the Go/npm builds. Unrehearsed, assume half a day —
  which is the argument for the rehearsal line below.
