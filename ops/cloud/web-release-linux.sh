#!/bin/bash
# web-release-linux.sh - the Linux port of ops/web-release.ps1 (read that file
# for the measured history behind each step). Build a candidate beside the live
# build, prove it on spare ports, and only then swap it in, keeping the previous
# build for rollback.
#
#   sudo -u signaldeck bash ops/cloud/web-release-linux.sh --build-only   every gate, nothing replaced
#   sudo -u signaldeck bash ops/cloud/web-release-linux.sh                gates, then promote on :8323
#
# Steps (any failure exits 1 and leaves web/.next untouched until step 6):
#   1. web/ is clean in git (stricter than the .ps1, so the record names a real commit)
#   2. build into web/.next-release-<shortrev> (SIGNALDECK_DIST_DIR); tsconfig.json bytes restored
#   3. ASSET GATE: candidate on :8331, ops/cloud/web-assets-check.sh must exit 0
#   4. BROWSER GATE: web/e2e/release-smoke.spec.ts via playwright (needs :8329 free)
#   5. --build-only stops here
#   6. PROMOTE under ops/.maintenance: stop unit, .next -> .next-prev-<stamp>, candidate -> .next, start
#   7. :8323 must serve a COMPLETE build within 45 s, else print the rollback and exit 1
#   8. record logs/web-release.json; 9. prune .next-prev-* to the newest 2 (never the recorded rollback)
set -u
set -o pipefail
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-portable.sh
. "$SD/ops/lib-portable.sh"
WEB="$SD/web"
PORT=8323
STAGE_PORT=8331
CHECK="$SD/ops/cloud/web-assets-check.sh"
LOGDIR="$SD/logs"
LOG="$LOGDIR/web-release.log"
LOCK="$SD/ops/.maintenance"
BUILD_ONLY=0
case "${1:-}" in
  "") ;;
  --build-only) BUILD_ONLY=1 ;;
  *) echo "usage: $(basename "$0") [--build-only]" >&2; exit 2 ;;
esac
mkdir -p "$LOGDIR"
say() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*" | tee -a "$LOG"; }
fail() { say "RELEASE ABORTED: $*"; exit 1; }
listening() { ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }

[ "$(id -u)" -ne 0 ] || fail "run as the service user (sudo -u signaldeck ...), never root"
[ -f "$WEB/package.json" ] || fail "no web/package.json under $SD"
[ -f "$CHECK" ] || fail "the asset check is missing at $CHECK; the completeness gate cannot run"
NEXT_BIN="$WEB/node_modules/next/dist/bin/next"
[ -f "$NEXT_BIN" ] || fail "the Next CLI is not at $NEXT_BIN; run npm ci in web first"

# 1. the revision this release is FOR, and the tree it is built from
REV="$(git -C "$SD" rev-parse HEAD 2>/dev/null)" || fail "git cannot name HEAD"
SHORT="$(git -C "$SD" rev-parse --short HEAD)"
dirty="$(sd_dirty_excluding_generated "$SD" | awk '{p=substr($0,4)} p ~ /^web\//')"
[ -z "$dirty" ] || { printf '%s\n' "$dirty" | head -20; fail "web/ has uncommitted changes; the release record would name the wrong commit"; }
say "releasing $REV"

CAND_NAME=".next-release-$SHORT"
CAND="$WEB/$CAND_NAME"
TSCONFIG="$WEB/tsconfig.json"
TS_SNAP=""
STAGE_PID=""
TOOK_LOCK=0
cleanup() {
  [ -n "$STAGE_PID" ] && kill "$STAGE_PID" 2>/dev/null
  [ -n "$TS_SNAP" ] && [ -f "$TS_SNAP" ] && { cp -p "$TS_SNAP" "$TSCONFIG"; rm -f "$TS_SNAP"; }
  [ "$TOOK_LOCK" -eq 1 ] && rm -f "$LOCK"
}
trap cleanup EXIT

# 2. BUILD into the candidate; web/.next is untouched. `next build` rewrites
# tsconfig.json for the distDir it was given, so the bytes are put back.
if [ -e "$CAND" ]; then say "removing an earlier candidate at $CAND"; rm -rf "$CAND"; fi
export SIGNALDECK_DIST_DIR="$CAND_NAME"
if [ -f "$TSCONFIG" ]; then TS_SNAP="$(mktemp)"; cp -p "$TSCONFIG" "$TS_SNAP"; fi
say "building into $CAND_NAME"
(cd "$WEB" && npm run build) || fail "npm run build failed"
cp -p "$TS_SNAP" "$TSCONFIG" 2>/dev/null

# 3. ASSET GATE on a spare port
STAGE_LOG="$LOGDIR/web-release-stage.log"
listening "$STAGE_PORT" && fail "CHECK UNAVAILABLE: :$STAGE_PORT is already in use (an orphaned candidate?). This is NOT a statement about the candidate."
say "starting the candidate on $STAGE_PORT for the asset gate (its log: $STAGE_LOG)"
(cd "$WEB" && exec node "$NEXT_BIN" start -H 127.0.0.1 -p "$STAGE_PORT") > "$STAGE_LOG" 2>&1 &
STAGE_PID=$!
up=0
for _ in $(seq 1 45); do
  sleep 2
  code="$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -m 5 "http://127.0.0.1:$STAGE_PORT/login" 2>/dev/null)"
  [ "${code:-0}" -ge 200 ] 2>/dev/null && [ "$code" -lt 400 ] && { up=1; break; }
