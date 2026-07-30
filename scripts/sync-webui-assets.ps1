$ErrorActionPreference = "Stop"

$root = (Resolve-Path (Split-Path -Parent $PSScriptRoot)).Path
$assetsPath = [System.IO.Path]::GetFullPath((Join-Path $root "internal/webserver/assets"))
if (-not $assetsPath.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
  throw "Refusing to sync assets outside repository: $assetsPath"
}

New-Item -ItemType Directory -Force -Path $assetsPath | Out-Null
Get-ChildItem -Path $assetsPath -Force -ErrorAction SilentlyContinue |
  Where-Object { $_.Name -ne ".keep" } |
  Remove-Item -Recurse -Force
Copy-Item (Join-Path $root "webui/dist/*") $assetsPath -Recurse -Force
