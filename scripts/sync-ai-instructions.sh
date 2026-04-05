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
      echo "  Repo-wide:"
      echo "    Copilot:  .github/copilot-instructions.md"
      echo "    Cursor:   .cursor/rules/project.mdc"
      echo "    Claude:   .claude/CLAUDE.md"
      echo ""
      echo "  Scoped rules:"
      echo "    Copilot:  .github/instructions/go.instructions.md"
      echo "              .github/instructions/frontend.instructions.md"
      echo "    Cursor:   .cursor/rules/go.mdc"
      echo "              .cursor/rules/frontend.mdc"
      echo "    Claude:   .claude/rules/go.md"
      echo "              .claude/rules/frontend.md"
      echo ""
      echo "  Manual files (never overwritten by sync):"
      echo "    .claude/rules/safety.md"
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

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

extract_body() {
  # Strip YAML frontmatter (--- ... ---) from a file, output only the body
  local file="$1"
  awk 'BEGIN{f=0} /^---$/{f++; next} f>=2{print} f==0{print}' "$file"
}

strip_agents_import() {
  # Strip the @AGENTS.md import line (and trailing blank lines) from CLAUDE.md
  sed '/^@AGENTS\.md$/d' | sed '/./,$!d'
}

write_file() {
  # Write content from stdin to dst. In dry-run mode, only report.
  local dst="$1"
  local content
  content="$(cat)"

  if $DRY_RUN; then
    if [[ -f "$dst" ]]; then
      local existing
      existing="$(cat "$dst")"
      if [[ "$existing" == "$content" ]]; then
        echo "  OK      $dst (identical)"
      else
        echo "  WOULD   $dst"
      fi
    else
      echo "  WOULD   $dst (new)"
    fi
    return
  fi

  mkdir -p "$(dirname "$dst")"
  printf '%s\n' "$content" > "$dst"

  echo "  SYNC    $dst"
}

sync_rule() {
  # Sync a scoped rule file: extract body from src, optionally prepend
  # frontmatter, write to dst.
  local src="$1"
  local dst="$2"
  local frontmatter="${3:-}"

  if [[ ! -f "$src" ]]; then
    echo "  SKIP    $src (not found)"
    return
  fi

  if [[ -n "$frontmatter" ]]; then
    { printf '%s\n\n' "$frontmatter"; extract_body "$src"; } | write_file "$dst"
  else
    extract_body "$src" | write_file "$dst"
  fi
}

# ---------------------------------------------------------------------------
# Frontmatter templates
# ---------------------------------------------------------------------------

GOCOPILOT_FM='---
description: Go backend code style, linting, and testing rules for all .go files
applyTo: "**/*.go"
---'

FRONTENDCOPILOT_FM='---
description: Frontend code style, linting, and build rules for React/Vite/TypeScript files
applyTo: "gui/frontend/**/*.{ts,tsx,css}"
---'

GOCURSOR_FM='---
description: Go backend code style, linting, and testing rules
globs: "**/*.go"
alwaysApply: false
---'

FRONTENDCURSOR_FM='---
description: Frontend code style and build rules for React/Vite/TypeScript
globs: "gui/frontend/**/*.ts,gui/frontend/**/*.tsx,gui/frontend/**/*.css"
alwaysApply: false
---'

PROJECT_CURSOR_FM='---
description: Project-wide guidelines for upbrr - upload preparation and tracker submission tool
alwaysApply: true
---'

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

echo "=== AI Instructions Sync ==="
echo "Source: $SOURCE"
if $DRY_RUN; then
  echo "Mode: dry-run (no changes will be made)"
fi
echo ""

case "$SOURCE" in
  copilot)
    echo "Syncing scoped rules: Copilot -> Cursor..."
    sync_rule ".github/instructions/go.instructions.md" ".cursor/rules/go.mdc" "$GOCURSOR_FM"
    sync_rule ".github/instructions/frontend.instructions.md" ".cursor/rules/frontend.mdc" "$FRONTENDCURSOR_FM"

    echo ""
    echo "Syncing scoped rules: Copilot -> Claude..."
    sync_rule ".github/instructions/go.instructions.md" ".claude/rules/go.md"
    sync_rule ".github/instructions/frontend.instructions.md" ".claude/rules/frontend.md"

    echo ""
    echo "Syncing project-level: Copilot -> Cursor + Claude..."
    if [[ -f ".github/copilot-instructions.md" ]]; then
      # -> Cursor: wrap with cursor frontmatter
      { printf '%s\n\n' "$PROJECT_CURSOR_FM"; cat ".github/copilot-instructions.md"; } | write_file ".cursor/rules/project.mdc"
      # -> Claude: prepend @AGENTS.md import
      { printf '@AGENTS.md\n\n'; cat ".github/copilot-instructions.md"; } | write_file ".claude/CLAUDE.md"
    else
      echo "  SKIP    .github/copilot-instructions.md (not found)"
    fi
    ;;

  cursor)
    echo "Syncing scoped rules: Cursor -> Copilot..."
    sync_rule ".cursor/rules/go.mdc" ".github/instructions/go.instructions.md" "$GOCOPILOT_FM"
    sync_rule ".cursor/rules/frontend.mdc" ".github/instructions/frontend.instructions.md" "$FRONTENDCOPILOT_FM"

    echo ""
    echo "Syncing scoped rules: Cursor -> Claude..."
    sync_rule ".cursor/rules/go.mdc" ".claude/rules/go.md"
    sync_rule ".cursor/rules/frontend.mdc" ".claude/rules/frontend.md"

    echo ""
    echo "Syncing project-level: Cursor -> Copilot + Claude..."
    if [[ -f ".cursor/rules/project.mdc" ]]; then
      local_body="$(extract_body ".cursor/rules/project.mdc")"
      # -> Copilot: body only (no frontmatter needed for copilot-instructions.md)
      printf '%s\n' "$local_body" | write_file ".github/copilot-instructions.md"
      # -> Claude: prepend @AGENTS.md import
      { printf '@AGENTS.md\n\n'; printf '%s\n' "$local_body"; } | write_file ".claude/CLAUDE.md"
    else
      echo "  SKIP    .cursor/rules/project.mdc (not found)"
    fi
    ;;

  claude)
    echo "Syncing scoped rules: Claude -> Copilot..."
    sync_rule ".claude/rules/go.md" ".github/instructions/go.instructions.md" "$GOCOPILOT_FM"
    sync_rule ".claude/rules/frontend.md" ".github/instructions/frontend.instructions.md" "$FRONTENDCOPILOT_FM"

    echo ""
    echo "Syncing scoped rules: Claude -> Cursor..."
    sync_rule ".claude/rules/go.md" ".cursor/rules/go.mdc" "$GOCURSOR_FM"
    sync_rule ".claude/rules/frontend.md" ".cursor/rules/frontend.mdc" "$FRONTENDCURSOR_FM"

    echo ""
    echo "Syncing project-level: Claude -> Copilot + Cursor..."
    if [[ -f ".claude/CLAUDE.md" ]]; then
      local_body="$(cat ".claude/CLAUDE.md" | strip_agents_import)"
      # -> Copilot: body without @AGENTS.md import
      printf '%s\n' "$local_body" | write_file ".github/copilot-instructions.md"
      # -> Cursor: wrap with cursor frontmatter
      { printf '%s\n\n' "$PROJECT_CURSOR_FM"; printf '%s\n' "$local_body"; } | write_file ".cursor/rules/project.mdc"
    else
      echo "  SKIP    .claude/CLAUDE.md (not found)"
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
echo "Manual files (never overwritten): .claude/rules/safety.md"
echo "Done."
