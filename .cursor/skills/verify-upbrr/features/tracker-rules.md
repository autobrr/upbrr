# Tracker rules

SAM enforces site-specific naming, content-structure, language, and duplicate-slot rules. The rules
live in `internal/trackers/impl/unit3d/sites/sam/` and are bound by `profile.go`; shared evidence and
duplicate plumbing live in `internal/mediafacts`, `internal/trackers/data`, `internal/trackers/dupe`,
and `pkg/api`.

## Sub-features

- **Naming** (`name.go`, policy `unit3d/sam/v3`): TV and anime names keep the release year. Incoming
  `DUAL`/`MULTI` markers are omitted unless parsed audio explicitly includes Brazilian Portuguese.
- **Structure** (`validation.go`, `sam_movie_structure` / `sam_tv_structure`): one work per torrent;
  one main video for a movie; one season per TV torrent; non-pack TV is one episode; season packs
  require a single detected season and a current provider status of ended/cancelled/completed.
- **Language** (`validation.go`, `sam_language`): original language is required. Non-Portuguese
  content needs original-language audio and at least one explicit PT-BR subtitle in every file.
  Partial or missing evidence fails closed.
- **Duplicate slots** (`dupe_policy.go`): source, resolution, video codec, audio codec, and audio
  channels define the slot. A new relevant audio-language set coexists; group or subtitle changes
  alone remain a duplicate.

## How to get to it (user POV)

- CLI: `upbrr <source>` runs duplicate search and tracker validation; `upbrr -site-check <source>`
  stops before upload; `upbrr -input-only <source>` prepares local policy evaluation with no remote
  work.
- Web UI: add an input on the Input page, then follow Dupe Check and Tracker Upload.

## Driving it with the harness

```bash
# Policy truth: naming, structure, language, duplicate slots, MediaInfo facts
go test -race -count=1 -timeout 20m ./internal/trackers/impl/unit3d/sites/sam/ \
  ./internal/trackers/dupe/ ./internal/trackers/data/ ./internal/mediafacts/

# End-to-end CLI path against local fake services
make e2e-build
pnpm --dir webui exec playwright test --project=cli-full-upload --workers=1 --reporter=list
```

Real tracker site-checks need credentials and network; they are not part of routine verification. Use
`-site-check` with `-live-test` only when a maintainer explicitly asks, and never upload.

## Gotchas

- Region matters. Generic `Portuguese`, `pt`, and `pt-PT` do not satisfy the PT-BR requirement; the
  evidence must carry `pt-BR` or a localized `Portuguese (Brazil)` / `Português (Brasil)` label.
- Partial MediaInfo evidence must stay conservative: an undeclared field on any audio track makes that
  dimension partial, and partial evidence must not prove a different slot.
- Debug mode suppresses submission but still prepares names, descriptions, and screenshots; it is not
  a no-op dry run.
- The public tracker overview intentionally stays generic; do not copy site-specific rules into
  `documentation/docs/trackers/index.md`.
