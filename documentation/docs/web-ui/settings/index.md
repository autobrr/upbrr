---
title: Settings
description: Save, reload, import, and verify every section of the Web UI Settings page.
---

# Settings

The Web UI **Settings** page edits runtime configuration and manages related security state. Open **Settings**, choose a section, make the change, then select **Save**.

## Field help

Use the information icon beside a setting’s label to read its effect, constraints and relevant defaults. Help floats near the icon without moving the form. Hover over the icon or focus it with the keyboard; on touch devices, tap to open or close it. You can move the pointer onto the help to read it. Press Escape, move away, or tap outside to dismiss it. Essential warnings remain visible beside the controls. Opening help does not change the setting.

Display names may differ from imported configuration keys. Keys, saved values and API fields are unchanged. Compatibility-only preferences are identified individually; they remain stored but do not implement the named feature. Changing them can still invalidate derived configuration state.

## Configuration actions

| Action     | Result                                                                                          |
| ---------- | ----------------------------------------------------------------------------------------------- |
| **Reload** | Reloads persisted configuration and discards unsaved edits.                                     |
| **Export** | Downloads the current configuration with secrets kept encrypted.                                |
| **Import** | Replaces persisted settings with an imported Python, YAML, or JSON configuration after warning. |
| **Save**   | Validates edits and activates them when current operations have safely finished.                |

Invalid configuration is rejected without replacing the active runtime. If an environment variable overrides a field, the stored value can save successfully while the environment value remains active.

A **Pending** save is not active yet. The Settings page checks for activation when opened and keeps checking while the save is pending. Another save while activation is pending returns busy and preserves your draft. If activation fails, the previous settings remain active; correct the settings and save again. Pending and failed status survive a restart.

Saving the same effective settings does not restart work. Changes invalidate the affected workflow stages, while compatible media and confirmed upload records remain available.

:::warning Before importing

Export the current configuration first. Import replaces the configuration stored in the database and cannot be undone from the Web UI.

:::

## Test a connection

Select **Test connection** beside an integration or configured instance to check the current form values without saving them. An unchanged `[REDACTED]` value uses its preserved secret; an edited credential is tested immediately. The request runs on the server with the integration's applicable proxy and TLS settings.

Wait for the result beside the button. Other instances can be tested independently. Editing the tested fields clears their result and cancels a pending test. Requests have a ten-second timeout; a denied response can also indicate account permissions, IP restrictions, or proxy access.

Cancellation stops waiting for storage, but a filesystem read already blocked by the operating system may need to finish later. Further storage reads wait for that operation or their own cancellation; canceled tests never start a service request afterward.

A successful check proves only the read operation described in its result. It does not prove upload permission. Connection tests do not upload images or torrents, add torrents to clients, save settings, or refresh stored tracker sessions. Public services may have no credentials to check. Where no safe check is available, the result explains the limitation and does not report success.

The checked credential depends on the provider. API probes check the entered API credentials; cookie probes check the imported session. Where temporary credential login is supported, entering either a username or password selects a fresh login and requires both values. A failed login does not fall back to imported cookies. Announce passkeys and credentials used only during upload are not verified by a session-page check.

The result describes the check's scope and limits. An API key may work for reads while a separate web login, two-factor step or upload permission still needs attention. Some supported upload APIs provide no compatible read-only credential check. Custom upload URLs may also have no verified companion read URL. No upload is attempted in these cases. A provider may record normal request or token-use audit information without creating content or refreshing credentials.

An **unverified** result means a compatible authenticated read contract has not yet been established. It is not evidence of invalid credentials or proof that the provider cannot support a safe check. No credentials are sent for an unverified check.

## Secrets and direct actions

Stored credentials appear as `[REDACTED]`. Leave that value unchanged to preserve the secret, enter a new value to replace it, or clear it to remove it. Never include configuration exports, API tokens, cookies, passkeys, or announce URLs in public reports.

Most sections stage edits until **Save**. These sections behave differently:

- **Application Details** is read-only.
- **Appearance** applies immediately and is saved in this browser; see [Appearance](./appearance.md).
- **API Tokens** creates and revokes tokens immediately.
- **Tracker authentication** imports, checks, and deletes auth state immediately.

Unsaved settings edits stay in the draft while you use other Web UI pages or browser Back and Forward. **Reload** and **Log out** ask before discarding them. Reloading or closing the browser tab triggers the browser's own unsaved-changes warning. If the session expires, sign in again; the unsaved draft has been discarded.

Use **Show advanced** only when a section offers it. Advanced fields are active settings unless their reference page identifies a compatibility-only field.

## Sections

| Section                                         | Purpose                                                          |
| ----------------------------------------------- | ---------------------------------------------------------------- |
| [General](./main.md)                            | Metadata access, browser history, icons, and scene detection.    |
| [Image Hosting](./image-hosting.md)             | Ordered upload hosts, credentials, and conditional hosts.        |
| [Metadata](./metadata.md)                       | Discovery, tracker-data, image, and Blu-ray lookup policy.       |
| [Screenshot capture](./screens.md)              | Screenshot generation, upload concurrency, and tone mapping.     |
| [Description](./description.md)                 | Shared description layout, headers, artwork, and signatures.     |
| [Sonarr and Radarr](./arr.md)                   | Sonarr and Radarr metadata enrichment.                           |
| [Post Upload](./post-upload.md)                 | Client-injection delay and tracker upload concurrency.           |
| [Trackers](./trackers.md)                       | Enabled trackers, defaults, credentials, and tracker options.    |
| [Torrent Clients](./torrent-clients.md)         | qBittorrent, qui proxy, watch-folder, linking, and path mapping. |
| [Client Handling](./client-handling.md)         | Default, search, and injection client selection.                 |
| [Torrent preparation](./torrent-specific.md)    | Reusable torrent piece preference and rehash scheduling.         |
| [Application Details](./application-details.md) | Build, platform, FFmpeg, and uptime diagnostics.                 |
| [Appearance](./appearance.md)                   | Bundled themes, color mode, and optional accents.                |
| [API Tokens](./api-tokens.md)                   | Scoped bearer-token creation and revocation.                     |
| [Tracker authentication](./tracker-auth.md)     | Encrypted cookies, remote checks, relogin, and 2FA state.        |

After changing upload behavior or credentials, inspect every tracker preview and run **Dry Run**. Dry Run suppresses tracker submission but still attempts client injection by default; select **Skip client injection** when no torrent should be added. For CLI testing, combine `--debug` with `--no-seed` (`-ns`).

Logging configuration and the live viewer are on the separate [Logging page](../logging.md).
