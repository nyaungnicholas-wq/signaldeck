#!/bin/bash
# Self-test for ops/cloud/web-release-linux.sh. Runs the real script in a throwaway
# git repo with stub systemctl / npm / npx / node / ss / curl / sleep / mv and a
# stub asset check, and proves each promote, rollback, lock and prune branch:
# foreign lock refused and left alone, a failed stop swaps nothing, a failed mv
# puts the previous build back and restarts it, an incomplete promotion exits 1
# and prints the rollback, prune keeps the newest 2 and never the recorded
# keptPrevious, and the lock is released on every exit. No real build, server,
# unit or network call happens. Prints SELFTEST OK.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$HERE/../.." && pwd)"
REAL_MV="$(command -v mv)"
export REAL_MV
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
fails=0
ok() { echo "  ok   $1"; }
bad() { echo "  FAIL $1"; fails=$((fails + 1)); }

STUBS="$T/stubs"; mkdir -p "$STUBS"
LOG="$T/calls.log"

cat > "$STUBS/uname" <<'EOF'
#!/bin/bash
echo Linux
EOF
cat > "$STUBS/id" <<'EOF'
#!/bin/bash
if [ "${1:-}" = -u ]; then echo "${STUB_UID:-1000}"; else echo signaldeck; fi
EOF
cat > "$STUBS/systemctl" <<'EOF'
#!/bin/bash
echo "systemctl $*" >> "$STUB_LOG"
case "$1" in
  cat) exit 0 ;;
  stop) exit "${STUB_STOP_RC:-0}" ;;
  start) exit "${STUB_START_RC:-0}" ;;
esac
exit 0
EOF
cat > "$STUBS/npm" <<'EOF'
#!/bin/bash
echo "npm $*" >> "$STUB_LOG"
if [ "$1" = run ] && [ "$2" = build ]; then
  mkdir -p "$PWD/$SIGNALDECK_DIST_DIR"
  echo new > "$PWD/$SIGNALDECK_DIST_DIR/BUILD_ID"
  [ -f "$PWD/tsconfig.json" ] && echo "// rewritten by next build" >> "$PWD/tsconfig.json"
  exit "${STUB_BUILD_RC:-0}"
fi
exit 0
EOF
cat > "$STUBS/node" <<'EOF'
#!/bin/bash
echo "node $*" >> "$STUB_LOG"
exit 0
EOF
cat > "$STUBS/npx" <<'EOF'
#!/bin/bash
echo "npx $*" >> "$STUB_LOG"
exit "${STUB_BROWSER_RC:-0}"
EOF
cat > "$STUBS/ss" <<'EOF'
#!/bin/bash
echo "ss $*" >> "$STUB_LOG"
for p in ${STUB_LISTENING:-}; do
  case "$*" in *":$p"*) echo "LISTEN 0 511 127.0.0.1:$p 0.0.0.0:*";; esac
done
exit 0
EOF
cat > "$STUBS/curl" <<'EOF'
#!/bin/bash
echo "curl $*" >> "$STUB_LOG"
printf 200
exit 0
EOF
cat > "$STUBS/sleep" <<'EOF'
#!/bin/bash
exit 0
EOF
cat > "$STUBS/mv" <<'EOF'
#!/bin/bash
echo "mv $*" >> "$STUB_LOG"
b1="$(basename "$1")"
b2="$(basename "$2")"
if [ "${STUB_MV_FAIL:-}" = cand ] && [[ "$b1" == .next-release-* ]]; then
  echo "mv REFUSED $*" >> "$STUB_LOG"
  exit 1
fi
if [ "${STUB_MV_FAIL:-}" = prev ] && [ "$b1" = .next ] && [[ "$b2" == .next-prev-* ]]; then
  echo "mv REFUSED $*" >> "$STUB_LOG"
  exit 1
