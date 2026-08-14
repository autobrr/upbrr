---
title: Metadata settings
description: Control torrent discovery, tracker-data lookup, and Blu-ray metadata matching.
---

# Metadata settings

Use **Settings → Metadata** to control how preparation gathers reusable torrent and tracker metadata.

| Field                            | Default | Effect                                                                                                 |
| -------------------------------- | ------- | ------------------------------------------------------------------------------------------------------ |
| **Skip auto torrent**            | Off     | Skips automated torrent-client searching during preparation.                                           |
| **Skip tracker filename lookup** | Off     | Prevents filename-based tracker-data lookup when no tracker ID is already known.                       |
| **Use largest playlist**         | Off     | Lets unattended CLI preparation choose the largest Blu-ray playlist instead of requiring confirmation. |
| **Get Bluray info**              | Off     | Enables blu-ray.com matching for BDMV or DVD sources with an IMDb ID.                                  |
| **Bluray score**                 | `94.5`  | Sets the normal blu-ray.com candidate score threshold.                                                 |
| **Bluray single score**          | `89.5`  | Sets the threshold used when only one candidate is available.                                          |

## Advanced compatibility fields

- **BTN API** is a legacy location. Current configuration migrates it to the BTN entry under [Trackers](./trackers.md).
- **Keep images**, **Only ID**, **User overrides**, **Ping Unit3D**, and **Check Predb** remain in the stored schema but have no current runtime reader. Similarly named per-release options can still affect an individual preparation.

## Verify the change

Save, prepare a synthetic release, then inspect **Tracker Data** and the preparation progress. Blu-ray lookup requires a disc source and IMDb identity; an ordinary video file will not exercise those settings.
