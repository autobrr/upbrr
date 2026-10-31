---
title: Post Upload settings
description: Configure client-injection delay and concurrent tracker submissions.
---

# Post Upload settings

Use **Settings → Post Upload** for timing and concurrency around tracker submission and torrent-client injection.

| Field                                | Default | Effect                                                                                                  |
| ------------------------------------ | ------- | ------------------------------------------------------------------------------------------------------- |
| **Client injection delay (seconds)** | `0`     | Waits this many seconds before each torrent-client injection. A tracker-specific value can override it. |
| **Concurrent tracker uploads**       | `4`     | Caps simultaneous tracker uploads. `0` uses the built-in limit of `4`; negative values are rejected.    |

Set a tracker-specific **Client injection delay (seconds)** under [Trackers](./trackers.md) when only one tracker needs extra time before client injection.

:::note Compatibility fields

**Show upload duration**, **Print tracker messages**, **Print tracker links**, **Search requests**, **Cross seeding**, and **Cross seed check everything** remain in the schema but have no current runtime reader.

:::

## Verify the change

Save and use **Dry Run** to validate tracker preparation. Dry Run suppresses tracker submission but still attempts torrent-client injection by default. Select **Skip client injection** when no torrent should be added; for CLI testing, combine `--debug` with `--no-seed` (`-ns`). Use a controlled approved upload when real submission concurrency must be verified.
