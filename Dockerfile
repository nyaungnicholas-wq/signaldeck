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
#   docker build --build-arg GIT_REV=$(git rev-parse HEAD) -t signaldeck .
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

# The database lives on a mounted volume, never in the image layer: a 2.5 GB
# SQLite file baked into an image is both unshippable and immediately stale.
ENV SIGNALDECK_DB=/data/signaldeck.db \
    SIGNALDECK_HTTP=127.0.0.1:8322 \
    SIGNALDECK_DAEMON=http://127.0.0.1:8322 \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=8080
VOLUME ["/data"]
EXPOSE 8080

COPY ops/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# tini reaps the daemon when the web process dies, so a crashed container
# actually exits instead of lingering with one half alive and healthchecks
# passing against a process that serves nothing.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
