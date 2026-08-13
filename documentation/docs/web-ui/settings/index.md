---
title: Settings
description: Save, reload, import, and verify every section of the Web UI Settings page.
---

# Settings

The Web UI **Settings** page edits runtime configuration and manages related security state. Open **Settings**, choose a section, make the change, then select **Save**.

## Configuration actions

| Action     | Result                                                                                          |
| ---------- | ----------------------------------------------------------------------------------------------- |
| **Reload** | Reloads persisted configuration and discards unsaved edits.                                     |
| **Export** | Downloads the current configuration with secrets kept encrypted.                                |
| **Import** | Replaces persisted settings with an imported Python, YAML, or JSON configuration after warning. |
| **Save**   | Validates, persists, and activates all unsaved configuration edits.                             |

Invalid configuration is rejected without replacing the active runtime. If an environment variable overrides a field, the stored value can save successfully while the environment value remains active.

:::warning Before importing

Export the current configuration first. Import replaces the configuration stored in the database and cannot be undone from the Web UI.

:::

## Secrets and direct actions

Stored credentials appear as `[REDACTED]`. Leave that value unchanged to preserve the secret, enter a new value to replace it, or clear it to remove it. Never include configuration exports, API tokens, cookies, passkeys, or announce URLs in public reports.

Most sections stage edits until **Save**. These sections behave differently:

- **Application Details** is read-only.
- **API Tokens** creates and revokes tokens immediately.
- **Tracker Auth** imports, checks, and deletes auth state immediately.

Use **Show advanced** only when a section offers it. Advanced fields are active settings unless their reference page identifies a compatibility-only field.

## Sections

| Section                                         | Purpose                                                          |
| ----------------------------------------------- | ---------------------------------------------------------------- |
| [Main](./main.md)                               | Metadata access, browser history, icons, and scene detection.    |
| [Image Hosting](./image-hosting.md)             | Ordered upload hosts, credentials, and conditional hosts.        |
| [Metadata](./metadata.md)                       | Discovery, tracker-data, image, and Blu-ray lookup policy.       |
| [Screens](./screens.md)                         | Screenshot generation, upload concurrency, and tone mapping.     |
| [Description](./description.md)                 | Shared description layout, headers, artwork, and signatures.     |
| [Arr](./arr.md)                                 | Sonarr and Radarr metadata enrichment.                           |
| [Post Upload](./post-upload.md)                 | Client-injection delay and tracker upload concurrency.           |
| [Trackers](./trackers.md)                       | Enabled trackers, defaults, credentials, and tracker options.    |
| [Torrent Clients](./torrent-clients.md)         | qBittorrent, qui proxy, watch-folder, linking, and path mapping. |
| [Client Handling](./client-handling.md)         | Default, search, and injection client selection.                 |
| [Torrent Specific](./torrent-specific.md)       | Reusable torrent piece preference and rehash scheduling.         |
| [Application Details](./application-details.md) | Build, platform, FFmpeg, and uptime diagnostics.                 |
| [API Tokens](./api-tokens.md)                   | Scoped bearer-token creation and revocation.                     |
| [Tracker Auth](./tracker-auth.md)               | Encrypted cookies, remote checks, relogin, and 2FA state.        |

After changing upload behavior or credentials, inspect every tracker preview and run **Dry Run**. Dry Run suppresses tracker submission but still attempts client injection by default; select **Skip client injection** when no torrent should be added. For CLI testing, combine `--debug` with `--no-seed` (`-ns`).

Logging configuration and the live viewer are on the separate [Logging page](../logging.md).