done
[ "$up" -eq 1 ] || { say "stage log: $(tail -15 "$STAGE_LOG" | tr '\n' '|')"; fail "the candidate never answered on $STAGE_PORT"; }
bash "$CHECK" -p "$STAGE_PORT"
assets=$?
[ "$assets" -eq 1 ] && fail "the candidate is INCOMPLETE: it serves markup naming assets it cannot produce"
[ "$assets" -ne 0 ] && fail "the asset check could not run (exit $assets); an unmeasured candidate is not promotable"
say "asset gate PASSED"
kill "$STAGE_PID" 2>/dev/null; wait "$STAGE_PID" 2>/dev/null; STAGE_PID=""

# 4. BROWSER GATE: catches an error boundary served as a 200 with every asset.
listening 8329 && fail "CHECK UNAVAILABLE: the browser gate needs 127.0.0.1:8329 and something is already listening there. This is NOT a statement about the candidate."
say "running web/e2e/release-smoke.spec.ts against the candidate"
(cd "$WEB" && npx playwright test release-smoke --reporter=list) \
  || fail "the browser gate FAILED. Read the playwright output above: a harness timeout and a page that does not render are different findings."
say "browser gate PASSED"

# 5.
if [ "$BUILD_ONLY" -eq 1 ]; then
  say "BUILD-ONLY: every gate passed for $REV; candidate is at $CAND. Nothing was replaced."
  exit 0
fi

# 6. PROMOTE. Someone else's lock means a backup or deploy owns the downtime.
[ -e "$LOCK" ] && fail "$LOCK exists - another maintenance job is running; retry after it finishes"
printf 'web-release %s %s\n' "$REV" "$(date -Is)" > "$LOCK"; TOOK_LOCK=1
say "maintenance lock taken; the web guard will stand down"
STAMP="$(date '+%Y%m%d-%H%M%S')"
KEPT="$WEB/.next-prev-$STAMP"
sd_svc_stop com.signaldeck.web || fail "could not stop signaldeck-web.service; nothing was swapped"
if [ -d "$WEB/.next" ]; then
  say "moving the previous build to $KEPT"
  mv "$WEB/.next" "$KEPT" || { sd_svc_start com.signaldeck.web; fail "could not move web/.next aside; restarted the old build"; }
else
  say "there was no web/.next to keep"; KEPT="(none)"
fi
if ! mv "$CAND" "$WEB/.next"; then
  [ "$KEPT" != "(none)" ] && mv "$KEPT" "$WEB/.next"
  sd_svc_start com.signaldeck.web
  fail "could not move the candidate into place; put the previous build back and restarted it"
fi
unset SIGNALDECK_DIST_DIR
sd_svc_start com.signaldeck.web || say "systemctl start signaldeck-web.service failed; checking the port anyway"

# 7. not done until the port serves a COMPLETE build
ok=0
for _ in $(seq 1 15); do
  sleep 3
  bash "$CHECK" -p "$PORT" --quiet >/dev/null 2>&1 && { ok=1; break; }
done
if [ "$ok" -ne 1 ]; then
  say "PROMOTION DID NOT COME UP COMPLETE on $PORT. Roll back:"
  say "  systemctl stop signaldeck-web.service && mv $WEB/.next $WEB/.next-failed-$STAMP && mv $KEPT $WEB/.next && systemctl start signaldeck-web.service"
  exit 1
fi
say "promoted and complete on $PORT"
rm -f "$LOCK"; TOOK_LOCK=0

# 8. RECORD: which commit the running frontend is
BUILD_ID="$(head -1 "$WEB/.next/BUILD_ID" 2>/dev/null | tr -d '\r\n ')"
RECORD="$LOGDIR/web-release.json"
printf '{\n  "revision": "%s",\n  "shortRevision": "%s",\n  "buildId": "%s",\n  "promotedAt": "%s",\n  "port": %s,\n  "keptPrevious": "%s"\n}\n' \
  "$REV" "$SHORT" "$BUILD_ID" "$(date -Is)" "$PORT" "$KEPT" > "$RECORD"
say "recorded $REV (build $BUILD_ID) in $RECORD"

# 9. PRUNE, only after a recorded promotion; a prune failure is not a failed release
protect="$(sed -n 's/.*"keptPrevious"[ ]*:[ ]*"\([^"]*\)".*/\1/p' "$RECORD")"
protect="${protect##*/}"
ls -1d "$WEB"/.next-prev-[0-9]*-[0-9]* 2>/dev/null | sort -r | tail -n +3 | while IFS= read -r d; do
  [ "${d##*/}" = "$protect" ] && continue
  rm -rf "$d" && say "pruned old build ${d##*/}"
done
exit 0
