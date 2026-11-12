---
title: Configuration
description: Where upbrr stores state, how setting groups map to behavior, and how to import or export configuration.
---

# Configuration

The Web UI **Settings** page is the normal configuration surface. Runtime settings are persisted in SQLite; YAML and JSON are import/export formats rather than the primary state store.

`serve --config <path>` can seed an empty database. Once that database has settings, the server uses its stored configuration; editing the startup file does not replace active settings. Apply later changes through Settings or configuration import. CLI workflow commands reject an effective configuration that differs from the database's active generation.

Configuration imports take effect before reporting success. If work or an unresolved upload outcome prevents activation, the import is rejected without changing stored settings; finish or reconcile that work and retry. Settings saves can instead report a pending activation.

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

To select custom state explicitly, set `main_settings.db_path` in a full configuration file and pass that file with `--config`:

```powershell
.\upbrr.exe serve --config ".\custom-state.yaml"
```

`UA_DEFAULT_DB_PATH` overrides the file's database path on this explicit-config route. Without `--config`, bootstrap first looks for settings in the default database. If it finds them, that default database wins over `UA_DEFAULT_DB_PATH`; this applies to `serve` and CLI workflow startup. Do not use the environment variable alone to isolate another build from normal state. Reports, queues, auth and web settings resolve relative to the active database directory, so check its actual location before backup, migration or maintenance.

## Environment overrides

These application variables override stored/imported configuration at runtime; they do not update the stored values. Empty values are ignored. Invalid integer/boolean syntax is ignored; a syntactically valid value must still pass configuration validation. Boolean values use `true`/`false` (also `1`/`0`, `t`/`f`, `T`/`F`, `TRUE`/`FALSE`, `True`/`False`). Lists are comma-separated, trim entries, and ignore blank entries. They are not a general mapping from every YAML key.

| Variable                                                                                                 | Setting or value                                                                                                             |
| -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `UA_DEFAULT_DB_PATH`                                                                                     | SQLite path override; a populated default database wins without explicit `--config` (see [State location](#state-location)). |
| `UA_DEFAULT_TMDB_API`                                                                                    | TMDB API key; keep it secret.                                                                                                |
| `UA_DEFAULT_SCREENS`                                                                                     | Positive decimal automatic screenshot count.                                                                                 |
| `UA_DEFAULT_ONLY_ID`                                                                                     | Boolean metadata-ID-only tracker lookup.                                                                                     |
| `UA_DEFAULT_KEEP_IMAGES`                                                                                 | Boolean existing-image collection.                                                                                           |
| `UA_TRACKERS_DEFAULT`                                                                                    | List of configured tracker IDs.                                                                                              |
| `UA_TRACKERS_PREFERRED`                                                                                  | Preferred tracker ID.                                                                                                        |
| `UA_DEFAULT_TORRENT_CLIENT`                                                                              | Configured default client entry name.                                                                                        |
| `UA_DEFAULT_SEARCHING_CLIENT_LIST`                                                                       | List of configured search-client names.                                                                                      |
| `UA_SONARR_USE`, `UA_RADARR_USE`                                                                         | Enable the respective integration with a boolean.                                                                            |
| `UA_SONARR_URL`, `UA_RADARR_URL`                                                                         | Primary service base URL.                                                                                                    |
| `UA_SONARR_API_KEY`, `UA_RADARR_API_KEY`                                                                 | Primary service secret.                                                                                                      |
| `UA_SONARR_URL_1` through `UA_SONARR_URL_3`, `UA_RADARR_URL_1` through `UA_RADARR_URL_3`                 | Additional instance URLs.                                                                                                    |
| `UA_SONARR_API_KEY_1` through `UA_SONARR_API_KEY_3`, `UA_RADARR_API_KEY_1` through `UA_RADARR_API_KEY_3` | Corresponding additional secrets.                                                                                            |

Environment-only changes can make CLI workflow startup differ from the active database generation. Resolve unfinished work and activate the intended settings before continuing; do not use another database to bypass recovery. Web listener variables and their precedence are documented [separately](./web-server.md#precedence).

## Setting groups

| Group                | Controls                                                               |
| -------------------- | ---------------------------------------------------------------------- |
| Main settings        | metadata API access, input history, tracker icons, scene detection     |
| Image hosting        | host priority and host credentials                                     |
| Metadata             | torrent discovery, playlist selection, cached images, external lookups |
| Screenshot handling  | count, concurrency, tone mapping, overlays, DVD menu limits            |
| Description settings | image layout, limits, headers, signatures, optional artwork            |
| Client setup         | default, searching, and injecting torrent clients                      |
| Arr integration      | Sonarr, Radarr, and mapped media directories                           |
| Torrent creation     | compatible torrent reuse and rehash scheduling                         |
| Post upload          | injection delay and tracker upload concurrency                         |
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

For Upload Assistant Python files, see [migration guidance](../getting-started/migrate-from-upload-assistant.md) for supported assignments and skipped incomplete client entries.

## Export configuration

On a fresh installation, the same export command generates a complete, editable
YAML template with the built-in setting and tracker defaults, comments, and empty
fields ready to fill in. No prior config,
import, or Web UI setup is required. This default template does not include
environment overrides or save settings to the database. The command can create or
migrate an empty database as part of resolving the export. Edit the template and
[import it](#import-configuration) when ready to apply your settings.

For a configured installation, export uses the stored settings with environment
overrides and preserves the existing secret-handling behavior. The default YAML
export preserves secret values in encrypted form:

```powershell
.\upbrr.exe --export-config ".\config-export.yaml"
```

On macOS and Linux, stored-settings YAML file exports are staged privately in the
same directory before replacing the destination. Readers see either the old complete
file or the new one; a failed write or replacement leaves the previous export intact.
Existing destinations must be regular files with owner read/write permissions.
Symbolic links and more restrictive files are rejected without changing them; choose
a regular writable destination instead. Successful replacements restrict permissions
to `0600`.

Windows retains in-place file writes to preserve existing security metadata. upbrr
coordinates its own config imports and exports to prevent partial reads, but this
does not make exports atomic for other applications or protect the previous contents
if a Windows write fails.

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
