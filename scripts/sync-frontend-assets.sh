#!/usr/bin/env sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
assets_path="$root/internal/guiapp/assets"

mkdir -p "$assets_path"
find "$assets_path" -mindepth 1 ! -name ".keep" -exec rm -rf {} +
cp -R "$root/gui/frontend/dist/." "$assets_path/"
