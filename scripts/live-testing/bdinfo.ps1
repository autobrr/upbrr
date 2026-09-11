#Requires -Version 7.0
# Private, source-bound BDInfo reports shared by otherwise isolated live profiles.

function Get-CaseBDMVPlaylists($Case) {
  if (-not $Case.Contains('bdmv_selection')) { return }
  $selection = $Case.bdmv_selection
  if ($Case.input_shape -ne 'disc-directory' -or $selection -isnot [System.Collections.IDictionary] -or
      $selection.playlists -isnot [System.Collections.IList] -or $selection.playlists.Count -eq 0 -or
      $selection.source_fingerprint -isnot [string] -or $selection.source_fingerprint -cnotmatch '^[a-f0-9]{64}$') { throw 'corpus_bdmv_selection_invalid' }
  $seen = @{}
  $scoped = $null
  foreach ($playlist in $selection.playlists) {
    if ($playlist -isnot [string] -or $playlist -cnotmatch '^(disc-[a-f0-9]{64}:)?[0-9]{5}\.[mM][pP][lL][sS]$' -or $seen.ContainsKey($playlist)) { throw 'corpus_bdmv_selection_invalid' }
    $hasDisc = $playlist.Contains(':')
    if ($null -ne $scoped -and $scoped -ne $hasDisc) { throw 'corpus_bdmv_selection_invalid' }
    $scoped = $hasDisc
    $seen[$playlist] = $true
    if ($hasDisc) { $parts = $playlist.Split(':'); $parts[0] + ':' + $parts[1].ToUpperInvariant() }
    else { $playlist.ToUpperInvariant() }
  }
}

function Get-CaseBDMVDiscs($Case) {
  $source = [IO.Path]::GetFullPath($Case.input_path)
  $pending = [Collections.Generic.Stack[string]]::new()
  $pending.Push($source)
  while ($pending.Count -gt 0) {
    $directory = $pending.Pop()
    if ((Get-Item -LiteralPath $directory -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'source_membership_reparse_point' }
    $name = [IO.Path]::GetFileName($directory.TrimEnd('\', '/'))
    if ($name -ieq 'BDMV') {
      $relative = [IO.Path]::GetRelativePath($source, $directory)
      if ($relative -eq '.') { $relative = $name }
      # Match sourcelayout.newDiscoveredDisc and layout.DiscTempDir.
      $id = 'disc-' + (Get-TextHash ('BDMV' + [char]0 + $relative.Replace('\', '/').ToLowerInvariant()))
      @{ id = $id; root = $directory }
      continue
    }
    if ($name -iin @('VIDEO_TS', 'HVDVD_TS')) { continue }
    foreach ($child in @(Get-ChildItem -LiteralPath $directory -Directory -Force)) { $pending.Push($child.FullName) }
  }
}

function Get-CaseBDMVScopedPlaylists($Case) {
  $playlists = @(Get-CaseBDMVPlaylists $Case)
  if ($playlists.Count -eq 0) { return }
  $discs = @(Get-CaseBDMVDiscs $Case)
  if ($discs.Count -eq 0) { throw 'bdmv_disc_missing' }
  $selected = @()
  foreach ($playlist in $playlists) {
    if ($playlist.Contains(':')) {
      if ($discs.Count -eq 1) { throw 'bdmv_disc_scope_unexpected' }
      $parts = $playlist.Split(':')
      $disc = @($discs | Where-Object id -CEQ $parts[0])
      if ($disc.Count -ne 1) { throw 'bdmv_disc_missing' }
      $file = $parts[1]
    } else {
      if ($discs.Count -ne 1) { throw 'bdmv_disc_scope_required' }
      $disc = $discs; $file = $playlist
    }
    if (-not (Test-Path -LiteralPath (Join-Path $disc[0].root "PLAYLIST/$file") -PathType Leaf)) { throw 'bdmv_playlist_missing' }
    $selected += $disc[0].id + ':' + $file
  }
  if (@($selected | ForEach-Object { $_.Split(':')[0] } | Select-Object -Unique).Count -ne $discs.Count) { throw 'bdmv_disc_selection_incomplete' }
  $selected
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
    if ($playlist -cnotmatch '^(disc-[a-f0-9]{64}):([0-9]{5}\.MPLS)$') { throw 'bdinfo_playlist_key_invalid' }
    $discID = $Matches[1]; $file = $Matches[2]
    foreach ($prefix in @('BD_SUMMARY_', 'BD_SUMMARY_EXT_', 'BD_SUMMARY_FULL_')) { "discs/$discID/${prefix}${file}.txt" }
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
    $names = @(Get-BDInfoReportNames @($playlist))
    $file = $playlist.Split(':')[1]
    $summary = Get-Content -LiteralPath (Join-Path $Directory $names[0]) -Raw
    $matches = [regex]::Matches($summary, '(?mi)^Playlist:\s*([^\r\n]+?)\s*$')
    if ($matches.Count -ne 1 -or $matches[0].Groups[1].Value.Trim() -ine $file) { throw 'bdinfo_report_playlist_mismatch' }
    if ((Get-Item -LiteralPath (Join-Path $Directory $names[2])).Length -eq 0) { throw 'bdinfo_full_report_empty' }
  }
  $reports
}

function Restore-BDInfoReports($Entry, $Profile, $PrivateRoot, [string]$ScannerFingerprint) {
  if (@(Get-CaseBDMVPlaylists $Entry.case).Count -eq 0) { return }
  if ((Get-SourceFingerprint $Entry.case).fingerprint -cne $Entry.stat.fingerprint) { throw 'source_changed_during_run' }
  $playlists = @(Get-CaseBDMVScopedPlaylists $Entry.case)
  $key = Get-TextHash ('2|' + $Entry.stat.fingerprint + '|' + $ScannerFingerprint + '|' + ($playlists -join ','))
  $directory = Assert-PrivatePath (Join-Path $PrivateRoot "bdinfo/$key") $PrivateRoot
  $target = Assert-PrivatePath (Join-Path (Split-Path -Parent $Profile.dbPath) ('tmp/' + (Get-BDInfoTempName $Entry.case))) $Profile.runDir
  $cache = @{ directory = $directory; target = $target; sourceFingerprint = $Entry.stat.fingerprint; scannerFingerprint = $ScannerFingerprint; playlists = $playlists; restored = $false }
  $manifestPath = Assert-PrivatePath (Join-Path $directory 'manifest.private.json') $PrivateRoot
  if (-not (Test-Path -LiteralPath $manifestPath)) { return $cache }
  $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json -AsHashtable -NoEnumerate
  if ($manifest -isnot [System.Collections.IDictionary] -or $manifest.version -isnot [long] -or $manifest.version -ne 2 -or
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
    New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
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
    New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $Cache.target $report.name) -Destination $destination -Force
    if ((Get-FileHash -LiteralPath $destination).Hash -cne $report.sha256) { throw 'bdinfo_report_changed_during_save' }
  }
  $manifest = @{ version = 2; sourceFingerprint = $Cache.sourceFingerprint; scannerFingerprint = $Cache.scannerFingerprint; playlists = $Cache.playlists; reports = $reports; producerBinarySHA256 = $BinarySHA256 }
  Write-PrivateJson (Join-Path $Cache.directory 'manifest.private.json') $manifest
  $manifest
}
