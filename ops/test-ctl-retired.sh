#!/bin/bash
# Self-test for the data/RETIRED guard in ops/signaldeck-ctl.sh (ops/cloud/README.md
# C2/C8): once the PC hands the writer role to the cloud VM, up/collect/deploy/
# launch/refresh must refuse before building or starting anything. Runs the real
# ctl in a throwaway repo with every service-control tool stubbed (and proves the
# stubs shadow the real ones first). Never runs `stop`. Prints SELFTEST OK.
set -u
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$(cd "$HERE/.." && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT

fails=0
ok() { echo "  ok   $1"; }
bad() { echo "  FAIL $1 ($2)"; fails=$((fails+1)); }

STUB_LOG="$T/stub.log"
mkdir -p "$T/stubs"
for t in uname systemctl schtasks pgrep tasklist powershell.exe taskkill curl open go; do
  cat >"$T/stubs/$t" <<'EOF'
#!/bin/bash
echo "$(basename "$0") $*" >>"$STUB_LOG"
case "$(basename "$0")" in
  uname) echo Linux ;;
  systemctl) exit 0 ;;
  schtasks) exit 1 ;;
  pgrep) exit 1 ;;
  tasklist) exit 0 ;;
  powershell.exe) echo 0 ;;
  taskkill) exit 1 ;;
  curl) exit 7 ;;
  open) exit 0 ;;
  go) exit 1 ;;
esac
EOF
  chmod +x "$T/stubs/$t"
done

for t in schtasks systemctl go uname; do
  cmd="$(env PATH="$T/stubs:$PATH" bash -c "command -v $t")"
  [ "$cmd" = "$T/stubs/$t" ] || { echo "stubs do not shadow $t - refusing to run"; exit 2; }
done

R="$T/root/signaldeck"
mkdir -p "$R/ops" "$R/data" "$R/daemon"
cp "$SRC/ops/signaldeck-ctl.sh" "$SRC/ops/lib-portable.sh" "$SRC/ops/lib-deploy.sh" "$SRC/ops/manifest-check.sh" "$SRC/ops/generated-docs.txt" "$R/ops/"
git -C "$R" init -q
git -C "$R" config user.email "t@t"
git -C "$R" config user.name "t"
git -C "$R" add -A 2>/dev/null   # CRLF notices on Windows hosts are noise here
git -C "$R" commit -qm fixture

run() {
  : >"$STUB_LOG"
  OUT="$(env PATH="$T/stubs:$PATH" STUB_LOG="$STUB_LOG" bash "$R/ops/signaldeck-ctl.sh" "$@" 2>&1)"
  RC=$?
}

echo "signaldeck-ctl.sh RETIRED self-test"

# 1. control, no RETIRED file
run deploy
if [ "$RC" -ne 0 ] && echo "$OUT" | grep -q "go vet failed" && grep -q "^go vet" "$STUB_LOG"; then
  ok "control deploy reaches go vet (no RETIRED)"
else
  bad "control deploy reaches go vet (no RETIRED)" "RC=$RC OUT='$OUT' LOG='$(cat "$STUB_LOG")'"
fi

# 2. create RETIRED, test blocked subcommands
echo test >"$R/data/RETIRED"
for sub in up collect deploy launch refresh; do
  run "$sub"
  if [ "$RC" -eq 1 ] && echo "$OUT" | grep -q "data/RETIRED exists" && [ ! -s "$STUB_LOG" ]; then
    ok "$sub refuses while data/RETIRED exists"
  else
    bad "$sub refuses while data/RETIRED exists" "RC=$RC OUT='$OUT' LOG='$(cat "$STUB_LOG")'"
  fi
done

# 3. status not blocked
run status
if [ "$RC" -eq 0 ] && ! echo "$OUT" | grep -q "data/RETIRED exists"; then
  ok "status not blocked by RETIRED"
else
  bad "status not blocked by RETIRED" "RC=$RC OUT='$OUT'"
fi

# 4. remove RETIRED, deploy works again
rm "$R/data/RETIRED"
run deploy
if ! echo "$OUT" | grep -q "data/RETIRED exists" && echo "$OUT" | grep -q "go vet failed"; then
  ok "deploy works after RETIRED removed"
else
  bad "deploy works after RETIRED removed" "RC=$RC OUT='$OUT'"
fi

[ "$fails" -eq 0 ] || { echo "ctl-retired self-test: $fails FAILED"; exit 1; }
echo "SELFTEST OK"