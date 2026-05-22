# upbrr

`upbrr` is a Go-based upload preparation and tracker submission tool for private-tracker workflows. This repository contains a shared core used by:

- a command-line app in `cmd/upbrr`
- a desktop GUI built with Wails in `gui`
- an embedded web-serving mode exposed through `upbrr serve`

The project handles the full preparation pipeline around an upload candidate: metadata lookup, naming overrides, dupe checks, screenshot planning and uploads, description generation, tracker-specific payload building, torrent creation, and post-upload seeding/injection.

## What this repository includes

- Shared Go core for preparation, upload orchestration, and persistence
- CLI workflow for interactive and unattended runs
- Wails desktop GUI with step-by-step preparation screens
- Embedded frontend assets for GUI and web-serving mode
- SQLite-backed configuration and history storage
- Tracker-specific implementations under `internal/trackers`
- Image-host, screenshot, description, metadata, and torrent services

## Key capabilities

- Import configuration from YAML, JSON, or a legacy Upload Assistant `config.py` and persist it to SQLite
- Export the current SQLite-backed config back to YAML
- Launch a desktop GUI with `--gui` or `./gui`
- Run uploads from the CLI against one or more paths
- Process queue roots instead of single paths
- Run site checks without uploading
- Build uploads from previously prepared metadata with `--upload-only`
- Discover BDMV playlists and persist playlist selection
- Generate screenshots, upload them to supported hosts, and manage selections
- Build tracker-specific descriptions and upload payload previews
- Check dupes before upload
- Integrate with torrent clients such as qBittorrent and watch folders
- Serve the embedded frontend through the built-in web server

## Repository layout

- `cmd/upbrr` - main CLI entrypoint
- `gui` - Wails desktop application entrypoint and build config
- `gui/frontend` - React/Vite frontend used by the GUI and web mode
- `internal` - core business logic, services, tracker implementations, web server, and GUI backend bindings
- `pkg/api` - shared request/response types used across the app
- `scripts/build.ps1` - Windows build helper
- `scripts/build.sh` - Unix-like build helper
- `.github/workflows` - CI for tests, linting, and binary packaging

## Requirements

- A TMDB API key in config: `main_settings.tmdb_api`
- SQLite is embedded via `modernc.org/sqlite`, so no separate database server is required
- Additional media tools may be needed depending on your workflow, especially for screenshots and media analysis

Setting up a development environment? See [CONTRIBUTING.md](./CONTRIBUTING.md) for dependencies, supported platforms, and git hooks.

## Quick start

### 1. Create or export config

The app embeds a default YAML config template at `internal/config/defaults/example.yaml`.

Typical first-run options:

- start the program once and let it create a new blank/default config state automatically
- if a `config.yaml` already exists in the same directory as the database, the app will automatically import it into the SQLite config store on startup
- use the embedded defaults and save changes through the GUI
- pass `--config path/to/config.yaml` to load a YAML file as the active config at startup
- import a config file into the SQLite store without starting the app: `--import-config path/to/config.{yaml,yml,json,py}` (legacy Upload Assistant `config.py` files are parsed natively)
- import the same formats through the GUI or web UI from the Settings page
- export the current SQLite-backed config with `--export-config path/to/config.yaml`
- create the web auth helper file for CLI-only setups with `--create-auth`

For authenticated GUI/web Settings exports, plaintext secret export is disabled by default. If you need that behavior for a local trusted setup, add `"allow_unencrypted_export": true` to the `web-auth.json` file stored beside the active database. This hidden flag only affects UI export behavior and is not exposed in the app.

Important: `main_settings.tmdb_api` must be set before the core can run normally.

If you are migrating from the older program and already have tracker cookie files under `data/cookies`, copy those files into the new cookie directory for this build. By default that location is `~/.upbrr/cookies` beside the default database `~/.upbrr/db.sqlite`. If you use a custom `main_settings.db_path`, place the cookie files in the `cookies` folder next to that database instead.

### 2. Launch the GUI

```bash
go run ./cmd/upbrr --gui
```

Or run the dedicated GUI entrypoint:

```bash
go run ./gui
```

### 3. Run the CLI against a source path

```bash
go run ./cmd/upbrr "D:\releases\Some.Release.2026.1080p.BluRay"
```

Useful variants:

```bash
go run ./cmd/upbrr --site-check --trackers BLU,OE "D:\releases\Some.Release"
go run ./cmd/upbrr --dry-run --trackers PTP,HDB "D:\releases\Some.Release"
go run ./cmd/upbrr --upload-only "D:\releases\Some.Release"
go run ./cmd/upbrr --queue "D:\upload-queue" --limit-queue 5
```

### 4. Run the embedded web mode

```bash
go run ./cmd/upbrr serve
```

This starts the internal web server and serves the frontend from embedded assets or `gui/frontend/dist` when available.

## Configuration model

Configuration is centered around `internal/config.Config` and includes:

- main settings and database path
- image hosting credentials and host order
- metadata behavior
- screenshot handling
- description formatting
- torrent client setup
- Sonarr/Radarr integration
- torrent creation defaults
- post-upload behavior
- logging
- tracker-specific settings

The app can:

- import YAML, JSON, or a legacy Upload Assistant `config.py` into the SQLite-backed config store (via `--import-config` or the Settings page in the GUI and web UI)
- load defaults from the embedded example config
- apply environment overrides at runtime without persisting them to the database
- export the current database-backed config back to YAML

## Packaging and distribution

GitHub Actions includes workflows for:

- Go tests
- frontend lint and type checks
- `golangci-lint`
- cross-platform CLI builds
- cross-platform GUI builds
- Docker image builds

The Dockerfile builds both `upbrr` and `upbrr-gui` binaries and places them in the final image.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for how to set up a development environment, run the test suite, install the git hooks, and write commit messages that pass the repo's commit-message validator ([`cmd/commitmsgcheck`](./cmd/commitmsgcheck)). The project uses [AGENTS.md](https://agents.md/) for AI-coding-agent guidance — see [`AGENTS.md`](./AGENTS.md).

## License

This project is licensed under `GPL-2.0-or-later`. See [LICENSE](./LICENSE).