fi
exec "$REAL_MV" "$@"
EOF
chmod +x "$STUBS"/*

R="$T/root/signaldeck"
mkdir -p "$R/ops/cloud" "$R/web/node_modules/next/dist/bin" "$R/logs"
cp "$SRC/ops/lib-portable.sh" "$SRC/ops/generated-docs.txt" "$R/ops/"
cp "$SRC/ops/cloud/web-release-linux.sh" "$R/ops/cloud/"
cat > "$R/ops/cloud/web-assets-check.sh" <<'EOF'
#!/bin/bash
echo "assets $*" >> "$STUB_LOG"
case " $* " in
  *" -p 8331 "*) exit "${STUB_STAGE_ASSETS_RC:-0}" ;;
  *" -p 8323 "*) exit "${STUB_LIVE_ASSETS_RC:-0}" ;;
esac
exit 3
EOF
chmod +x "$R/ops/cloud/web-assets-check.sh"
echo '{}' > "$R/web/package.json"
echo '{"compilerOptions":{}}' > "$R/web/tsconfig.json"
echo stub > "$R/web/node_modules/next/dist/bin/next"
printf 'web/node_modules/\nweb/.next*\nlogs/\nops/.maintenance\n' > "$R/.gitignore"
git -C "$R" init -q
git -C "$R" -c user.email=t@t -c user.name=t add -A 2>/dev/null   # CRLF notices on Windows hosts
git -C "$R" -c user.email=t@t -c user.name=t commit -qm fixture
REV="$(git -C "$R" rev-parse HEAD)"
SHORT="$(git -C "$R" rev-parse --short HEAD)"

reset() {
  rm -rf "$R/web/.next"* "$R/logs" "$R/ops/.maintenance"
  mkdir -p "$R/web/.next"
  echo old > "$R/web/.next/BUILD_ID"
  git -C "$R" checkout -q -- web/tsconfig.json
  rm -f "$R/web/dirty.txt"
}

run() {
  : > "$LOG"
  OUT="$(env PATH="$STUBS:$PATH" STUB_LOG="$LOG" REAL_MV="$REAL_MV" "$@" bash "$R/ops/cloud/web-release-linux.sh" ${ARGS:-} 2>&1)"
  RC=$?
}

built() { head -1 "$R/web/.next/BUILD_ID" 2>/dev/null; }
stopped() { grep -q '^systemctl stop signaldeck-web.service' "$LOG"; }
started() { grep -q '^systemctl start signaldeck-web.service' "$LOG"; }
nolock() { [ ! -e "$R/ops/.maintenance" ]; }
prevs() { ls -1d "$R"/web/.next-prev-* 2>/dev/null; }

echo "web-release-linux.sh self-test"

# 1 success
reset; run env
kept_prev="$(prevs)"
if [ "$RC" -eq 0 ] && [ "$(built)" = new ] && [ "$(echo "$kept_prev" | wc -l)" -eq 1 ] && [ -n "$kept_prev" ] && [ "$(head -1 "$kept_prev/BUILD_ID" 2>/dev/null)" = old ] \
   && grep -q "\"revision\": \"$REV\"" "$R/logs/web-release.json" \
   && grep -q "\"keptPrevious\": \"$kept_prev\"" "$R/logs/web-release.json" \
   && stopped && started && nolock \
   && ! ls "$R"/web/.next-release-* >/dev/null 2>&1 \
   && git -C "$R" diff --quiet -- web/tsconfig.json; then
  ok "success promotes, records, restores tsconfig"
else
  bad "success (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 2 --build-only
reset; ARGS=--build-only run env
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q BUILD-ONLY && [ "$(built)" = old ] \
   && [ -f "$R/web/.next-release-$SHORT/BUILD_ID" ] && [ "$(head -1 "$R/web/.next-release-$SHORT/BUILD_ID")" = new ] \
   && ! stopped && nolock && [ ! -f "$R/logs/web-release.json" ]; then
  ok "--build-only builds candidate, stops before promote"
else
  bad "--build-only (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 3 foreign lock
reset; echo backup > "$R/ops/.maintenance"; run env
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "another maintenance job" \
   && [ -f "$R/ops/.maintenance" ] && [ "$(cat "$R/ops/.maintenance")" = backup ] \
   && [ "$(built)" = old ] && ! stopped; then
  ok "foreign lock refuses and leaves lock intact"
else
  bad "foreign lock (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi
rm -f "$R/ops/.maintenance"

# 4 stop failure
reset; run env STUB_STOP_RC=1
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "nothing was swapped" \
   && [ "$(built)" = old ] && [ -z "$(prevs)" ] && nolock; then
  ok "stop failure aborts before any swap"
else
  bad "stop failure (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 5 moving live aside fails
reset; run env STUB_MV_FAIL=prev
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "restarted the old build" \
   && [ "$(built)" = old ] && started && nolock; then
  ok "mv .next aside failure rolls back and restarts old"
else
  bad "mv prev fail (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 6 moving candidate in fails
reset; run env STUB_MV_FAIL=cand
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "put the previous build back" \
   && [ "$(built)" = old ] && [ -z "$(prevs)" ] && started && nolock; then
  ok "mv candidate failure restores previous and restarts"
else
  bad "mv cand fail (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 7 incomplete promotion
reset; run env STUB_LIVE_ASSETS_RC=1
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "PROMOTION DID NOT COME UP COMPLETE" \
   && printf '%s' "$OUT" | grep -q "mv $R/web/.next $R/web/.next-failed-" \
   && printf '%s' "$OUT" | grep -q "&& mv $R/web/.next-prev-" \
   && [ ! -f "$R/logs/web-release.json" ] && nolock; then
  ok "incomplete promotion prints rollback and leaves no record"
else
  bad "incomplete promo (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 8 prune
reset
for d in 20200101-000001 29991231-000001 29991231-000002; do
  mkdir -p "$R/web/.next-prev-$d"
  echo x > "$R/web/.next-prev-$d/BUILD_ID"
done
run env
kept="$(sed -n 's/.*"keptPrevious"[ ]*:[ ]*"\([^"]*\)".*/\1/p' "$R/logs/web-release.json" | sed 's|.*/||')"
if [ "$RC" -eq 0 ] && [ ! -d "$R/web/.next-prev-20200101-000001" ] \
   && [ -d "$R/web/.next-prev-29991231-000001" ] && [ -d "$R/web/.next-prev-29991231-000002" ] \
   && [ -n "$kept" ] && [ -d "$R/web/$kept" ]; then
  ok "prune removes oldest, keeps newest 2 and protected"
