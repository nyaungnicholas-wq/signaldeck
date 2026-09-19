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

# The tree must be clean EXCEPT for the nightly-regenerated docs. This used to
# be a blanket `wc -l != 0`, which meant this script refused on any day the
# grader had run -- every day -- while ops/signaldeck-ctl.sh built happily from
# the same tree. Two deploy paths disagreeing about what "clean" means is how
# the container path came to be untestable. One spelling now, in
# ops/lib-portable.sh, used by both.
# FAIL CLOSED on the library. Sourcing it used to be unchecked, and `set -e` is
# deliberately off here, so a missing or renamed lib-portable.sh left
# sd_dirty_excluding_generated undefined, $dirty empty, and the check below
# silently PASSED -- the build then stamped a dirty tree with a provenance
# nobody verified. ops/test-docker-build.sh has been red on exactly this: its
# fixture does not copy the library, so every dirty-tree assertion failed.
lib="$(dirname "$0")/lib-portable.sh"
. "$lib" || { echo "REFUSED: cannot source $lib -- the dirty-tree check cannot run." >&2; exit 1; }
command -v sd_dirty_excluding_generated >/dev/null 2>&1 \
  || { echo "REFUSED: $lib defines no sd_dirty_excluding_generated -- refusing to skip the dirty-tree check." >&2; exit 1; }
dirty="$(sd_dirty_excluding_generated "$(pwd)")"
if [ -n "$dirty" ]; then
  echo "REFUSED: working tree is not clean." >&2
  printf '%s\n' "$dirty" | head -20 >&2
  echo "" >&2
  echo "A stamped image must be reproducible from a commit. Commit or stash" >&2
  echo "first -- the container cannot see this tree, so GIT_REV would assert a" >&2
  echo "provenance nobody observed." >&2
  echo "(docs listed in ops/generated-docs.txt are exempt and not counted above)" >&2
  exit 1
fi

rev="$(git rev-parse HEAD 2>/dev/null)"
if [ "${#rev}" -ne 40 ] || [ -n "${rev//[0-9a-f]/}" ]; then
  echo "REFUSED: cannot resolve HEAD to a commit (got '${rev}')." >&2
  exit 1
fi

# RESOLVABILITY IS PROVED HERE, BECAUSE IT CANNOT BE PROVED LATER.
#
# internal/lineage.RevisionResolvable shells out to git, and the image has no
# git binary and no .git (.dockerignore excludes it), so /api/version reports
# resolvable:false in a container FOREVER. That is the daemon telling the
# truth, and it must not be faked -- but it also means the native deploy
# gate's "revision resolvable" check has no container equivalent unless the
# proof is moved to the one place git actually exists: this script, on the
# build host.
#
# `git rev-parse` above only parses a string. This asks whether that string is
# a real commit object in this repository.
if ! git cat-file -e "${rev}^{commit}" 2>/dev/null; then
  echo "REFUSED: ${rev} does not resolve to a commit object in this repository." >&2
  exit 1
fi

# NEXT_PUBLIC_SITE_URL is inlined into the web bundle at BUILD time and cannot
# be set later from the container environment, so it crosses here or not at all.
# Unset, web/src/lib/site.ts falls back to http://localhost:8323 SILENTLY and
# the image serves a robots.txt and sitemap.xml no crawler can use, plus og:
# and twitter: cards pointing at localhost. None of that is visible until
# someone shares a link, which is why this refuses rather than warns.
#
# A local image is a legitimate reason to have no hostname, so the opt-out is
# explicit and named rather than implied by an empty variable.
if [ -z "${NEXT_PUBLIC_SITE_URL:-}" ] && [ "${SIGNALDECK_ALLOW_LOCALHOST_SITE_URL:-}" != "1" ]; then
  echo "docker build REFUSED: NEXT_PUBLIC_SITE_URL is unset." >&2
  echo "  The web bundle inlines it at build time. Unset, this image serves" >&2
  echo "  robots.txt, sitemap.xml and og: cards pointing at http://localhost:8323." >&2
  echo "  Publishable:  NEXT_PUBLIC_SITE_URL=https://<host> $0 $*" >&2
  echo "  Local only:   SIGNALDECK_ALLOW_LOCALHOST_SITE_URL=1 $0 $*" >&2
  exit 1
fi
if [ -n "${NEXT_PUBLIC_SITE_URL:-}" ]; then
  echo "docker build: site URL $NEXT_PUBLIC_SITE_URL" >&2
else
  echo "docker build: site URL unset -- localhost fallback (local image only)" >&2
fi

# THE AUDIENCE OF THE IMAGE, chosen rather than defaulted into.
#
# NEXT_PUBLIC_SIGNALDECK_PUBLIC is inlined at build time exactly like the site
# URL above, and until 2026-09-13 the Dockerfile had no ARG for it at all -- so
# every container image ever built shipped in PRIVATE mode no matter what the
# operator put in the environment, and DEPLOY.md's instruction to set it was
# describing something that could not work. On a published deployment that
# means an anonymous visitor is redirected to /login by AuthGate, and /proof
# shows them remediation copy written for the operator.
#
# The Dockerfile default is private, which is the safe direction to be wrong in.
# But a PUBLISHABLE image -- one carrying a real hostname -- almost certainly
# wants the public profile, and silently shipping a locked front door on a site
# whose whole argument is "check my claims yourself" is its own failure. So a
# build with a hostname must SAY which audience it is for.
case "${NEXT_PUBLIC_SIGNALDECK_PUBLIC:-}" in
  0|1|"") ;;
  *) echo "docker build REFUSED: NEXT_PUBLIC_SIGNALDECK_PUBLIC must be 0 or 1, got '$NEXT_PUBLIC_SIGNALDECK_PUBLIC'." >&2
     exit 1 ;;
