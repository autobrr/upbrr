$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
Write-Host "Building WebUI..."
Push-Location "webui"
pnpm install --frozen-lockfile
pnpm run build
Pop-Location

Write-Host "Syncing embedded WebUI assets..."
& (Join-Path $PSScriptRoot "sync-webui-assets.ps1")

Write-Host "Building upbrr binary..."
$distPath = Join-Path $root "dist"
if (-not (Test-Path $distPath)) {
  New-Item -ItemType Directory -Force -Path $distPath | Out-Null
}
$cliOut = Join-Path $distPath "upbrr.exe"
go build -o $cliOut ./cmd/upbrr
$sourceBin = Join-Path $root "bin"
if (Test-Path $sourceBin) {
  Write-Host "Syncing optional bundled tools to CLI output..."
  $distBin = Join-Path $distPath "bin"
  if (Test-Path $distBin) {
    Remove-Item $distBin -Recurse -Force
  }
  Copy-Item $sourceBin $distBin -Recurse -Force
} else {
  Write-Host "Skipping optional bundled tools: no top-level bin directory found."
}

Write-Host "Done. Binary: dist/upbrr.exe"
