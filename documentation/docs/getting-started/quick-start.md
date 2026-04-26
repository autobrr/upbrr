---
sidebar_position: 2
title: Quick Start
---

# Quick start

## 1. Configure TMDB

`main_settings.tmdb_api` must be set before normal metadata workflows can run.

The app can create a default configuration state automatically, import an existing YAML or JSON config, or import a legacy Upload Assistant `config.py` file.

## 2. Run a local dry run

Use dry-run mode to build the request and preview tracker behavior without uploading:

```bash
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
```

Use site-check mode when you want safe tracker checks without upload side effects:

```bash
go run ./cmd/upbrr --site-check --trackers BLU,OE "D:\releases\Some.Release"
```

## 3. Inspect in the GUI

For step-by-step review:

```bash
go run ./cmd/upbrr --gui
```

The GUI exposes the same core workflow as the CLI, with screens for metadata, dupes, screenshots, uploaded images, description building, and tracker upload review.

## 4. Upload intentionally

Run a normal CLI upload only after configuration, metadata, screenshots, and tracker options are correct:

```bash
go run ./cmd/upbrr --trackers BLU,OE "D:\releases\Some.Release"
```

For automation, prefer explicit `--dry-run`, `--site-check`, `--upload-only`, queue limits, and tracker selection so unattended runs do not make ambiguous choices.
