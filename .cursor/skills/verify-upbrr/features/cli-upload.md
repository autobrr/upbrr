# CLI upload pipeline

The interactive CLI prepares a source, checks duplicates, lets the user approve trackers, creates the
torrent, and submits one tracker at a time. Entry point: `cmd/upbrr`, workflow: `internal/core` and
`internal/releaseworkflow`.

## Sub-features

- Input preparation: metadata lookup, MediaInfo analysis, screenshots, description composition.
- Duplicate check per tracker, with user approval of coexisting slots.
- Torrent creation and (unless `-ns` / skip-client-injection) client injection.
- Tracker submission with per-tracker continuation after a blocked lane.
- Debug mode (`-debug`): full preparation, submission suppressed.
- Unattended safety: `-unattended` never prompts; `-unattended_confirm` may ask only for required
  confirmation/manual input.

## How to get to it (user POV)

```bash
upbrr <source-path>                  # interactive upload
upbrr -debug <source-path>           # prepare everything, do not submit
upbrr -input-only <source-path>      # local preparation and policy only
upbrr -site-check <source-path>      # duplicate/site checks, no upload
upbrr -unattended_confirm <source>   # prompts only for required confirmations
```

## Driving it with the harness

```bash
make e2e-build
pnpm --dir webui exec playwright test --project=cli-full-upload --workers=1 --reporter=list
```

The spec (`webui/e2e/cli-full-upload.spec.ts`) spawns `dist/upbrr-e2e` with a temp config, fake
tracker/image-host/metadata services, and a `--no-seed` upload. It asserts exit code, output
milestones, tracker upload count, client search/injection counts, and torrent contents.

Manual smoke:

```bash
dist/upbrr --version
dist/upbrr -input-only --config tmp/verify-upbrr/run1/config.yaml <source-path>
```

## Gotchas

- `-input-only` does not exercise duplicate search or submission; it proves preparation only.
- Never drive a real tracker or torrent client in automated verification.
- Check exit code and the user-visible milestones, not only the last line of output.
