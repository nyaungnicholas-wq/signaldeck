# SignalDeck — Disaster-Recovery Runbook (clean second Windows machine)

Institutional-readiness gap this closes: **"no path off this machine."** Every
Scheduled Task, the database, and the daemon live on one machine. This runbook
is the exact sequence to bring SignalDeck back up on a clean Windows machine
from the newest offsite backup, ending with cryptographic proof that the
restored track record is the same one that was published externally before the
disaster.

Read it once now, not during the incident. The quarterly rehearsal line in
`ops/GO-LIVE.md` exists because a runbook nobody has executed on a real second
machine is a hypothesis, not a recovery plan.

> **Rewritten 2026-08-07 for Windows.** The previous version targeted "a clean
> second Mac": launchd, `.plist` files, and an iCloud Drive backup path. This
> machine moved to Windows on 2026-07-31, so every mechanism in that document
> was inert — it was a recovery plan for a machine nobody owns any more. If you
> are reading this on macOS, `git log` has the old one.

## What survives the machine, and what does not

Survives (recovery inputs):

| artifact | where |
|---|---|
| Database backups (nightly `VACUUM INTO`, newest plain `.db`, older `.gz`, each with a `.sha256`) | **Off-machine (2026-09-07): GitHub release assets on the private repo named by `SIGNALDECK_OFFSITE_GH_REPO`** (`nyaungnicholas-wq/signaldeck`), one release `backup-<timestamp>` per nightly run holding `<backup>.db.gz` + `.sha256`, newest 7 kept. Fetch the newest with `gh release download <tag> --repo <repo> --pattern '*.db.gz' --pattern '*.sha256'` or rehearse a restore with `ops/restore-rehearsal.sh --from-github`. Same-volume copies: `%SIGNALDECK_OFFSITE_DIR%` if set, else `%OneDrive%\SignalDeckBackups` (NOT off-machine unless that folder actually syncs) |
| Source, ops scripts, the `.plist` files the task installer reads | GitHub: `nyaungnicholas-wq/signaldeck` |
| External anchors, prereg chain head, accuracy registry | the public anchors repo (`anchors.log`, `prereg.log`, `accuracy_registry.json` in its git history) |

Does NOT survive automatically — keep offline copies (password manager or
encrypted disk), or accept the stated consequence:

| artifact | consequence if lost |
|---|---|
| `daemon\.env` (gitignored: API token, TV webhook secret, data-source keys, allowlists) | re-enter every secret by hand; nothing else breaks |
| `%USERPROFILE%\.signaldeck\ledger_anchor.key` (Ed25519 signing key — deliberately OUTSIDE `data\`, so it is in NO backup) | survivable but publicly visible: old anchors still verify (each `Record` embeds its `pubKey`), but the daemon generates a fresh key and every NEW anchor signs with it — external observers see a key rotation they are entitled to question |
| everything written after the last nightly backup | gone. RPO = last offsite backup (≤ ~24h); backfill workers close price-data gaps, but resolved outcomes emitted in the lost window are not reconstructable |

## 0. Prerequisites on the clean machine (~30 min)

1. **Git for Windows** — required, and it must be Git Bash, never WSL. The ops
   scripts address `C:\Users\...` directly; WSL's filesystem cannot see those
   paths the way they expect.
2. **Go 1.26.5+**, **Node.js 20+**, **sqlite3** on `PATH`.
3. Set `SIGNALDECK_OFFSITE_DIR` if your backups are not in OneDrive. With
   neither that variable nor OneDrive, `signaldeck-backup-offline.sh` logs
   `offsite SKIPPED` and there is nothing here to recover from — check
   `logs\backup-offline.log` says `offsite OK` on the machine you are
   recovering FROM, before you need this page.

## 1. Clone and build (~15 min)

```powershell
mkdir $env:USERPROFILE\Desktop\"claude code"; cd $env:USERPROFILE\Desktop\"claude code"
git clone https://github.com/nyaungnicholas-wq/signaldeck.git
cd signaldeck\daemon
go build -o ..\bin\signaldeckd.exe .\cmd\signaldeckd
go build -o ..\bin\sdmaint.exe .\cmd\sdmaint
cd ..\web; npm ci; npm run build
```

The `.exe` suffix is load-bearing: the Scheduled Task launches
`bin\signaldeckd.exe`, and a build that writes an extensionless `bin\signaldeckd`
leaves the task pointing at nothing.

## 2. Restore the database, and verify it before trusting it

**Fetch from GitHub, not from the local folder.** The table above already says
which copy is off-machine, and this section used to contradict it: it read only
`%SIGNALDECK_OFFSITE_DIR%`, defaulting to `%OneDrive%\SignalDeckBackups`, which
lives on the SAME VOLUME as the database it is meant to replace. In the disk
loss this runbook exists for, that folder is gone with everything else, and the
block would `throw "no backup found"` at the worst possible moment.

It is also silently behind even when the disk is fine. Measured 2026-09-12: the
local folder's newest file was `signaldeck-20260907-131007.db` while the GitHub
release `backup-20260910-131009` was three days newer. Following the old block
restored the older one and said nothing.

```powershell
cd $env:USERPROFILE\Desktop\"claude code"\signaldeck
$repo = if ($env:SIGNALDECK_OFFSITE_GH_REPO) { $env:SIGNALDECK_OFFSITE_GH_REPO } else { 'nyaungnicholas-wq/signaldeck' }
$tag  = (gh release list --repo $repo --limit 50 |
         Select-String -Pattern 'backup-\d{8}-\d{6}' -AllMatches |
         ForEach-Object { $_.Matches.Value } | Sort-Object -Descending | Select-Object -First 1)
if (-not $tag) { throw "no backup-* release found in $repo -- is gh authenticated? (gh auth status)" }
New-Item -ItemType Directory -Force restore-dl | Out-Null
gh release download $tag --repo $repo --pattern '*.db.gz' --pattern '*.sha256' --dir restore-dl --clobber
$src = Get-ChildItem restore-dl -Filter '*.db.gz' | Select-Object -First 1
if (-not $src) { throw "release $tag carried no .db.gz asset" }
$offsite = $src.DirectoryName
"restoring from $tag / $($src.Name) ($([math]::Round($src.Length/1GB,2)) GB)"
```

FALLBACK, and only when you have checked it is not stale: if GitHub is
unreachable and the volume survived, the local folder is a faster path to a
possibly-older database. Compare its newest file against the newest
`backup-*` tag before trusting it, and expect to lose whatever fell between.

```powershell
$offsite = if ($env:SIGNALDECK_OFFSITE_DIR) { $env:SIGNALDECK_OFFSITE_DIR } else { "$env:OneDrive\SignalDeckBackups" }
$src = Get-ChildItem $offsite -Filter 'signaldeck-*.db*' |
       Where-Object { $_.Name -notlike '*.sha256' } |
       Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $src) { throw "no backup found in $offsite (expected -- this folder does not survive disk loss)" }
