---
title: Post Upload settings
description: Configure client-injection delay and concurrent tracker submissions.
---

# Post Upload settings

Use **Settings → Post Upload** for timing and concurrency around tracker submission and torrent-client injection.

| Field                       | Default | Effect                                                                                                  |
| --------------------------- | ------- | ------------------------------------------------------------------------------------------------------- |
| **Inject delay**            | `0`     | Waits this many seconds before each torrent-client injection. A tracker-specific value can override it. |
| **Max concurrent trackers** | `4`     | Caps simultaneous tracker uploads. `0` uses the built-in limit of `4`; negative values are rejected.    |

Set a tracker-specific **Inject delay** under [Trackers](./trackers.md) when only one tracker needs extra time before client injection.

:::note Compatibility fields

**Show upload duration**, **Print tracker messages**, **Print tracker links**, **Search requests**, **Cross seeding**, and **Cross seed check everything** remain in the schema but have no current runtime reader.

:::

## Verify the change

Save and use **Dry Run** to validate tracker preparation. Dry Run suppresses submission and therefore cannot measure real submission concurrency or post-success client injection; use a controlled approved upload when those effects must be verified.
