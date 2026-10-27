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
| **HDR Analysis**   | Generate native HDR plots, audio analysis, and optional picture checks.  |
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

### Generate HDR analysis

**HDR Analysis** appears when prepared Input contains eligible targets. Opening the page does not start analysis. Select targets and metadata formats, then choose a mode. **HDR metadata plots and primary audio** is the default; **Bounded scene synchronization** adds checks around selected scene boundaries, with **Analyze audio** enabled by default and optional **Active-area borders**. **Picture checks and primary audio** offers sampled checks or **Measure every video frame (slower)**. Audio-enabled modes can select **Analyze all audio tracks (up to 64)**.

Click **Generate**, then inspect each format and picture/audio outcome. Native plots and selected audio images/statistics are included in descriptions by default. Select individual picture-check images to include them. Successful artifacts remain inspectable after partial attempts; a sampled or bounded result does not establish whole-video picture coverage. See [HDR analysis](../workflow/hdr-analysis.md) for source requirements, comparison sources, retries, and colour-thumbnail interpretation.

### Correct Input facts

Use **Input** to correct provider IDs, genre, release naming fields, and languages before duplicate checking. **Title** and **Original title** are read-only; **Manual year** is editable for movies and locked for TV. Selected trackers share provider lookups where possible. Missing fields identify the affected trackers. Category, Type, Source, and Resolution must be present in the saved prepared metadata before upload. When any are missing, Input shows **Required facts missing, please edit release details.** and outlines only the missing controls in red. Draft edits do not clear these indicators: apply **Refresh metadata** successfully to save each correction. A failed refresh leaves the existing indicators in place. **Auto** is complete only when its saved, derived value is present.

**Category**, **Type**, **Source**, and **Resolution** accept only the supported dropdown choices. They have no custom-entry or blank option. If a current value is missing or unsupported, a disabled placeholder keeps it visible without creating a correction; choose a supported value or use **Auto**. **Service**, **Region**, and **Distributor** (under **Metadata and languages**) offer searchable suggestions: focus the field or use its browse button to see all choices, then type a partial name or code to filter. Service choices show the full name and acronym, and selecting one stores the recognized acronym. Distributor suggestions come from the supported publisher catalog; selecting one keeps the publisher name so each tracker can resolve its own ID. Region suggestions include the shared country codes. Custom names and explicit numeric tracker IDs remain editable. Use the arrow keys and **Enter** to select a suggestion, or **Escape** to dismiss the list. Clearing a searchable field keeps an explicit blank correction; use **Auto** to restore automatic detection.

Edited fields show a pending change until you apply **Refresh metadata**. Applied overrides remain marked **Manual value · Applied** after refresh. **Auto** clears the local draft and queues removal of the saved correction; the field shows **Auto reset pending** until refresh derives the automatic value. The previous prepared value may remain visible while that reset is pending.

Use **Release version** to set `PROPER`, `REPACK`, their supported numbered variants, or `RERIP` independently of **Edition**. Choose **None** to remove the detected version, or **Auto** to restore detection. Leave cut or edition text in **Edition**. The legacy **No edition** option also suppresses an automatic release version; an explicit **Release version** selection takes precedence.

Corrections survive metadata refresh, history reload, and restart. Explicit values take precedence over saved values and provider results. An empty list, an empty string, a removed provider ID, or explicit **No** remains a manual value until you choose **Auto**.

**Refresh metadata** verifies the source again and applies your corrections, reusing provider lookups for unchanged inputs. It does not repeat lookups that returned no result while the same input remains active. Loading the input again after closing it can retry those empty results; completed fetch failures remain retained until you remove the input from **History**. A changed source, provider ID, or lookup query requests its own result. Compatible screenshot content, selection, order, and hosted links can be reused when media preparation runs again. Refreshing the same verified input can preserve a still-valid duplicate acknowledgement when the backend verifies unchanged finalized facts and compatible tracker policy. This includes normalized no-op edits and saved corrections that resolve to the same finalized facts. Changed metadata, changed source content, forced fresh preparation, or unavailable or expired evidence requires renewed review. A new prepared generation still rebuilds tracker projections and preflight; staff-token attestations require fresh confirmation when evidence is rebound. The acknowledgement switch reflects the current backend decision; an earlier acknowledgement cannot keep it checked for a result that requires review. Refreshing a successfully completed workflow reopens preparation; confirmed uploads remain excluded from resubmission.

Tracker naming policies can declare mandatory rules for specific name components. Those rules take precedence over manual naming choices for that tracker only; they do not change the saved Input facts. When a rule overrides a choice or replaces a complete manual name, the review shows an explanation beside the effective tracker name. This authority is part of the tracker implementation, not a user setting.

A complete manual name has no reliable component boundaries. If a mandatory rule cannot safely apply to it, clear the complete-name override and regenerate the automatic name. A tracker policy may explicitly rebuild it instead. Confirm the effective name shown in review: submitting an edit that would be changed by enforcement does not confirm the unseen replacement.

