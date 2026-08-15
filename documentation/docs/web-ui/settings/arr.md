---
title: Arr settings
description: Enrich prepared releases through one or more Sonarr and Radarr instances.
---

# Arr settings

Use **Settings → Arr** to let upbrr match a source path against Sonarr for TV or Radarr for movies. A match supplies non-authoritative IDs, year, genres, and release-group evidence to preparation.

## Configure Sonarr or Radarr

1. Turn on **Use Sonarr** and/or **Use Radarr**.
2. Enter the service base URL and API key in the unnumbered fields.
3. Use **Show advanced** for up to three additional URL and API-key pairs per service.
4. Select **Save**.

Each instance requires both a URL and API key. Incomplete pairs are ignored. Instances are checked in this order: unnumbered, `1`, `2`, then `3`; lookup stops when one returns useful evidence.

Use the URL reachable from the upbrr process. In Docker, `localhost` refers to the upbrr container, not the host or another container. Enter a base URL such as `http://sonarr:8989`, without adding an API route.

API keys are secrets and appear as `[REDACTED]` after saving.

:::note Compatibility fields

**Emby dir** and **Emby TV dir** remain in the schema but have no current runtime reader.

:::

## Verify the change

There is no connection-test button in this section. Save, prepare a source already known to the configured Arr service, then inspect its metadata and sanitized logs. A failed or empty Arr lookup does not block preparation; upbrr continues with other evidence.
