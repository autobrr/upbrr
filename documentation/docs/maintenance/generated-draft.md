---
sidebar_position: 2
title: "CLI Site-Check Safety"
---

# CLI site-check safety

`upbrr` is a Go upload preparation and tracker submission tool for private-tracker workflows. It is not a download manager.

`--site-check` (alias `--sc`) is the safest way to run a tracker check pass from the CLI: it searches/checks sites without uploading.

## Safety contract

- `--site-check` performs site checks without upload submission.
- `--dry-run` runs the pipeline without uploading.
- Unattended safety policy treats site-check as a dry-run safety path.
- When a run cannot make a safe decision, prefer `--site-check` or `--dry-run` over uploading.

## CLI shape

The CLI accepts release source paths and flags. It does not provide download/list subcommands.

```bash
go run ./cmd/upbrr --site-check --trackers BLU,OE "D:\releases\Some.Release"
```

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

## Related execution modes

Queue processing:

```bash
go run ./cmd/upbrr --queue "D:\upload-queue" --limit-queue 5
```

Upload-only processing from prepared metadata cache:

```bash
go run ./cmd/upbrr --upload-only "D:\releases\Some.Release"
```

UI entrypoints:

```bash
go run ./cmd/upbrr --gui
go run ./cmd/upbrr serve
```

- `--gui` launches desktop GUI mode.
- `serve` starts embedded web mode.

## Practical safety guidance

- Start with `--site-check` when validating trackers and upload readiness.
- Use explicit tracker selection with `--trackers` during checks.
- Use `--queue` with `--limit-queue` to keep batch runs bounded.
- Move to upload paths only after site-check/dry-run results are acceptable.

## License

`upbrr` is licensed under `GPL-2.0-or-later`.
