#!/bin/bash
# Offline SQLite backup — runs AFTER market-close stops the daemon, so the
# 2GB+ VACUUM INTO copy never competes with the live app for the (niced,
# LowPriorityIO) daemon's disk/CPU budget. This replaces the in-daemon
# "nightly" backup as the primary path: with the daemon gated to 06:20-13:10
# PT the worker's restart-gate used to fire the full backup at BOOT every
# morning, wedging the dashboard for 10+ minutes (root-caused 2026-07-24).
# The in-daemon worker stays as a failsafe (MinGap raised to 30h): if this
# script keeps succeeding daily, the worker always sees a fresh backup_last_ts
# and skips.
set -u
# Repo root, derived from this script's own location rather than hardcoded.
# This was "/Users/natalienyaung/claude code/signaldeck" — same defect already
# fixed in ops/accuracy-registry.sh and left standing here, so on any machine
# but the original Mac this script backed up nothing and logged to nowhere.
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# caffeinate / sqlite3 / osascript are macOS-only and none exist under Git Bash,
# which is why this script produced nothing after the 2026-07-31 move.
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
# 2026-09-01: the scheduled task's process env carries no SIGNALDECK_OFFSITE_* and
# nothing here sourced daemon/.env, so a destination set only in .env never reached
# this script (21 days with no off-machine copy). Environment still wins; .env fills
# the gaps; values are never logged. Selftest: ops/test-offsite-env.sh
. "$SD/ops/lib-offsite-env.sh" && sd_offsite_env_from_dotenv "$SD/daemon/.env"

# python3 on Windows/Git Bash is a Microsoft Store alias stub that resolves,
# prints "Python was not found", and exits 0 — so `command -v` is not enough,
# the candidate has to actually run. Same resolution as accuracy-registry.sh.
PY=""
for cand in python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    PY="$cand"; break
  fi
done
DB="$SD/data/signaldeck.db"
DIR="$SD/data/backups"
# Where the off-MACHINE copy goes. Overridable, because the hardcoded iCloud
# path below is only off-machine on the Mac this was written on: on Windows it
# resolved to ordinary folders under C:\Users\...\Library\Mobile Documents,
# i.e. the SAME physical disk as the database, while /api/quality happily
# reported offsiteConfigured:true. Point SIGNALDECK_OFFSITE_DIR at an external
# drive (or any genuinely separate volume) and this becomes true again.
#
# The iCloud default therefore applies on macOS ONLY. Everywhere else offsite is
# opt-in: an unset destination copies nothing and says so, which is honest,
# where the old default copied 2.7GB onto C: and looked like it worked.
if [ -n "${SIGNALDECK_OFFSITE_DIR:-}" ]; then
  OFFSITE="$SIGNALDECK_OFFSITE_DIR"
elif [ "$(uname -s)" = "Darwin" ]; then
  OFFSITE="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
elif [ -n "${OneDrive:-}" ] && [ -d "$OneDrive" ]; then
  # The Windows counterpart of the iCloud default above, and the reason offsite
  # went from "never ran" to "runs".
  #
  # Every line of logs/backup-offline.log since the migration read "offsite
  # SKIPPED: no destination configured" — so the database AND every backup of it
  # lived on one disk, and this machine has exactly one volume (C:). A synced
  # OneDrive folder is genuinely off-machine in the way that matters: the copy
  # survives losing this disk. It is NOT a second physical volume, so an
  # external drive in SIGNALDECK_OFFSITE_DIR still beats it and still wins.
  #
  # QUOTA: a generation is ~5GB raw and compresses to ~20%. Check the OneDrive
  # plan has room before trusting this — a sync that silently stops uploading is
  # the one failure this whole file exists to prevent. compress_and_prune runs
  # against the destination too, so the folder stays bounded rather than growing
  # a generation a night.
  OFFSITE="$OneDrive/SignalDeckBackups"
else
  OFFSITE=""
fi
# Retention exists because data/backups hit 13GB on 2026-07-26: the compress
# path shipped 07-25 but market-close.sh — its only trigger — doesn't fire on
# weekends, so it never ran while the in-daemon failsafe kept adding raw ~1.9GB
# snapshots. The budget is the tripwire that would have caught that.
KEEP_RAW=1      # newest generation kept as an instantly-restorable plain .db
KEEP_ZST=5      # older generations kept compressed (zstd -3, gzip fallback)
KEEP_RAW_MAX=2  # backstop cap on plain .db if compression itself keeps failing
LOG="$SD/logs/backup-offline.log"

