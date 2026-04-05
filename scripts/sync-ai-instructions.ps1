<#
.SYNOPSIS
    Synchronizes AI coding assistant instruction files across Copilot, Cursor,
    and Claude Code directories.

.DESCRIPTION
    Keeps rule/instruction files in sync so all LLM coding assistants see the
    same project guidance. Source of truth: AGENTS.md (project guidelines).

    File mapping:
      Copilot:  .github/copilot-instructions.md, .github/instructions/*.instructions.md
      Cursor:   .cursor/rules/*.mdc
      Claude:   .claude/CLAUDE.md, .claude/rules/*.md

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
        return $Matches[1].TrimStart()
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
        Write-Host "  SKIP  $SrcPath (not found)"
        return
    }

    $body = Extract-Body -FilePath $SrcPath

    if ($Frontmatter) {
        $content = "$Frontmatter`n`n$body"
    } else {
        $content = $body
    }

    if ($DryRun) {
        if (Test-Path $DstPath) {
            $existing = Get-Content $DstPath -Raw
            if ($existing.TrimEnd() -eq $content.TrimEnd()) {
                Write-Host "  OK    $DstPath (identical)"
            } else {
                Write-Host "  WOULD $SrcPath -> $DstPath"
            }
        } else {
            Write-Host "  WOULD $SrcPath -> $DstPath (new)"
        }
        return
    }

    $dstDir = Split-Path -Parent $DstPath
    if (-not (Test-Path $dstDir)) {
        New-Item -ItemType Directory -Force -Path $dstDir | Out-Null
    }

    Set-Content -Path $DstPath -Value $content -NoNewline -Encoding utf8NoBOM
    Write-Host "  SYNC  $SrcPath -> $DstPath"
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

switch ($Source) {
    "copilot" {
        Write-Host "Syncing Copilot -> Cursor..."
        Sync-RuleFile `
            -SrcPath ".github/instructions/go.instructions.md" `
            -DstPath ".cursor/rules/go.mdc" `
            -Frontmatter $gocursorFm

        Sync-RuleFile `
            -SrcPath ".github/instructions/frontend.instructions.md" `
            -DstPath ".cursor/rules/frontend.mdc" `
            -Frontmatter $frontendcursorFm

        Write-Host ""
        Write-Host "Syncing Copilot -> Claude..."
        Sync-RuleFile `
            -SrcPath ".github/instructions/go.instructions.md" `
            -DstPath ".claude/rules/go.md"

        Sync-RuleFile `
            -SrcPath ".github/instructions/frontend.instructions.md" `
            -DstPath ".claude/rules/frontend.md"
    }

    "cursor" {
        Write-Host "Syncing Cursor -> Copilot..."
        Sync-RuleFile `
            -SrcPath ".cursor/rules/go.mdc" `
            -DstPath ".github/instructions/go.instructions.md" `
            -Frontmatter $gocopilotFm

        Sync-RuleFile `
            -SrcPath ".cursor/rules/frontend.mdc" `
            -DstPath ".github/instructions/frontend.instructions.md" `
            -Frontmatter $frontendcopilotFm

        Write-Host ""
        Write-Host "Syncing Cursor -> Claude..."
        Sync-RuleFile `
            -SrcPath ".cursor/rules/go.mdc" `
            -DstPath ".claude/rules/go.md"

        Sync-RuleFile `
            -SrcPath ".cursor/rules/frontend.mdc" `
            -DstPath ".claude/rules/frontend.md"
    }

    "claude" {
        Write-Host "Syncing Claude -> Copilot..."
        Sync-RuleFile `
            -SrcPath ".claude/rules/go.md" `
            -DstPath ".github/instructions/go.instructions.md" `
            -Frontmatter $gocopilotFm

        Sync-RuleFile `
            -SrcPath ".claude/rules/frontend.md" `
            -DstPath ".github/instructions/frontend.instructions.md" `
            -Frontmatter $frontendcopilotFm

        Write-Host ""
        Write-Host "Syncing Claude -> Cursor..."
        Sync-RuleFile `
            -SrcPath ".claude/rules/go.md" `
            -DstPath ".cursor/rules/go.mdc" `
            -Frontmatter $gocursorFm

        Sync-RuleFile `
            -SrcPath ".claude/rules/frontend.md" `
            -DstPath ".cursor/rules/frontend.mdc" `
            -Frontmatter $frontendcursorFm
    }
}

Write-Host ""
Write-Host "Note: AGENTS.md is shared across all assistants and was not modified."
Write-Host "Done."
