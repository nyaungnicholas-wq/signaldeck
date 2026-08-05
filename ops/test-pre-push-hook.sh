#!/bin/bash
# Self-test for ops/githooks/pre-push.
#
# A hook that cannot prove it still refuses is not a guard. This drives the
# hook the way git does — the four-field ref line on stdin — and checks both
# directions, because a hook that refuses everything is as broken as one that
# refuses nothing: it would block every feature-branch push and get uninstalled
# the same afternoon.
set -u
cd "$(dirname "$0")/.." || exit 1
HOOK=ops/githooks/pre-push
fail=0

# The hook must be EXECUTABLE. git skips a non-executable hook silently, which
# is how ops/githooks/pre-rebase sat mode 644 and never ran on Linux or macOS.
if [ ! -x "$HOOK" ]; then
  echo "FAIL: $HOOK is not executable — git will skip it without saying so"
  fail=1
fi

run() { printf '%s\n' "$2" | sh "$HOOK" origin git@github.com:x/y.git >/dev/null 2>&1; echo $?; }

MAIN='refs/heads/main aaaa refs/heads/main bbbb'
FEAT='refs/heads/feat aaaa refs/heads/feat bbbb'

got=$(run "" "$MAIN")
[ "$got" = "1" ] || { echo "FAIL: a push to main exited $got, want 1 (refused)"; fail=1; }

got=$(run "" "$FEAT")
[ "$got" = "0" ] || { echo "FAIL: a push to a feature branch exited $got, want 0 (allowed)"; fail=1; }

# A push carrying several refs must be refused when ANY of them is main.
got=$(run "" "$FEAT
$MAIN")
[ "$got" = "1" ] || { echo "FAIL: a mixed push including main exited $got, want 1"; fail=1; }

# The escape hatch has to work, or the hook becomes something people delete.
got=$(printf '%s\n' "$MAIN" | SIGNALDECK_ALLOW_DIRECT_MAIN_PUSH=1 sh "$HOOK" origin url >/dev/null 2>&1; echo $?)
[ "$got" = "0" ] || { echo "FAIL: the override exited $got, want 0"; fail=1; }

# A branch merely NAMED like main is a different branch.
got=$(run "" 'refs/heads/main2 aaaa refs/heads/main2 bbbb')
[ "$got" = "0" ] || { echo "FAIL: refs/heads/main2 exited $got, want 0 — only main is protected"; fail=1; }

[ "$fail" -eq 0 ] && echo "ok: pre-push hook refuses main, allows everything else"
exit "$fail"