"restoring from $($src.Name) ($([math]::Round($src.Length/1GB,2)) GB), taken $($src.LastWriteTime)"
"CHECK THIS DATE against the newest backup-* release before continuing."
```

Either path leaves `$src` and `$offsite` set, so the decompress-and-verify
steps below are shared:

```powershell
New-Item -ItemType Directory -Force data | Out-Null
if ($src.Extension -eq '.gz') {
    $in = [IO.File]::OpenRead($src.FullName); $out = [IO.File]::Create('data\signaldeck.db')
    $gz = New-Object IO.Compression.GZipStream($in, [IO.Compression.CompressionMode]::Decompress)
    $gz.CopyTo($out); $gz.Close(); $out.Close(); $in.Close()
} else {
    Copy-Item $src.FullName 'data\signaldeck.db'
}
```

Verify the checksum recorded when the backup was taken, then the file itself.
A backup that fails either check is not a backup — go back one generation:

```powershell
# TWO sidecar conventions, and they hash DIFFERENT FILES. The GitHub uploader
# (ops/lib-offsite-gh.sh:67) runs sha256sum over the COMPRESSED asset and names
# it "<file>.db.gz.sha256"; the local nightly records the UNCOMPRESSED .db as
# written by VACUUM INTO, as "<file>.db.sha256". Checking only the second --
# which this block used to do -- prints "integrity is unverified" for every
# GitHub restore, i.e. on the one copy that survives a disk loss.
$gzSidecar = Join-Path $offsite ($src.Name + '.sha256')      # hashes the archive
$dbSidecar = Join-Path $offsite ($src.BaseName + '.sha256')  # hashes the restored db
if (Test-Path $gzSidecar) {
    $want = (Get-Content $gzSidecar -Raw).Split()[0].Trim()
    $got  = (Get-FileHash $src.FullName -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "SHA256 MISMATCH on the archive: recorded $want, got $got" }
    'sha256 OK (compressed asset)'
} elseif (Test-Path $dbSidecar) {
    $want = (Get-Content $dbSidecar -Raw).Split()[0].Trim()
    $got  = (Get-FileHash 'data\signaldeck.db' -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "SHA256 MISMATCH: recorded $want, restored $got" }
    'sha256 OK (restored database)'
} else { 'WARNING: no .sha256 sidecar - integrity is unverified' }
sqlite3 data\signaldeck.db "PRAGMA quick_check;"
sqlite3 data\signaldeck.db "SELECT COUNT(*) FROM bars;"
```

Neither check is optional. `PRAGMA quick_check` catches a corrupt page; the
sha256 catches a truncated or swapped file, which quick_check will happily
pass on a structurally valid prefix.

## 3. Recreate secrets, keys, and the anchor repo

1. `daemon\.env` — restore from your offline copy, or re-enter each key. At
   minimum: `SIGNALDECK_NVIDIA_KEY`, `SIGNALDECK_TV_WEBHOOK_SECRET`,
   `SIGNALDECK_ALLOWED_HOSTS`, `SIGNALDECK_API_TOKEN`.
2. `%USERPROFILE%\.signaldeck\ledger_anchor.key` — restore from your offline
   copy. If it is gone, let the daemon generate a new one and **say so publicly**
   alongside the next anchor; a silent key rotation looks exactly like a forged
   chain to anyone verifying it.
3. Clone the public anchors repo to
   `%USERPROFILE%\.signaldeck\anchor-publish`.

## 4. Reinstall the Scheduled Task fleet

There are no task XML files to import. `ops\install-windows-tasks.ps1` reads the
same `ops\com.*.plist` files the Mac used and registers the Windows equivalents,
so the two schedules cannot drift. It reports by default and changes nothing
without `-Install`:

```powershell
cd $env:USERPROFILE\Desktop\"claude code"\signaldeck\ops
.\install-windows-tasks.ps1              # dry run — read this before installing
.\install-windows-tasks.ps1 -Install
.\fix-task-principals.ps1                # S4U principals; see the console-kill note below
schtasks /query /fo TABLE | Select-String SignalDeck
```

Tasks are named `SignalDeck <Leaf>` — `com.signaldeck.daemon` becomes
`SignalDeck Daemon`. Start the stack the sanctioned way rather than by running
tasks by hand:

```bash
bash ops/signaldeck-ctl.sh up
```

If tasks exit immediately with `0xC000013A`, that is a console control event
killing an Interactive-logon task — run `fix-task-principals.ps1` **elevated**.

## 5. Verify the restored ledger against what was published

This is the step that distinguishes "a database came back" from "the track
record came back". Reads are authenticated: `daemon/.env` allowlists a public
tunnel hostname, so `PublicReads` defaults closed and these calls need the
bearer token.

```bash
TOKEN=$(grep -m1 '^SIGNALDECK_API_TOKEN=' daemon/.env | cut -d= -f2-)
AUTH=(-H "Authorization: Bearer $TOKEN" -H "X-Signaldeck: 1")

