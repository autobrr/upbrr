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
| **Dupe Check**     | Review per-tracker search results, rules, and candidate upload names.    |
| **Audio Analysis** | Generate audio PNGs and amplitude statistics text files.                 |
| **Screenshots**    | Generate, import, order, and select screenshots.                         |
| **Disc Menus**     | Capture DVD menus automatically or import disc-menu images.              |
| **Upload Images**  | Publish selected images through configured hosts.                        |
| **Descriptions**   | Build and inspect tracker-specific rendered descriptions.                |
| **Upload**         | Preview payloads, dry run, approve eligible trackers, and submit.        |

Navigation guards prevent later operations from silently using missing or stale prerequisites. When a page is unavailable, read the notice and return to the required stage.

### Generate audio analysis

**Audio Analysis** appears after Input has produced authoritative audio-track facts. Opening the page does not start FFmpeg. Choose the primary track, all tracks, or specific tracks; select waveform, spectrogram, or both; then click **Generate**.

Each successful graph can be previewed, opened at full size, or downloaded as its original PNG. Statistics can be viewed and downloaded as text. Analysis is optional: leaving the page unopened does not block upload. When descriptions are generated from a completed analysis, upbrr hosts the graphs for each tracker and adds them with the statistics to the default description.

**Cancel** stops the active analysis. **Disable** first waits for active work to stop, then hides the result from the current workflow; retained files have no time-based expiry but can be removed when the workflow or source changes. A compatible retry regenerates only missing or failed variants. Results belong to the exact prepared generation, so stale images are not shown after the source facts change.

See [Audio analysis](../workflow/audio-analysis.md) for selection, output, and troubleshooting details.

### Correct Input facts

Use **Input** to correct titles, genre, release naming fields, and languages before duplicate checking. Selected trackers share provider lookups where possible. Missing fields identify the affected trackers. An unknown source or release type requires a correction.

Corrections survive metadata refresh, history reload, and restart. Explicit values take precedence over saved values and provider results. **Auto** removes the saved correction. An empty list or explicit **No** remains a manual value.

**Refresh metadata** verifies the source again and fetches current provider facts. Compatible screenshot content, selection, order, and hosted links can be reused when media preparation runs again. Earlier duplicate decisions and upload approval must be renewed.

Tracker naming policies can declare mandatory rules for specific name components. Those rules take precedence over manual naming choices for that tracker only; they do not change the saved Input facts. When a rule overrides a choice or replaces a complete manual name, the review shows an explanation beside the effective tracker name. This authority is part of the tracker implementation, not a user setting.

A complete manual name has no reliable component boundaries. If a mandatory rule cannot safely apply to it, clear the complete-name override and regenerate the automatic name. A tracker policy may explicitly rebuild it instead. Confirm the effective name shown in review: submitting an edit that would be changed by enforcement does not confirm the unseen replacement.

Clearing **Manual year** saves an explicit zero. Choose **Auto** to restore the derived year.

Audio, subtitle, and hardcoded-subtitle fields accept comma-separated languages. Individual track edits apply only to the selected inspected track. Release-level language lists affect the aggregate without assigning languages to each track. Uninspected collection members remain marked as incomplete coverage.

Changing Movie/TV with the same TMDB ID loads metadata from the correct category. If the source or provider identity changes, saved content corrections may require confirmation, replacement, or reset. A changed track manifest requires selecting a current track again.

Input preparation stops after facts and local readiness. It does not run duplicate searches or later media operations. Generated descriptions later include manually supplied languages. User-supplied descriptions pass through each tracker's normal formatting. Selected screenshots still appear in the tracker's usual location, including separate screenshot fields where applicable.

### Clear a metadata provider

On **Input**, open **Edit Release Details** and find **External IDs**. Click **Remove** beside TMDB, IMDb, TVDB, TVmaze, or MAL. You can also delete an existing ID from its field.

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

## History

**History** shows retained releases and lets you reopen their overview. Deleting a release from History removes its stored release state; it does not delete the source media.

Opening a historical input checks its local source before adopting reusable content. Deleting an idle active release closes its input automatically; running work still prevents deletion. Deletion removes associated workflow, effect, reusable-media, and submission records, plus generated files managed by upbrr, even if the original source folder is missing. Local repeat-submission protection is removed with those records. Shared generated files and their directories are kept while another retained release still references them. Remote uploads and source media are unchanged.

Restarting the application leaves the previous idle input closed. Its History and saved corrections remain available when you explicitly reopen it. Reloading a browser tab while the application keeps running restores the current input.

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
