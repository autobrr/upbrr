---
title: Main settings
description: Configure metadata access, input history, tracker icons, and scene detection.
---

# Main settings

Use **Settings → Main** for application-wide metadata and Web UI presentation controls.

| Field                       | Default          | Effect                                                                                                               |
| --------------------------- | ---------------- | -------------------------------------------------------------------------------------------------------------------- |
| **TMDB API**                | Empty            | Enables TMDB lookups and enrichment. Other providers and supplied IDs remain available when empty.                   |
| **Input history limit**     | `20`             | Caps source paths retained in each browser. `0` disables and clears retained history; negative values are rejected.  |
| **DB path**                 | Runtime-selected | Shows the active SQLite path. Saving cannot move the running instance; choose the database path when starting upbrr. |
| **Use favicons**            | On               | Fetches and displays tracker icons on supported workflow pages.                                                      |
| **Favicon only**            | Off              | Hides tracker names where an icon is shown. Has no effect when **Use favicons** is off.                              |
| **Scene detection (srrdb)** | On               | Enables SRRDB scene detection during preparation. Turning it off prevents those SRRDB requests.                      |

TMDB API is a secret. The Web UI masks a saved key; leaving `[REDACTED]` unchanged preserves it.

:::note Compatibility fields

**Update notification**, **Verbose notification**, and **Tracker pass checks** remain in the imported/exported schema but have no current runtime reader.

:::

## Verify the change

Save, prepare a synthetic release such as `Example.Release.2026.1080p-GRP`, then inspect its metadata. Tracker icon changes appear across Input, Tracker Data, Dupe Check, and Descriptions without restarting the server.
