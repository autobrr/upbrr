<#
.SYNOPSIS
    Synchronizes AI coding assistant instruction files across Copilot, Cursor,
    and Claude Code directories.

.DESCRIPTION
    Keeps rule/instruction files in sync so all LLM coding assistants see the
    same project guidance. Sync source: the file set selected by -Source
    (copilot, cursor, or claude). AGENTS.md provides shared project guidelines
    and is referenced by CLAUDE.md, but is not read or modified by this script.

    File mapping:
      Repo-wide:
        Copilot:  .github/copilot-instructions.md
        Cursor:   .cursor/rules/project.mdc
        Claude:   .claude/CLAUDE.md

      Scoped rules:
        Copilot:  .github/instructions/go.instructions.md
                  .github/instructions/frontend.instructions.md
        Cursor:   .cursor/rules/go.mdc
                  .cursor/rules/frontend.mdc
        Claude:   .claude/rules/go.md
                  .claude/rules/frontend.md

      Manual files (never overwritten by sync):
        .claude/rules/safety.md

.PARAMETER Source
    Which assistant directory to treat as source. Default: copilot.
    Valid values: copilot, cursor, claude.

.PARAMETER DryRun
    Preview changes without writing any files.

.EXAMPLE
    .\scripts\sync-ai-instructions.ps1
    .\scripts\sync-ai-instructions.ps1 -Source cursor
    .\scripts\sync-ai-instructions.ps1 -DryRun
#>

