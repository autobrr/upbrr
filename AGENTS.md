# Repository Guidelines

Keep this file cross-cutting; put area-specific instructions in the nearest `AGENTS.md`. Paths and commands below are relative to the repository root.

## Find the applicable guidance

- Setup, supported tool versions, hooks and check selection: [CONTRIBUTING.md](CONTRIBUTING.md). Command/config sources are `Makefile`, `lefthook.yml`, `.golangci.yml`, package manifests and active `.github/workflows/*.yml`; `.yml22` files are disabled templates. Reconcile stale prose with these sources rather than weakening checks.
- All Go work, including `cmd/` and `pkg/`: [internal/AGENTS.md](internal/AGENTS.md).
- CLI flags, prompts and unattended behavior: [cmd/upbrr/AGENTS.md](cmd/upbrr/AGENTS.md).
- Shared request, preparation and runtime contracts: [pkg/api/AGENTS.md](pkg/api/AGENTS.md).
- Tracker definitions, registration, auth, naming, validation or duplicate contracts, including callers outside the subtree: [internal/trackers/AGENTS.md](internal/trackers/AGENTS.md).
- Duplicate search, evaluation and policies: [internal/trackers/dupe/AGENTS.md](internal/trackers/dupe/AGENTS.md).
- MediaInfo artifact ownership: [internal/metadata/AGENTS.md](internal/metadata/AGENTS.md). Waveform and spectrogram fidelity: [internal/services/audioanalysis/AGENTS.md](internal/services/audioanalysis/AGENTS.md).
- Browser transport and runtime activation: [internal/webserver/AGENTS.md](internal/webserver/AGENTS.md). Frontend checks: [webui/AGENTS.md](webui/AGENTS.md). Frontend source and release-session ownership: [webui/src/AGENTS.md](webui/src/AGENTS.md). Browser tests: [webui/e2e/AGENTS.md](webui/e2e/AGENTS.md).
- Public site: [documentation/AGENTS.md](documentation/AGENTS.md). Opt-in live testing: [scripts/live-testing/AGENTS.md](scripts/live-testing/AGENTS.md).

Read the relevant scoped instructions before editing that area; do not load every reference for unrelated work. Preserve ancestor rules when following a scoped file.

## Shared behavior and safety

- All automatically managed release artifacts belong beneath the shared `tmp/<release>/` owner. Use the release resolver; keep source validation independent of directory naming. Follow `internal/AGENTS.md` when changing this lifecycle.
- Preserve shared CLI/WebUI workflow behavior unless intentionally changing one entrypoint.
- Multiple manual provider IDs remain authoritative independently, even when provider-returned IDs conflict. Cross-provider agreement cannot veto those selections or discard their successfully fetched metadata. Preserve explicit clearing and Auto/reset; see the metadata boundary rules in [internal/AGENTS.md](internal/AGENTS.md).
- Debug mode is not a non-mutating dry run. It runs normal checks and preserves their actual outcomes while allowing diagnostic preparation past policy blocks when inputs permit it. The shared submission boundary unconditionally rejects debug uploads, including approvals, continuations and retries. Client injection remains default; CLI `-ns` and WebUI Upload skip-client-injection are explicit opt-outs. Do not classify these semantics as defects.
- Never expose credentials, tokens, API keys, cookies or secret payloads in logs, UI, diagnostics or fixtures; use the existing redaction boundaries.
- Use synthetic media titles, release names and content identifiers in shareable examples and fixtures. Exclude private URLs and identifiable user data. Public service names, provider codes, format labels and production mappings are allowed. Prefer `GRP` for incidental groups; retain actual groups when behavior depends on them.
- Never commit generated/local output: `dist/`, `webui/dist/`, `documentation/build/`, populated `internal/webserver/assets`, Playwright reports/results or repo-local scratch files.
- Follow explicit maintainer PR-body instructions when provided; otherwise use `.github/pull_request_template.md`. Formatting exceptions do not waive validation or truthful disclosures.

## Torrent structure and content names

- Preserve the structure selected by the shared torrent workflow. Never add or drop content, change the torrent root, flatten or rearrange paths, or switch between single-file and folder layouts to satisfy a tracker. Existing shared input and layout options define what belongs in the payload. A validated reuse conversion may produce a separate torrent matching that exact layout. Announce edits and supported piece-size variants must preserve it.
- Never rename or move source files or folders, rewrite their metainfo paths, stage copies or links under substitute names, or rewrite MediaInfo filename evidence to satisfy a tracker. Tracker-specific upload and search titles may still change independently. If the selected source fails a tracker requirement for content names or layout, block that tracker lane and identify the unmet requirement. See [content filename policy](ADDING_TRACKERS.md#content-filename-policy).

## Test value

- Test observable outcomes, not repeated types/constants, delegation without an outcome, unchanged dependency errors, duplicate branches or CSS/static-markup snapshots.
- Preserve distinct wiring, serialization, persistence, compatibility, accessibility and failure-boundary coverage. Before removing a regression test, identify retained coverage that catches the same defect.
- Reuse sufficient coverage and remove redundant setup or unused fixtures. Log wording/level and constant-only changes do not need tests merely to restate the change. Explain meaningful removals and retained guarantees.

## Logging levels

- `INFO`: concise end-user progress and outcomes for uploads and top-level workflows, not only final errors. Warnings identify failed/blocked outcomes needing attention.
- `DEBUG`: finalized effective facts (including compatible cached generations), concise provider outcomes and one bounded outcome per tracker/client check. Keep empty facts explicit; provider availability does not imply a new fetch.
- `TRACE`: repeated cache decisions, parser intermediates, per-candidate evidence, pagination and detailed lifecycle/counters.
- Use stable searchable fields, e.g. `tracker=%s state=%s decision=%s count=%d`.

## Validation

Start with affected checks from the scoped guidance and `CONTRIBUTING.md`; expand for shared behavior or safety-sensitive changes. Hook wrappers do not replace area checks. Honor explicit full-suite requests; unrelated/docs-only changes do not require browser tests. Run `git diff --check` before committing. Report passed, failed, blocked and unrun checks separately.
