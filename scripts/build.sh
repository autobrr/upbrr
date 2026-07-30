#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
echo "Building WebUI..."
(
  cd webui
  pnpm install --frozen-lockfile
  pnpm run build
)

echo "Syncing embedded WebUI assets..."
sh scripts/sync-webui-assets.sh

echo "Building upbrr binary..."
mkdir -p dist
GOOS="" GOARCH="" go build -o dist/upbrr ./cmd/upbrr

echo "Done. Binary: dist/upbrr"