# GZ_RATIO_PCT: a compressed generation as a percentage of the raw DB it came
# from. Measured on this repo's own history — 2715MB raw -> 552MB gz (20.3%),
# 2180MB -> 527MB (24.2%) — and rounded UP, because a budget that is too tight
# produces false alarms, which is the failure this derivation exists to end.
GZ_RATIO_PCT=25
BUDGET_FLOOR_MB=2048  # never derive a budget so small that a tiny DB false-alarms

# derive_budget_mb DB_MB — the largest footprint the retention policy can
# LEGITIMATELY produce at this database size.
#
# The budget used to be the constant 6144, derived by hand when the DB was ~2GB.
# The DB is now 3.3GB, so the constant went stale and the tripwire fired every
# weekday — and a check that is always red is not a check, it is training to
# ignore red. Exactly the failure mode hud-sync and regime-outcome-runner had.
#
# The most the policy can legitimately hold AT ANY INSTANT, including the
# transient moment inside a pass before prune has run:
#   KEEP_RAW_MAX raw copies      (the backstop cap when compression keeps failing;
#                                 also covers the new copy sitting beside the old)
# + (KEEP_ZST + 1) compressed    (the extra one exists between compress and prune)
#
# Anything ABOVE that is not the policy working hard, it is the policy broken.
# Below it, nothing is wrong and the alarm stays quiet — which is the point.
#
# Deliberately NOT more generous than that. Budgeting a third raw copy pushed the
# limit to 14GB, which would have tolerated four stray generations before saying
# anything; the 2026-07-26 incident was caught at 13GB. A tripwire that only
# trips after the thing it exists to catch has already happened is decoration.
derive_budget_mb() {
  local db_mb="${1:-0}" budget
  budget=$(( db_mb * KEEP_RAW_MAX + db_mb * (KEEP_ZST + 1) * GZ_RATIO_PCT / 100 ))
  [ "$budget" -lt "$BUDGET_FLOOR_MB" ] && budget=$BUDGET_FLOOR_MB
  printf '%s' "$budget"
}

# Derived from the live DB every run, so growth can never silently invalidate it.
DB_MB=$(du -m "$DB" 2>/dev/null | cut -f1)
BUDGET_MB=$(derive_budget_mb "${DB_MB:-0}")

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$LOG"; }

# volume_of PATH — the mount point PATH lives on, or "" when it cannot be told.
#
# `df -P` is the portable answer and the only one that works on BOTH sides of
# this repo's platforms: it prints /c or /d under the Git Bash these tasks run
# on Windows, and / or /mnt/... on Unix. `stat -c %d` is NOT usable here —
# measured 2026-08-11 under Git Bash it returned the identical device id
# (2585421839) for every path on the machine, so it cannot distinguish volumes
# at all and would silently answer "same" forever.
volume_of() {
  df -P "$1" 2>/dev/null | tail -1 | awk '{print $NF}'
}

# same_volume A B — true when both live on one volume. UNKNOWN COUNTS AS
# DIFFERENT on purpose: this gates whether a copy is CALLED offsite, and the
# caller keeps the copy either way, so an unanswerable question must not
# downgrade a genuinely-external destination. The Go side takes the opposite
# default for the opposite reason (api.go treats unknown as "not configured",
# because there it gates a claim rather than a label).
same_volume() {
  local a b
  a="$(volume_of "$1")"
  b="$(volume_of "$2")"
  [ -n "$a" ] && [ -n "$b" ] && [ "$a" = "$b" ]
}

# file_size PATH — size in bytes, or 0. GNU first, then BSD; the VALUE is
# validated rather than the exit status, because `stat -f` on GNU prints a
# filesystem report AND exits 0 (see ops/restore-rehearsal.sh:file_mtime).
file_size() {
  local v
  v="$(stat -c %s "$1" 2>/dev/null)"
  case "$v" in ''|*[!0-9]*) v="" ;; esac
  if [ -z "$v" ]; then
    v="$(stat -f %z "$1" 2>/dev/null)"
    case "$v" in ''|*[!0-9]*) v="" ;; esac
  fi
  printf '%s' "${v:-0}"
}

