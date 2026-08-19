#!/bin/bash
# Restore rehearsal — H8 (hostile review, 2026-07-26): prove a backup can
# actually be RESTORED, not just that VACUUM INTO or a file copy returned no
# error. Before this script existed, nothing in the fleet ever restored a
# backup anywhere — "success" was daemon/internal/backup's
# os.Stat(target)==nil, which a corrupt-but-nonzero-sized file always
# satisfies, and KEEP=7 rotation would then age the last good generation out
# from under a silently-broken one.
#
# What backup.go's own PRAGMA quick_check (added alongside this script) does
# NOT cover: it verifies the copy the moment it's written, on the SAME disk,
# in the SAME process. It cannot catch bit rot after the fact, an offsite
# sync that corrupts bytes days later, or a restore procedure that nobody
# has actually rehearsed and turns out to be wrong when it's needed for
# real. This script is the end-to-end drill: take the newest backup — same
# one an operator would reach for during an actual incident — restore it to
# an ISOLATED temp path (never touches anything live), open it, run a full
# PRAGMA integrity_check, and check a sane row count on `bars` (the core
# price table; an openable-but-empty copy is not a usable backup).
#
# H8 DEEPENING (2026-07-26): mechanics are not enough — a restore that
# silently dropped or truncated prediction_ledger/ledger_anchors sails
# through integrity_check and the bars floor while destroying exactly the
# accountability record an operator would be trying to prove intact after an
# incident. So the drill also asserts the DOMAIN invariants on the restored
# copy: (2) prediction_ledger is at least as long as the copy's own newest
# signed anchor committed to, and (3) the ledger hash chain recomputes and
# every anchor signature re-verifies — via the daemon's own
# `sdmaint ledger-verify`, because the sqlite3 CLI cannot check Ed25519.
#
# Exit 0 = the newest backup is provably restorable right now.
# Exit 1 = it is not — treat this as a page, not a log line to scroll past.
#
# WIRING: this script does not self-schedule, but it IS scheduled —
# ops/com.signaldeck.restore.plist runs it weekly (Sunday 07:00, pre-market)
# through ops/restore-rehearsal-notify.sh, which pages on non-zero exit via
# the daemon/.env notify transports (same env vars internal/notify reads).
# Weekly is the right cadence: a full integrity_check + ledger-chain
# recomputation over a 2GB+ file is not free, and this is meant to catch
# slow rot, not every single night's backup individually — that's
# backup.go's job.

set -uo pipefail

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_DIR="$SD/data/backups"

# OFFSITE_DIR must resolve the SAME way ops/signaldeck-backup-offline.sh does,
# or the rehearsal reads a different location than the backup wrote. That script
# honours SIGNALDECK_OFFSITE_DIR, then $OneDrive/SignalDeckBackups on Windows;
# this one hardcoded the macOS iCloud path with no override, so after the
# Windows move the fallback pointed at a directory that does not exist — and if
# data/backups were ever empty the rehearsal would die at "no backup found"
# instead of falling back to the copy it had just written.
if [ -n "${SIGNALDECK_OFFSITE_DIR:-}" ]; then
  OFFSITE_DIR="$SIGNALDECK_OFFSITE_DIR"
elif [ -n "${OneDrive:-}" ] && [ -d "$OneDrive" ]; then
  OFFSITE_DIR="$OneDrive/SignalDeckBackups"
else
  OFFSITE_DIR="$HOME/Library/Mobile Documents/com~apple~CloudDocs/SignalDeckBackups"
fi

# file_mtime FILE — modification time as a unix epoch, portably.
#
# `stat -f %m` is a BSD-ism. Under the Git Bash these tasks now run, stat is GNU
# and -f means --file-system: measured 2026-08-11 it printed a multi-line
# filesystem report ("ID: 9a1a680f... Namelen: 255 ...") AND EXITED 0, so the
# old `|| echo 0` fallback never fired and that text was assigned to
# BACKUP_MTIME. The integer compare on the next line then errored with
# "integer expected" and evaluated FALSE, making the transitional branch
# unreachable on Windows — a legitimately pre-anchor backup pages an operator
# instead of warning. Because the failing command exits 0, only validating the
# VALUE catches it; `||` cannot.
file_mtime() {
  local f="$1" v
  v="$(stat -c %Y "$f" 2>/dev/null)"        # GNU (Linux, Git Bash)
  case "$v" in ''|*[!0-9]*) v="" ;; esac
  if [ -z "$v" ]; then
    v="$(stat -f %m "$f" 2>/dev/null)"      # BSD (macOS)
    case "$v" in ''|*[!0-9]*) v="" ;; esac
  fi
  printf '%s' "${v:-0}"
}
LOG="$SD/logs/restore-rehearsal.log"

