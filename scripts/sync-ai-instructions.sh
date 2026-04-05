#!/usr/bin/env bash
# sync-ai-instructions.sh
#
# Synchronizes AI coding assistant instruction files across Copilot, Cursor,
# and Claude Code directories. Run from the repository root.
#
# Source of truth: AGENTS.md (project guidelines)
#
# Usage:
#   ./scripts/sync-ai-instructions.sh                    # default: copilot -> cursor, claude
#   ./scripts/sync-ai-instructions.sh --source copilot   # copilot is source
#   ./scripts/sync-ai-instructions.sh --source cursor    # cursor is source
#   ./scripts/sync-ai-instructions.sh --source claude    # claude is source
#   ./scripts/sync-ai-instructions.sh --dry-run          # preview changes without writing

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SOURCE="copilot"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      SOURCE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [--source copilot|cursor|claude] [--dry-run]"
      echo ""
      echo "Syncs AI instruction files across all three assistant directories."
      echo "Default source: copilot"
      echo ""
      echo "File mapping:"
      echo "  Copilot:  .github/copilot-instructions.md (repo-wide)"
      echo "            .github/instructions/go.instructions.md"
      echo "            .github/instructions/frontend.instructions.md"
      echo "  Cursor:   .cursor/rules/project.mdc"
      echo "            .cursor/rules/go.mdc"
      echo "            .cursor/rules/frontend.mdc"
      echo "  Claude:   .claude/CLAUDE.md (repo-wide)"
      echo "            .claude/rules/go.md"
      echo "            .claude/rules/frontend.md"
      echo "            .claude/rules/safety.md"
      echo ""
      echo "AGENTS.md is shared by all three and is never overwritten by sync."
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

sync_file() {
  local src="$1"
  local dst="$2"

  if [[ ! -f "$src" ]]; then
    echo "  SKIP  $src (not found)"
    return
  fi

  local dst_dir
  dst_dir="$(dirname "$dst")"

  if $DRY_RUN; then
    if [[ -f "$dst" ]]; then
      if diff -q "$src" "$dst" > /dev/null 2>&1; then
        echo "  OK    $dst (identical)"
      else
        echo "  WOULD $src -> $dst"
      fi
    else
      echo "  WOULD $src -> $dst (new)"
    fi
    return
  fi

  mkdir -p "$dst_dir"
  cp "$src" "$dst"
  echo "  SYNC  $src -> $dst"
}

extract_body() {
  # Strip YAML frontmatter (--- ... ---) from a file, output only the body
  local file="$1"
  awk 'BEGIN{f=0} /^---$/{f++; next} f>=2{print} f==0{print}' "$file"
}

echo "=== AI Instructions Sync ==="
echo "Source: $SOURCE"
if $DRY_RUN; then
  echo "Mode: dry-run (no changes will be made)"
fi
echo ""