param(
    [ValidateSet("copilot", "cursor", "claude")]
    [string]$Source = "copilot",

    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Extract-Body {
    param([string]$FilePath)
    $lines = Get-Content $FilePath -Raw
    # Strip YAML frontmatter (--- ... ---)
    if ($lines -match "(?s)^---\r?\n.*?\r?\n---\r?\n(.*)$") {
        return $Matches[1]
    }
    return $lines
}

function Sync-RuleFile {
    param(
        [string]$SrcPath,
        [string]$DstPath,
        [string]$Frontmatter = ""
    )

    if (-not (Test-Path $SrcPath)) {
        Write-Host "  SKIP    $SrcPath (not found)"
        return
    }

    $body = Extract-Body -FilePath $SrcPath

    if ($Frontmatter) {
        $fm = $Frontmatter.TrimEnd("`r", "`n")
        $content = "$fm`n`n$body"
    } else {
        $content = $body
    }

    Write-SyncFile -DstPath $DstPath -Content $content
}

function Write-SyncFile {
    param(
        [string]$DstPath,
        [string]$Content
    )

    # Ensure trailing newline
    $Content = $Content.TrimEnd("`r", "`n") + "`n"

    if ($DryRun) {
        if (Test-Path $DstPath) {
            $existing = Get-Content $DstPath -Raw
            if ($existing -and ($existing.TrimEnd() -eq $Content.TrimEnd())) {
                Write-Host "  OK      $DstPath (identical)"
            } else {
                Write-Host "  WOULD   $DstPath"
            }
        } else {
            Write-Host "  WOULD   $DstPath (new)"
        }
        return
    }

    $dstDir = Split-Path -Parent $DstPath
    if (-not (Test-Path $dstDir)) {
        New-Item -ItemType Directory -Force -Path $dstDir | Out-Null
    }

    Set-Content -Path $DstPath -Value $Content -Encoding utf8NoBOM
    Write-Host "  SYNC    $DstPath"
}

function Strip-AgentsImport {
    param([string]$Content)
    # Remove @AGENTS.md line and any following blank lines
    $Content -replace "(?m)^@AGENTS\.md\s*\r?\n+", ""
}

Write-Host "=== AI Instructions Sync ==="
Write-Host "Source: $Source"
if ($DryRun) {
    Write-Host "Mode: dry-run (no changes will be made)"
}
Write-Host ""

# Frontmatter templates
$gocopilotFm = @"
---
description: Go backend code style, linting, and testing rules for all .go files
applyTo: ""**/*.go""
---
"@

$frontendcopilotFm = @"
---
description: Frontend code style, linting, and build rules for React/Vite/TypeScript files
applyTo: ""gui/frontend/**/*.{ts,tsx,css}""
---
"@

$gocursorFm = @"
---
description: Go backend code style, linting, and testing rules
globs: "**/*.go"
alwaysApply: false
---
"@

$frontendcursorFm = @"
---
description: Frontend code style and build rules for React/Vite/TypeScript
globs: "gui/frontend/**/*.ts,gui/frontend/**/*.tsx,gui/frontend/**/*.css"
alwaysApply: false
---
"@

$projectcursorFm = @"
---
description: Project-wide guidelines for upbrr - upload preparation and tracker submission tool
alwaysApply: true
---
"@

switch ($Source) {
    "copilot" {
        Write-Host "Syncing scoped rules: Copilot -> Cursor..."
        Sync-RuleFile `
            -SrcPath ".github/instructions/go.instructions.md" `
            -DstPath ".cursor/rules/go.mdc" `
            -Frontmatter $gocursorFm

        Sync-RuleFile `
            -SrcPath ".github/instructions/frontend.instructions.md" `
            -DstPath ".cursor/rules/frontend.mdc" `
            -Frontmatter $frontendcursorFm

        Write-Host ""
        Write-Host "Syncing scoped rules: Copilot -> Claude..."
        Sync-RuleFile `
            -SrcPath ".github/instructions/go.instructions.md" `
            -DstPath ".claude/rules/go.md"

        Sync-RuleFile `
            -SrcPath ".github/instructions/frontend.instructions.md" `
            -DstPath ".claude/rules/frontend.md"

        Write-Host ""
        Write-Host "Syncing project-level: Copilot -> Cursor + Claude..."
        if (Test-Path ".github/copilot-instructions.md") {
            $globalBody = Get-Content ".github/copilot-instructions.md" -Raw
            # -> Cursor: wrap with cursor frontmatter
            Write-SyncFile -DstPath ".cursor/rules/project.mdc" `
                -Content "$projectcursorFm`n`n$globalBody"
            # -> Claude: prepend @AGENTS.md import
            Write-SyncFile -DstPath ".claude/CLAUDE.md" `
                -Content "@AGENTS.md`n`n$globalBody"
        } else {
            Write-Host "  SKIP    .github/copilot-instructions.md (not found)"
        }
    }

    "cursor" {
        Write-Host "Syncing scoped rules: Cursor -> Copilot..."
        Sync-RuleFile `
            -SrcPath ".cursor/rules/go.mdc" `
            -DstPath ".github/instructions/go.instructions.md" `
            -Frontmatter $gocopilotFm

        Sync-RuleFile `
            -SrcPath ".cursor/rules/frontend.mdc" `
            -DstPath ".github/instructions/frontend.instructions.md" `
            -Frontmatter $frontendcopilotFm

        Write-Host ""
        Write-Host "Syncing scoped rules: Cursor -> Claude..."
        Sync-RuleFile `
            -SrcPath ".cursor/rules/go.mdc" `
            -DstPath ".claude/rules/go.md"

        Sync-RuleFile `
            -SrcPath ".cursor/rules/frontend.mdc" `
            -DstPath ".claude/rules/frontend.md"

        Write-Host ""
        Write-Host "Syncing project-level: Cursor -> Copilot + Claude..."
        if (Test-Path ".cursor/rules/project.mdc") {
            $globalBody = Extract-Body -FilePath ".cursor/rules/project.mdc"
            # -> Copilot: body only
            Write-SyncFile -DstPath ".github/copilot-instructions.md" `
                -Content $globalBody
            # -> Claude: prepend @AGENTS.md import
            Write-SyncFile -DstPath ".claude/CLAUDE.md" `
                -Content "@AGENTS.md`n`n$globalBody"
        } else {
            Write-Host "  SKIP    .cursor/rules/project.mdc (not found)"
        }
    }

    "claude" {
        Write-Host "Syncing scoped rules: Claude -> Copilot..."
        Sync-RuleFile `
            -SrcPath ".claude/rules/go.md" `
            -DstPath ".github/instructions/go.instructions.md" `
            -Frontmatter $gocopilotFm

        Sync-RuleFile `
            -SrcPath ".claude/rules/frontend.md" `
            -DstPath ".github/instructions/frontend.instructions.md" `
            -Frontmatter $frontendcopilotFm

        Write-Host ""
        Write-Host "Syncing scoped rules: Claude -> Cursor..."
        Sync-RuleFile `
            -SrcPath ".claude/rules/go.md" `
            -DstPath ".cursor/rules/go.mdc" `
            -Frontmatter $gocursorFm

        Sync-RuleFile `
            -SrcPath ".claude/rules/frontend.md" `
            -DstPath ".cursor/rules/frontend.mdc" `
            -Frontmatter $frontendcursorFm

        Write-Host ""
        Write-Host "Syncing project-level: Claude -> Copilot + Cursor..."
        if (Test-Path ".claude/CLAUDE.md") {
            $rawContent = Get-Content ".claude/CLAUDE.md" -Raw
            $globalBody = Strip-AgentsImport -Content $rawContent
            # -> Copilot: body without @AGENTS.md
            Write-SyncFile -DstPath ".github/copilot-instructions.md" `
                -Content $globalBody
            # -> Cursor: wrap with cursor frontmatter
            Write-SyncFile -DstPath ".cursor/rules/project.mdc" `
                -Content "$projectcursorFm`n`n$globalBody"
        } else {
            Write-Host "  SKIP    .claude/CLAUDE.md (not found)"
        }
    }
}

Write-Host ""
Write-Host "Note: AGENTS.md is shared across all assistants and was not modified."
Write-Host "Manual files (never overwritten): .claude/rules/safety.md"
Write-Host "Done."
