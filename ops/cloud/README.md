# SignalDeck cloud kit: one Oracle Cloud Always Free VM

> **CUTOVER IS DEFERRED.** Nothing in this kit has run on a VM, and it must not
> carry production until two owner questions are closed:
>
> 1. **Owner account / age gate.** An Oracle Cloud account can only be opened by
>    the owner: Oracle's website terms (section 18) require the age of majority,
>    signup needs the owner's own card and phone, and Oracle allows one account
>    per person. Nobody else can open it on the owner's behalf.
> 2. **Alpaca data terms.** Alpaca's terms may restrict copying its market data
>    to another server. The database holds Alpaca-derived bars, so restoring it
>    on a VM, even for a staging rehearsal, waits on the owner's answer.
>
> Until both are answered **yes**, the Windows PC stays the one and only writer
> and the PC runbooks (`ops/DR_RUNBOOK.md`, `ops/CLOUDFLARE_TUNNEL.md`) stay in force.

**[OWNER]** marks a step only the owner can do: account creation, entering
credentials or a card, accepting terms, DNS or Cloudflare changes, sending
email, and touching the owner's PC. Everything else an operator with SSH can do.

## 1. What is in this directory

| file | what it is | replaces on Windows |
|---|---|---|
| `systemd/signaldeck-daemon.service` | signaldeckd on 127.0.0.1:8322, `Restart=always` | task `SignalDeck Daemon` + liveness half of `Daemon Keepalive` |
| `systemd/signaldeck-web.service`, `start-web.sh` | `next start` on 127.0.0.1:8323, the keyed local proxy | task `SignalDeck Web` (`start-local-workspace.ps1 -Port 8323`) |
| `systemd/signaldeck-<job>.service` + `.timer` (13 pairs) | the scheduled jobs, same scripts, same times | the calendar tasks in `ops/tasks.psd1` (section 9) |
| `systemd/signaldeck-daemon-guard.*`, `daemon-guard.sh` | every 5 min: maintenance lock, universe fail-safe, posture, hang strikes, stale binary | rest of `ops/daemon-guard.ps1` |
| `systemd/signaldeck-web-guard.*`, `web-guard.sh` | every 5 min: liveness + asset completeness, once per build | rest of `ops/web-guard.ps1` |
| `systemd/signaldeck-alert@.service`, `alert.sh` | `OnFailure=` target of every unit | `ops/lib-notify.ps1` |
| `systemd/50-signaldeck.rules` | polkit: the service user may start/stop/restart/reset-failed its two units, nothing else | the S4U / RunLevel Highest task principals |
| `deploy-linux.sh` | the only source-to-running path on the VM | `ops/signaldeck-ctl.sh deploy` |
| `web-release-linux.sh`, `web-assets-check.sh` | build-only, smoke, promote, keep previous build | `ops/web-release.ps1`, `ops/web-assets-check.ps1` |
| `check-units.sh` | unit drift, failed units, ports, clock | `ops/check-task-health.ps1` + `install-windows-tasks.ps1` report mode |
| `check-grader-health.sh` | grader heartbeat, WAL, ledger checks (line-for-line port) | `ops/check-grader-health.ps1` |
| `db-manifest.py` | file sha256 + schema hash + ledger/anchor heads + per-table counts, for cutover diffs | (new) |
| `test-deploy-linux.sh`, `test-linux-ops.sh`, `test-web-assets-check.sh` | stub-driven self-tests; each prints `SELFTEST OK` / `selftest OK` | |

Two shared files changed so the existing bash jobs work under systemd:
`ops/lib-portable.sh` (`sd_svc_start`/`sd_svc_stop` drive `systemctl` on Linux
for the daemon and web units; the Windows path is untouched and the self-test
proves a non-Linux host never takes the new branch) and
`ops/market-close-restart.sh` (restarts the daemon through that library on
Linux instead of a bare `schtasks`, which exited 127 there and left the daemon
down after every backup).

## 2. Architecture

One VM, shape **VM.Standard.A1.Flex, 2 OCPU / 12 GB**, in the home region (the
only region where Always Free applies). 2 OCPU x 744 h = 1,488 OCPU-hours and
12 GB x 744 h = 8,928 GB-hours in a 31-day month, inside the 1,500 / 9,000 free
allowance, so the VM can run 24x7. The AMD micro VMs (1/8 OCPU, 1 GB) are too
small for the daemon and the web build and are not used.

**OS: Ubuntu 24.04 LTS (aarch64), Canonical's OCI image.** CI runs on
`ubuntu-latest` (24.04), so the VM gets the same bash, coreutils, systemd and
Python 3.12 that every `ops/test-*.sh` and the tools job already pass on, and
3.12 is what the PC venv uses. Ubuntu 24.04's polkit takes the JavaScript rule
in `systemd/50-signaldeck.rules`, and `sqlite3`, `zstd`, `chrony` and `gh` are
plain apt packages. Oracle Linux 9 would also work, but its default Python
(3.9), SELinux labelling of `/srv` and dnf packaging are all things CI never
exercises.

