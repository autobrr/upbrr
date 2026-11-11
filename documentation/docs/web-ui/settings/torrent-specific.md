---
title: Torrent preparation settings
description: Automatic torrent reuse and rehash-dependent upload scheduling.
---

# Torrent preparation settings

Use **Settings → Torrent preparation** to delay rehash-dependent submissions. Torrent artifact selection is automatic.

| Field                                       | Default | Effect                                                                                                      |
| ------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------- |
| **Rehash-dependent upload delay (seconds)** | `0`     | Waits this many seconds after reusable-torrent submissions finish before starting rehash-dependent uploads. |

A negative rehash cooldown behaves as zero.

Preparation selects a compatible base for each tracker from discovered torrents, preferring 1,000–2,000 pieces and then the closest available count. Equal scores preserve candidate order. Existing torrents are capped at 10,000 pieces unless using the effective maximum piece size makes a larger count unavoidable. Tracker limits and an explicit maximum piece size take precedence over piece-count goals; torrent-file size limits still apply. Compatible bases are retained for reuse without generating optional compact or general-purpose variants.

The former `prefer_max_16_torrent` setting and `UA_DEFAULT_PREFER_MAX_16_TORRENT` environment variable are removed. Older configuration files can still be imported; that preference is ignored and omitted when settings are exported. The CLI `--max-piece-size` override remains available.

With `--nohash`, upbrr only retains and reuses suitable existing pieces. It does not hash media to create a missing variant, and skips trackers for which no compatible artifact is available.

Compatible existing torrents are checked for metainfo, layout and applicable limits without automatically reading every media byte. Those checks do not prove that same-sized media bytes match the piece hashes. Explicit recheck verifies pieces; rehash creates a fresh torrent. Tracker metadata personalization and eligible wrapper-only changes can reuse existing pieces.

Reuse currently supports v1 torrents. V2 and hybrid v1/v2 torrents require a new torrent.

:::note Compatibility field

**Mkbrr threads** remains in the schema but has no current runtime reader.

:::

## Verify the change

Save, prepare a source with known torrents in a configured search client, and inspect the selected reusable torrent. Rehash cooldown is observable only in a real upload plan containing both reusable and rehash-dependent tracker submissions.