# A real production backup has millions of `bars` rows. Four digits is
# comfortably below any normal size and comfortably above "empty or
# truncated copy" — it separates the failure mode this script exists to
# catch from ordinary day-to-day size variance, without hard-coding a
# brittle exact count.
MIN_BARS_ROWS=1000

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S') $*" | tee -a "$LOG"; }

pick_newest() {
  # -t sorts newest-first; names are also timestamp-sortable, but mtime is
  # the honest "what would an operator actually grab right now" signal.
  # signaldeck-backup-offline.sh compresses generations beyond the newest
  # (KEEP_RAW), so a rehearsal must be able to reach for .zst/.gz exactly
  # like an operator would during a real incident.
  ls -t "$1"/signaldeck-*.db "$1"/signaldeck-*.db.zst "$1"/signaldeck-*.db.gz 2>/dev/null | head -n1
}

SRC="$(pick_newest "$LOCAL_DIR")"
SRC_LABEL="local"
if [ -z "$SRC" ]; then
  SRC="$(pick_newest "$OFFSITE_DIR")"
  SRC_LABEL="offsite"
fi
if [ -z "$SRC" ]; then
  log "FAIL: no backup found in '$LOCAL_DIR' or '$OFFSITE_DIR' — nothing to rehearse a restore from"
  exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  log "FAIL: sqlite3 CLI not found — cannot verify the restored copy"
  exit 1
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/signaldeck-restore-XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
TARGET="$TMP/$(basename "$SRC")"
TARGET="${TARGET%.zst}"; TARGET="${TARGET%.gz}"

# The rehearsal restores the compressed generations the same way an operator
# would: decompress to the isolated temp path, then verify. Explicit zstd
# path probes because launchd's PATH won't see a brew-installed zstd.
case "$SRC" in
  *.zst)
    ZSTD_BIN=""
    for z in "$(command -v zstd 2>/dev/null || true)" /opt/homebrew/bin/zstd /usr/local/bin/zstd; do
      [ -n "$z" ] && [ -x "$z" ] && ZSTD_BIN="$z" && break
    done
    if [ -z "$ZSTD_BIN" ]; then
      log "FAIL: newest backup $(basename "$SRC") is zstd-compressed but no zstd binary is installed — it cannot be restored on this machine as-is"
      exit 1
    fi
    log "restore rehearsal: decompressing $SRC_LABEL backup $(basename "$SRC") to isolated temp path $TARGET"
    if ! "$ZSTD_BIN" -d -q -f -o "$TARGET" "$SRC"; then
      log "FAIL: zstd decompression of $SRC failed"
      exit 1
    fi
    ;;
  *.gz)
    log "restore rehearsal: decompressing $SRC_LABEL backup $(basename "$SRC") to isolated temp path $TARGET"
    if ! gzip -dc "$SRC" > "$TARGET"; then
      log "FAIL: gzip decompression of $SRC failed"
      exit 1
    fi
    ;;
  *)
    log "restore rehearsal: restoring $SRC_LABEL backup $(basename "$SRC") to isolated temp path $TARGET"
    if ! cp "$SRC" "$TARGET"; then
      log "FAIL: restore copy from $SRC to $TARGET failed"
      exit 1
    fi
    ;;
esac

# (1) Full integrity_check, not quick_check: quick_check is what backup.go
# already runs at write time; the weekly drill can afford the deeper pass
# that also validates index consistency against table content.
RESULT="$(sqlite3 "$TARGET" "PRAGMA integrity_check;" 2>&1)"
if [ "$RESULT" != "ok" ]; then
  log "FAIL: PRAGMA integrity_check on the restored copy did not report ok — $RESULT"
  exit 1
fi

ROWS="$(sqlite3 "$TARGET" "SELECT COUNT(*) FROM bars;" 2>&1)"
if ! [[ "$ROWS" =~ ^[0-9]+$ ]]; then
  log "FAIL: could not read a bars row count from the restored copy — $ROWS"
  exit 1
fi
if [ "$ROWS" -lt "$MIN_BARS_ROWS" ]; then
  log "FAIL: restored copy has only $ROWS bars rows (floor is $MIN_BARS_ROWS) — treat as an empty/truncated backup, not a usable one"
  exit 1
fi