case "$SOURCE" in
  copilot)
    echo "Syncing Copilot -> Cursor..."
    # Extract body from copilot instructions and wrap with cursor frontmatter
    if [[ -f ".github/instructions/go.instructions.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .github/instructions/go.instructions.md -> .cursor/rules/go.mdc"
      else
        mkdir -p .cursor/rules
        {
          echo '---'
          echo 'description: Go backend code style, linting, and testing rules'
          echo 'globs: "**/*.go"'
          echo 'alwaysApply: false'
          echo '---'
          echo ''
          extract_body ".github/instructions/go.instructions.md"
        } > .cursor/rules/go.mdc
        echo "  SYNC  go.instructions.md -> go.mdc"
      fi
    fi

    if [[ -f ".github/instructions/frontend.instructions.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .github/instructions/frontend.instructions.md -> .cursor/rules/frontend.mdc"
      else
        mkdir -p .cursor/rules
        {
          echo '---'
          echo 'description: Frontend code style and build rules for React/Vite/TypeScript'
          echo 'globs: "gui/frontend/**/*.ts,gui/frontend/**/*.tsx,gui/frontend/**/*.css"'
          echo 'alwaysApply: false'
          echo '---'
          echo ''
          extract_body ".github/instructions/frontend.instructions.md"
        } > .cursor/rules/frontend.mdc
        echo "  SYNC  frontend.instructions.md -> frontend.mdc"
      fi
    fi

    echo ""
    echo "Syncing Copilot -> Claude..."
    if [[ -f ".github/instructions/go.instructions.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .github/instructions/go.instructions.md -> .claude/rules/go.md"
      else
        mkdir -p .claude/rules
        extract_body ".github/instructions/go.instructions.md" > .claude/rules/go.md
        echo "  SYNC  go.instructions.md -> go.md"
      fi
    fi

    if [[ -f ".github/instructions/frontend.instructions.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .github/instructions/frontend.instructions.md -> .claude/rules/frontend.md"
      else
        mkdir -p .claude/rules
        extract_body ".github/instructions/frontend.instructions.md" > .claude/rules/frontend.md
        echo "  SYNC  frontend.instructions.md -> frontend.md"
      fi
    fi
    ;;

  cursor)
    echo "Syncing Cursor -> Copilot..."
    if [[ -f ".cursor/rules/go.mdc" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .cursor/rules/go.mdc -> .github/instructions/go.instructions.md"
      else
        mkdir -p .github/instructions
        {
          echo '---'
          echo 'description: Go backend code style, linting, and testing rules for all .go files'
          echo 'applyTo: "**/*.go"'
          echo '---'
          echo ''
          extract_body ".cursor/rules/go.mdc"
        } > .github/instructions/go.instructions.md
        echo "  SYNC  go.mdc -> go.instructions.md"
      fi
    fi

    if [[ -f ".cursor/rules/frontend.mdc" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .cursor/rules/frontend.mdc -> .github/instructions/frontend.instructions.md"
      else
        mkdir -p .github/instructions
        {
          echo '---'
          echo 'description: Frontend code style, linting, and build rules for React/Vite/TypeScript files'
          echo 'applyTo: "gui/frontend/**/*.{ts,tsx,css}"'
          echo '---'
          echo ''
          extract_body ".cursor/rules/frontend.mdc"
        } > .github/instructions/frontend.instructions.md
        echo "  SYNC  frontend.mdc -> frontend.instructions.md"
      fi
    fi

    echo ""
    echo "Syncing Cursor -> Claude..."
    if [[ -f ".cursor/rules/go.mdc" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .cursor/rules/go.mdc -> .claude/rules/go.md"
      else
        mkdir -p .claude/rules
        extract_body ".cursor/rules/go.mdc" > .claude/rules/go.md
        echo "  SYNC  go.mdc -> go.md"
      fi
    fi

    if [[ -f ".cursor/rules/frontend.mdc" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .cursor/rules/frontend.mdc -> .claude/rules/frontend.md"
      else
        mkdir -p .claude/rules
        extract_body ".cursor/rules/frontend.mdc" > .claude/rules/frontend.md
        echo "  SYNC  frontend.mdc -> frontend.md"
      fi
    fi
    ;;

  claude)
    echo "Syncing Claude -> Copilot..."
    if [[ -f ".claude/rules/go.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .claude/rules/go.md -> .github/instructions/go.instructions.md"
      else
        mkdir -p .github/instructions
        {
          echo '---'
          echo 'description: Go backend code style, linting, and testing rules for all .go files'
          echo 'applyTo: "**/*.go"'
          echo '---'
          echo ''
          cat ".claude/rules/go.md"
        } > .github/instructions/go.instructions.md
        echo "  SYNC  go.md -> go.instructions.md"
      fi
    fi

    if [[ -f ".claude/rules/frontend.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .claude/rules/frontend.md -> .github/instructions/frontend.instructions.md"
      else
        mkdir -p .github/instructions
        {
          echo '---'
          echo 'description: Frontend code style, linting, and build rules for React/Vite/TypeScript files'
          echo 'applyTo: "gui/frontend/**/*.{ts,tsx,css}"'
          echo '---'
          echo ''
          cat ".claude/rules/frontend.md"
        } > .github/instructions/frontend.instructions.md
        echo "  SYNC  frontend.md -> frontend.instructions.md"
      fi
    fi

    echo ""
    echo "Syncing Claude -> Cursor..."
    if [[ -f ".claude/rules/go.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .claude/rules/go.md -> .cursor/rules/go.mdc"
      else
        mkdir -p .cursor/rules
        {
          echo '---'
          echo 'description: Go backend code style, linting, and testing rules'
          echo 'globs: "**/*.go"'
          echo 'alwaysApply: false'
          echo '---'
          echo ''
          cat ".claude/rules/go.md"
        } > .cursor/rules/go.mdc
        echo "  SYNC  go.md -> go.mdc"
      fi
    fi

    if [[ -f ".claude/rules/frontend.md" ]]; then
      if $DRY_RUN; then
        echo "  WOULD .claude/rules/frontend.md -> .cursor/rules/frontend.mdc"
      else
        mkdir -p .cursor/rules
        {
          echo '---'
          echo 'description: Frontend code style and build rules for React/Vite/TypeScript'
          echo 'globs: "gui/frontend/**/*.ts,gui/frontend/**/*.tsx,gui/frontend/**/*.css"'
          echo 'alwaysApply: false'
          echo '---'
          echo ''
          cat ".claude/rules/frontend.md"
        } > .cursor/rules/frontend.mdc
        echo "  SYNC  frontend.md -> frontend.mdc"
      fi
    fi
    ;;

  *)
    echo "Unknown source: $SOURCE" >&2
    echo "Valid sources: copilot, cursor, claude" >&2
    exit 1
    ;;
esac

echo ""
echo "Note: AGENTS.md is shared across all assistants and was not modified."
echo "Done."