```
visitor --HTTPS--> Cloudflare edge --(outbound-only tunnel)--> cloudflared.service (VM)
                                                                    |
                                                    http://127.0.0.1:8323  signaldeck-web (next start, keyed local proxy)
                                                                    |
                                                    http://127.0.0.1:8322  signaldeck-daemon (signaldeckd)
                                                                    |
                                       /srv/sd/signaldeck/data/signaldeck.db (SQLite, WAL)
timers (America/Los_Angeles) --> ops/*.sh jobs --> same DB, same scripts as the PC
```

Nothing listens on a public interface. The VCN security list allows inbound
22/tcp from the owner's address only (or nothing, with OCI Bastion). Port 25 is
blocked outbound on OCI, which only matters for SMTP alerts (section 10).

The checkout must live in a directory named `signaldeck`: the daemon finds its
project root as the nearest ancestor holding `signaldeck/daemon`
(`daemon/internal/config/config.go`), so `/srv/sd/signaldeck` gives root
`/srv/sd`, exactly as `C:\Users\...\claude code\signaldeck` does on the PC. The
units do not set `SIGNALDECK_ROOT`, so the deploy posture check and the running
daemon resolve the same `daemon/.env`.

## 3. Disk layout and sizing (200 GB Always Free cap)

| volume | size | mount | holds |
|---|---|---|---|
| boot | 50 GB | `/` | OS, `/usr/local/go`, Node, `/home/signaldeck` (keys in `~/.signaldeck`, Go and npm caches), 4 GB swapfile |
| block (paravirtualized, ext4) | 150 GB | `/srv/sd` | `/srv/sd/signaldeck` (checkout, `data/`, `logs/`, web builds, `.venv`), `/srv/sd/tmp` (every job's `TMPDIR`) |

50 + 150 = 200 GB, the entire boot-plus-block allowance: any further volume is
billed. Peak use on the data volume at today's ~10.8 GB database (already over
the 10,240 MB budget the backup script tracks):

| item | estimate |
|---|---|
| live DB + WAL | ~11 GB |
| local backups: up to 2 raw + 6 compressed (`KEEP_RAW_MAX`, `KEEP_ZST`+1 in `signaldeck-backup-offline.sh`, ~20% ratio per its header) | ~22 + ~13 GB |
| restore rehearsal's decompressed copy (weekly, in `TMPDIR`) | ~11 GB |
| off-site staging (`.gz` + `.enc`) | ~5 GB |
| web: `.next`, one candidate, two `.next-prev-*`; `node_modules` | ~3 GB |
| `.venv` | ~2 GB |
| **total** | **~67 GB** |

That leaves roughly 2x headroom for growth. Raise the data volume, not the boot
volume, if `check-units.sh` or the backup budget line starts complaining.
`/etc/fstab` uses the filesystem UUID with `defaults,nofail`, and every unit
carries `RequiresMountsFor=/srv/sd`, so nothing starts against an empty mount
point. Always Free includes 5 volume backups: use two for "before cutover" and
"after cutover" (section 14). They live in the same Oracle account, so they are
NOT off-site; the sealed GitHub releases are.

## 4. Exactly one writer

At any moment exactly one host runs `signaldeckd` and the scheduled jobs
against the database. Two writers fork the prediction-ledger hash chain: both
hosts append, both publish anchors, and the public `anchors.log` then carries
two heads that cannot both be true, which looks exactly like tampering. They
also double every member email and notification and every Alpaca request.

How the kit enforces it:

- Units are installed **disabled** (section 16). Nothing on the VM writes until
  cutover step C7.
- At cutover the PC's tasks are **disabled, not deleted**, and its binary is
  renamed so no stray `signaldeck-ctl.sh up` can start it (C2, C8).
- One tunnel connector. Cloudflare treats every connector of a named tunnel as a
  replica and spreads visitors across them, so the PC connector and the VM
  connector must never run at the same time (C7).
- `deploy-linux.sh`, `web-release-linux.sh` and the guards honour
  `ops/.maintenance`, so a deploy cannot restart the daemon under a backup.

## 5. Users and permissions

- **`signaldeck`**: system user, home `/home/signaldeck`, shell
  `/usr/sbin/nologin`, owns `/srv/sd`. No sudo, no SSH login. Every unit runs as
  this user with `NoNewPrivileges=yes`, `ProtectSystem=strict`,
  `ReadWritePaths=/srv/sd /home/signaldeck` and `PrivateTmp=yes`.
