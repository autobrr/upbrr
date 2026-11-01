# Repository Guidelines

Keep this file cross-cutting; put area-specific instructions in the nearest `AGENTS.md`. Paths and commands below are relative to the repository root.

## Find the applicable guidance

- Setup, supported tool versions, hooks and check selection: [CONTRIBUTING.md](CONTRIBUTING.md). Command/config sources are `Makefile`, `lefthook.yml`, `.golangci.yml`, package manifests and active `.github/workflows/*.yml`; `.yml22` files are disabled templates. Reconcile stale prose with these sources rather than weakening checks.
- All Go work, including `cmd/` and `pkg/`: [internal/AGENTS.md](internal/AGENTS.md).
- CLI flags, prompts and unattended behavior: [cmd/upbrr/AGENTS.md](cmd/upbrr/AGENTS.md).
- Shared request, preparation and runtime contracts: [pkg/api/AGENTS.md](pkg/api/AGENTS.md).
- Tracker definitions, registration, auth, naming, validation or duplicate contracts, including callers outside the subtree: [internal/trackers/AGENTS.md](internal/trackers/AGENTS.md).
- Browser transport/runtime: [internal/webserver/AGENTS.md](internal/webserver/AGENTS.md). Frontend: [webui/AGENTS.md](webui/AGENTS.md). Browser tests: [webui/e2e/AGENTS.md](webui/e2e/AGENTS.md).
- Public site: [documentation/AGENTS.md](documentation/AGENTS.md). Opt-in live testing: [scripts/live-testing/AGENTS.md](scripts/live-testing/AGENTS.md).

Read the relevant scoped instructions before editing that area; do not load every reference for unrelated work. Preserve ancestor rules when following a scoped file.

## Shared behavior and safety

- All automatically managed release artifacts belong beneath the shared `tmp/<release>/` owner. Use the release resolver; keep source validation independent of directory naming. Follow `internal/AGENTS.md` when changing this lifecycle.
- Preserve shared CLI/WebUI workflow behavior unless intentionally changing one entrypoint.
- Debug mode is not a non-mutating dry run. It suppresses tracker submission but may bypass policy gates such as banned-group blocking so later stages remain testable. Client injection remains default; CLI `-ns` and WebUI Upload skip-client-injection are explicit opt-outs. Do not classify these semantics as defects.
- Never expose credentials, tokens, API keys, cookies or secret payloads in logs, UI, diagnostics or fixtures; use the existing redaction boundaries.
- Use synthetic media titles, release names and content identifiers in shareable examples and fixtures. Exclude private URLs and identifiable user data. Public service names, provider codes, format labels and production mappings are allowed. Prefer `GRP` for incidental groups; retain actual groups when behavior depends on them.
- Never commit generated/local output: `dist/`, `webui/dist/`, `documentation/build/`, populated `internal/webserver/assets`, Playwright reports/results or repo-local scratch files.
- Follow explicit maintainer PR-body instructions when provided; otherwise use `.github/pull_request_template.md`. Formatting exceptions do not waive validation or truthful disclosures.

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
