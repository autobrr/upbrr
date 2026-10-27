---
title: Torrent Clients settings
description: Configure qBittorrent, qui proxy, watch-folder, staging-link, and path-mapping behavior.
---

# Torrent Clients settings

Use **Settings → Torrent Clients** to create named clients. Those names become choices under [Client Handling](./client-handling.md) and tracker-specific **Client receiving this tracker's torrents** fields.

## Add an entry

Select **Add entry**, enter a unique name, then choose its **Client type**:

- **qBittorrent** connects through a qui proxy URL or directly to qBittorrent WebUI.
- **Watch folder** copies a returned torrent file into a watched folder.

### qBittorrent connection

Choose one connection method per entry:

| Field                                                  | Purpose                                                                                            |
| ------------------------------------------------------ | -------------------------------------------------------------------------------------------------- |
| **qui proxy URL**                                      | Base URL for a qui-backed qBittorrent proxy. A nonblank value takes precedence over direct fields. |
| **Configure direct qBittorrent connection**            | Shows or clears the direct qBittorrent connection fields.                                          |
| **qBittorrent WebUI URL** / **qBittorrent WebUI port** | Direct qBittorrent WebUI address. `http://` is added when the scheme is omitted.                   |
| **qBittorrent username** / **qBittorrent password**    | Use the credentials required by your qBittorrent server; either field may be empty.                |
| **Verify connection TLS certificate**                  | Verifies HTTPS certificates. Keep enabled unless a trusted self-signed setup requires otherwise.   |

To switch from qui proxy to direct qBittorrent, clear **qui proxy URL**, enable **Configure direct qBittorrent connection**, and fill the direct fields. Turning **Configure direct qBittorrent connection** off clears both direct and proxy connection fields.

For a direct connection, the URL is required. Leave user and password empty only when qBittorrent permits authentication bypass for the address it sees from upbrr (for example, an explicitly allowed local subnet). A username with an empty password is also passed through to the server. Empty fields do not enable bypass in qBittorrent: authentication failures and HTTPS certificate verification still apply. Keep WebUI authentication enabled for untrusted networks.

Stored proxy URLs and credentials are encrypted in configuration transport and appear as `[REDACTED]` in the Web UI.

### Watch folder

**Torrent watch folder** is required and must be writable by the upbrr process. URL-only tracker results cannot be delivered to a watch folder because no torrent file exists to copy.

**Torrent storage directory (compatibility)** remains stored for configuration compatibility but is not currently used to change watch-folder copying or torrent storage.

## qBittorrent add options

| Field                                         | Effect                                                                                                                     |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| **Upload category**                           | Category for normal injections.                                                                                            |
| **Upload tags**                               | Comma-separated tag value for normal injections.                                                                           |
| **Cross-seed category** / **Cross-seed tags** | Overrides category or tag for torrents marked as cross-seeds.                                                              |
| **Use tracker name when tags are empty**      | Uses the tracker name only when no configured tag applies.                                                                 |
| **Automatic-management source roots**         | Enables qBittorrent automatic management when the original local save path is under a listed root and linking is not used. |

## Link staging and path mapping

**File linking method** can stage source files as `hardlink`, `reflink`, or `symlink` before injection. Leave it as **None** for direct source-path injection.

| Field                         | Effect                                                                                                                                                                            |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Link staging folders**      | One or more roots where per-tracker staged layouts can be created. Required when linking is enabled.                                                                              |
| **Allow client fallback**     | Allows original-path injection for supported failed or unavailable staging cases, and URL-only fallback when selected clients cannot consume URLs. Path validation still applies. |
| **Local path mapping roots**  | upbrr-visible path prefix. Entries pair by position with **Client path mapping roots**.                                                                                           |
| **Client path mapping roots** | Corresponding qBittorrent-visible prefix used for save paths.                                                                                                                     |

Hardlinks require source and destination on the same filesystem. Reflinks require supported storage and same-filesystem cloning. Symlinks require the needed OS permissions and a path qBittorrent can resolve. Turn off fallback when silently using the original path would be unsafe.

## Verify the entry

Save, select it under **Clients searched for existing torrents**, and prepare a known source to test read-only discovery. There is no connection-test button. Injection writes or links data, so verify mappings and permissions before the first controlled upload.
