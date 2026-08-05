$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
Write-Host "Building WebUI..."
Push-Location "webui"
pnpm install --frozen-lockfile
if ($LASTEXITCODE -ne 0) {
  throw "pnpm install failed with exit code $LASTEXITCODE"
}
pnpm run build
if ($LASTEXITCODE -ne 0) {
  throw "pnpm build failed with exit code $LASTEXITCODE"
}
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
if ($LASTEXITCODE -ne 0) {
  throw "go build failed with exit code $LASTEXITCODE"
}

Write-Host "Done. Binary: dist/upbrr.exe"
