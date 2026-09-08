#Requires -Version 7.0
# Private, source-bound BDInfo reports shared by otherwise isolated live profiles.

function Get-CaseBDMVPlaylists($Case) {
  if (-not $Case.Contains('bdmv_selection')) { return }
  $selection = $Case.bdmv_selection
  if ($Case.input_shape -ne 'disc-directory' -or $selection -isnot [System.Collections.IDictionary] -or
      $selection.playlists -isnot [System.Collections.IList] -or $selection.playlists.Count -eq 0 -or
      $selection.source_fingerprint -isnot [string] -or $selection.source_fingerprint -cnotmatch '^[a-f0-9]{64}$') { throw 'corpus_bdmv_selection_invalid' }
  $seen = @{}
  foreach ($playlist in $selection.playlists) {
    if ($playlist -isnot [string] -or $playlist -notmatch '^[0-9]{5}\.mpls$' -or $seen.ContainsKey($playlist)) { throw 'corpus_bdmv_selection_invalid' }
    $seen[$playlist] = $true
    $playlist.ToUpperInvariant()
  }
}

function Get-BDInfoTempName($Case) {
  # Match pathing/layout.ReleaseTempBaseFor: one underscore per non-ASCII rune.
  $base = [IO.Path]::GetFullPath($Case.input_path.Trim()).Replace('\', '/').TrimEnd('/').Split('/')[-1]
  [regex]::Replace($base, '[\uD800-\uDBFF][\uDC00-\uDFFF]|[^a-zA-Z0-9._-]', '_')
}

function Get-BDInfoScannerFingerprint {
  # Build timestamps and unrelated application edits must not cause another scan.
  $paths = @('go.mod', 'go.sum', 'internal/metadata/service.go', 'internal/services/db/paths.go', 'internal/sourcelayout/layout.go',
    'internal/pathing/pathutil.go', 'internal/pathing/layout/release_tmp.go', 'internal/pathing/layout/bdinfo.go')
  foreach ($dir in @('internal/services/bdinfo', 'internal/metadata/discparse')) {
    $paths += @(Get-ChildItem -LiteralPath (Join-Path $script:RepoRoot $dir) -File -Filter '*.go' |
      Where-Object Name -NotLike '*_test.go' | ForEach-Object { [IO.Path]::GetRelativePath($script:RepoRoot, $_.FullName).Replace('\', '/') })
  }
  Get-TextHash ((@($paths | Sort-Object | ForEach-Object { $_ + ':' + (Get-FileHash -LiteralPath (Join-Path $script:RepoRoot $_)).Hash })) -join "`n")
}

function Get-BDInfoReportNames([string[]]$Playlists) {
  foreach ($playlist in $Playlists) {
    foreach ($prefix in @('BD_SUMMARY_', 'BD_SUMMARY_EXT_', 'BD_SUMMARY_FULL_')) { "${prefix}${playlist}.txt" }
  }
}

function Get-BDInfoReports($Directory, [string[]]$Playlists, $PrivateRoot) {
  $reports = @()
  foreach ($name in @(Get-BDInfoReportNames $Playlists)) {
    $path = Assert-PrivatePath (Join-Path $Directory $name) $PrivateRoot
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return }
    $reports += @{ name = $name; sha256 = (Get-FileHash -LiteralPath $path).Hash }
  }
  foreach ($playlist in $Playlists) {
    $summary = Get-Content -LiteralPath (Join-Path $Directory "BD_SUMMARY_${playlist}.txt") -Raw
    $matches = [regex]::Matches($summary, '(?mi)^Playlist:\s*([^\r\n]+?)\s*$')
    if ($matches.Count -ne 1 -or $matches[0].Groups[1].Value.Trim() -ine $playlist) { throw 'bdinfo_report_playlist_mismatch' }
    if ((Get-Item -LiteralPath (Join-Path $Directory "BD_SUMMARY_FULL_${playlist}.txt")).Length -eq 0) { throw 'bdinfo_full_report_empty' }
  }
  $reports
}

function Restore-BDInfoReports($Entry, $Profile, $PrivateRoot, [string]$ScannerFingerprint) {
  $playlists = @(Get-CaseBDMVPlaylists $Entry.case)
  if ($playlists.Count -eq 0) { return }
  if ((Get-SourceFingerprint $Entry.case).fingerprint -cne $Entry.stat.fingerprint) { throw 'source_changed_during_run' }
  $key = Get-TextHash ('1|' + $Entry.stat.fingerprint + '|' + $ScannerFingerprint + '|' + ($playlists -join ','))
  $directory = Assert-PrivatePath (Join-Path $PrivateRoot "bdinfo/$key") $PrivateRoot
  $target = Assert-PrivatePath (Join-Path (Split-Path -Parent $Profile.dbPath) ('tmp/' + (Get-BDInfoTempName $Entry.case))) $Profile.runDir
  $cache = @{ directory = $directory; target = $target; sourceFingerprint = $Entry.stat.fingerprint; scannerFingerprint = $ScannerFingerprint; playlists = $playlists; restored = $false }
  $manifestPath = Assert-PrivatePath (Join-Path $directory 'manifest.private.json') $PrivateRoot
  if (-not (Test-Path -LiteralPath $manifestPath)) { return $cache }
  $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json -AsHashtable -NoEnumerate
  if ($manifest -isnot [System.Collections.IDictionary] -or $manifest.version -isnot [long] -or $manifest.version -ne 1 -or
      $manifest.sourceFingerprint -isnot [string] -or $manifest.sourceFingerprint -cne $cache.sourceFingerprint -or
      $manifest.scannerFingerprint -isnot [string] -or $manifest.scannerFingerprint -cne $ScannerFingerprint -or
      $manifest.playlists -isnot [System.Collections.IList] -or $manifest.playlists.Count -ne $playlists.Count) { throw 'bdinfo_cache_identity_mismatch' }
  for ($index = 0; $index -lt $playlists.Count; $index++) {
    if ($manifest.playlists[$index] -isnot [string] -or $manifest.playlists[$index] -cne $playlists[$index]) { throw 'bdinfo_cache_identity_mismatch' }
  }
  if ($manifest.producerBinarySHA256 -isnot [string] -or $manifest.producerBinarySHA256 -notmatch '\A[0-9a-f]{64}\z') { throw 'bdinfo_cache_producer_invalid' }
  $reports = @(Get-BDInfoReports $directory $playlists $PrivateRoot)
  if ($reports.Count -ne $playlists.Count * 3 -or $manifest.reports -isnot [System.Collections.IList] -or $manifest.reports.Count -ne $reports.Count) { throw 'bdinfo_cache_incomplete' }
  for ($index = 0; $index -lt $reports.Count; $index++) {
    $record = $manifest.reports[$index]
    if ($record -isnot [System.Collections.IDictionary] -or $record.name -isnot [string] -or $record.sha256 -isnot [string] -or
        $record.sha256 -notmatch '\A[0-9a-f]{64}\z' -or $reports[$index].name -cne $record.name -or $reports[$index].sha256 -cne $record.sha256) { throw 'bdinfo_cache_report_changed' }
  }
  New-Item -ItemType Directory -Path $target -Force | Out-Null
  foreach ($report in $reports) {
    $destination = Assert-PrivatePath (Join-Path $target $report.name) $Profile.runDir
    Copy-Item -LiteralPath (Join-Path $directory $report.name) -Destination $destination -Force
    if ((Get-FileHash -LiteralPath $destination).Hash -cne $report.sha256) { throw 'bdinfo_cache_report_changed' }
  }
  $cache.restored = $true
  $cache.reports = $reports
  $cache.manifest = $manifest
  $cache
}

function Save-BDInfoReports($Cache, $Entry, $PrivateRoot, [string]$BinarySHA256) {
  if (-not $Cache) { return }
  if ((Get-SourceFingerprint $Entry.case).fingerprint -cne $Cache.sourceFingerprint) { throw 'source_changed_during_run' }
  $reports = @(Get-BDInfoReports $Cache.target $Cache.playlists $PrivateRoot)
  if ($reports.Count -ne $Cache.playlists.Count * 3) { return }
  if ($Cache.restored) {
    for ($index = 0; $index -lt $reports.Count; $index++) {
      if ($reports[$index].sha256 -cne $Cache.reports[$index].sha256) { throw 'restored_bdinfo_reports_changed' }
    }
    return $Cache.manifest
  }
  if ($BinarySHA256 -notmatch '\A[0-9a-f]{64}\z') { throw 'bdinfo_cache_producer_invalid' }
  New-Item -ItemType Directory -Path $Cache.directory -Force | Out-Null
  foreach ($report in $reports) {
    $destination = Assert-PrivatePath (Join-Path $Cache.directory $report.name) $PrivateRoot
    Copy-Item -LiteralPath (Join-Path $Cache.target $report.name) -Destination $destination -Force
    if ((Get-FileHash -LiteralPath $destination).Hash -cne $report.sha256) { throw 'bdinfo_report_changed_during_save' }
  }
  $manifest = @{ version = 1; sourceFingerprint = $Cache.sourceFingerprint; scannerFingerprint = $Cache.scannerFingerprint; playlists = $Cache.playlists; reports = $reports; producerBinarySHA256 = $BinarySHA256 }
  Write-PrivateJson (Join-Path $Cache.directory 'manifest.private.json') $manifest
  $manifest
}
