#!/bin/bash
# Self-test for ops/pre-publish-scan.sh (A13, 2026-07-26 hostile re-audit).
#
# The finding this pins: a scan with no test suite silently rotted — it used
# `grep -E` with no `-i` against UPPERCASE env var names, so it could never
# have caught the credential shapes this project actually uses, and it never
# looked at git history at all. It had already printed "SAFE TO PUBLISH" on
# this tree, which was independently true but was not evidence the scan
# worked. This test is that evidence, going forward.
#
# Strategy: build a disposable git repo per case (the scanner shells out to
# git and assumes it's the repo root), plant synthetic secrets shaped like
# this project's REAL env vars (never a real value), run the scanner against
# it, and assert exit code + expected output. Every planted secret here is
# synthesized — none is a value that has ever been live.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
SCAN="$(pwd)/ops/pre-publish-scan.sh"

pass=0
fail=0
note() { printf '%s\n' "$*"; }
check_pass() { pass=$((pass+1)); note "  ✓ $*"; }
check_fail() { fail=$((fail+1)); note "  ✗ $*"; }

# Builds a throwaway git repo at $1 with an initial commit of whatever files
# already exist under it, so the scan's "tracked files" / "history" checks
# have something real to walk.
init_repo() {
  local dir="$1"
  git -C "$dir" init -q
  git -C "$dir" config user.email test@example.invalid
  git -C "$dir" config user.name "pre-publish-scan-test"
  mkdir -p "$dir/ops"
  cp "$SCAN" "$dir/ops/pre-publish-scan.sh"
  chmod +x "$dir/ops/pre-publish-scan.sh"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m "init"
}

run_scan() {
  local dir="$1"
  ( cd "$dir" && ./ops/pre-publish-scan.sh )
}

# ── Case 1: clean tree passes ───────────────────────────────────────────
d1=$(mktemp -d)
init_repo "$d1"
echo "hello" > "$d1/README.md"
git -C "$d1" add -A && git -C "$d1" commit -q -m "readme"
out1=$(run_scan "$d1"); rc1=$?
if [ "$rc1" -eq 0 ] && printf '%s' "$out1" | grep -q "SAFE TO PUBLISH"; then
  check_pass "clean tree: scan exits 0 and says SAFE TO PUBLISH"
else
  check_fail "clean tree: expected exit 0 + SAFE TO PUBLISH, got exit=$rc1"
  printf '%s\n' "$out1" | sed 's/^/      /'
fi
rm -rf "$d1"

# ── Case 2: secret in a TRACKED file, project-shaped, currently missed by
#    the pre-fix regex because the var name is UPPERCASE ────────────────
d2=$(mktemp -d)
init_repo "$d2"
cat > "$d2/config.sh" <<'EOF'
export SIGNALDECK_NVIDIA_KEY=nvapi-aB3dEfGh9012IjKlMnOp3456QrStUvWx
export SIGNALDECK_TV_WEBHOOK_SECRET=whsec_9f2c7a1b4e6d8091ff3c2a
EOF
git -C "$d2" add -A && git -C "$d2" commit -q -m "add config"
out2=$(run_scan "$d2"); rc2=$?
if [ "$rc2" -ne 0 ] && printf '%s' "$out2" | grep -q "SIGNALDECK_NVIDIA_KEY\|SIGNALDECK_TV_WEBHOOK_SECRET"; then
  check_pass "uppercase project-shaped secret in tracked file: scan FAILS and names the hit"
else
  check_fail "uppercase project-shaped secret in tracked file: expected nonzero exit naming the var, got exit=$rc2"
  printf '%s\n' "$out2" | sed 's/^/      /'
fi
rm -rf "$d2"

# ── Case 3: a tracked daemon/.env ───────────────────────────────────────
d3=$(mktemp -d)
init_repo "$d3"
mkdir -p "$d3/daemon"
echo "SIGNALDECK_NVIDIA_KEY=nvapi-placeholderdoesnotmatter12345" > "$d3/daemon/.env"
git -C "$d3" add -A && git -C "$d3" commit -q -m "oops committed .env"
out3=$(run_scan "$d3"); rc3=$?
if [ "$rc3" -ne 0 ] && printf '%s' "$out3" | grep -q "\.env"; then
  check_pass "tracked daemon/.env: scan FAILS and flags the .env file"
else
  check_fail "tracked daemon/.env: expected nonzero exit flagging .env, got exit=$rc3"
  printf '%s\n' "$out3" | sed 's/^/      /'
fi
rm -rf "$d3"

