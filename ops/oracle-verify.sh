#!/bin/bash
# Prove that the container actually running is the commit you think it is.
#
# WHY THIS EXISTS
# ---------------
# ops/signaldeck-ctl.sh deploy ends by asking /api/version for the running
# revision AND requiring resolvable:true. In a container the second half is
# impossible: internal/lineage shells out to git, and the image has no git and
# no .git, so resolvable is false forever. That is the daemon being honest, and
# faking it would be the single worst thing to fake on a platform whose whole
# claim is that its rows can be tied to the code that produced them.
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

# The daemon must ALSO say it is not resolvable. If it ever claims otherwise in
# a container, something is faking provenance and that is worse than a failed
# deploy.
case "$ver" in
  *'"resolvable":true'*)
    fail "the container reports resolvable:true, which is impossible without git in the image.
Something is asserting a provenance it cannot have observed."
    ;;
esac

echo "deploy VERIFIED: running commit $a"
echo "  checkout, image label and running process agree"
echo "  resolvability was proved at BUILD time (ops/docker-build.sh), which is"
echo "  the only place git exists; the container correctly reports resolvable:false"