# Hard footprint assertion: retention regressions must page, not silently
# refill the disk. market-close.sh ignores our exit code, so the banner here
# (same channel as the H9 silence banner below) is what reaches a human.
# The budget governs what the ROTATION CONTROLS, not everything in the folder.
# Only timestamped signaldeck-YYYYmmdd-HHMMSS files are rotated; ad-hoc
# snapshots (signaldeck-premaint-*, etc.) are deliberately left alone. Judging
# the policy on a total that includes files it is forbidden to touch means a
# human dropping one snapshot in the folder makes retention look broken — which
# is what happened on 2026-08-03: 5776MB managed (fine) + one 583MB ad-hoc file,
# reported as "compression/prune is not holding".
#
# Unmanaged bytes are still REPORTED, because the disk does not care who wrote
# them — they just cannot fail the retention check.
assert_budget() {
  local used managed unmanaged
  used=$(du -sm "$DIR" 2>/dev/null | cut -f1)
  managed=$(du -cm "$DIR"/signaldeck-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-*.db* 2>/dev/null | tail -1 | cut -f1)
  unmanaged=$(( ${used:-0} - ${managed:-0} ))
  if [ "${managed:-0}" -gt "$BUDGET_MB" ]; then
    log "FAIL: managed backups ${managed}MB exceed the derived budget ${BUDGET_MB}MB " \
        "(DB ${DB_MB}MB -> ${KEEP_RAW_MAX} raw + $(( KEEP_ZST + 1 )) gz @ ${GZ_RATIO_PCT}%) — retention is NOT holding"
    sd_notify "SignalDeck: retention is not holding" \
      "Managed backups are ${managed} MB against a ${BUDGET_MB} MB budget derived from a ${DB_MB} MB database. Compression or prune has stopped working."
    return 1
  fi
  log "budget OK: ${managed:-0}MB managed of ${BUDGET_MB}MB derived (DB ${DB_MB}MB); ${unmanaged}MB ad-hoc, not rotated"
  return 0
}

# --prune-only: compress/prune/assert WITHOUT taking a new backup. Never
# touches the live DB, so it is safe (and gate-exempt) while the daemon runs —
# the weekend/failsafe catch-up path the 13GB pile-up proved we need.
PRUNE_ONLY=false
[ "${1:-}" = "--prune-only" ] && PRUNE_ONLY=true

# --explain-budget: print the derivation and exit. A self-adjusting limit that
# nobody can inspect is just a different kind of magic number.
if [ "${1:-}" = "--explain-budget" ]; then
  used=$(du -sm "$DIR" 2>/dev/null | cut -f1)
  managed=$(du -cm "$DIR"/signaldeck-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-*.db* 2>/dev/null | tail -1 | cut -f1)
  cat <<EOF
live database          ${DB_MB:-0} MB
policy                 KEEP_RAW=$KEEP_RAW (max $KEEP_RAW_MAX), KEEP_ZST=$KEEP_ZST, gz ~${GZ_RATIO_PCT}% of raw
  $KEEP_RAW_MAX raw copies       $(( ${DB_MB:-0} * KEEP_RAW_MAX )) MB   (backstop cap; covers new beside old)
  $(( KEEP_ZST + 1 )) compressed        $(( ${DB_MB:-0} * (KEEP_ZST + 1) * GZ_RATIO_PCT / 100 )) MB   (extra one exists between compress and prune)
  ---------------------------------
  derived budget       $BUDGET_MB MB   (floor $BUDGET_FLOOR_MB MB)

actual managed         ${managed:-0} MB   $([ "${managed:-0}" -le "$BUDGET_MB" ] && echo "OK" || echo "OVER - retention not holding")
actual ad-hoc          $(( ${used:-0} - ${managed:-0} )) MB   reported, never rotated, cannot fail the check
actual total           ${used:-0} MB
EOF
  exit 0
fi

[ -f "$DB" ] || { log "SKIP: no db at $DB"; exit 0; }

# Refuse to run against a live daemon — that's the exact contention this
# script exists to avoid. The in-daemon failsafe covers that case. Still
# assert the budget: failsafe backups pile up raw on exactly this path.
if [ "$PRUNE_ONLY" != true ] && sd_is_running signaldeckd; then
  log "SKIP: signaldeckd is running (in-daemon worker owns backups while live)"
  assert_budget || exit 1
  exit 0
fi

mkdir -p "$DIR"
TS="$(date +%Y%m%d-%H%M%S)"
TARGET="$DIR/signaldeck-$TS.db"