else
  bad "prune (rc=$RC, kept=$kept): $(printf '%s' "$OUT" | tail -3)"
fi

# 9 asset gate fails
reset; run env STUB_STAGE_ASSETS_RC=1
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q INCOMPLETE \
   && ! stopped && [ "$(built)" = old ] && nolock; then
  ok "stage asset gate failure aborts before stop"
else
  bad "stage assets fail (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 10 asset check cannot run
reset; run env STUB_STAGE_ASSETS_RC=2
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "could not run" && ! stopped; then
  ok "asset check error aborts before stop"
else
  bad "asset check error (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 11 browser gate fails
reset; run env STUB_BROWSER_RC=1
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "browser gate FAILED" \
   && ! stopped && [ "$(built)" = old ]; then
  ok "browser gate failure aborts before stop"
else
  bad "browser gate fail (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 12 stage port busy
reset; run env STUB_LISTENING=8331
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "CHECK UNAVAILABLE" \
   && ! grep -q '^node ' "$LOG"; then
  ok "busy stage port aborts, candidate never started"
else
  bad "stage port busy (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

# 13 dirty web tree
reset; echo x > "$R/web/dirty.txt"; run env
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "uncommitted changes" \
   && ! grep -q '^npm ' "$LOG"; then
  ok "dirty web tree refuses before build"
else
  bad "dirty tree (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi
rm -f "$R/web/dirty.txt"

# 14 root
reset; run env STUB_UID=0
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "never root" \
   && ! grep -q '^npm ' "$LOG"; then
  ok "running as root refuses immediately"
else
  bad "root (rc=$RC): $(printf '%s' "$OUT" | tail -3)"
fi

[ "$fails" -eq 0 ] || { echo "web-release-linux self-test: $fails FAILED"; exit 1; }
echo "SELFTEST OK"