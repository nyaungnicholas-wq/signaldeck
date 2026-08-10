#!/bin/bash
# Provenance-preserving docker build. Use this instead of `docker build`.
#
# The image has to be attributable to a commit: every row the daemon writes
# carries a revision stamp, and tools/accuracy_registry.py refuses a verdict
# whose rows name a commit this repository cannot resolve.
#
# .dockerignore excludes .git/, so the Go toolchain inside the build can embed
# no vcs.revision and the Dockerfile bakes the commit via
# --build-arg GIT_REV -> -ldflags instead. That path is TRUSTED, not checked:
# internal/lineage sets modified=false whenever it falls back to ldflagsRev
# (daemon/internal/lineage/lineage.go), because a container cannot see the
# working tree it was built from. So
#
#   docker build --build-arg GIT_REV=$(git rev-parse HEAD) -t signaldeck .
#
# run against a DIRTY tree produces an image built from uncommitted code,
# stamped with HEAD's SHA, reporting modified=false — a provenance claim that is
# false and that nothing downstream can detect. The native path already refuses
# that (ops/signaldeck-ctl.sh build_from_head: clean tree, then build from
# `git archive HEAD` so "the binary is the commit" holds by construction). This
# is the same refusal for the Docker path, which had no equivalent.
#
# It can only REFUSE. It never edits, commits, or relaxes anything.
#
#   ops/docker-build.sh                 -> signaldeck
#   ops/docker-build.sh signaldeck:demo -> that tag
#   ops/docker-build.sh signaldeck:demo --no-cache
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

tag="signaldeck"
if [ "$#" -gt 0 ]; then
  tag="$1"
  shift
fi

dirty="$(git status --porcelain | wc -l | tr -d ' ')"
if [ "$dirty" != "0" ]; then
  echo "REFUSED: working tree is not clean ($dirty path(s))." >&2
  git status --porcelain | head -20 >&2
  echo "" >&2
  echo "A stamped image must be reproducible from a commit. Commit or stash" >&2
  echo "first — the container cannot see this tree, so GIT_REV would assert a" >&2
  echo "provenance nobody observed." >&2
  exit 1
fi

rev="$(git rev-parse HEAD 2>/dev/null)"
if [ "${#rev}" -ne 40 ] || [ -n "${rev//[0-9a-f]/}" ]; then
  echo "REFUSED: cannot resolve HEAD to a commit (got '${rev}')." >&2
  exit 1
fi

echo "docker build: stamping commit $rev into $tag" >&2
docker build --build-arg GIT_REV="$rev" -t "$tag" "$@" .
