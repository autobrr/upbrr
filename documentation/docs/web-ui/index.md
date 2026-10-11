---
title: Web UI
description: Navigate the embedded browser workflow, settings, history, logging, and host browse access.
---

# Web UI

The Web UI is embedded in the upbrr binary and uses the same preparation, tracker, configuration, and persistence services as the CLI.

Start it with:

```powershell
.\upbrr.exe serve
```

The default address is [http://localhost:7480](http://localhost:7480).

## First-run setup

Create the administrator account in the browser, then configure one or more host browse roots in the initial setup screen. Browse roots control which folders the browser can open for release selection and file import.

Use a dedicated account password. Do not expose an unconfigured instance to an untrusted network.

## Release workspace

The left navigation follows the release workflow. Some pages appear or unlock only when their input exists.

The database owns one active input. Tabs in the same session follow its current state, including after reconnecting. Use **Close input** to release it before another session or CLI process starts work. Selecting another input waits for safe cleanup; an unfinished submission or unknown outcome must be resolved first. See [input lifecycle and reuse](../workflow/index.md#1-select-the-source).

If **Legacy workflow recovery** appears, choose **Recover workflow** and check the tracker or torrent client for the interrupted operation. Use **Confirmed not completed; allow a fresh exact attempt** only after verifying that it did not complete. Recovery does not resubmit anything, and unresolved outcomes keep new inputs blocked.

| Page               | Purpose                                                                  |
| ------------------ | ------------------------------------------------------------------------ |
| **Input**          | Choose the source path, trackers, metadata IDs, and preparation options. |
| **Tracker Data**   | Review tracker-derived metadata when available.                          |
| **Blu-ray**        | Select Blu-ray playlist or candidate data when the source requires it.   |
| **Dupe Check**     | Answer tracker questions and review search results, rules, and names.    |
| **Audio Analysis** | Generate audio PNGs and amplitude statistics text files.                 |
| **HDR Analysis**   | Generate, inspect, download, and include HDR10+ brightness plots.        |
| **Screenshots**    | Generate, import, order, and select screenshots.                         |
| **Disc Menus**     | Capture DVD menus automatically or import disc-menu images.              |
| **Upload Images**  | Publish selected images through configured hosts.                        |
| **Descriptions**   | Build and inspect tracker-specific rendered descriptions.                |
| **Upload**         | Preview payloads, dry run, approve eligible trackers, and submit.        |

Navigation guards prevent later operations from silently using missing or stale prerequisites. When a page is unavailable, read the notice and return to the required stage.

Background workflow updates preserve an unsubmitted **Source path**, unsaved **Descriptions** text, and the selected TVDB preview language for the same input. Description drafts are cleared when the active input, prepared generation, or selected trackers change. **Render** previews the current editor text without saving it. Use **Save group** to retain the complete edited description, including image order and custom text placement, without adding generated sections again. **Reset group** replaces that group with its generated description.

On **Input**, focus **Source path** to choose a recently used path from this browser, if available. [Input history limit](./settings/main.md) controls how many paths the browser keeps; setting it to `0` clears the list.

After a source preview is prepared, open the panel below **Edit Release Details** to inspect its technical report. Blu-ray discs show **BDInfo Preview** with the selected playlist summaries; other sources show **MediaInfo Preview**, where you can expand **Raw MediaInfo** for the original text. If a report is unavailable, the panel says so. This preview is separate from **Descriptions**: tracker descriptions show MediaInfo or BDInfo only when that tracker includes it.

### Reuse tracker images

With **Settings → Metadata → Keep images** enabled, preparation can retain validated screenshots from tracker data. Inspect the imported description and images on **Tracker Data**, then open **Screenshots**. When **Saved tracker images** appears, choose **Use saved images** to include those images in the workflow. upbrr can capture additional frames when the retained images do not meet the requested count or tracker requirements.

Review the final screenshot selection and order before continuing. Existing URLs are reused only when the destination tracker and chosen image host allow them; other images need rehosting. Imported comparison blocks remain part of the description and do not count as ordinary screenshots. See [image-host requirements](./settings/image-hosting.md).

### Generate audio analysis

**Audio Analysis** appears after Input has produced authoritative audio-track facts. Opening the page does not start FFmpeg. Choose the primary track, all tracks, or specific tracks; select waveform, spectrogram, or both; then click **Generate**.

Each successful graph can be previewed, opened at full size, or downloaded as its original PNG. Statistics can be viewed and downloaded as text. Analysis is optional: leaving the page unopened does not block upload. When descriptions are generated from a completed analysis, upbrr hosts the graphs for each tracker and adds them with the statistics to the default description.

**Cancel** stops the active analysis. **Disable** first waits for active work to stop, then hides the result from the current workflow; retained files have no time-based expiry but can be removed when the workflow or source changes. A compatible retry regenerates only missing or failed variants. Results belong to the exact prepared generation, so stale images are not shown after the source facts change.

See [Audio analysis](../workflow/audio-analysis.md) for selection, output, and troubleshooting details.

**HDR Analysis** appears when prepared Input contains eligible targets. MKV files require MediaInfo-confirmed HDR10+ on a unique HEVC video track. For Blu-ray, check **Check for HDR10+ in the selected playlists** during playlist selection, or use **Review selected playlists** for an already prepared disc. Confirmed playlists are plotted during preparation, ready to view on **HDR Analysis**. Opening the page does not read the source. For MKV files, select targets and a peak estimator, then click **Generate**. MKV progress shows structure discovery and metadata collection. Complete retained metadata supports estimator changes without another source scan. Completed analyses are automatically included in generated descriptions; partial plots remain inspectable. Compatible saved plots and their hosted links are restored when reopening a release. See [HDR analysis](../workflow/hdr-analysis.md).

### Correct Input facts

Use **Input** to correct provider IDs, genre, release naming fields, and languages before duplicate checking. **Title** and **Original title** are read-only; **Manual year** is editable for movies and locked for TV. Selected trackers share provider lookups where possible. Missing fields identify the affected trackers. An unknown source or release type requires a correction.

**Category**, **Type**, **Source**, and **Resolution** accept only the supported dropdown choices. They have no custom-entry or blank option. If a current value is missing or unsupported, a disabled placeholder keeps it visible without creating a correction; choose a supported value or use **Auto**. **Service**, **Region**, and **Distributor** (under **Metadata and languages**) offer searchable suggestions: focus the field or use its browse button to see all choices, then type a partial name or code to filter. Service choices show the full name and acronym, and selecting one stores the recognized acronym. Distributor suggestions come from the supported publisher catalog; selecting one keeps the publisher name so each tracker can resolve its own ID. Region suggestions include the shared country codes. Custom names and explicit numeric tracker IDs remain editable. Use the arrow keys and **Enter** to select a suggestion, or **Escape** to dismiss the list. Clearing a searchable field keeps an explicit blank correction; use **Auto** to restore automatic detection.

Edited fields show a pending change until you apply **Refresh metadata**. Applied overrides remain marked **Manual value · Applied** after refresh. **Auto** clears the local draft and queues removal of the saved correction; the field shows **Auto reset pending** until refresh derives the automatic value. The previous prepared value may remain visible while that reset is pending.

Use **Release version** to set `PROPER`, `REPACK`, their supported numbered variants, or `RERIP` independently of **Edition**. Choose **None** to remove the detected version, or **Auto** to restore detection. Leave cut or edition text in **Edition**. The legacy **No edition** option also suppresses an automatic release version; an explicit **Release version** selection takes precedence.

Corrections survive metadata refresh, history reload, and restart. Explicit values take precedence over saved values and provider results. An empty list, an empty string, a removed provider ID, or explicit **No** remains a manual value until you choose **Auto**.

**Refresh metadata** verifies the source again and applies your corrections, reusing provider lookups for unchanged inputs. It does not repeat lookups that returned no result while the same input remains active. Loading the input again after closing it can retry those empty results; completed fetch failures remain retained until you remove the input from **History**. A changed source, provider ID, or lookup query requests its own result. Compatible screenshot content, selection, order, and hosted links can be reused when media preparation runs again. Earlier duplicate decisions and upload approval must be renewed.

Tracker naming policies can declare mandatory rules for specific name components. Those rules take precedence over manual naming choices for that tracker only; they do not change the saved Input facts. When a rule overrides a choice or replaces a complete manual name, the review shows an explanation beside the effective tracker name. This authority is part of the tracker implementation, not a user setting.

A complete manual name has no reliable component boundaries. If a mandatory rule cannot safely apply to it, clear the complete-name override and regenerate the automatic name. A tracker policy may explicitly rebuild it instead. Confirm the effective name shown in review: submitting an edit that would be changed by enforcement does not confirm the unseen replacement.

The **Edition** field shows the detected cut, edition, and presentation labels, or just `2in1` for a two-edition set. Replace or clear this field to override those automatic labels, or set **No edition** to **Yes** to omit them. Apply **Refresh metadata**; choose **Auto** on the edited control to restore automatic handling.

A solitary Theatrical cut or edition is ignored; compound editions retain it. Review the effective selections and supporting evidence alongside the payload preview. See [editions and commentary](../trackers/index.md#editions-and-commentary) for manual corrections.

Clearing **Manual year** saves an explicit zero. Choose **Auto** to restore the derived year.

Audio languages accept comma-separated values. Subtitle and hardcoded-subtitle languages use editable, searchable rows with **Remove** controls and an empty row for adding another language; custom names and comma-separated entries are accepted. Hardcoded English choices distinguish **English (Full)** from **English (Forced)**. Individual track edits apply only to the selected inspected track. Release-level language lists affect the aggregate without assigning languages to each track. Uninspected collection members remain marked as incomplete coverage.

Changing Movie/TV with the same TMDB ID loads metadata from the correct category. If the source or provider identity changes, saved content corrections may require confirmation, replacement, or reset. A changed track manifest requires selecting a current track again.

Input preparation stops after facts and local readiness. It does not run duplicate searches or later media operations. Generated descriptions later include manually supplied languages. User-supplied descriptions pass through each tracker's normal formatting. Selected screenshots still appear in the tracker's usual location, including separate screenshot fields where applicable.

### Clear a metadata provider

On **Input**, open **Edit Release Details** and find **Provider IDs**. Click **Remove** beside TMDB, IMDb, TVDB, TVmaze, or MAL. You can also delete an existing ID from its field.

Click **Refresh metadata** to apply the change. The provider's ID and metadata are removed from the prepared release, and automatic lookups cannot restore them. Other providers remain available.

If the first fetch fails before a preview appears, **Edit Release Details** is still available. Remove the failing provider and click **Retry metadata**.

The clear is saved for this source and survives release reloads. Enter a positive ID and refresh metadata to use that provider again. Check tracker eligibility afterward: trackers that require the cleared provider can remain blocked.

See [metadata troubleshooting](../troubleshooting/index.md#a-metadata-provider-fails-or-selects-the-wrong-title) for CLI examples and recovery guidance.

### Multi-disc sources

On **Input**, select the parent containing all DVD or BDMV disc folders. BDMV playlist choices are grouped by disc, and preparation cannot continue until every disc has at least one selected playlist. Identical playlist filenames on different discs remain independent choices.

The **Screenshots** page groups planned frames and generated images by disc and lets you choose the disc used for live preview. **Disc Menus** groups captured and imported menu images by disc. These groups survive release reloads, and **Upload** creates one collection-root torrent containing every disc folder.

## Settings

Use **Settings** to manage:

- metadata services;
- tracker defaults, credentials, auth status, and tracker-owned fields;
- image-host priority and credentials;
- screenshot and description behavior;
- torrent creation and client integration;
- post-upload behavior;
- config import and export.

Saving settings can remain **Pending** until current operations finish safely. Wait for activation status before using the change. Recheck tracker auth and run a dry run after changing credentials or upload behavior.

See the [Settings reference](./settings/index.md) for every section, field behavior, and verification guidance.

Use [Appearance](./settings/appearance.md) to choose a bundled theme and light or dark mode. The change applies immediately in this browser, including the sign-in screen.

## History

**History** shows retained releases and lets you reopen their overview. Deleting a release from History removes its stored release state; it does not delete the source media.

Opening a historical input checks its local source before adopting reusable content. Deleting an idle active release closes its input automatically; running work still prevents deletion. Deletion removes associated workflow, effect, reusable-media, and submission records, plus generated files managed by upbrr, even if the original source folder is missing. Local repeat-submission protection is removed with those records. Shared generated files and their directories are kept while another retained release still references them. Remote uploads and source media are unchanged.

Restarting the application leaves the previous idle input closed. Its History and saved corrections remain available when you explicitly reopen it. Unfinished operations are interrupted rather than automatically resumed; completed checkpoints are retained. Before retrying an interrupted upload, check the tracker and torrent client because startup can clear the local block on an unresolved attempt. See [restart recovery](../troubleshooting/index.md#an-operation-was-interrupted-by-restart). Reloading a browser tab while the application keeps running restores the current input.

## Logging

**Logging** shows recent sanitized application logs, a live stream, runtime verbosity, and file-rotation settings.

Logs are designed for safer sharing, but inspect them before posting. Never include configuration exports, cookies, credentials, announce URLs, or private API payloads.

See [Logging](./logging.md) for persistence, filtering, rotation, and buffer behavior.

## Host browser

The host browser lists only configured roots unless unrestricted browsing was explicitly enabled. If a valid folder is missing:

1. verify the upbrr process or container can read it;
2. verify the path is mounted into the container when using Docker;
3. stop the server and replace the roots with `upbrr auth browse-roots <path>...`;
4. use the path as visible inside the container, not the host-only path.

Imported application config does not change browse roots. The Web UI route accepts only the initial browse policy; later password and browse-policy changes require the local CLI.

## Safe first use

Use **Dry Run** on the Upload page and disable client injection for the first test. Compare every tracker tab with the tracker's current upload form and rules before allowing submission.

See [Upload workflow](../workflow/index.md) for stage ownership and review boundaries.