if [ "$PRUNE_ONLY" != true ]; then
  rm -f "$TARGET"

  # caffeinate: don't let the Mac sleep mid-copy (post-close is exactly when it
  # wants to). VACUUM INTO gives a consistent compacted copy even against WAL.
  if sd_sqlite "$DB" "VACUUM INTO '$TARGET';" 2>>"$LOG"; then
    SIZE=$(du -m "$TARGET" | cut -f1)
    log "OK: $TARGET (${SIZE} MB)"
  else
    rm -f "$TARGET"
    log "FAIL: VACUUM INTO error (partial removed)"
    exit 1
  fi

  # A zero exit from VACUUM INTO is NOT evidence the copy has the data in it.
  # The 2026-08-01 backup exited clean, passed PRAGMA quick_check, cleared the
  # 1000-row bars floor with 3.36M rows -- and carried 14 prediction_ledger rows
  # against 261,164, with no anchors at all. Structure was perfect and the
  # accountability record was gone. So verify CONTENT here, before anything
  # downstream is allowed to call this a backup.
  #
  # Quarantine rather than delete: a backup that failed verification is the
  # evidence for why it failed, and it is the only artifact of that run.
  if [ -z "$PY" ]; then
    log "FAIL: no working python3 found — cannot verify $TARGET has its content; refusing to record an unverified backup"
    exit 1
  fi
  if ! "$PY" "$SD/tools/verify_backup.py" "$TARGET" --live "$DB" >>"$LOG" 2>&1; then
    QDIR="$SD/quarantine/backups-$(date +%Y%m%d)"
    mkdir -p "$QDIR"
    mv "$TARGET" "$QDIR/" 2>>"$LOG"
    log "FAIL: $TARGET failed content verification (see above) — quarantined in $QDIR, NOT recorded as a backup"
    exit 1
  fi

  # SHA-256 sidecar. verify_backup.py already proves the file is a coherent
  # SQLite database with plausible contents AT THIS MOMENT; the digest is what
  # proves the bytes have not changed SINCE — bit-rot at rest, a truncated copy
  # to another volume, an edit. It also lets a copy be validated wherever it
  # lands without opening it or having the live DB to compare against.
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "$TARGET")" && sha256sum "$(basename "$TARGET")" > "$(basename "$TARGET").sha256") \
      2>>"$LOG" || log "WARN: sha256 sidecar failed"
  else
    "$PY" - "$TARGET" >"$TARGET.sha256" 2>>"$LOG" <<'PY' || log "WARN: sha256 sidecar failed"
import hashlib, os, sys
p = sys.argv[1]
h = hashlib.sha256()
with open(p, "rb") as f:
    for chunk in iter(lambda: f.read(1 << 20), b""):
        h.update(chunk)
# sha256sum's own format, so `sha256sum -c` validates it unchanged.
print(f"{h.hexdigest()}  {os.path.basename(p)}")
PY
  fi
  [ -s "$TARGET.sha256" ] && log "sha256: $(cut -d' ' -f1 < "$TARGET.sha256")"

  # Record success in meta so the in-daemon failsafe worker's restart gate sees
  # it and skips at the next boot. Daemon is down, so writing directly is safe.
  NOW=$(date +%s)
  sd_sqlite "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_ts','$NOW'),('backup_last_file','$(basename "$TARGET")');" 2>>"$LOG" || log "WARN: meta update failed"

  # The daemon is down, so this is the one uncontended moment the WAL (256MB at
  # last audit — live readers pin it all session) can actually truncate to zero.
  sd_sqlite "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null 2>>"$LOG" || log "WARN: wal_checkpoint(TRUNCATE) failed"
fi

# Generation policy: the newest $KEEP_RAW stay plain .db (instant restore),
# older ones are compressed in place (zstd -3 cuts SQLite backups 60-70%;
# stock gzip when zstd isn't installed — the explicit path probes matter
# because launchd's PATH won't see a brew-installed zstd), and compressed
# generations are pruned beyond $KEEP_ZST. Only timestamped
# signaldeck-YYYYmmdd-HHMMSS names are managed — ad-hoc snapshots like
# signaldeck-premaint-*.db are left alone. restore-rehearsal.sh knows how to
# pick and decompress .zst/.gz backups.
ZSTD_BIN=""
for z in "$(command -v zstd 2>/dev/null || true)" /opt/homebrew/bin/zstd /usr/local/bin/zstd; do
  [ -n "$z" ] && [ -x "$z" ] && ZSTD_BIN="$z" && break
done
EXT="gz"; [ -n "$ZSTD_BIN" ] && EXT="zst"

compress_file() { # removes the source on success
  if [ -n "$ZSTD_BIN" ]; then
    sd_nosleep "$ZSTD_BIN" -3 -q --rm -f "$1" 2>>"$LOG"
  else
    sd_nosleep gzip -f "$1" 2>>"$LOG"
  fi
}

