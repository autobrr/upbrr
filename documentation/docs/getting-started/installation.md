---
sidebar_position: 1
title: Installation
---

# Installation

`upbrr` can be run from source during development or built into CLI and GUI binaries.

## Requirements

- Go matching the version in `go.mod`
- Node.js 20 or newer for frontend and GUI asset builds
- pnpm 10 or newer for frontend development
- Wails CLI `v2.10.1` for desktop GUI builds
- Media tools required by your workflow, especially for media analysis and screenshots

SQLite is embedded through `modernc.org/sqlite`, so no separate database server is required.

## Run from source

Run the CLI:

```bash
go run ./cmd/upbrr
```

Launch the desktop GUI:

```bash
go run ./cmd/upbrr --gui
```

Or use the dedicated GUI entrypoint:

```bash
go run ./gui
```

Start embedded web mode:

```bash
go run ./cmd/upbrr serve
```

## Build

Build a CLI binary:

```bash
go build -o dist/upbrr ./cmd/upbrr
```

On Windows:

```powershell
go build -o dist/upbrr.exe ./cmd/upbrr
```

For full CLI and GUI packaging, use the repository build scripts:

```bash
./scripts/build.sh
```

```powershell
./scripts/build.ps1
```
