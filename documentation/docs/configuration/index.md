---
title: Configuration
description: Where upbrr stores state, how setting groups map to behavior, and how to import or export configuration.
---

# Configuration

The Web UI **Settings** page is the normal configuration surface. Runtime settings are persisted in SQLite; YAML and JSON are import/export formats rather than the primary state store.

## State location

Without `XDG_CONFIG_HOME`, upbrr uses:

```text
Windows: %USERPROFILE%\.upbrr\db.sqlite
Linux/macOS: ~/.upbrr/db.sqlite
```

With `XDG_CONFIG_HOME`, the preferred path is:

```text
$XDG_CONFIG_HOME/upbrr/db.sqlite
```

When an older database already exists at `$XDG_CONFIG_HOME/.upbrr/db.sqlite`, upbrr keeps using it so an upgrade does not orphan existing state.

The Docker image sets `XDG_CONFIG_HOME=/config`, so new containers use `/config/upbrr/db.sqlite`. Existing `/config/.upbrr/db.sqlite` installations remain discoverable.

Database-adjacent files include:

| Path              | Purpose                                                                   |
| ----------------- | ------------------------------------------------------------------------- |
| `web-auth.json`   | Web authentication, browse policy, and key material for encrypted secrets |
| `web-config.json` | Persisted Web UI listen and proxy settings                                |
| `cookies/`        | Legacy tracker cookie import location                                     |

Back up the whole state directory, not only `db.sqlite`.

## Setting groups

| Group                | Controls                                                               |
| -------------------- | ---------------------------------------------------------------------- |
| Main settings        | updates, metadata API access, input history, scene detection           |
| Image hosting        | host priority and host credentials                                     |
| Metadata             | torrent discovery, playlist selection, cached images, external lookups |
| Screenshot handling  | count, concurrency, tone mapping, overlays, DVD menu limits            |
| Description settings | image layout, limits, headers, signatures, optional artwork            |
| Client setup         | default, searching, and injecting torrent clients                      |
| Arr integration      | Sonarr, Radarr, and mapped media directories                           |
| Torrent creation     | hashing threads, piece-size preference, rehash scheduling              |
| Post upload          | injection delay, tracker concurrency, messages, cross-seeding          |
| Logging              | level, file output, retention size and count                           |
| Trackers             | defaults, preferred tracker, auth, and tracker-owned options           |
| Torrent clients      | qBittorrent and other registered client settings                       |

Fields shown for each tracker come from the active tracker catalog. Configure only fields presented for that tracker.

See the [Web UI Settings reference](../web-ui/settings/index.md) for section-by-section field behavior.

## Import configuration

Import Upload Assistant Python, upbrr YAML, or upbrr JSON:

```powershell
.\upbrr.exe --import-config ".\config.yaml"
```

Import merges the supplied data with current defaults before saving it. Review warnings and inspect Settings afterward.

## Export configuration

The default YAML export preserves secret values in encrypted form:

```powershell
.\upbrr.exe --export-config ".\config-export.yaml"
```

Plaintext export is explicit:

```powershell
.\upbrr.exe --export-config ".\config-export.yaml" --export-config-plaintext
```

:::danger Plaintext secrets

A plaintext export can contain tracker credentials, API keys, passkeys, client passwords, and service tokens. Restrict access, never attach it to an issue, and delete it securely when finished.

:::

The Web UI can import and export configuration from **Settings**.

## Configuration safety

- Never copy raw configuration, cookies, announce URLs, or API responses into public reports.
- Keep `web-auth.json` with the database when moving an installation; encrypted secrets depend on its key material.
- Use the **Logging** page for sanitized diagnostics rather than exposing config values.
- Test tracker authentication after changing credentials.
- Use a dry run after changes to tracker, image-host, torrent, or client behavior.

Web listen settings have separate precedence and storage. See [Web server and reverse proxy](./web-server.md).