# (names sort chronologically; macOS head has no negative -n, so compute the
# excess count explicitly)
compress_and_prune() {
  local dir="$1" n
  n=$(ls "$dir"/signaldeck-[0-9]*.db 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_RAW" ]; then
    ls "$dir"/signaldeck-[0-9]*.db | sort | head -n $((n - KEEP_RAW)) | while read -r f; do
      if compress_file "$f"; then
        log "compressed ${f}.${EXT}"
      else
        rm -f "${f}.${EXT}"
        log "WARN: compress failed on $f (kept plain)"
      fi
    done
  fi
  # One list across .gz and .zst so a mixed history (the gzip era before zstd
  # got installed) prunes as a single chronological sequence — but ONLY those
  # two extensions. The glob here was signaldeck-[0-9]*.db.*, which also matched
  # the .sha256 digest written beside each backup, so digests occupied KEEP_ZST
  # slots: with 5 names matching and 2 of them digests, the retained depth was
  # 3 real backups, not 5. Depth is the whole point of the setting, and it was
  # silently 40% short.
  #
  # A digest is not a generation, so it is not counted — and when its backup is
  # pruned it goes with it, rather than lingering to describe a file that is
  # gone.
  compressed_list() { ls "$dir"/signaldeck-[0-9]*.db.gz "$dir"/signaldeck-[0-9]*.db.zst 2>/dev/null | sort; }
  n=$(compressed_list | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_ZST" ]; then
    compressed_list | head -n $((n - KEEP_ZST)) | while read -r f; do
      rm -f "$f" && log "pruned $f"
      # ...db.gz -> ...db.sha256, the digest recorded for the pre-compression
      # file. Guarded by -e so the log never claims a removal that did not
      # happen: `rm -f` succeeds on a missing path.
      d="${f%.*}.sha256"
      [ -e "$d" ] && rm -f "$d" && log "pruned $d (digest of a pruned backup)"
    done
  fi
  # A digest whose backup never existed or was removed by hand is not evidence
  # of anything; sweep the orphans so they cannot be mistaken for one.
  for s in "$dir"/signaldeck-[0-9]*.db.sha256; do
    [ -e "$s" ] || continue          # the glob itself when nothing matches
    b="${s%.sha256}"                 # ...db.sha256 -> ...db
    [ -e "$b" ] || [ -e "$b.gz" ] || [ -e "$b.zst" ] || {
      rm -f "$s" && log "pruned orphan digest $s"
    }
  done
  n=$(ls "$dir"/signaldeck-[0-9]*.db 2>/dev/null | wc -l | tr -d ' ')
  if [ "$n" -gt "$KEEP_RAW_MAX" ]; then
    ls "$dir"/signaldeck-[0-9]*.db | sort | head -n $((n - KEEP_RAW_MAX)) | while read -r f; do
      rm -f "$f" && log "pruned $f (compression backstop)"
    done
  fi
}
compress_and_prune "$DIR"

if [ "$PRUNE_ONLY" = true ]; then
  log "prune-only pass done"
  assert_budget || exit 1
  exit 0
fi

# Offsite copy (best-effort, atomic tmp+rename).
#
# `caffeinate` is macOS-only; on any other platform it is not on PATH and the
# whole copy silently failed as "command not found". Use it only where it
# exists, so the copy itself is portable.
NOSLEEP=""
command -v caffeinate >/dev/null 2>&1 && NOSLEEP="caffeinate -i"

# s3_upload_verified SRC S3URI TMPGZ — compress, upload, and PROVE it arrived.
# Returns 0 only when S3 itself reports the object at exactly the byte count we
# uploaded. Sets S3_VERIFIED_BYTES on success and S3_FAIL_REASON on failure.
#
# A function, not an inline block, so ops/test-offsite-s3.sh can drive it with a
# stubbed `aws` and assert BOTH directions. An upload path that has never been
# watched failing is not a backup, it is a hope: this file already carries two
# scars from trusting a success signal instead of the artifact — a cp that
# reported success while writing a short file, and a same-disk copy that logged
# "offsite OK" nightly for twelve days.
s3_upload_verified() {
  local src="$1" uri="$2" tmp="$3" gz_bytes remote bucket key
  S3_VERIFIED_BYTES=0
  S3_FAIL_REASON=""
  # AWS_PAGER='' or the CLI can block an unattended run waiting on a pager.
  export AWS_PAGER=''

  if ! sd_nosleep gzip -c "$src" > "$tmp" 2>>"$LOG"; then
    rm -f "$tmp"
    S3_FAIL_REASON="could not compress the backup for upload"
    return 1
  fi
  gz_bytes="$(file_size "$tmp")"
  if [ "$gz_bytes" = "0" ]; then
    rm -f "$tmp"
    S3_FAIL_REASON="compressed to 0 bytes"
    return 1
  fi

  if ! sd_nosleep aws s3 cp "$tmp" "$uri" --only-show-errors >>"$LOG" 2>&1; then
    rm -f "$tmp"
    S3_FAIL_REASON="aws s3 cp failed"
    return 1
  fi
  rm -f "$tmp"

  # THE VERIFICATION. `aws s3 cp` exiting 0 says the CLI finished, not that the
  # object is intact and complete at the far end. --output text keeps this free
  # of a jq dependency.
  bucket="$(printf '%s' "$uri" | sed -e 's|^[sS]3://||' -e 's|/.*$||')"
  key="$(printf '%s' "$uri" | sed -e 's|^[sS]3://[^/]*/||')"
  remote="$(aws s3api head-object --bucket "$bucket" --key "$key" \
              --query ContentLength --output text 2>>"$LOG")"
  # A missing object, an error string, or None must never compare equal to a
  # byte count. Anything non-numeric collapses to 0 and fails the test below.
  case "$remote" in ''|*[!0-9]*) remote=0 ;; esac
  if [ "$remote" != "$gz_bytes" ]; then
    S3_FAIL_REASON="uploaded $gz_bytes bytes but S3 reports $remote"
    return 1
  fi
  S3_VERIFIED_BYTES="$remote"
  return 0
}

# ── S3: the only destination on this machine that is genuinely off-machine ──
#
# There is ONE volume here (C:), so every local directory fails same_volume and
# is correctly refused above. OneDrive is not an exception: measured, it
# resolves to /c exactly like the database.
#
# Uploads the GZIPPED copy, not $TARGET. The raw VACUUM INTO output is 5.3 GB
# and compresses to ~925 MB, and this runs nightly.
#
# VERIFICATION IS THE POINT, and `aws s3 cp` exiting 0 is not it. The object is
# read BACK with head-object and its ContentLength compared to the bytes we
# actually uploaded. This file already carries two scars from trusting a
# success signal instead of the artifact — a cp that reported success while
# writing a short file, and a same-disk copy that logged "offsite OK" every
# night for twelve days — and an upload that half-lands is the same defect with
# a network in the middle. backup_last_offsite is written ONLY after the remote
# object is confirmed present and exactly the right size.
if [ -n "${SIGNALDECK_OFFSITE_S3:-}" ]; then
  S3_DEST="${SIGNALDECK_OFFSITE_S3%/}"
  case "$(printf '%s' "$S3_DEST" | tr '[:upper:]' '[:lower:]')" in
    s3://*) ;;
    *)
      log "WARN: SIGNALDECK_OFFSITE_S3='$S3_DEST' is not an s3:// URI — refusing to treat it as an offsite destination"
      S3_DEST=""
      ;;
  esac
  if [ -z "$S3_DEST" ]; then
    :
  elif ! command -v aws >/dev/null 2>&1; then
    log "WARN: SIGNALDECK_OFFSITE_S3 is set but the aws CLI is not on PATH — no off-machine copy was made"
    sd_sqlite "$DB" "INSERT INTO dq_events(ts,kind,detail) VALUES($(date +%s),'backup_offsite_s3_unavailable','SIGNALDECK_OFFSITE_S3 is configured but the aws CLI is missing; no off-machine copy exists');" 2>>"$LOG"
  else
    S3_KEY="$S3_DEST/$(basename "$TARGET").gz"
    if s3_upload_verified "$TARGET" "$S3_KEY" "$DIR/.s3-$TS.db.gz"; then
      sd_sqlite "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_offsite','$(date +%s)');" 2>>"$LOG"
      sd_sqlite "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_offsite_dir','$S3_DEST');" 2>>"$LOG"
      log "offsite OK: $S3_KEY ($S3_VERIFIED_BYTES bytes verified by head-object)"
    else
      log "WARN: off-machine upload to $S3_KEY did not verify ($S3_FAIL_REASON) — backup_last_offsite NOT updated"
      sd_sqlite "$DB" "INSERT INTO dq_events(ts,kind,detail) VALUES($(date +%s),'backup_offsite_s3_failed','$S3_KEY: $S3_FAIL_REASON; no trustworthy off-machine copy for this run');" 2>>"$LOG"
    fi
  fi
