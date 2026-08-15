---
title: Torrent Clients settings
description: Configure qBittorrent, qui proxy, watch-folder, staging-link, and path-mapping behavior.
---

# Torrent Clients settings

Use **Settings → Torrent Clients** to create named clients. Those names become choices under [Client Handling](./client-handling.md) and tracker-specific **Torrent client** fields.

## Add an entry

Select **Add entry**, enter a unique name, then choose its **Type**:

- **qBit** connects through a qui proxy URL or directly to qBittorrent WebUI.
- **Watch** copies a returned torrent file into a watched folder.

### qBittorrent connection

Choose one connection method per entry:

| Field                        | Purpose                                                                                            |
| ---------------------------- | -------------------------------------------------------------------------------------------------- |
| **Qui proxy URL**            | Base URL for a qui-backed qBittorrent proxy. A nonblank value takes precedence over direct fields. |
| **qBit direct**              | Shows or clears the direct qBittorrent connection fields.                                          |
| **qBit URL / port**          | Direct qBittorrent WebUI address. `http://` is added when the scheme is omitted.                   |
| **qBit user / pass**         | Required for a direct connection.                                                                  |
| **Verify WebUI certificate** | Verifies HTTPS certificates. Keep enabled unless a trusted self-signed setup requires otherwise.   |

To switch from qui proxy to direct qBittorrent, clear **Qui proxy URL**, enable **qBit direct**, and fill the direct fields. Turning **qBit direct** off clears both direct and proxy connection fields.

Stored proxy URLs and credentials are encrypted in configuration transport and appear as `[REDACTED]` in the Web UI.

### Watch folder

**Watch folder** is required and must be writable by the upbrr process. URL-only tracker results cannot be delivered to a watch folder because no torrent file exists to copy.

**Storage directory** remains visible for configuration compatibility but has no current runtime reader.

## qBittorrent add options

| Field                          | Effect                                                                                                                     |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------- |
| **qBit category**              | Category for normal injections.                                                                                            |
| **qBit tag**                   | Comma-separated tag value for normal injections.                                                                           |
| **qBit cross category/tag**    | Overrides category or tag for torrents marked as cross-seeds.                                                              |
| **Use tracker as tag**         | Uses the tracker name only when no configured tag applies.                                                                 |
| **Automatic management paths** | Enables qBittorrent automatic management when the original local save path is under a listed root and linking is not used. |

## Link staging and path mapping

**Linking** can stage source files as `hardlink`, `reflink`, or `symlink` before injection. Leave it as **None** for direct source-path injection.

| Field                   | Effect                                                                                                |
| ----------------------- | ----------------------------------------------------------------------------------------------------- |
| **Linked folder**       | One or more roots where per-tracker staged layouts can be created. Required when linking is enabled.  |
| **Allow link fallback** | Falls back to the original source path when staging or torrent-layout validation cannot be completed. |
| **Local path**          | upbrr-visible path prefix. Entries pair by position with **Remote path**.                             |
| **Remote path**         | Corresponding qBittorrent-visible prefix used for save paths.                                         |

Hardlinks require source and destination on the same filesystem. Reflinks require supported storage and same-filesystem cloning. Symlinks require the needed OS permissions and a path qBittorrent can resolve. Turn off fallback when silently using the original path would be unsafe.

## Verify the entry

Save, select it under **Searching clients**, and prepare a known source to test read-only discovery. There is no connection-test button. Injection writes or links data, so verify mappings and permissions before the first controlled upload.
