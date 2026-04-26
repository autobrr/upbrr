---
sidebar_position: 1
title: Overview
---

# Configuration overview

Configuration is centered around `internal/config.Config` and persisted through the SQLite-backed config store.

The config covers:

- main settings and database path
- image hosting credentials and host order
- metadata behavior
- screenshot handling
- description formatting
- torrent client setup
- Sonarr and Radarr integration
- torrent creation defaults
- post-upload behavior
- logging
- tracker-specific settings

## Required setting

Set `main_settings.tmdb_api` before running normal metadata workflows.

## Defaults

The embedded default template lives at:

```text
internal/config/defaults/example.yaml
```

Use the GUI Settings page or import commands to persist changes into the SQLite config store.
