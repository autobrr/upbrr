param(
    [ValidateSet('all', 'core', 'core-hdr', 'core-decoded', 'workflow', 'media', 'metadata', 'other')]
    [string]$Shard = 'all'
)

$ErrorActionPreference = 'Stop'
$packages = @(go list ./...)
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$packageShard = if ($Shard -in @('core-hdr', 'core-decoded')) { 'core' } else { $Shard }

$selected = @($packages | Where-Object {
    $owner = switch -Regex ($_) {
        '/internal/core$' { 'core'; break }
        '/internal/(releaseworkflow|preparedrelease|webserver)(/|$)' { 'workflow'; break }
        '/internal/(services/(audioanalysis|hdranalysis|screenshots|dvdmenus)|imagehosting|imageevidence)(/|$)' { 'media'; break }
        '/internal/(metadata|externalidentity|configstore|config|services/db)(/|$)' { 'metadata'; break }
        default { 'other' }
    }
    $Shard -eq 'all' -or $owner -eq $packageShard
})
if ($selected.Count -eq 0) { throw "Go test shard '$Shard' has no packages" }
$selected
