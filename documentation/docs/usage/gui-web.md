---
sidebar_position: 2
title: GUI and Web Mode
---

# GUI and web mode

The Wails GUI and embedded web mode use the same core services as the CLI.

## Desktop GUI

Launch the desktop GUI:

```bash
go run ./cmd/upbrr --gui
```

Or:

```bash
go run ./gui
```

The GUI guides a release through input, metadata, dupe checks, screenshots, image uploads, description building, and tracker upload review.

## Embedded web mode

Start the built-in web server:

```bash
go run ./cmd/upbrr serve
```

The server serves embedded frontend assets, or `gui/frontend/dist` when available during local development.

## Authentication and browsing

Web mode uses a local auth helper for browser sessions and protected data handling. CLI-only setups can create the helper with:

```bash
go run ./cmd/upbrr --create-auth
```

Browser file picking is limited by the configured browse policy. Remote web sessions should enter server paths manually unless native browsing is available for that local request.
