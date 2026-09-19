#!/bin/bash
# Prove that the container actually running is the commit you think it is.
#
# WHY THIS EXISTS
# ---------------
# ops/signaldeck-ctl.sh deploy ends by asking /api/version for the running
# revision AND requiring resolvable:true. That used to be impossible in a
# container — internal/lineage shells out to git, and the image had no git and
# no .git — and this script asserted the honest false rather than faking one.
#
# The image now carries the commit objects instead (a pack of commits only, no
# trees or blobs), because the hash-pinned grader asks the same question of
# every historical forecast row and was answering false for all of them. The
# distinction this file has always defended is unchanged: shipping evidence is
# not faking it, so the flag is now expected true AND a negative control below
# proves the store still refuses a commit that was never made.
#
# So the proof is split across the two places where each half can actually be
# established:
#
#   BUILD TIME, on the build host, in ops/docker-build.sh:
#     - the tree is clean (modulo the generated-docs allowlist)
#     - `git cat-file -e <rev>^{commit}` proves the revision RESOLVES
#     - the revision is baked via ldflags AND recorded as an OCI label
#
#   DEPLOY TIME, here:
#     - the checkout, the image label and the running process all agree
#
# Together that is strictly as strong as the native path. The native path
# proves resolvability at deploy time on the build host; this proves it at
# build time on the build host and then proves the running binary IS that
# build. Nothing is asserted that was not observed.
#
# Usage:  ops/oracle-verify.sh [image] [url]
set -uo pipefail

IMAGE="${1:-signaldeck}"
URL="${2:-http://127.0.0.1:8080}"
SRC="${SIGNALDECK_SRC:-$(cd "$(dirname "$0")/.." && pwd)}"

fail() { echo "deploy UNVERIFIED: $*" >&2; exit 1; }

# A -- what the checkout says
a="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)" || fail "cannot read HEAD in $SRC"
[ ${#a} -eq 40 ] || fail "HEAD in $SRC is not a commit sha (got '$a')"

# B -- what the image claims
b="$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$IMAGE" 2>/dev/null)"
[ -n "$b" ] || fail "image '$IMAGE' carries no org.opencontainers.image.revision label.
Build it with ops/docker-build.sh, never with a bare 'docker build' -- the
wrapper is what refuses a dirty tree and proves the revision resolves."

# C -- what the running process reports
ver="$(curl -fsS --max-time 10 "$URL/api/version" 2>/dev/null)" || fail "$URL/api/version did not answer"
c="$(printf '%s' "$ver" | sed -n 's/.*"revision"[[:space:]]*:[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p')"
[ -n "$c" ] || fail "could not read a revision from /api/version: $ver"

[ "$a" = "$b" ] || fail "checkout $a != image label $b (the image is not built from this tree)"
[ "$b" = "$c" ] || fail "image label $b != running process $c (an older container is still serving)"

# RESOLVABILITY, WHICH THE IMAGE CAN NOW ANSWER FOR ITSELF.
#
# This used to FAIL on resolvable:true, and it was right to: with no git and no
# objects in the image, a true could only have been asserted. The image now
# ships this repository's commit objects (ops/docker-build.sh packs them, the
# Dockerfile unpacks them into /app/.git) because the hash-pinned grader asks
# the same question of every historical row and stripped every verdict when the
# answer was always false. So the expected answer is inverted -- and the check
# that matters is no longer the flag but whether the store can be made to lie.
case "$ver" in
  *'"resolvable":true'*) ;;
  *) fail "the container reports resolvable:false, so its commit store is missing or unreadable.
Every row the grader reads is then unattributable and it publishes no verdicts.
Rebuild with ops/docker-build.sh, which packs the commit objects." ;;
esac

# THE NEGATIVE CONTROL. A store that says yes to everything would satisfy the
# check above while proving nothing, so ask it for a commit that cannot exist.
# Git objects are content-addressed, so a real store must refuse this.
absent="0000000000000000000000000000000000000000"
# Through `sh -c`, NOT `--entrypoint git ... -C /app`. MSYS (Git Bash, which is
# how this script runs on the Windows build host) rewrites a bare /app argument
# into C:/Program Files/Git/app, so the command failed for a path reason and the
# `if` read that as "the store refused" — a negative control that could never
# fire, which is worse than not having one. Measured 2026-09-16:
#   fatal: cannot change to 'C:/Program Files/Git/app': No such file or directory
# An argument that does not begin with / is not translated, so the whole command
# travels as one string and means the same thing on every host.
if docker run --rm --entrypoint sh "$IMAGE" -c "git -C /app cat-file -e ${absent}^{commit}" 2>/dev/null; then
  fail "the image's commit store resolved $absent, a revision that does not exist.
It is not a git object store; it is something answering yes, which is worse than
no provenance at all."
fi

echo "deploy VERIFIED: running commit $a"
echo "  checkout, image label and running process agree"
echo "  the image resolves its own revision against the commit objects it ships,"
echo "  and refuses a revision that does not exist"
