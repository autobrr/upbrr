$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

Write-Host "Building frontend..."
Push-Location "gui/frontend"
pnpm install
pnpm run build
Pop-Location

Write-Host "Syncing embedded GUI assets..."
$assetsPath = Join-Path $root "internal/guiapp/assets"
if (Test-Path $assetsPath) {
  Remove-Item $assetsPath -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $assetsPath | Out-Null
Copy-Item "gui/frontend/dist/*" $assetsPath -Recurse -Force
# Keep placeholder tracked so git does not report it as deleted after builds.
New-Item -ItemType File -Force -Path (Join-Path $assetsPath ".keep") | Out-Null

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
go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
Push-Location "gui"
wails build -platform windows/amd64
Pop-Location

Write-Host "Done. Binaries: dist/upbrr.exe (CLI) and gui/build/bin/upbrr-gui.exe (GUI)"
