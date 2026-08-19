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
 && CGO_ENABLED=0 go build -o /out/sdmaint ./cmd/sdmaint

# ---- stage 2: the Next.js app ---------------------------------------------
FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# ---- stage 3: runtime ------------------------------------------------------
FROM node:24-alpine
RUN apk add --no-cache ca-certificates tini
WORKDIR /app

COPY --from=daemon-build /out/signaldeckd /usr/local/bin/signaldeckd
COPY --from=daemon-build /out/sdmaint     /usr/local/bin/sdmaint
COPY --from=web-build /src/web/.next      ./web/.next
COPY --from=web-build /src/web/public     ./web/public
COPY --from=web-build /src/web/node_modules ./web/node_modules
COPY --from=web-build /src/web/package.json ./web/package.json

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
