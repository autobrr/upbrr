## syntax=docker/dockerfile:1.7

ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG BUILD_ID=

FROM --platform=$BUILDPLATFORM node:20-bookworm AS frontend

WORKDIR /src/gui/frontend

COPY gui/frontend/package.json gui/frontend/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile

COPY gui/frontend/ ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.26.3-bookworm AS cli-builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
        go mod download

COPY . .

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

FROM golang:1.26.3-bookworm AS gui-builder

WORKDIR /src

ARG VERSION=dev
ARG BUILD_ID=

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    libgtk-3-dev \
    libwebkit2gtk-4.1-dev \
    libglib2.0-dev \
    libx11-dev \
    libxkbcommon-dev \
    libxrandr-dev \
    libxinerama-dev \
    libxcursor-dev \
    libxi-dev \
    pkg-config \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=frontend /src/gui/frontend/dist ./gui/frontend/dist

RUN --mount=type=cache,target=/root/.cache/go-build \
        go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildIdentifier=${BUILD_ID}" -o /out/upbrr-gui ./gui

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgtk-3-0 \
    libwebkit2gtk-4.1-0 \
    libglib2.0-0 \
    libx11-6 \
    libxkbcommon0 \
    libxrandr2 \
    libxinerama1 \
    libxcursor1 \
    libxi6 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=cli-builder /out/upbrr /usr/local/bin/upbrr
COPY --from=gui-builder /out/upbrr-gui /usr/local/bin/upbrr-gui

ENTRYPOINT ["/usr/local/bin/upbrr"]
