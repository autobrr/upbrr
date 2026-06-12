$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$assetsPath = Join-Path $root "internal/guiapp/assets"

New-Item -ItemType Directory -Force -Path $assetsPath | Out-Null
Get-ChildItem -Path $assetsPath -Force -ErrorAction SilentlyContinue |
  Where-Object { $_.Name -ne ".keep" } |
  Remove-Item -Recurse -Force
Copy-Item (Join-Path $root "gui/frontend/dist/*") $assetsPath -Recurse -Force