elif [ -z "$OFFSITE" ]; then
  log "offsite SKIPPED: no destination configured (set SIGNALDECK_OFFSITE_DIR to an external volume, or SIGNALDECK_OFFSITE_S3 to an s3:// URI)"
elif mkdir -p "$OFFSITE" 2>/dev/null; then
  if sd_nosleep cp "$TARGET" "$OFFSITE/.tmp-$TS" 2>>"$LOG" && mv "$OFFSITE/.tmp-$TS" "$OFFSITE/$(basename "$TARGET")"; then
    COPY="$OFFSITE/$(basename "$TARGET")"
    SRC_BYTES="$(file_size "$TARGET")"
    DST_BYTES="$(file_size "$COPY")"
    if [ "$SRC_BYTES" = "0" ] || [ "$SRC_BYTES" != "$DST_BYTES" ]; then
      # cp reporting success is not the same as the bytes arriving. A
      # destination that fills mid-write, or a sync client that truncates,
      # leaves a short file behind a zero exit status.
      log "WARN: offsite copy is SHORT ($DST_BYTES of $SRC_BYTES bytes) — not recording an offsite backup"
      rm -f "$COPY"
    elif same_volume "$DB" "$OFFSITE"; then
      # THE COPY LANDED, BUT IT IS NOT OFFSITE, AND SAYING SO IS THE WHOLE JOB.
      #
      # Writing backup_last_offsite here is what made a same-disk copy read as
      # disaster recovery. The Go side already refuses to call this configured
      # (api.go: offsiteConfigured requires volume separation), so recording a
      # fresh timestamp produced a contradictory pair — offsiteConfigured:false
      # beside a lastOffsiteTs from minutes ago — and the timestamp is the more
      # persuasive of the two.
      #
      # Measured 2026-08-11 on this machine: OFFSITE resolved to
      # $OneDrive/SignalDeckBackups, NO OneDrive account is signed in (every
      # account's UserFolder/cid/UserEmail is empty), the folder is an ordinary
      # directory with no sync-placeholder attributes, exactly 3 files exist
      # under the whole OneDrive tree, and it sits on C: — the SAME physical
      # disk (one drive, PHYSICALDRIVE0) as the database. Nothing was uploading
      # anywhere, and the log said "offsite OK" every night.
      #
      # That is verbatim the defect this file's own header says the OneDrive
      # default was added to fix ("the old default copied 2.7GB onto C: and
      # looked like it worked"), reintroduced by the replacement.
      #
      # The copy is KEPT — a second copy still survives an accidental delete —
      # but it is recorded as what it is.
      log "offsite NOT OFFSITE: $OFFSITE is on the same volume as the database — kept as a LOCAL second copy; backup_last_offsite NOT updated. Point SIGNALDECK_OFFSITE_DIR at an external volume for real DR."
      sd_sqlite "$DB" "INSERT INTO dq_events(ts,kind,detail) VALUES($(date +%s),'backup_offsite_same_volume','offsite destination $OFFSITE shares a volume with the database; no off-machine copy exists');" 2>>"$LOG"
      compress_and_prune "$OFFSITE"
    else
      sd_sqlite "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('backup_last_offsite','$(date +%s)');" 2>>"$LOG"
      log "offsite OK ($DST_BYTES bytes verified)"
      compress_and_prune "$OFFSITE"
    fi
  else
    rm -f "$OFFSITE/.tmp-$TS"
    log "WARN: offsite copy failed (local backup kept)"
  fi
