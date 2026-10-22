#!/bin/bash
# Does the backup script still know an offsite copy from a same-disk one?
#
# WHY THIS EXISTS. On 2026-08-11 logs/backup-offline.log had said "offsite OK"
# every night while the destination was $OneDrive/SignalDeckBackups on C: — the
# SAME physical disk as the database. Measured that day: no OneDrive account was
# signed in (every account's UserFolder/cid/UserEmail empty), the folder had no
# sync-placeholder attributes, exactly 3 files existed under the whole OneDrive
# tree, and the machine has one drive. Nothing was uploading anywhere. The
# script recorded backup_last_offsite regardless, so a reader saw a fresh
# "last offsite copy" timestamp for a copy that would die with the disk.
#
# The Go side already refused to call that configured (api.go: offsiteConfigured
# requires volume separation), which made the pair CONTRADICTORY — and a fresh
# timestamp is the more persuasive half.
#
# The functions are extracted from the real script rather than copied, so this
# test cannot drift from the implementation it is checking.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$SD/ops/signaldeck-backup-offline.sh"

eval "$(sed -n '/^volume_of()/,/^}/p; /^same_volume()/,/^}/p; /^file_size()/,/^}/p' "$SRC")"

fails=0
check() {
  if [ "$2" = "1" ]; then echo "  ok   $1"; else echo "  FAIL $1"; fails=$((fails + 1)); fi
}

# The functions must actually have been extracted — a silent no-op here would
# make every assertion below vacuously pass.
check "volume_of was extracted from the real script" \
  "$(type volume_of >/dev/null 2>&1 && echo 1 || echo 0)"
check "same_volume was extracted from the real script" \
  "$(type same_volume >/dev/null 2>&1 && echo 1 || echo 0)"
check "file_size was extracted from the real script" \
  "$(type file_size >/dev/null 2>&1 && echo 1 || echo 0)"

# A path is always on its own volume.
check "same_volume is true for a path against itself" \
  "$(same_volume "$SD" "$SD" && echo 1 || echo 0)"

# UNKNOWN COUNTS AS DIFFERENT. A destination whose volume cannot be determined
# must not be downgraded to "local copy" — the caller keeps the copy either way,
# and mislabelling a genuinely external disk is the costlier error here.
check "same_volume is false when a volume cannot be determined" \
  "$(same_volume "$SD" "/nonexistent-volume-xyz" && echo 0 || echo 1)"

# file_size validates the VALUE, not the exit status: GNU stat -f prints a
# filesystem report and still exits 0 (the ops/restore-rehearsal.sh lesson).
check "file_size returns a plausible size for a real file" \
  "$([ "$(file_size "$SRC")" -gt 100 ] 2>/dev/null && echo 1 || echo 0)"
check "file_size returns 0 for a missing file" \
  "$([ "$(file_size "/nope/missing.db")" = "0" ] && echo 1 || echo 0)"
check "file_size never returns non-numeric text" \
  "$(case "$(file_size "$SD")" in ''|*[!0-9]*) echo 0 ;; *) echo 1 ;; esac)"

# THE LIVE ONE. Whatever this machine's destination resolves to today, the
# script's claim must match the volumes. This is the assertion that would have
# caught the original defect on the day it shipped.
DB="$SD/data/signaldeck.db"
if [ -n "${SIGNALDECK_OFFSITE_DIR:-}" ]; then
  OFFSITE="$SIGNALDECK_OFFSITE_DIR"
elif [ "$(uname -s)" = "Darwin" ]; then
  OFFSITE="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
elif [ -n "${OneDrive:-}" ] && [ -d "$OneDrive" ]; then
  OFFSITE="$OneDrive/SignalDeckBackups"
else
  OFFSITE=""
fi
if [ -n "$OFFSITE" ] && [ -e "$DB" ] && [ -d "$OFFSITE" ]; then
  if same_volume "$DB" "$OFFSITE"; then
    echo "  note this machine's offsite dir is on the SAME volume as the DB:"
    echo "         db      $(volume_of "$DB")   $DB"
    echo "         offsite $(volume_of "$OFFSITE")   $OFFSITE"
    echo "       -> the script must record it as a LOCAL second copy and must NOT"
    echo "          update backup_last_offsite. Set SIGNALDECK_OFFSITE_DIR to an"
    echo "          external volume for real disaster recovery."
  else
    echo "  note offsite dir is on a separate volume ($(volume_of "$OFFSITE")) — genuine DR"
  fi
fi

# The script must not have regained an unconditional offsite claim: every meta
# write has to sit behind a guard that proves the copy left this machine: the
# same-volume branch for a directory target, or a VERIFIED upload for the S3
# target added 2026-08-25 (which this check flagged as unguarded for a week,
# because comments and the S3 branch precede the elif in the file). Comment
# lines are skipped; a write before either guard still trips it (mutation-checked).
GUARDED="$(awk '/^ *#/{next} /if s3_upload_verified /{g=1} /^    elif same_volume /{f=1} /backup_last_offsite/{if(!f && !g) print "UNGUARDED"}' "$SRC")"
check "backup_last_offsite is only written behind a verified S3 upload or the same_volume check" \
  "$([ -z "$GUARDED" ] && echo 1 || echo 0)"

# A configured backup key refuses the PLAINTEXT directory copy (2026-10-22).
# The real offsite if/elif chain runs with S3 and GitHub unset and its side
# effects stubbed, once with a key and once without (the copy must then land,
# or the keyed case would pass without the copy path ever running).
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
CHAIN="$(sed -n '/^if \[ -n "${SIGNALDECK_OFFSITE_S3:-}" \]; then$/,/^fi$/p' "$SRC")"
echo data > "$W/signaldeck-1.db"
dir_copy() { # $1 = key file or ""; $2 = run name. Prints what landed.
  ( unset SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_GH_REPO SIGNALDECK_OFFSITE_KEY_FILE
    export USERPROFILE="$W/home" HOME="$W/home"; [ -n "$1" ] && export SIGNALDECK_OFFSITE_KEY_FILE="$1"
    . "$SD/ops/lib-offsite-gh.sh"
    log() { echo "$*" >> "$LOG"; }; sd_sqlite() { :; }; sd_nosleep() { "$@"; }; compress_and_prune() { :; }
    DB="$W/sd.db" TARGET="$W/signaldeck-1.db" OFFSITE="$W/off-$2" TS=1 LOG="$W/log-$2"
    eval "$CHAIN"; ls "$OFFSITE" 2>/dev/null )
}
check "the offsite chain was extracted from the real script" \
  "$(printf '%s' "$CHAIN" | grep -q 'if s3_upload_verified ' && printf '%s' "$CHAIN" | grep -q 'mkdir -p "$OFFSITE"' && echo 1 || echo 0)"
check "with no key the plaintext directory copy still lands" \
  "$([ "$(dir_copy "" nokey)" = "signaldeck-1.db" ] && echo 1 || echo 0)"
check "with a key the plaintext directory copy is refused" \
  "$([ -z "$(dir_copy "$W/backup.key" key)" ] && echo 1 || echo 0)"
check "the refusal is logged as a WARNING" \
  "$(grep -q '^WARNING: offsite dir copy skipped: key configured and directory copies are not sealed' "$W/log-key" 2>/dev/null && echo 1 || echo 0)"

if [ "$fails" -gt 0 ]; then echo "$fails check(s) failed"; exit 1; fi
echo "all offsite-honesty checks passed"