esac
if [ -n "${NEXT_PUBLIC_SITE_URL:-}" ] && [ -z "${NEXT_PUBLIC_SIGNALDECK_PUBLIC:-}" ]; then
  echo "docker build REFUSED: this image carries a hostname but no audience." >&2
  echo "  NEXT_PUBLIC_SIGNALDECK_PUBLIC is inlined at build time and defaults to 0," >&2
  echo "  so leaving it unset ships a site that bounces every anonymous visitor to" >&2
  echo "  /login and shows operator remediation copy on /proof." >&2
  echo "  Public site:  NEXT_PUBLIC_SIGNALDECK_PUBLIC=1 $0 $*" >&2
  echo "  Private host: NEXT_PUBLIC_SIGNALDECK_PUBLIC=0 $0 $*" >&2
  exit 1
fi
if [ "${NEXT_PUBLIC_SIGNALDECK_PUBLIC:-0}" = "1" ]; then
  echo "docker build: audience PUBLIC (anonymous visitors land on /)" >&2
else
  echo "docker build: audience PRIVATE (anonymous visitors land on /login)" >&2
fi

# BIND THE IMAGE TO THE SOURCE, HERE, WHERE GIT EXISTS.
#
# GIT_REV below is TRUSTED, not checked -- this file says so fifteen lines up,
# and an image repeating back a label the caller supplied proves only that
# someone typed it. ops/grade.sh's header records the consequence: the container
# applies one gate fewer than the dev box, because tools/deployment_drift.py
# shells out to git and .dockerignore excludes .git.
#
# What CAN cross that boundary is content. The files that decide a verdict are
# copied into the image byte-for-byte, so hashing them HERE -- against the tree
# the operator is looking at, on the machine that can resolve a revision -- and
# re-hashing them inside the container at grade time binds the running artifact
# to the reviewed source through something no build argument can forge.
#
# Emitted immediately before the build so it describes THIS tree, and refused if
# a pinned file is missing: a manifest that pins nothing verifies everything.
# The build context is `.` at the bottom of this file, so the repo root is the
# current directory -- the same assumption sd_dirty_excluding_generated already
# makes above. The manifest has to land IN that context or the Dockerfile's COPY
# cannot see it.
manifest="./build-manifest.json"

# Resolve a python that actually RUNS. `command -v python3` is not sufficient on
# Windows: the Microsoft Store ships an app-execution-alias stub at that name
# which resolves fine, prints "Python was not found" to stdout and exits 0.
# ops/accuracy-registry.sh learned this the hard way; same detection here.
bm_py=""
for cand in python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    bm_py="$cand"; break
  fi
done
if [ -z "$bm_py" ]; then
  echo "docker build REFUSED: no working python on PATH (tried python3, python, py)." >&2
  echo "  It is needed to emit the build manifest that binds this image to its source." >&2
  exit 1
fi

rm -f "$manifest"
if ! "$bm_py" ./tools/build_manifest.py emit --repo . --out "$manifest"; then
  echo "docker build REFUSED: could not emit the build manifest (see above)." >&2
  echo "  The image would then be unbindable to any reviewed source." >&2
  exit 1
fi

# THE COMMIT OBJECTS THE GRADER NEEDS, AND ONLY THOSE.
#
# tools/accuracy_registry.py is pinned by hash on the pre-registration chain and
# refuses to run when its own bytes change, so its revision_resolvable() cannot
# be edited to suit a container: it runs `git cat-file -e <rev>^{commit}` in the
# repo root and, finding no git, answers False for EVERY revision. Its gate then
# strips the verdict from every directional and structural row, so a container
# grade produced a registry with no verdicts in it at all. The build manifest
# above does not help -- it binds the BYTES of this image, while the gate is
# asking whether each historical row's revision names a commit that exists.
#
# So ship the commits. `git rev-list --all | git pack-objects` packs exactly the
# object names it is given -- commit objects, no trees and no blobs -- which is
# under a megabyte for this history and is all `cat-file -e <sha>^{commit}` has
# to read. Git objects are content-addressed: an object that hashes to a sha IS
# that commit, so this store cannot be made to affirm a revision that never
# existed. That is the difference between shipping evidence and forging it, and
# it is why the answer is not a stub that returns true.
commits="./build-commits.pack"
rm -f "$commits"
if ! git rev-list --all | git pack-objects --stdout > "$commits" 2>/dev/null; then
  echo "docker build REFUSED: could not pack this repository's commit objects." >&2
  echo "  Without them the container grader cannot attribute a single row and" >&2
  echo "  would publish a registry with every verdict stripped." >&2
  rm -f "$commits"
  exit 1
fi
if [ ! -s "$commits" ]; then
  echo "docker build REFUSED: the commit pack came out empty." >&2
  rm -f "$commits"
  exit 1
fi
echo "docker build: packed $(git rev-list --all | wc -l | tr -d ' ') commit objects ($(wc -c < "$commits" | tr -d ' ') bytes)" >&2

echo "docker build: stamping commit $rev into $tag" >&2
# The OCI label is what ops/oracle-verify.sh reads back. GIT_REV goes into the
# binary via ldflags and is TRUSTED there (a container cannot check it); the
# label is the same claim recorded where the deploy check can compare it
# against both the source tree and the running process.
docker build \
  --build-arg GIT_REV="$rev" \
  --build-arg NEXT_PUBLIC_SITE_URL="${NEXT_PUBLIC_SITE_URL:-}" \
  --build-arg NEXT_PUBLIC_SIGNALDECK_PUBLIC="${NEXT_PUBLIC_SIGNALDECK_PUBLIC:-0}" \
  --label "org.opencontainers.image.revision=$rev" \
  -t "$tag" "$@" .
