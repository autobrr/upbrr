$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$wailsCli = "github.com/wailsapp/wails/v2/cmd/wails@v2.12.0"

Write-Host "Building frontend..."
Push-Location "gui/frontend"
pnpm install
pnpm run build
Pop-Location

Write-Host "Syncing embedded GUI assets..."
$assetsPath = Join-Path $root "internal/guiapp/assets"
New-Item -ItemType Directory -Force -Path $assetsPath | Out-Null
Get-ChildItem -Path $assetsPath -Force -ErrorAction SilentlyContinue |
  Where-Object { $_.Name -ne ".keep" } |
  Remove-Item -Recurse -Force
Copy-Item "gui/frontend/dist/*" $assetsPath -Recurse -Force

Write-Host "Building CLI binary..."
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

Write-Host "Building GUI binary (portable exe)..."
Push-Location "gui"
go run $wailsCli build -platform windows/amd64
Pop-Location

Write-Host "Done. Binaries: dist/upbrr.exe (CLI) and gui/build/bin/upbrr-gui.exe (GUI)"