# 1. The restored chain is internally intact.
curl -s "${AUTH[@]}" http://127.0.0.1:8322/api/ledger/verify | jq .

# 2. The newest anchor the restored chain reproduces.
curl -s "${AUTH[@]}" 'http://127.0.0.1:8322/api/ledger/anchors?limit=1' | jq '.anchors[0]'

# 3. The externally published line it must match.
tail -1 "$USERPROFILE/.signaldeck/anchor-publish/anchors.log"
```

**Interpretation:**

- **The `publish` line matches the tail of the public `anchors.log`, `ok: true`** —
  the restored history reproduces, byte for byte, a head timestamped before the
  disaster. Recovery is proven. Done.
- **An OLDER published line reproduces, but not the newest** — the backup
  predates the last anchor. Everything up to the matching anchor is proven; the
  gap is attested only by the backup file. State that gap publicly.
- **No published line reproduces** — STOP. Either the wrong backup was restored
  or the restored history is not the published history. Do not publish new
  anchors, and do not serve the track record, until this is explained.

## 6. Rollback — when the restore is the thing that went wrong

A restore is itself a change that can fail, so it needs its own way back. This
applies to the recovery above and to any migration that rewrites `data\`.

**Before** overwriting a database you may still want:

```powershell
cd $env:USERPROFILE\Desktop\"claude code"\signaldeck
bash ops/signaldeck-ctl.sh stop
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
Move-Item data\signaldeck.db "data\signaldeck.db.pre-restore-$stamp"
Remove-Item data\signaldeck.db-wal, data\signaldeck.db-shm -ErrorAction SilentlyContinue
```

The `-wal` and `-shm` files belong to the OLD database. Leaving them beside a
restored `.db` is how a restore silently half-applies: SQLite replays a write
log the new file never wrote.

**To roll back** to the pre-restore state:

```powershell
bash ops/signaldeck-ctl.sh stop
Remove-Item data\signaldeck.db, data\signaldeck.db-wal, data\signaldeck.db-shm -ErrorAction SilentlyContinue
Move-Item "data\signaldeck.db.pre-restore-<stamp>" data\signaldeck.db
bash ops/signaldeck-ctl.sh up
```

**Rolling back the BINARY** is separate and already handled: `build_from_head`
moves the previous executable to `bin\signaldeckd.exe.prev` before installing a
new one.

```powershell
bash ops/signaldeck-ctl.sh stop
Move-Item -Force bin\signaldeckd.exe.prev bin\signaldeckd.exe
bash ops/signaldeck-ctl.sh up
```

Note that `ops\daemon-guard.ps1` restarts a stopped daemon every 5 minutes. It
stands down while `ops\.maintenance` exists, so create that file before any
swap and delete it after — otherwise the guard races you and Windows refuses
the replacement because the running image is held open.

## 7. Close out

```bash
curl -s "${AUTH[@]}" http://127.0.0.1:8322/api/health | jq '{degraded, workers, revision}'
curl -s -X POST "${AUTH[@]}" http://127.0.0.1:8322/api/notify/test
```

`degraded: false` with an empty `workers` map is the goal; anything listed there
is a worker whose latest run did not deliver. Then open
`http://127.0.0.1:8323` and confirm the dashboard paints.

Finally: **write down the date you executed this.** A runbook rehearsed a year
ago is a hypothesis again.