# (2) The prediction ledger must be at least as long as the copy's OWN newest
# signed anchor committed to. The anchor's ledger_count rides inside the same
# backup, so this needs no live state: a restore whose ledger is shorter than
# what its own anchor signed has lost anchored history, full stop.
ANCHORED_COUNT="$(sqlite3 "$TARGET" "SELECT ledger_count FROM ledger_anchors ORDER BY seq DESC LIMIT 1;" 2>&1)"
ANCHOR_FLAGS=""
if [[ "$ANCHORED_COUNT" =~ ^[0-9]+$ ]]; then
  LEDGER_ROWS="$(sqlite3 "$TARGET" "SELECT COUNT(*) FROM prediction_ledger;" 2>&1)"
  if ! [[ "$LEDGER_ROWS" =~ ^[0-9]+$ ]]; then
    log "FAIL: could not read a prediction_ledger row count from the restored copy — $LEDGER_ROWS"
    exit 1
  fi
  if [ "$LEDGER_ROWS" -lt "$ANCHORED_COUNT" ]; then
    log "FAIL: restored prediction_ledger has $LEDGER_ROWS rows but its own newest signed anchor committed to $ANCHORED_COUNT — anchored ledger history is missing from this backup"
    exit 1
  fi
else
  # No anchor rows (or no ledger_anchors table) in the restore. Benign ONLY
  # while the backup predates the first anchor ever signed (anchoring shipped
  # 2026-07-26); once anchors exist at backup time, their absence from a
  # restore is a page. Compare against the LIVE DB's oldest anchor to tell
  # the two apart — this branch self-retires as post-anchor backups rotate in.
  OLDEST_LIVE_ANCHOR="$(sqlite3 -readonly "$SD/data/signaldeck.db" "SELECT MIN(created_at) FROM ledger_anchors;" 2>/dev/null)"
  BACKUP_MTIME="$(file_mtime "$SRC")"
  if [[ "$OLDEST_LIVE_ANCHOR" =~ ^[0-9]+$ ]] && [ "$OLDEST_LIVE_ANCHOR" -gt "$BACKUP_MTIME" ]; then
    log "WARN: restored copy has no ledger anchors, but the live DB's oldest anchor (ts $OLDEST_LIVE_ANCHOR) postdates this backup (mtime $BACKUP_MTIME) — transitional, will page once a post-anchor backup is the newest"
    ANCHOR_FLAGS="-allow-no-anchors"
  else
    log "FAIL: restored copy has no ledger anchors while the live DB has anchored history — the anchor evidence did not survive backup/restore (sqlite3 said: $ANCHORED_COUNT)"
    exit 1
  fi
fi

# (3) The cryptographic re-verification the sqlite3 CLI cannot do: recompute
# the ledger hash chain from row payloads and re-verify every Ed25519 anchor
# signature against the restored copy, through the daemon's own store code
# (sdmaint ledger-verify). This is also the "can the daemon's store actually
# open this restore" drill — schema + migrations run against the temp copy.
# Windows needs the .exe suffix or the binary is neither -x nor runnable, and
# the checked-in bin/sdmaint is a macOS build — so on this box the drill failed
# at "missing and could not be rebuilt" and the backups went unverified. Same
# suffix idiom ops/signaldeck-ctl.sh already uses.
SDMAINT_EXE=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) SDMAINT_EXE=".exe" ;; esac
SDMAINT="$SD/bin/sdmaint$SDMAINT_EXE"
if [ ! -x "$SDMAINT" ]; then
  # launchd's PATH has no go, so the user-local SDK is the macOS fallback;
  # on Windows go IS on PATH and that hardcoded path does not exist, which is
  # why the rebuild never fired here.
  GO="$HOME/.local/go-sdk/go/bin/go"
  [ -x "$GO" ] || GO="$(command -v go || true)"
  if [ -n "$GO" ] && [ -x "$GO" ]; then
    (cd "$SD/daemon" && "$GO" build -o "$SDMAINT" ./cmd/sdmaint) >/dev/null 2>&1
  fi
fi
if [ ! -x "$SDMAINT" ]; then
  log "FAIL: $SDMAINT is missing and could not be rebuilt — cannot re-verify the ledger anchors on the restored copy"
  exit 1
fi
if ! LEDGER_OUT="$("$SDMAINT" ledger-verify -db "$TARGET" $ANCHOR_FLAGS 2>&1)"; then
  log "FAIL: sdmaint ledger-verify rejected the restored copy — $LEDGER_OUT"
  exit 1
fi
log "ledger-verify: $(echo "$LEDGER_OUT" | tr '\n' ' ')"

log "OK: $SRC_LABEL backup $(basename "$SRC") restores clean — integrity_check ok, $ROWS bars rows, ledger chain + anchors verified"
exit 0
