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

**Collect existing tracker images** and **Limit tracker lookup to only metadata ids** work independently. With both enabled, supported trackers can still supply images while omitting description text. Imported comparison blocks stay in descriptions and are excluded from the ordinary screenshot set. Eligible existing description images are loaded automatically when you open **Screenshots**. Review them in the normal screenshot list, where you can unselect, delete or reorder them. Their capture timestamps and provenance are unknown, including their chronological order relative to new captures.

## Blu-ray edition matching

A recognized three-letter country marker in the selected source folder, such as `USA` or `GER`, restricts the displayed Blu-ray.com candidates to that release country. This country is separate from playback regions A, B, and C, which appear in each candidate's technical details.

Discovery reads the search and release listing without fetching individual release pages. Candidates initially show **Details not fetched** and **Not compared**. Exactly one matching-country edition is selected automatically regardless of score. If several editions match, automatic score selection waits until all eligible editions have been inspected; the highest score must meet **Blu-ray match threshold**, with equal scores using title order. You can select another edition from that country's candidates manually. If none match, no edition is selected or invented.

Use **Input → Release country** or CLI `--region` to correct the country. An explicit correction takes precedence over the folder marker. **Auto** restores folder detection. Clearing the field or entering an unknown country leaves matching unrestricted, using the normal thresholds, including **Single-candidate Blu-ray threshold** for one candidate. A country change reconsiders the catalogue rather than reusing an incomplete list from the previous country.

Prepare older stored generations again after upgrading so their automatic edition selections follow these rules. Requiring an existing prepared generation rejects incompatible older facts; this does not require a database migration.

On **Blu-ray Candidates**, choose **Fetch release details** for one edition or **Fetch all release details** for the eligible listing. These actions add technical specifications, scores, dates and available covers to the existing cards. Expand **Score details** to read every scoring reason. Selecting a listing uses its country, publisher and release link immediately; enabled cover handling fetches only that selected edition. A failed detail request leaves the listing and successful editions available. A failed catalogue lookup is reported for an explicitly requested inspection. **Cancel** requests cancellation of the inspection. The interface waits for its outcome before confirming cancellation. If cancellation cannot be confirmed within 30 seconds or the request outcome is unknown, refresh the workflow before retrying; an inspection may still be running.

**Skip selection** explicitly leaves the release unselected. **Auto selection** restores country and score matching. Manual choices survive ordinary metadata edits and later detail/scoring updates. Compatible catalogue and detail evidence is reused, including after reloading the input. Changing the IMDb identity or release country reconsiders the applicable catalogue.

Older saved edition details are rechecked before they can affect matching or descriptions. Saved manual choices remain selected by their exact release identity. If an old unselected listing cannot be verified, refresh it or supply an exact edition URL.

To select an exact release without scanning other editions, paste its Blu-ray.com release URL into **Input → Site URL override**. Tracker and other metadata URLs continue to work there. Use **Auto selection** to remove this manual choice, or clear the URL before refreshing metadata. Invalid or unavailable exact releases produce an error.

After inspection, **Release date** shows the edition-specific `YYYY-MM-DD` value, or **Unavailable** if it cannot be read. Uninspected dates say **Not fetched**. Dates are calendar values and are not shifted by your timezone.

## Verify the change

Save, prepare a synthetic release, then inspect **Tracker Data** and the preparation progress. Automatic Blu-ray discovery requires a disc source and IMDb identity; an exact-release override does not require an IMDb lookup. An ordinary video file will not exercise those settings.