The **Edition** field shows the detected cut and edition, or just `2in1` for a two-edition set. Editing it preserves independent **Presentation** and **Release features**. Presentation supports IMAX, Open Matte, or both; open the **Release features** dropdown to select multiple features, such as Hybrid and 4K Restoration. **None** clears that control; **Auto** restores detection. Apply **Refresh metadata** to save the change. Legacy **No edition** suppresses automatic edition-related labels; explicit presentation/features selections take precedence.

Recognized compounds such as `Director's Cut / Hybrid` split into the cut and independent feature. Every slash-separated fragment must be recognized; custom wording remains intact. Presentation/features appear immediately after Edition, or in its position when Edition is absent, subject to each tracker's naming policy. Complete manual names retain their existing authority. Feature annotations do not change measured media facts.

The **3D** control offers Auto, Yes and No. Auto restores detected evidence without turning a subtype annotation into a measured 3D fact; Yes establishes effective 3D. Both preserve detected or manually selected stereoscopic subtype annotations even with legacy No edition enabled. An explicit Release features selection, including None, still takes precedence. No clears 3D without changing Edition or release version. No also suppresses effective stereoscopic subtype annotations while preserving saved feature selections for Auto. Recognized subtype annotations contribute only one literal `3D` marker in the dedicated naming position, immediately before Source rather than beside Edition. Generated names never include Full/Half SBS or OU wording; the underlying subtype facts remain available for tracker payloads. Explicit complete upload-name overrides remain unchanged.

A solitary Theatrical cut or edition is ignored; compound editions retain it. Review the effective selections and supporting evidence alongside the payload preview. See [editions and commentary](../trackers/index.md#editions-and-commentary) for manual corrections.

Clearing **Manual year** saves an explicit zero. Choose **Auto** to restore the derived year.

**Audio languages**, **Subtitle languages**, and **Hardcoded subtitle languages** show a compact comma-separated summary. Open a dropdown to browse or search its language choices and select or deselect the checkboxes. Custom saved names remain selectable; to add a new custom name, enter it in the search box and choose **Add**. **Clear selections** keeps an explicit empty correction, while **Auto** restores automatic detection. Use **Tab** to move between the search, actions, and checkboxes, **Space** to toggle a checkbox, and **Escape** to close the dropdown. Hardcoded English choices distinguish **English (Full)** from **English (Forced)**. Individual track edits apply only to the selected inspected track. Release-level language lists affect the aggregate without assigning languages to each track. Uninspected collection members remain marked as incomplete coverage.

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

On **Input**, choose **Browse file** or **Browse folder**. **Roots** shows every allowed browse root, even when only one is configured. **Search all browse roots** finds matching names in the roots and their nested folders, including the roots themselves. Each result shows its exact path so you can distinguish identically named files or folders.

Inside a folder, **Filter this directory** searches only the entries already listed there. Both scopes ignore case, treat common separators such as dots, underscores, hyphens, and brackets as spaces, and match query words in any order. For example, `Beacon Voyage` matches `Beacon.Voyage` and `Beacon_Voyage`. A restricted ordered-character match also finds close abbreviations; it does not correct arbitrary typos.

Choose **Open** to enter a folder or **Select** to apply that exact path to Input. File browsing continues to show only recognized video files alongside folders. **Clear search** restores the root listing or removes the current directory filter; opening another folder also clears the search.

Root searches return at most 200 matches and inspect at most 100,000 entries. They check a two-second search budget between filesystem calls; a slow or unavailable filesystem can keep a call blocked longer. If the results are marked incomplete, open a root and browse directly. A narrower query can help with the match limit, but does not reduce how many filenames must be inspected. Unreadable directories can also leave results incomplete.

Searches can use two workers when every browse root is on a recognized local filesystem. Within each worker, separate roots are visited before their subdirectories, so a deep library does not have to finish before another root gets searched. A single large directory can still use up the search budget. If roots overlap, each root is traversed in order to preserve its access boundary. If any root is on network storage or a filesystem cannot be identified, the search uses one worker. Filesystem identities do not guarantee separate physical disks. When a search reaches a limit, repeated searches may return different subsets of matching paths.

Typing another query, navigating, or closing the browser cancels the previous request, and late replies cannot replace newer results. Each browser request has a 15-second timeout that includes network time and any authentication refresh. A timeout or busy message offers **Retry**; requests are not automatically repeated after network failures. The server allows at most two host-browser operations at once and keeps an operation's slot until its filesystem work actually returns, even after cancellation.

The host browser lists only configured roots unless unrestricted browsing was explicitly enabled. If a valid folder is missing:

1. verify the upbrr process or container can read it;
2. verify the path is mounted into the container when using Docker;
3. stop the server and replace the roots with `upbrr auth browse-roots <path>...`;
4. use the path as visible inside the container, not the host-only path.

Imported application config does not change browse roots. The Web UI route accepts only the initial browse policy; later password and browse-policy changes require the local CLI.

## Safe first use

Use **Dry Run** on the Upload page and disable client injection for the first test. Compare every tracker tab with the tracker's current upload form and rules before allowing submission.

See [Upload workflow](../workflow/index.md) for stage ownership and review boundaries.
