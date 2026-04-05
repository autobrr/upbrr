#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

echo "Building frontend..."
(
  cd gui/frontend
  pnpm install
  pnpm run build
)

echo "Syncing embedded GUI assets..."
mkdir -p internal/guiapp/assets
find internal/guiapp/assets -mindepth 1 ! -name '.keep' -exec rm -rf {} +
cp -R gui/frontend/dist/* internal/guiapp/assets/

echo "Building CLI binary..."
mkdir -p dist
GOOS="" GOARCH="" go build -o dist/upbrr ./cmd/upbrr
if [[ -d bin ]]; then
  echo "Syncing optional bundled tools to CLI output..."
  rm -rf dist/bin
  cp -R bin dist/bin
else
  echo "Skipping optional bundled tools: no top-level bin directory found."
fi

echo "Building GUI binary..."
go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
(
  cd gui
  wails build
)

echo "Done. Binaries: dist/upbrr (CLI) and gui/build/bin/upbrr-gui (GUI)"
