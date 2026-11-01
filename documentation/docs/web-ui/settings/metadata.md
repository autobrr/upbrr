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
| **Limit tracker lookup to only metadata ids**     | Off     | Limits supported tracker-data lookups to identity fields.                                              |
| **Automatically select largest Blu-ray playlist** | Off     | Lets unattended CLI preparation choose the largest Blu-ray playlist instead of requiring confirmation. |
| **Look up Blu-ray.com releases**                  | Off     | Enables blu-ray.com matching for BDMV or DVD sources with an IMDb ID.                                  |
| **Blu-ray match threshold**                       | `94.5`  | Sets the normal blu-ray.com candidate score threshold.                                                 |
| **Single-candidate Blu-ray threshold**            | `89.5`  | Sets the threshold for one candidate when no release country is locked.                                |

**Limit tracker lookup to only metadata ids** restricts normal description and image enrichment. Inspect the prepared result when combining it with image collection. Imported comparison blocks stay in descriptions and are excluded from the ordinary screenshot set. To reuse eligible saved screenshots, open **Screenshots** and choose **Use saved images** when offered.

## Blu-ray edition matching

A recognized three-letter country marker in the selected source folder, such as `USA` or `GER`, restricts the displayed Blu-ray.com candidates to that release country. This country is separate from playback regions A, B, and C, which appear in each candidate's technical details.

Exactly one matching-country edition is selected automatically regardless of score. If several editions match, the highest score must meet **Blu-ray match threshold**; equal scores use title order. You can select another edition from that country's candidates manually. If none match, no edition is selected or invented.

Use **Input → Release country** or CLI `--region` to correct the country. An explicit correction takes precedence over the folder marker. **Auto** restores folder detection. Clearing the field or entering an unknown country leaves matching unrestricted, using the normal thresholds, including **Single-candidate Blu-ray threshold** for one candidate. A country change reconsiders the catalogue rather than reusing an incomplete list from the previous country.

Prepare older stored generations again after upgrading so their automatic edition selections follow these rules. Requiring an existing prepared generation rejects incompatible older facts; this does not require a database migration.

Each release candidate shows its edition-specific **Release date** as `YYYY-MM-DD`, or **Unavailable** when the date cannot be read. Dates are calendar values and are not shifted by your timezone.

## Verify the change

Save, prepare a synthetic release, then inspect **Tracker Data** and the preparation progress. Blu-ray lookup requires a disc source and IMDb identity; an ordinary video file will not exercise those settings.
