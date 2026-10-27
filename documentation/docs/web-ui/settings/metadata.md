---
title: Metadata settings
description: Control torrent discovery, tracker-data lookup, and Blu-ray metadata matching.
---

# Metadata settings

Use **Settings → Metadata** to control how preparation gathers reusable torrent and tracker metadata.

| Field                                             | Default | Effect                                                                                                 |
| ------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------ |
| **Skip automatic torrent-client search**          | Off     | Skips automated torrent-client searching during preparation.                                           |
| **Skip filename-based tracker lookup**            | Off     | Prevents filename-based tracker-data lookup when no tracker ID is already known.                       |
| **Collect existing tracker images**               | On      | Retains validated tracker images for screenshot reuse and descriptions.                                |
| **Limit tracker lookup to identifiers**           | Off     | Limits supported tracker-data lookups to identity fields.                                              |
| **Automatically select largest Blu-ray playlist** | Off     | Lets unattended CLI preparation choose the largest Blu-ray playlist instead of requiring confirmation. |
| **Look up Blu-ray.com releases**                  | Off     | Enables blu-ray.com matching for BDMV or DVD sources with an IMDb ID.                                  |
| **Blu-ray match threshold**                       | `94.5`  | Sets the normal blu-ray.com candidate score threshold.                                                 |
| **Single-candidate Blu-ray threshold**            | `89.5`  | Sets the threshold used when only one candidate is available.                                          |

**Limit tracker lookup to identifiers** restricts normal description and image enrichment. Inspect the prepared result when combining it with image collection. Imported comparison blocks stay in descriptions and are excluded from the ordinary screenshot set. To reuse eligible saved screenshots, open **Screenshots** and choose **Use saved images** when offered.

## Verify the change

Save, prepare a synthetic release, then inspect **Tracker Data** and the preparation progress. Blu-ray lookup requires a disc source and IMDb identity; an ordinary video file will not exercise those settings.