else
  log "WARN: offsite dir unavailable"
fi

# ── Offsite staleness (2026-08-23) ─────────────────────────────────────────
# verify_backup.py only validates backup contents, never age.
# The meta key backup_last_offsite went 12 days stale with no alert.
# A check that cannot tell "quiet because healthy" from "quiet because nobody looked"
# is not a real check, so we add this age-based guard.
OFFSITE_STALE_SECS=$((3 * 24 * 3600))
OFFSITE_BANNER_TS="$SD/data/.offsite-stale-banner-ts"

# Read last offsite timestamp from meta table
last_offsite=$(sd_sqlite "$DB" "SELECT v FROM meta WHERE k='backup_last_offsite';" 2>>"$LOG")
# Treat missing or non-numeric as zero
if [[ ! "$last_offsite" =~ ^[0-9]+$ ]]; then
    last_offsite=0
fi

now=$(date +%s)
age=$(( now - last_offsite ))

if (( last_offsite == 0 )); then
    log "NO OFF-MACHINE BACKUP HAS EVER BEEN RECORDED; the database exists on exactly one volume; set SIGNALDECK_OFFSITE_DIR to an external drive."
    stale=true
elif (( age >= OFFSITE_STALE_SECS )); then
    days=$(( age / 86400 ))
    threshold_days=$(( OFFSITE_STALE_SECS / 86400 ))
    log "Last off-machine backup is $days days old (>= $threshold_days days threshold)."
    stale=true
else
    days=$(( age / 86400 ))
    log "Off-machine backup is current (age $days days)."
    stale=false
