---
title: Client Handling settings
description: Select which configured torrent clients are used by default, for search, and for injection.
---

# Client Handling settings

Use **Settings → Client Handling** after creating entries under [Torrent Clients](./torrent-clients.md). The selectors contain configured client names and reject stale references when saved.

| Field                 | Purpose                                                                            |
| --------------------- | ---------------------------------------------------------------------------------- |
| **Default client**    | Fallback client for search and injection when their dedicated selectors are empty. |
| **Injected clients**  | Set of clients that receive successfully registered torrents.                      |
| **Searching clients** | qBittorrent or qui-backed clients searched for existing reusable torrents.         |

## Selection order

Injection uses the first applicable source:

1. workflow or tracker-specific client override;
2. **Injected clients**;
3. **Default client**;
4. the only configured client, when exactly one exists.

Search uses the first applicable source:

1. workflow client override;
2. **Searching clients**;
3. **Default client**;
4. all configured qBittorrent and qui-backed clients.

Watch-folder clients can receive torrent files but are not used for torrent search. Removing a Torrent Clients entry in the Web UI also clears its global and tracker references before save.

## Verify the change

Save, prepare a source already present in the intended search client, and inspect client-discovery results. For first injection, keep **Skip client injection** enabled during review, then perform one controlled upload after confirming paths, categories, and tags.