# ── Case 4: THE A13 case — a secret committed once, then deleted in a
#    later commit. Working tree is clean; history is not. This is the exact
#    gap the finding described ("removed in a later commit is still in the
#    repo forever"). ───────────────────────────────────────────────────
d4=$(mktemp -d)
init_repo "$d4"
echo "export SIGNALDECK_TV_WEBHOOK_SECRET=whsec_7f1a9c3e5b2d8091aa44cc" > "$d4/leak.sh"
git -C "$d4" add -A && git -C "$d4" commit -q -m "leak a secret by accident"
rm "$d4/leak.sh"
git -C "$d4" add -A && git -C "$d4" commit -q -m "remove it (too late — it's in history)"
out4=$(run_scan "$d4"); rc4=$?
if [ "$rc4" -ne 0 ] && printf '%s' "$out4" | grep -qi "history"; then
  check_pass "secret deleted in a later commit: scan still FAILS via history scan"
else
  check_fail "secret deleted in a later commit: expected nonzero exit citing history, got exit=$rc4"
  printf '%s\n' "$out4" | sed 's/^/      /'
fi
rm -rf "$d4"

# ── Case 5: regression guard — fake test-fixture credentials must NOT fail
#    the scan (the false-positive tuning done alongside this fix) ───────
d5=$(mktemp -d)
init_repo "$d5"
mkdir -p "$d5/daemon/internal/api"
cat > "$d5/daemon/internal/api/auth_test.go" <<'EOF'
package api

func TestLogin(t *testing.T) {
	body := map[string]string{"username": "alice", "password": "hunter2secret"}
	_ = body
}
EOF
git -C "$d5" add -A && git -C "$d5" commit -q -m "add auth test"
out5=$(run_scan "$d5"); rc5=$?
if [ "$rc5" -eq 0 ]; then
  check_pass "fake credential in _test.go: scan still passes (no false positive)"
else
  check_fail "fake credential in _test.go: expected exit 0, got exit=$rc5 (regression in exclusion list)"
  printf '%s\n' "$out5" | sed 's/^/      /'
fi
rm -rf "$d5"

# ── Case 6: the self-test-fixture exclusion is PATH-SCOPED ──────────────
#    This file plants synthetic nvapi-/whsec_ strings on purpose, so the
#    scanner excludes it by exact path — otherwise it prints DO NOT PUBLISH
#    on a clean repo forever and everyone learns to ignore it. The risk of
#    that exclusion is that it quietly widens into "*-test.sh" or "ops/*"
#    and blinds the scan. All three assertions below must hold together:
#      6a the excluded path itself does not trip the scan,
#      6b the SAME planted strings in a near-miss neighbouring path do,
#      6c and they do from git history too, not just the working tree.
PLANTED='export SIGNALDECK_NVIDIA_KEY=nvapi-zZ9yYx8wWv7uUt6sSr5qQp4o
export SIGNALDECK_TV_WEBHOOK_SECRET=whsec_1a2b3c4d5e6f7081bb55dd'

d6=$(mktemp -d)
init_repo "$d6"
printf '%s\n' "$PLANTED" > "$d6/ops/pre-publish-scan-test.sh"
git -C "$d6" add -A && git -C "$d6" commit -q -m "add self-test fixture"
out6=$(run_scan "$d6"); rc6=$?
if [ "$rc6" -eq 0 ]; then
  check_pass "6a excluded fixture path: planted secrets do NOT fail the scan"
else
  check_fail "6a excluded fixture path: expected exit 0, got exit=$rc6"
  printf '%s\n' "$out6" | sed 's/^/      /'
fi

# 6b: one character off the excluded path — must still fail.
printf '%s\n' "$PLANTED" > "$d6/ops/pre-publish-scan-test-scope.sh"
git -C "$d6" add -A && git -C "$d6" commit -q -m "planted in a neighbouring path"
out6b=$(run_scan "$d6"); rc6b=$?
if [ "$rc6b" -ne 0 ] && printf '%s' "$out6b" | grep -q "pre-publish-scan-test-scope.sh"; then
  check_pass "6b neighbouring path: same planted secrets still FAIL the scan"
else
  check_fail "6b neighbouring path: exclusion is not path-scoped (exit=$rc6b)"
  printf '%s\n' "$out6b" | sed 's/^/      /'
fi

# 6c: delete the neighbouring file — the working tree is clean again, but
# history still carries it, and the history scan must not inherit the
# exclusion for a path that was never excluded.
rm "$d6/ops/pre-publish-scan-test-scope.sh"
git -C "$d6" add -A && git -C "$d6" commit -q -m "delete it (still in history)"
out6c=$(run_scan "$d6"); rc6c=$?
if [ "$rc6c" -ne 0 ] && printf '%s' "$out6c" | grep -qi "history"; then
  check_pass "6c history scan: exclusion does not leak to other paths in history"
else
  check_fail "6c history scan: expected nonzero exit citing history, got exit=$rc6c"
  printf '%s\n' "$out6c" | sed 's/^/      /'
fi
rm -rf "$d6"

note ""
note "pre-publish-scan self-test: $pass passed, $fail failed"
exit "$([ "$fail" -eq 0 ] && echo 0 || echo 1)"
