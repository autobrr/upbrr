---
title: Client Handling settings
description: Select which configured torrent clients are used by default, for search, and for injection.
---

# Client Handling settings

Use **Settings → Client Handling** after creating entries under [Torrent Clients](./torrent-clients.md). The selectors contain configured client names and reject stale references when saved.

| Field                                      | Purpose                                                                            |
| ------------------------------------------ | ---------------------------------------------------------------------------------- |
| **Default torrent client**                 | Fallback client for search and injection when their dedicated selectors are empty. |
| **Clients receiving torrents**             | Set of clients that receive successfully registered torrents.                      |
| **Clients searched for existing torrents** | qBittorrent or qui-backed clients searched for existing reusable torrents.         |

## Selection order

Injection uses the first applicable source:

1. workflow or tracker-specific client override;
2. **Clients receiving torrents**;
3. **Default torrent client**;
4. the only configured client, when exactly one exists.

Search uses the first applicable source:

1. workflow client override;
2. **Clients searched for existing torrents**;
3. **Default torrent client**;
4. all configured qBittorrent and qui-backed clients.

An explicit [`--infohash` or API `torrent.infoHash`](../../cli/index.md#client-and-torrent) lookup searches the default client, then other available clients, overriding the search list, `none` selectors and skip-search setting. A named client supplied with that request still restricts lookup to that client. This changes search only; only-ID, keep-images and injection choices remain in effect.

Watch-folder clients can receive torrent files but are not used for torrent search. Removing a Torrent Clients entry in the Web UI also clears its global and tracker references before save.

## Verify the change

Save, prepare a source already present in the intended search client, and inspect client-discovery results. For first injection, keep **Skip client injection** enabled during review, then perform one controlled upload after confirming paths, categories, and tags.