- **`ubuntu`** (the image's admin user): SSH key only, has sudo. Runs deploys as
  `sudo -u signaldeck ...` so nothing in the checkout ends up root-owned
  (`deploy-linux.sh` refuses to run as root).
- **polkit** (`50-signaldeck.rules`): `signaldeck` may `start`, `stop`,
  `restart` and `reset-failed` exactly `signaldeck-daemon.service` and
  `signaldeck-web.service`. That is what the market-close backup, the refresh
  sweep, deploys and the guards need; no sudo means `NoNewPrivileges` stays on.
- **cloudflared** runs as its own service from `cloudflared service install`.

## 6. Secrets

Never in git, never pasted into chat or tickets, never in a backup.

| secret | where on the VM | mode | how it gets there |
|---|---|---|---|
| `daemon/.env` (API token, local-proxy key, data-source keys, notifier webhooks, public URL and origins) | `/srv/sd/signaldeck/daemon/.env` | 0600 `signaldeck` | **[OWNER]** copies the PC file over SSH (`scp`), or re-enters values from the offline copy |
| ledger anchor signing key | `/home/signaldeck/.signaldeck/ledger_anchor.key` | 0600 | **[OWNER]** copies the PC's key. A new key is a public key rotation (`ops/DR_RUNBOOK.md` section 3) |
| off-site backup key | `/home/signaldeck/.signaldeck/backup.key` | 0600 | **[OWNER]** copies the PC's key: the same key must open older releases and seal new ones |
| deletion tombstones | `/home/signaldeck/.signaldeck/deletions.jsonl` | 0600 | copied at cutover (C6) |
| anchor repo push credential, `gh` login for off-site uploads | `signaldeck`'s home | 0600 | **[OWNER]** credential steps |
| tunnel token | inside `cloudflared.service` | root | **[OWNER]** `cloudflared service install` |

`~/.signaldeck` is 0700. Scripts read single keys from `daemon/.env` with
`grep -m1 '^NAME='`, never by sourcing it; the web unit gets only
`SIGNALDECK_LOCAL_PROXY_KEY` (`start-web.sh`), not an `EnvironmentFile=` of the
whole file. The daemon keeps reading `daemon/.env` itself, as on the PC.

## 7. TLS and ingress: the existing Cloudflare named tunnel

TLS terminates at Cloudflare; the origin stays `http://127.0.0.1:8323`, so the
tunnel's public-hostname configuration does not change. Moving the site is
moving the **connector**: **[OWNER]** installs `cloudflared` from Cloudflare's
apt repository and runs `sudo cloudflared service install <tunnel token>` on the
VM **only at cutover step C7, after the PC connector is stopped**. The VM opens
no inbound port for this; `cloudflared` dials out.

Before the connector starts, the daemon must already read as published
(`SIGNALDECK_PUBLIC_URL` etc. in `daemon/.env`; `ops/CLOUDFLARE_TUNNEL.md`
section 3). `deploy-linux.sh` and the daemon guard both refuse to start a binary
that reads as unpublished while a tunnel runs, the same AUD-09 check as the PC.

## 8. Time

- `timedatectl set-timezone America/Los_Angeles`, and every unit also sets
  `TZ=America/Los_Angeles`, so scripts that use local time behave as on the PC.
  `ops/market-open-guard.sh` sets its own POSIX TZ string and refuses a
  non-Pacific offset, which works unchanged on Linux.
- `chrony` with OCI's NTP endpoint `169.254.169.254` first and
  `ntp.ubuntu.com` second. `check-units.sh` fails the day `timedatectl` stops
  reporting `NTPSynchronized=yes`.
- Timers use LA wall-clock time (`OnCalendar=... America/Los_Angeles`), so DST
  needs no attention. **Flag for the PC (UNVERIFIED):** most calendar triggers in
  `ops/tasks/*.xml` carry a fixed `-07:00` offset in `StartBoundary`. If Task
  Scheduler treats those as synchronized across time zones, they fire one hour
  earlier in local time after DST ends on 2026-11-01, e.g. Market-Close at
  12:10 PST, before the 13:00 PT close. Check on the PC after 11-01 with
  `Get-ScheduledTaskInfo 'SignalDeck Market-Close'` (NextRunTime).

## 9. Scheduled tasks to systemd

Every task in `ops/tasks.psd1`, in order. Times are America/Los_Angeles. All
calendar timers have `Persistent=true` (Windows `StartWhenAvailable`: a run
missed while the VM was down fires once at boot) and the jobs keep their
6-hour limit (`TimeoutStartSec=6h`). Logs append to the same `logs/*.log` files
as on Windows; jobs without a redirect there go to the journal.

| Windows task | Windows trigger | Linux | notes |
|---|---|---|---|
| Accuracy | daily 14:05 | `signaldeck-accuracy.timer` | `ops/accuracy-registry.sh` |
| Anchor-Publish | daily 19:30 | `signaldeck-anchor-publish.timer` | |
| Bias | daily 02:40 | `signaldeck-bias.timer` | |
| Check-Grader-Health | daily 09:20 | `signaldeck-check-grader-health.timer` | runs the bash port |
| Check-Task-Health | daily 09:05 | `signaldeck-check-task-health.timer` | runs `check-units.sh` |
| Cleanup | daily 14:05 | `signaldeck-cleanup.timer` | |
| Daemon | no trigger (started by the guard, market-open, deploy) | `signaldeck-daemon.service` | enabled at boot |
| Daemon Keepalive | logon + every 5 min | `Restart=always` + `signaldeck-daemon-guard.timer` | |
| Daily-Refresh | Mon-Fri 13:15 | `signaldeck-daily-refresh.timer` | |
| Eighty Loop | **disabled** | not ported | disabled on the PC, and its program is quarantined (repo `CLAUDE.md`) |
| Local Workspace | every 5 min, `next start` on :3000 | not ported | the operator's second local instance; on a headless VM use `ssh -L 8323:127.0.0.1:8323` |
| Market-Close | Mon-Fri 13:10 | `signaldeck-market-close.timer` | stops/starts the daemon through polkit |
| Market-Open | Mon-Fri 06:20 | `signaldeck-market-open.timer` | `signaldeck-ctl.sh collect`; the ngrok tunnel reads as "not registered (optional)" |
| Quick Tunnel | logon/boot (enabled in `tasks.psd1`, disabled in its newer XML) | not ported | replaced by the named tunnel's `cloudflared.service` |
| Research-Liveness | daily 08:15 | `signaldeck-research-liveness.timer` | |
| Restore | Sun 07:00 | `signaldeck-restore.timer` | restore rehearsal; temp copy in `/srv/sd/tmp` |
| Revalidation | monthly, day 1, 03:20 | `signaldeck-revalidation.timer` | |
| Structural-Liveness | daily 08:30 | `signaldeck-structural-liveness.timer` | |
| Tunnel | none (ngrok, webhooks to :8322) | not ported | TradingView webhooks would need a second public hostname on the named tunnel: **[OWNER]** decision |
| Tunnel Keepalive | logon + every 10 min | not ported | keeps the ngrok task alive; nothing to keep |
| Web | boot | `signaldeck-web.service` | enabled at boot |
| Web Keepalive | every 5 min | `Restart=always` + `signaldeck-web-guard.timer` | the quick-tunnel and :3000 halves do not apply |

`ops/tasks/SignalDeck Cloudflare Tunnel.xml` is not in `tasks.psd1` (it is
staged in `ops/tasks/.pending` and unused); its Linux form is the owner's
`cloudflared.service`.

Two `signaldeck-ctl.sh` subcommands stay Windows-only: `status` reads
schtasks (use `systemctl list-units 'signaldeck-*'` and `systemctl list-timers
'signaldeck-*'`), and `deploy`/`launch` build `.exe` paths (use
`deploy-linux.sh`).

## 10. Monitoring and alerting

| signal | mechanism |
|---|---|
| any unit fails | `OnFailure=signaldeck-alert@%n.service` -> `alert.sh` |
| daemon crash loop | 5 starts in 10 min fails the unit (alert); the guard starts it again 5 min later |
| daemon up but hung | guard: three silent `/api/health` checks, 20 min uptime floor, then restart + alert |
| web up but serving an incomplete build | guard: one restart per `BUILD_ID`, then suppress + alert |
| unit drift, disabled timers, failed last runs, ports, clock | `check-units.sh` daily 09:05 |
| grader heartbeat, WAL, ledger revisions/manifest/coverage/identities | `check-grader-health.sh` daily 09:20 |
| backup, restore rehearsal, forward test | unchanged scripts; their `sd_notify` lines land in the logs |

`alert.sh` appends to `logs/health-alerts.log` (as `lib-notify.ps1` did), logs
to the journal (`journalctl -t signaldeck-alert`), and posts to whichever of
`SIGNALDECK_DISCORD_WEBHOOK`, `SIGNALDECK_SLACK_WEBHOOK` and
`SIGNALDECK_WEBHOOK_URL` are set in `daemon/.env`, the transports the daemon's
own notifier already uses (`daemon/internal/notify`). The desktop balloon has no
headless equivalent. The daemon's SMTP transport needs port 587 or 465 because
OCI blocks 25.

Nothing on the VM can report the VM itself dying. **[OWNER]** optionally adds an
external uptime check on the public URL (any free uptime monitor; an account is
needed). OCI Monitoring shows CPU, memory and network per instance with no
setup; section 13 depends on it.

## 11. Restart behaviour

| unit | on crash | on stop | at boot |
|---|---|---|---|
| daemon | restart after 10 s; 5 in 10 min marks it failed (alert) | SIGTERM, 150 s drain (`ShutdownGrace` + journal close + WAL checkpoint), then SIGKILL | enabled after `/srv/sd` is mounted |
| web | restart after 5 s; same start limit | SIGTERM, 30 s | enabled |
| guards | n/a (oneshot every 5 min) | n/a | first run 2 min after boot |
| jobs | not retried (Windows did not retry them either); the failure alerts and `check-units.sh` reports it | `TimeoutStartSec=6h` | missed runs fire once (`Persistent=true`) |

`systemctl stop` is a deliberate stop, so `Restart=always` does not undo it.
That is why `ops/lib-portable.sh` does not write the daemon's stop file on Linux:
the daemon exits 0 on it, which systemd would read as a crash to recover from,
restarting it in the middle of the backup.

Security updates install unattended; reboots are manual
(`Unattended-Upgrade::Automatic-Reboot "false"`) and happen outside 06:00-14:30
PT on weekdays.

## 12. Backup and restore

Unchanged: Market-Close runs `ops/signaldeck-backup-offline.sh` with the daemon
stopped (`VACUUM INTO`), seals the archive with `sdmaint offsite-seal` and the
key at `~/.signaldeck/backup.key`, and uploads it as a release to the private
backups repository; the Restore timer rehearses the newest one weekly. On the VM
`gh` must be logged in as `signaldeck` (**[OWNER]** credential).

Restoring on the VM is `ops/DR_RUNBOOK.md` section 2 in bash. Restore into a
staging path first, never straight over `data/`:

```bash
cd /srv/sd/signaldeck
repo=nyaungnicholas-wq/signaldeck-backups
tag=${TAG:-$(gh release list --repo "$repo" --limit 50 | grep -oE 'backup-[0-9]{8}-[0-9]{6}' | sort -r | head -1)}   # TAG=backup-<stamp> pins one
mkdir -p /srv/sd/restore && cd /srv/sd/restore
gh release download "$tag" --repo "$repo" --pattern '*.db.gz.enc' --pattern '*.sha256' --pattern 'deletions.jsonl.enc' --clobber
enc=$(ls *.db.gz.enc)
sha256sum -c "$enc.sha256"                      # the sealed file, before anything opens it
/srv/sd/signaldeck/bin/sdmaint offsite-open -key ~/.signaldeck/backup.key -in "$enc" -out "${enc%.enc}"
gunzip -k "${enc%.enc}"                         # -> signaldeck-<stamp>.db
db="${enc%.gz.enc}"
sqlite3 "$db" 'PRAGMA integrity_check;'
/srv/sd/signaldeck/bin/sdmaint ledger-verify -db "$db"
python3 /srv/sd/signaldeck/ops/cloud/db-manifest.py "$db" > manifest-vm.txt
```

(Run as `signaldeck`: `sudo -u signaldeck -H bash`. Build `bin/sdmaint` once
with `cd daemon && go build -o ../bin/sdmaint ./cmd/sdmaint`.)

## 13. Idle reclamation

Oracle may reclaim an Always Free instance that is idle for 7 days, where idle
means CPU (95th percentile), network and memory are **all** under 20%. The
daemon runs 24x7 with its worker loops, the web serves the public site, and the
daily jobs are bursty, but **none of the three numbers has been measured on this
workload**. Memory (20% of 12 GB is 2.4 GB) is the likeliest to stay above the
line with the daemon, Next.js and the page cache, but that is an expectation,
not a measurement.

- During the first 7 days after cutover, read the instance's CPU, memory and
  network charts in OCI Monitoring daily.
- If they sit under 20%, the choice is the owner's: upgrade the account to Pay
  As You Go (Oracle's documentation says Always Free resources on such accounts
  are not reclaimed for idleness; re-check the current wording first), or accept
  the risk. Never generate artificial load to dodge the policy.
- Either way, a reclaimed VM costs at most one day of data: the sealed off-site
  backups and this kit rebuild it (sections 12 and 16).

## 14. CUTOVER runbook (DEFERRED; do not start until the banner's questions are closed)

**Preconditions, all required:**

- P1 **[OWNER]** Oracle account open, VM and block volume created (section 16).
- P2 **[OWNER]** Alpaca has been asked and copying its data to the VM is allowed.
- P3 This kit is merged into the branch the PC runs, and the VM checkout is at
  the same commit as the PC (`git rev-parse HEAD` on both).
- P4 Section 16 provisioning is done, units installed but **disabled**, and the
  section 17 rehearsal passed within the last 7 days.
- P5 Window: **Saturday 09:30-13:30 PT**. Markets are closed and no scheduled
  task falls in it (09:20 grader health is the last before it, 14:05 accuracy
  and cleanup the first after).

**C1. Freeze.** Note the PC's `git rev-parse HEAD`. On the VM:
`sudo -u signaldeck bash ops/cloud/deploy-linux.sh --dry-run` must end `deploy DRY RUN: every gate passed`.

**C2. Quiesce the PC writer** (elevated PowerShell on the PC, **[OWNER]**'s machine):

```powershell
cd 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
Get-ScheduledTask -TaskName 'SignalDeck*' | Where-Object State -ne 'Disabled' |
  Select-Object -ExpandProperty TaskName | Set-Content cutover-enabled-tasks.txt
Get-ScheduledTask -TaskName 'SignalDeck*' | Disable-ScheduledTask
bash ops/signaldeck-ctl.sh stop          # daemon drains (stop file, then /End); web and tunnel tasks stop
Get-Process signaldeckd -ErrorAction SilentlyContinue   # must print nothing
Test-Path data\signaldeck.db-wal         # absent, or 0 bytes after the checkpoint
```

`cutover-enabled-tasks.txt` is the list rollback re-enables. Disabling the tasks
first stops `daemon-guard.ps1` from restarting the daemon behind you.

**C3. Final consistent snapshot, encrypted** (Git Bash on the PC, daemon down):

```bash
bash ops/signaldeck-backup-offline.sh     # refuses a live daemon; writes data/backups/signaldeck-<stamp>.db + .sha256, seals and uploads release backup-<stamp>
.venv/Scripts/python.exe ops/cloud/db-manifest.py data/backups/signaldeck-<stamp>.db > cutover-manifest-pc.txt
```

Record the release tag, `signaldeck-<stamp>.db.sha256` and
`cutover-manifest-pc.txt`. They hold hashes and counts only and can be copied to
the VM. The VM fetches the database through the off-site path itself, which
also proves that path works.

**C4. Restore on the VM** with the section 12 block, run with `TAG=backup-<stamp>` (the C3 release, not merely the newest).

**C5. Verify it is the exact snapshot.** All must hold; any failure stops the
cutover (go to R1):

```bash
sha256sum signaldeck-<stamp>.db                # == the PC's signaldeck-<stamp>.db.sha256 (byte-identical)
sqlite3 signaldeck-<stamp>.db 'PRAGMA integrity_check;'   # ok
/srv/sd/signaldeck/bin/sdmaint ledger-verify -db signaldeck-<stamp>.db   # chain recomputes, every anchor signature verifies
diff cutover-manifest-pc.txt manifest-vm.txt  # no output: schema hash, ledger and anchor heads, every table count equal
```

Then move it into place:
`mv signaldeck-<stamp>.db /srv/sd/signaldeck/data/signaldeck.db` (with no stale
`-wal`/`-shm` beside it).

**C6. Keys and tombstones.** **[OWNER]** confirms `daemon/.env`,
`ledger_anchor.key`, `backup.key`, the anchor-publish clone and `gh` login are in
place (section 6), and copies the PC's newest `~/.signaldeck/deletions.jsonl`.
Replay it: `bin/sdmaint replay-tombstones -db data/signaldeck.db -log ~/.signaldeck/deletions.jsonl`
(dry run; expect nothing to delete, since the snapshot is newer than every
tombstone on the same machine), adding `-apply` only if it lists something.

**C7. Enable exactly one writer.**

1. PC: `Get-ScheduledTask -TaskName 'SignalDeck*' | Where-Object State -ne 'Disabled'` prints nothing;
   `Get-Process signaldeckd -ErrorAction SilentlyContinue` prints nothing (the web never opens the database, so a leftover `node` is not a writer).
2. **[OWNER]** stop the PC's tunnel connector, recoverably:
   `Stop-Service cloudflared; Set-Service cloudflared -StartupType Disabled`.
   The public site is now down; that is expected.
3. VM: `sudo -u signaldeck bash ops/cloud/deploy-linux.sh` builds HEAD into
   `bin/signaldeckd` (nothing was installed there before), starts the daemon
   (a unit need not be enabled to start) and must end `deploy VERIFIED`:
   running revision == HEAD, resolvable, worker runs stamped. No tunnel runs
   yet, so nothing outside the VM can reach it.
4. VM: `cd daemon && ../bin/signaldeckd -publication-posture; echo $?` must print 0
   (published) BEFORE the connector exists.
5. VM: `sudo systemctl enable signaldeck-daemon.service && sudo systemctl enable --now signaldeck-web.service`.
6. VM: `ops/DR_RUNBOOK.md` section 5 against `127.0.0.1:8322`: `/api/ledger/verify`
   ok, and the newest anchor matches the tail of the public `anchors.log`.
7. **[OWNER]** `sudo cloudflared service install <tunnel token>` on the VM. In the
   Cloudflare dashboard the tunnel must show exactly **one** connector, the VM's.
8. Public checks from `ops/CLOUDFLARE_TUNNEL.md` section 3 step 3: a non-admin
   account's `/api/auth/me` shows `"member":true`; signed out,
   `/api/export/bars.csv?symbol=AAPL` does not return 200. **If either fails,
   [OWNER] runs `sudo systemctl stop cloudflared` on the VM at once** (the site
   goes down; nothing more leaks), then fix `daemon/.env`, redeploy and repeat
   from step 4, or roll back with R2.
9. VM: `sudo systemctl enable --now $(cd ops/cloud/systemd && ls signaldeck-*.timer)`.
10. VM: `sudo -u signaldeck bash ops/cloud/check-units.sh` ends `check-units: HEALTHY`.

**C8. Hold the PC for 7 days, recoverable.** Tasks stay disabled, not deleted.
Rename the binary so nothing can start it:
`Rename-Item bin\signaldeckd.exe signaldeckd.exe.cloud-cutover`. Leave
`data\signaldeck.db` untouched as the rollback reference. After 7 clean days
**[OWNER]** decides whether to retire the PC fleet.

**C9. One-hour PC-off check.** On the first weekday after cutover, **[OWNER]**
powers the PC off from 13:05 to 14:10 PT, which covers Market-Close (13:10),
Daily-Refresh (13:15) and Accuracy/Cleanup (14:05). During the hour:
`https://<host>/login` serves and `web-assets-check.sh` passes; `/api/health`
answers; `logs/backup-offline.log` reaches `offsite OK` for the new release;
`worker_runs` keeps gaining rows; the Cloudflare dashboard shows only the VM
connector. Anything that breaks while the PC is off was still depending on it.

## 15. ROLLBACK

**R1. Before any cloud write** (the VM daemon never started, i.e. stopped
before C7.3's deploy):

1. VM: `sudo systemctl disable --now signaldeck-daemon.service signaldeck-web.service`
   and every `signaldeck-*.timer`.
2. PC: rename `bin\signaldeckd.exe.cloud-cutover` back if C8 ran; re-enable
   exactly the tasks in `cutover-enabled-tasks.txt`
   (`Get-Content cutover-enabled-tasks.txt | ForEach-Object { Enable-ScheduledTask -TaskName $_ }`);
   `bash ops/signaldeck-ctl.sh up`.
3. **[OWNER]** `Set-Service cloudflared -StartupType Automatic; Start-Service cloudflared` on the PC.

**R2. After cloud writes** (the VM daemon ran, even briefly). **Never restart
the PC's database blindly**: it lacks every row the VM wrote (worker runs,
predictions, ledger entries, anchors already published to the public
`anchors.log`, member sign-ups and deletions). Restarting it forks the ledger
in public.

1. Stop the VM writer: disable the timers, stop daemon and web; **[OWNER]** stops
   the VM connector (`sudo systemctl disable --now cloudflared`).
2. Take a final snapshot on the VM: `sudo -u signaldeck bash ops/signaldeck-backup-offline.sh`
   (sealed release) and `db-manifest.py` of it.
3. The VM's final snapshot is the truth: the PC's copy plus every cloud write.
   Default rollback moves **that** to the PC: restore it there with
   `ops/DR_RUNBOOK.md` section 2 using exactly the release step 2 uploaded
   (`logs/backup-offline.log` on the VM says `offsite OK` for it), never merely
   the newest one; if the upload did not complete, copy
   `data/backups/signaldeck-<stamp>.db` and its `.db.sha256` from the VM over
   SSH instead. Check sha256 + `ledger-verify` + `db-manifest` diff, keep the
   PC's old file as `data\signaldeck.db.pre-cloud-<stamp>`, then R1 steps 2-3.
4. Only if the VM's database is unusable (fails `integrity_check` or
   `ledger-verify`): fall back to the PC's pre-cutover database, and before
   restarting anything list what is lost (VM-era ledger entries and anchors,
   sign-ups, deletions). Replay the VM's `deletions.jsonl` on the PC
   (`sdmaint replay-tombstones ... -apply`), publish nothing until the public
   anchor head is reconciled (`ops/DR_RUNBOOK.md` section 5, "no published line
   reproduces" = stop), and state the gap publicly.

## 16. Provisioning (once, before cutover)

**[OWNER]** in the OCI console: VM.Standard.A1.Flex 2 OCPU / 12 GB, image
Canonical Ubuntu 24.04 (aarch64), boot volume 50 GB, the owner's SSH public
key; a 150 GB block volume attached paravirtualized; a security list with
22/tcp from the owner's IP only.

Operator, as `ubuntu`:

```bash
sudo apt-get update && sudo apt-get install -y git sqlite3 zstd chrony python3-venv lsof unattended-upgrades   # the daemon shells out to git, pgrep and lsof
sudo timedatectl set-timezone America/Los_Angeles
echo 'server 169.254.169.254 iburst prefer' | sudo tee /etc/chrony/sources.d/oci.sources && sudo systemctl restart chrony
sudo fallocate -l 4G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
# data volume (check the device with lsblk first)
sudo mkfs.ext4 -L sd-data /dev/sdb && sudo mkdir -p /srv/sd
echo "UUID=$(sudo blkid -s UUID -o value /dev/sdb) /srv/sd ext4 defaults,nofail 0 2" | sudo tee -a /etc/fstab && sudo mount /srv/sd
sudo useradd --system --create-home --home-dir /home/signaldeck --shell /usr/sbin/nologin signaldeck
sudo install -d -o signaldeck -g signaldeck -m 0750 /srv/sd /srv/sd/tmp
sudo install -d -o signaldeck -g signaldeck -m 0700 /home/signaldeck/.signaldeck
```

Toolchains (versions come from the repo, not from this page): Go from
go.dev's `linux-arm64` tarball at the `toolchain` version in `daemon/go.mod`,
unpacked to `/usr/local/go`; Node at the `node-version` CI uses
(`.github/workflows/ci.yml`), `linux-arm64`; golangci-lint at the `version` CI
pins, from its GitHub release (`linux-arm64`, check the checksums file), into
`/usr/local/bin`; `gh` from GitHub's apt repository. Then, as `signaldeck`
(`sudo -u signaldeck -H bash`):

```bash
cd /srv/sd && git clone https://github.com/nyaungnicholas-wq/signaldeck.git signaldeck && cd signaldeck
git checkout <the commit the PC runs>
mkdir -p logs data bin
python3 -m venv .venv && .venv/bin/pip install -r requirements-quant.txt   # plus the PC venv's other packages: compare with its pip freeze
# compatibility shim: three ops scripts call .venv/Scripts/python.exe (the Windows layout)
mkdir -p .venv/Scripts && ln -s ../bin/python .venv/Scripts/python.exe
(cd web && npm ci && npx playwright install chromium)
(cd daemon && go build -o ../bin/sdmaint ./cmd/sdmaint)
```

`npx playwright install --with-deps chromium` needs root for its apt step; run
`sudo npx playwright install-deps chromium` from `ubuntu` once. Then install the
units, **without enabling anything**:

```bash
sudo cp ops/cloud/systemd/signaldeck-* /etc/systemd/system/
sudo cp ops/cloud/systemd/50-signaldeck.rules /etc/polkit-1/rules.d/
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/signaldeck-*.service /etc/systemd/system/signaldeck-*.timer
for t in ops/cloud/systemd/*.timer; do systemd-analyze calendar "$(sed -n 's/^OnCalendar=//p' "$t")" 2>/dev/null | head -3; done
printf 'Unattended-Upgrade::Automatic-Reboot "false";\n' | sudo tee /etc/apt/apt.conf.d/52signaldeck-no-reboot
```

`check-units.sh` compares `/etc/systemd/system` against this directory byte for
byte, so re-copy after every change here.

## 17. Staging rehearsal (also waits on P2: it copies Alpaca-derived data)

Read-only with respect to production; no daemon, no timers:

1. Section 12 restore of the newest release into `/srv/sd/restore` (proves
   `gh`, the key, `offsite-open`, `integrity_check`, `ledger-verify`).
2. `sudo -u signaldeck bash ops/cloud/deploy-linux.sh --dry-run` (vet, lint,
   tests and the arm64 build on the VM).
3. `sudo -u signaldeck bash ops/cloud/web-release-linux.sh --build-only` (the
   first real ARM build of the web app: asset and browser gates on spare ports).
4. Delete `/srv/sd/restore` afterwards.

## 18. Validation status

Done on the Windows host that wrote this kit (Git Bash; no VM, no WSL, no Docker):

- `bash -n` on every script; `test-deploy-linux.sh` (16 cases: dirty tree,
  vet, lint, failing test, build, posture, root, foreign lock, revision
  mismatch, unresolvable, no stamped worker run, failed restart, dry run),
  `test-linux-ops.sh` (21 cases: systemd branch, non-Linux untouched,
  market-close restart, guard locks/strikes/posture, web guard),
  `test-web-assets-check.sh` (5 cases against a real HTTP server) and
  `check-grader-health.sh __selfcheck`, `db-manifest.py __selfcheck`;
  mutation checks on the deploy and guard tests.
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build` of `cmd/signaldeckd` and
  `cmd/sdmaint` (ELF aarch64).

Not available on that host, so not run: `shellcheck`, `systemd-analyze verify`
and `systemd-analyze calendar` (section 16 runs them on the VM).

**Unverified until someone does it on real hardware:**

- the ARM web build (`next build` and the playwright browser gate on linux-arm64;
  the lockfile carries linux-arm64 natives that have never been built),
- a real VM boot with these units, the polkit rule and `ProtectSystem=strict`,
- a staging restore of a real encrypted backup on the VM (section 17),
- the one-hour PC-off check (C9),
- idle-reclamation metrics (section 13) and the Python packages beyond
  `requirements-quant.txt` on aarch64,
- the PC's DST trigger behaviour (section 8).
