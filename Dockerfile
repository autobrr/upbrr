# syntax=docker/dockerfile:1

# GO_VERSION is the only global ARG: it is a custom arg interpolated into a FROM
# below. The predefined platform args (BUILDPLATFORM, TARGET*) are auto-available
# to FROM, and VERSION/BUILD_ID are declared inside the build stage where they are
# consumed (see below), so they do not need global declarations.
ARG GO_VERSION=1.26.4

FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend

WORKDIR /src/gui/frontend

COPY gui/frontend/package.json gui/frontend/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile

COPY gui/frontend/ ./
RUN pnpm run build:bundle

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS cli-builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
        go mod download

COPY . .

# Embed the built web UI so `upbrr serve` can serve it. Mirrors
# scripts/sync-frontend-assets.ps1 used by the binary release workflow.
COPY --from=frontend /src/gui/frontend/dist/ ./internal/guiapp/assets/

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG BUILD_ID=

RUN --mount=type=cache,target=/root/.cache/go-build \
        set -eu; \
        goarm=""; \
        if [ "$TARGETARCH" = "arm" ] && [ -n "$TARGETVARIANT" ]; then \
            goarm="${TARGETVARIANT#v}"; \
        fi; \
        CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOARM="$goarm" \
            go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildIdentifier=${BUILD_ID}" -o /out/upbrr ./cmd/upbrr

FROM alpine:3.23

# curl is used by the HEALTHCHECK below; the alpine base ships no HTTP client.
RUN apk add --no-cache ca-certificates ffmpeg mesa-vulkan-swrast vulkan-loader curl

# Run as a non-root user and give it a writable config dir. chown happens before
# VOLUME so anonymous/named volumes inherit the ownership.
RUN addgroup -g 1000 upbrr \
    && adduser -u 1000 -G upbrr -s /sbin/nologin -D -H upbrr \
    && mkdir -p /config \
    && chown upbrr:upbrr /config

COPY --from=cli-builder /out/upbrr /usr/local/bin/upbrr

# Persist config + database under /config (upbrr resolves XDG_CONFIG_HOME/upbrr).
ENV XDG_CONFIG_HOME=/config
# Probed by the HEALTHCHECK below. Uses loopback (works regardless of the serve bind
# host); override this env if you change the served --port so the probe stays accurate.
ENV UPBRR_HEALTHCHECK_URL=http://127.0.0.1:7480/api/auth/status
VOLUME /config
EXPOSE 7480
USER upbrr

HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
    CMD curl -fsS "$UPBRR_HEALTHCHECK_URL"

ENTRYPOINT ["/usr/local/bin/upbrr"]
# Default to serving the web UI on all interfaces so published ports are reachable.
# Override the command (e.g. `docker run ... upbrr <cli args>`) for one-off CLI use.
CMD ["serve", "--host", "0.0.0.0"]
