# SignalDeck — daemon + web in one image.
#
# Two processes in one container is not the shape you'd want at scale, but this
# is a demo: the web app proxies /api/* to the daemon over loopback, and the
# daemon owns a SQLite file that only one process may write. Splitting them
# across containers would mean sharing that file over a network volume, which
# is the one thing SQLite is bad at. One container, one writer, one volume.
#
# The daemon REFUSES to start from a build it cannot attribute to a commit
# (rows it writes could not otherwise be graded), so the revision is passed in
# at build time and baked via ldflags. Build with:
#
#   ops/docker-build.sh                 # or: ops/docker-build.sh <tag> [flags]
#
# Use that wrapper, not `docker build` directly. It supplies GIT_REV and — the
# part a raw invocation cannot do — refuses a dirty tree first. GIT_REV is
# TRUSTED, never checked: internal/lineage sets modified=false whenever it falls
# back to ldflagsRev, because a container cannot see the tree it was built from.
# So `docker build --build-arg GIT_REV=$(git rev-parse HEAD) .` against
# uncommitted code yields an image stamped with HEAD's SHA, reporting
# modified=false, whose provenance claim is false and undetectable downstream.
# ops/signaldeck-ctl.sh already refuses that on the native path; the wrapper is
# the same refusal here. ops/test-docker-build.sh is its self-test.
#
# Without GIT_REV the image builds fine and then refuses to serve, loudly. That
# is deliberate — a demo publishing ungradable numbers is worse than no demo.

# ---- stage 1: the Go daemon ------------------------------------------------
FROM golang:1.26-alpine AS daemon-build
WORKDIR /src/daemon
RUN apk add --no-cache git
COPY daemon/go.mod daemon/go.sum ./
RUN go mod download
COPY daemon/ ./
ARG GIT_REV=""
RUN CGO_ENABLED=0 go build \
      -ldflags "-X github.com/nyaungnicholas-wq/signaldeck/internal/lineage.ldflagsRev=${GIT_REV}" \
      -o /out/signaldeckd ./cmd/signaldeckd \
 && CGO_ENABLED=0 go build -o /out/sdmaint ./cmd/sdmaint \
 && CGO_ENABLED=0 go build -o /out/collapsecheck ./cmd/collapsecheck

# ---- stage 2: the Next.js app ---------------------------------------------
FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1
# NEXT_PUBLIC_* is INLINED AT BUILD TIME, so this has to be an ARG here — setting
# it in the container's environment later does nothing (web/src/lib/site.ts says
# so, and it was verified the hard way). It feeds robots.txt's Sitemap: line and
# every URL in sitemap.xml. Left unset the build falls back to
# http://localhost:8323, which is deliberate — a sitemap advertising a domain
# this build is not served from looks right and gets indexed — but it means a
# published deployment that does not pass this ships a sitemap no crawler can
# use. ops/docker-build.sh forwards it; DEPLOY.md tells the operator to set it.
ARG NEXT_PUBLIC_SITE_URL=""
ENV NEXT_PUBLIC_SITE_URL=${NEXT_PUBLIC_SITE_URL}
# The SAME build-time inlining rule, and the flag it was never applied to.
#
# NEXT_PUBLIC_SIGNALDECK_PUBLIC is documented in .env.example and DEPLOY.md and
# read in two places -- AuthGate.tsx, which sends an unauthenticated visitor to
# "/" instead of "/login", and proof/page.tsx, which suppresses operator-only
# remediation copy. Neither was ever reachable from a container build: there was
# no ARG, so the bundle inlined `undefined` and every image shipped in PRIVATE
# mode. Setting it in the runtime environment does nothing, for exactly the
# reason the comment above gives about SITE_URL.
#
# The consequence on a published deployment is the one that matters: a stranger
# arriving at the front door of a site whose entire argument is "check my
# claims yourself" is bounced to a sign-in form, and the receipts page shows
# them instructions written for the operator.
#
# DEFAULT IS PRIVATE, deliberately. An image that silently decided it was public
# would open the front door on any deployment that forgot the flag, and the
# wrong direction to be wrong in is obvious. ops/docker-build.sh refuses to
# build a PUBLISHABLE image (one carrying a real NEXT_PUBLIC_SITE_URL) without
# an explicit choice, so "forgot the flag" cannot quietly ship either way.
ARG NEXT_PUBLIC_SIGNALDECK_PUBLIC="0"
ENV NEXT_PUBLIC_SIGNALDECK_PUBLIC=${NEXT_PUBLIC_SIGNALDECK_PUBLIC}
RUN npm run build

# ---- stage 3: runtime ------------------------------------------------------
FROM node:24-alpine
# python3 is here so the GRADER can run in the container. Without it
# /api/accuracy is 503 REFUSED forever (the handler is fail-closed on an
# unreadable registry and on a stale grader heartbeat), so the honesty
# page -- the product -- was permanently dead on every container deploy.
# DEPLOY.md told the operator to cron ops/accuracy-registry.sh, a 680-line
# dev-box job needing git, a checkout and a README to rewrite; none of that
# exists here. ops/grade.sh is the container-sized replacement.
#
# No pip and no venv: accuracy_registry.py, selection_honesty.py,
# grader_heartbeat.py and backfill_delistings.py import only the standard
# library. requirements-quant.txt (numpy/pandas/scipy) is for the research
# tools, which do not run here.
# git is here for the GRADER's revision gate, not for a checkout. See the
# commit-object store below: tools/accuracy_registry.py is pinned by hash on the
# pre-registration chain and runs `git cat-file -e <rev>^{commit}` against every
# revision in the database, so without git every historical row is
# unattributable and every verdict is stripped.
RUN apk add --no-cache ca-certificates tini python3 git
WORKDIR /app

