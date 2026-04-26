---
sidebar_position: 1
title: CLI Usage
---

# CLI usage

The CLI entrypoint is `cmd/upbrr`.

```bash
go run ./cmd/upbrr "D:\releases\Some.Release.2026.1080p.BluRay"
```

## Common modes

Run a safe site check:

```bash
go run ./cmd/upbrr --site-check --trackers BLU,OE "D:\releases\Some.Release"
```

Run a dry run:

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

Upload from previously prepared metadata:

```bash
go run ./cmd/upbrr --upload-only "D:\releases\Some.Release"
```

Process a queue directory:

```bash
go run ./cmd/upbrr --queue "D:\upload-queue" --limit-queue 5
```

## Unattended safety

Unattended and unattended-confirm flows are safety-critical. They should stay non-blocking and conservative:

- prefer dry-run or site-check when a choice cannot be made safely
- keep tracker selection explicit
- keep queue limits explicit
- avoid hidden interactive prompts
- preserve skip behavior for dupes, rule failures, screenshot/image-host uploads, torrent injection, and retries

## Overrides

The CLI supports release-name, external ID, screenshot, tracker, and execution overrides. Prefer narrow overrides on the command line and keep durable defaults in config.
