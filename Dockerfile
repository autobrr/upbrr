# syntax=docker/dockerfile:1

ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG BUILD_ID=
ARG GO_VERSION=1.26.4

FROM --platform=$BUILDPLATFORM node:20-bookworm AS frontend

WORKDIR /src/gui/frontend

COPY gui/frontend/package.json gui/frontend/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile

COPY gui/frontend/ ./
RUN pnpm run build:bundle

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS cli-builder

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

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=mwader/static-ffmpeg:8.1.1 /ffmpeg /bin/
COPY --from=mwader/static-ffmpeg:8.1.1 /ffprobe /bin/

COPY --from=cli-builder /out/upbrr /usr/local/bin/upbrr

ENTRYPOINT ["/usr/local/bin/upbrr"]