fi

if [ "$stale" = true ]; then
    # Read cooldown timestamp, default 0 if missing/invalid
    prev=$(cat "$OFFSITE_BANNER_TS" 2>/dev/null || echo 0)
    if ! [[ "$prev" =~ ^[0-9]+$ ]]; then
        prev=0
    fi
    if (( now - prev >= 86400 )); then
        sd_notify "SignalDeck: No current off-machine backup" "Losing this disk would lose the database because there is no verified off-machine copy."
        printf '%s\n' "$now" >"$OFFSITE_BANNER_TS"
    fi
fi

# ── H9 (hostile review, 2026-07-26): nothing pages a human ─────────────────
# Unrelated to backups — piggybacked here on purpose. This is the one script
# in the fleet guaranteed to run once a day (market-close.sh -> this script),
# so it needs no new launchd job, and this remediation pass's ops/*.sh budget
# was one new FILE (ops/restore-rehearsal.sh, for H8) — a second standalone
# script for this would have needed its own scheduling to ever actually run.
#
# THE PROBLEM: daemon/cmd/signaldeckd/run.go logs "remote notify: no
# transports configured" exactly ONCE per daemon start (54 occurrences seen
# live) — a line in a log nobody tails routinely. Until
# SIGNALDECK_DISCORD_WEBHOOK / SIGNALDECK_TELEGRAM_BOT_TOKEN+CHAT_ID /
# SIGNALDECK_WEBHOOK_URL is set in daemon/.env, EVERY daemon alert degrades to
# a macOS `display notification` banner that reaches nobody with the lid
# closed — and that silence is itself invisible: nothing distinguishes "quiet
# because nothing is wrong" from "quiet because nobody could see it."
#
# This sets NO credential (deliberately out of scope). It only makes the
# absence loud, once a day, in the same channel every other ops script here
# already uses to reach a human.
NOTIFY_ENV="$SD/daemon/.env"
NOTIFY_LOG="$SD/logs/notify-silence.log"
NOTIFY_COOLDOWN_FILE="$SD/data/.notify-silence-banner-ts"
NOTIFY_COOLDOWN_SECS=$((24 * 3600))

notify_log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" >> "$NOTIFY_LOG"; }

# has_nonempty KEY — true if KEY=<something non-empty> appears in
# daemon/.env. Never echoes the value, only presence/absence — the same
# redaction discipline internal/notify.Redact applies to error strings.
has_nonempty() {
  [ -f "$NOTIFY_ENV" ] && grep -qE "^${1}=.+" "$NOTIFY_ENV"
}

notify_configured=false
has_nonempty SIGNALDECK_DISCORD_WEBHOOK && notify_configured=true
if has_nonempty SIGNALDECK_TELEGRAM_BOT_TOKEN && has_nonempty SIGNALDECK_TELEGRAM_CHAT_ID; then
  notify_configured=true
fi
has_nonempty SIGNALDECK_WEBHOOK_URL && notify_configured=true

if [ "$notify_configured" = true ]; then
  notify_log "remote notify configured — daemon alerts are pageable beyond this Mac"
else
  notify_log "SILENT-FAILURE MODE — no remote transport configured (Discord/Telegram/webhook all unset). Every daemon alert is macOS-only and goes unseen with the lid closed or nobody at the keyboard. Set SIGNALDECK_DISCORD_WEBHOOK, SIGNALDECK_TELEGRAM_BOT_TOKEN+SIGNALDECK_TELEGRAM_CHAT_ID, or SIGNALDECK_WEBHOOK_URL in $NOTIFY_ENV and restart the daemon."
  # Rate-limited: this banner is warning about missed alerts, so it must not
  # itself become one more notification the operator learns to swipe away.
  now=$(date +%s)
  last=0
  [ -f "$NOTIFY_COOLDOWN_FILE" ] && last=$(cat "$NOTIFY_COOLDOWN_FILE" 2>/dev/null || echo 0)
  if [ $((now - last)) -ge $NOTIFY_COOLDOWN_SECS ]; then
    sd_notify "SignalDeck: alerts are SILENT beyond this machine" \
      "No Discord/Telegram/webhook transport configured — daemon alerts stay local and go unseen when nobody is at the keyboard."
    echo "$now" > "$NOTIFY_COOLDOWN_FILE"
  fi
fi

# ── Hard size assertion (2026-07-26): the 13GB pile-up went unnoticed because
# nothing measured the directory. This is the script's exit status: over
# budget means retention regressed — page and fail instead of refilling disk.
assert_budget || exit 1
exit 0
