---
sidebar_position: 1
title: Overview
---

# Configuration overview

Configuration is centered around `internal/config.Config` and persisted through the SQLite-backed config store.

The config covers:

- `main_settings` for global behavior and database paths
- `image_hosting` for image host credentials and host order
- `metadata_settings` for metadata lookup behavior
- `screenshot_handling` for screenshot capture and selection behavior
- `description_settings` for generated description formatting
- `client_setup` and `torrent_clients` for torrent client integration
- `arr_integration` for Sonarr and Radarr integration
- `torrent_creation` for torrent generation defaults
- `post_upload` for behavior after a successful upload
- `logging` for run log output
- `trackers` for tracker-specific settings

## Required setting

Set `main_settings.tmdb_api` before running normal metadata workflows.

## Defaults

The embedded default template lives at:

```text
internal/config/defaults/example.yaml
```

Use the GUI Settings page or import commands to persist changes into the SQLite config store.