COPY --from=daemon-build /out/signaldeckd /usr/local/bin/signaldeckd
COPY --from=daemon-build /out/sdmaint     /usr/local/bin/sdmaint
COPY --from=daemon-build /out/collapsecheck /usr/local/bin/collapsecheck
COPY --from=web-build /src/web/.next      ./web/.next
COPY --from=web-build /src/web/public     ./web/public
COPY --from=web-build /src/web/node_modules ./web/node_modules
COPY --from=web-build /src/web/package.json ./web/package.json

# The grader, and the document whose hash it checks against the chain.
# tools/*.py only -- tools/alpha/ is the research corpus and is
# .dockerignored. PREREGISTRATION.md sits at /app so REPO_ROOT resolves as
# it does in a checkout, and so ops/grade.sh can compare its sha256 to the
# newest prereg-document record before publishing anything.
COPY tools/*.py         /app/tools/
COPY PREREGISTRATION.md /app/PREREGISTRATION.md
COPY ops/grade.sh       /usr/local/bin/grade.sh
RUN chmod +x /usr/local/bin/grade.sh

# THE BUILD MANIFEST, and the seal that completes it.
#
# ops/docker-build.sh emits build-manifest.json on the HOST, immediately before
# this build, hashing the files that decide a verdict against the tree the
# operator reviewed. That is the half git can do and this image cannot.
#
# This COPY is deliberately REQUIRED, not optional: a hand `docker build` with
# no manifest present fails here rather than producing an image that cannot be
# bound to any source. Use ops/docker-build.sh.
#
# `seal` then adds what the host could not know -- the hashes of binaries
# compiled during this build -- so a binary swapped inside a running container
# is caught too. It must come after both the tools COPY (for python) and the
# binary COPYs above.
COPY build-manifest.json /app/build-manifest.json
RUN python3 /app/tools/build_manifest.py seal \
      --manifest /app/build-manifest.json --root /

# THE COMMIT OBJECTS, so the grader's revision gate can answer honestly.
#
# The manifest above binds the BYTES of this image. It cannot answer the other
# question the grader asks on every run: does the revision stamped on each
# historical forecast row name a commit that exists? revision_resolvable() in
# the hash-pinned grader runs `git cat-file -e <rev>^{commit}` in the repo root
# and treats "git could not be run" as False, which is the correct doctrine —
# an unverifiable provenance claim must block a verdict. With no git and no
# objects it answered False for everything, so apply_revision_gate() stripped
# the verdict from every directional and structural row and a container grade
# published a registry with no verdicts in it.
#
# ops/docker-build.sh packs this repository's COMMIT objects — no trees, no
# blobs, well under a megabyte — and this unpacks them into an object store at
# the path the grader already looks in. Nothing here is asserted: git objects
# are content-addressed, so an object that hashes to a sha IS that commit, and a
# revision that was never committed still does not resolve. The store cannot be
# talked into saying yes.
#
# The last line is the build's own check on that claim: this image's revision
# must resolve in the store it ships, and an empty GIT_REV (a raw `docker build`)
# fails here rather than producing an image whose grades silently carry no
# verdicts.
ARG GIT_REV=""
COPY build-commits.pack /tmp/build-commits.pack
RUN git init -q /app \
 && mv /tmp/build-commits.pack /app/.git/objects/pack/build-commits.pack \
 && git -C /app index-pack /app/.git/objects/pack/build-commits.pack \
 && git -C /app cat-file -e "${GIT_REV}^{commit}" \
 && echo "signaldeck: commit store holds ${GIT_REV}"

# The accuracy page is a server component that reads data/accuracy_registry.json
# relative to the web app's cwd (/app/web), i.e. /app/data. Point that at the
# volume so whatever the grader writes is what the page renders. Without this
# the page degrades to "not readable on this deployment" and the honesty
# surface — the whole point of the project — shows nothing.
RUN ln -s /data /app/data

# The database lives on a mounted volume, never in the image layer: a 2.5 GB
# SQLite file baked into an image is both unshippable and immediately stale.
ENV SIGNALDECK_DB=/data/signaldeck.db \
    SIGNALDECK_HTTP=127.0.0.1:8322 \
    SIGNALDECK_DAEMON=http://127.0.0.1:8322 \
    SIGNALDECK_LOG_FILE=/data/logs/signaldeckd.log \
    SIGNALDECK_REGISTRY=/data/accuracy_registry.json \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=8080
VOLUME ["/data"]
EXPOSE 8080

# The ONLY health probe used to live in fly.toml, and fly.toml itself invites
# Railway/Render/a plain VPS as alternatives — on any of those the container ran
# with no probe at all, so a half-dead stack (web up, daemon wedged) would keep
# serving. Hitting /api/health THROUGH the web app on 8080 exercises both halves:
# the Next server has to answer and its proxy has to reach the daemon on
# loopback. /api/health, not /api/ready: readiness is deliberately strict (it
# 503s when a worker is merely not delivering), and restarting a container over
# a model with no edge would be a restart loop over a true statement.
# wget is busybox's, already in the node:24-alpine base.
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1

COPY ops/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# tini reaps the daemon when the web process dies, so a crashed container
# actually exits instead of lingering with one half alive and healthchecks
# passing against a process that serves nothing.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
