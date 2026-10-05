---
title: Upload workflow
description: How upbrr turns one source into reviewed tracker operations and registered torrent artifacts.
---

# Upload workflow

upbrr separates source preparation, tracker decisions, media work, payload review, submission, and post-upload effects. Each stage consumes the exact prepared state approved by earlier stages.

## 1. Select the source

Provide a release folder or file. upbrr resolves the source layout, finds reusable torrent-client data when enabled, and creates a prepared release generation.

One input can be active per database, shared by the Web UI and CLI. Tabs in the same browser session resume that input. Another session or process receives a busy result while it is owned. Close the active input before handing it to another session. Closing retains history and reusable images.

Switching during local preparation cancels that work and waits for cleanup. A submission or uncertain remote outcome must finish or be reconciled before switching. Closing a browser tab does not release the input.

After upgrading an existing database, an earlier interrupted external effect can require reconciliation before normal input work resumes. Inspect the remote result, then choose **Confirmed not completed; allow a fresh exact attempt** only when that outcome is known. This process never repeats a submission automatically. Resolve every listed workflow before opening a new input.

Every explicit open, history restore, or refresh checks the source path and inventory and hashes at most the first 1 MiB of each file. File sizes and modification times are checked before and after sampling. This is a lightweight identity check, not verification of every source byte. Torrent creation still performs its required piece hashing. A plain page reload reads the current state without starting another verification.

A fresh application start leaves the previous idle input closed while retaining its History and saved corrections. Open it explicitly to continue. Work waiting for API feedback or external-outcome reconciliation remains available through explicit continuation or recovery; startup does not automatically resume it.

Folder handling matters. `--keep-folder` preserves a supplied folder instead of processing only a selected video file.

### Multi-disc DVD and Blu-ray folders

Select the collection parent when every disc belongs to one release. Supported collections contain extracted folders of one disc format:

```text
Example DVD Collection/
├── Disc 1/VIDEO_TS/...
└── Disc 2/VIDEO_TS/...
```

```text
Example BDMV Collection/
├── Disc 1/BDMV/...
└── Disc 2/BDMV/...
```

upbrr prepares one release and creates one torrent rooted at the selected collection, preserving every disc folder. For BDMV, select at least one playlist for every disc. Duplicate playlist names such as `00001.MPLS` remain separate because upbrr tracks their disc identity.

## 2. Review canonical metadata

Metadata providers and local media inspection produce shared release facts. Review at least:

- title and year;
- movie, TV, anime, or other category;
- release type, source, and resolution;
- season, episode, edition, service, distributor, region, and group;
- external IDs;
- generated release name.

Automatic category detection checks source folders and filenames for TV hints before using the parsed release category, with Movie as the final fallback. Parent folders affect category only; they do not replace the parsed release title. A `Season 1` directory is a TV-category hint without supplying a season number; canonical season tokens still require two or four digits. Tracker-provided categories take precedence over automatic detection, and an explicit category correction in Input or `--category` remains authoritative.

Overrides change the prepared generation. Later operations must use that exact generation rather than silently rebuilding it.

Refresh obtains current provider facts and withdraws earlier duplicate decisions and upload approval. It preserves compatible screenshot content and hosted links. Reset requests a new preparation of the source; source or capture changes can make earlier images incompatible. A provider failure is reported rather than presenting old provider data as fresh.

Input readiness evaluates missing release facts and selected tracker metadata before tracker assessment. Correct missing source, type, genre, or languages on Input. A tracker-specific requirement affects that tracker; global missing facts prevent advancement.

Explicit corrections win over history and provider metadata. Auto removes a correction, while an empty list, a zero manual year, or explicit false retains manual authority. Provider failures preserve accepted edits. A changed content identity can require confirmation of saved corrections. An explicit TV category correction permits TVDB lookup even when the filename was parsed as Movie; TV naming year still follows eligible TVDB alias evidence.

When **Saved values need review** appears on Input, review the affected fields and the source or provider identity changes shown with each saved value. Choose **Keep saved value**, edit the value where supported, or choose **Use automatic value** to discard only that field's saved correction. Provider-owned fields remain read-only. **Use automatic values for all affected fields** resets only the fields listed in that review, preserving other corrections and history. Select **Apply and continue** to resume preparation; any unresolved fields remain visible. Automatic values are derived during preparation and are not shown as known while the release snapshot is unavailable. If identity evidence changes again, review the refreshed context before confirming again.

You can reload a paused correction review in the same owning browser session. To remove a stored release instead, select it in **History** and choose **Remove from database**. The deletion action remains available when its details fail to load; the confirmation still applies.

Video naming follows the effective release type, including your corrections: AVC/HEVC encodes use `x264`/`x265`, Blu-ray remuxes retain `AVC`/`HEVC`, and WEB-DL releases use `H.264`/`H.265`. An explicit release type takes precedence over the filename and source; select Auto to restore automatic type detection. The underlying measured codec remains unchanged. Older cached names are regenerated the next time the release is prepared; existing corrections are retained.

If a provider fails or selects the wrong title, supply a correct ID or [clear that provider](../cli/index.md#clear-a-metadata-provider). Clearing suppresses its ID and metadata for this source, including later reloads. Other providers remain available, but trackers that require the cleared provider may be blocked.

## 3. Resolve tracker names and eligibility

Each tracker can project its own upload and duplicate-search names from the reviewed source facts. upbrr resolves those names before duplicate checks so the search evidence and eventual payload refer to the same reviewed identity.

Tracker group policies are evaluated separately for each selected tracker and travel with that tracker's reviewed projection. Changing a tracker's group lists invalidates the affected reviewed projection and duplicate decision so they are rebuilt from the new policy. A matching personal-release group supplies the default only while **Personal Release** is **Auto**; an explicit **Yes** or **No** remains authoritative through upload and restart.

Tracker rules and constructibility checks can mark a lane ready, blocked, skipped, or requiring manual review. A tracker-specific block need not stop other eligible trackers.

For warnings that permit an override, turn on **Acknowledge tracker warnings** on the tracker's **Dupe Check** card or answer the CLI prompt. Approval covers the current warnings. Turn the toggle off to withdraw approval. Changed warnings or a new prepared generation require renewed approval. Strict failures cannot be overridden. See [tracker warning approval](../trackers/index.md#approve-tracker-warnings) for unattended and debug behavior.

## 4. Review duplicate evidence

Duplicate search results are evidence, not an automatic upload decision. Review candidate names, metadata, and tracker warnings. Where approval is required, select an explicit non-empty tracker subset after the duplicate stage.

Before searching, upbrr excludes trackers with a confirmed upload of the same verified submitted content. These appear as **Already uploaded**. Remaining trackers receive fresh duplicate checks; if every selected tracker is excluded, the operation completes successfully without another approval or upload. Changing metadata or a screenshot playlist does not make a full-disc upload new content. A different actual file subset can be different submitted content.

A complete structured group is preferred for group-policy decisions. When a tracker omits that field, only one unambiguous normalized release-name suffix can prove different-group ownership; conflicting, missing, or multi-group text keeps the normal duplicate review.

## 5. Prepare media and descriptions

Depending on the source and trackers, upbrr can:

- inspect MediaInfo, BDInfo, DVD, or other prepared technical data;
- optionally generate local waveform and spectrogram PNGs from prepared audio tracks;
- select Blu-ray playlists;
- generate screenshots at chosen frames;
- capture compatible DVD menus or import disc-menu images;
- upload selected images to allowed hosts;
- build tracker-specific BBCode descriptions.

Automatic screenshot plans distribute the requested images across all prepared discs, with at least one planned image per disc. Manual frame numbers apply to every disc. Screenshot previews, captures, and DVD menu images remain grouped by disc; DVD menu capture can warn about partial coverage when its collection-wide safety cap or the available menus leave a disc uncovered.

Capturing an additional frame keeps previously generated screenshots, including their selection and order, without adding those images again.

Inspect image ordering, host URLs, technical blocks, headers, and rendered BBCode. Open an image to inspect it at its natural resolution in a nearly full-viewport lightbox. Scroll horizontally or vertically for larger images; **Close** and **Escape** remain available while scrolling.

Compatible images retain their selection and order across refresh. In the Web UI, the Screenshots page reloads its saved-image suggestions when preparation or tracker assessment changes, while keeping edited frame times. Deleted images stay removed. upbrr verifies local image bytes before reuse and reuses hosted links only for a compatible host account and purpose. A hosted link can remain usable when its local preview is missing. Changed capture settings or stricter tracker requirements can require additional images.

[Audio analysis](audio-analysis.md) is a separate, optional operation. It streams decoded samples from FFmpeg into Go without writing a full decoded-audio file. Its PNGs are retained for preview and download. When descriptions are generated from a completed analysis, upbrr hosts the graphs for each tracker and adds them with the statistics to the default description.

## 6. Preview immutable tracker operations

Tracker preparation captures an immutable operation. Payload preview and live submission use that captured state rather than regenerating names, rereading mutable prepared input, or uploading images again.

- **Description preview** prepares only the description.
- **Dry run** and upload review can prepare a preview but cannot submit it.
- **Upload** consumes the approved operation once.

Short-lived remote tokens can still be acquired at submission time when required by a tracker.

## 7. Submit and retain registered torrents

After confirmed tracker success, upbrr records the tracker result and attempts to retain the tracker-registered torrent. Client injection consumes that registered artifact, not the pre-upload torrent.

A failure to download or persist the registered torrent does not turn a confirmed remote upload into a failed upload. Review the warning and recover the torrent manually when needed.

Deleting a release from History removes its associated local workflow, effect, and submission records along with generated artifacts. This also removes local repeat-submission protection for that release. It does not undo remote uploads or delete source media. Retained unknown outcomes require reconciliation and are never treated as confirmed success.

## 8. Inject into clients

Client injection is enabled by default when configured. Disable it explicitly with CLI `--no-seed` or the corresponding Web UI upload option.

Check save path, category, tags, automatic management, staging mode, and source-file access. Hardlink, reflink, and symlink staging have filesystem-specific requirements.

## Debug and unattended modes

| Mode                   | Submission                                | Prompts                  | Client injection           |
| ---------------------- | ----------------------------------------- | ------------------------ | -------------------------- |
| Normal                 | allowed after review                      | allowed                  | enabled when configured    |
| `--debug`              | suppressed                                | allowed                  | enabled unless `--no-seed` |
| `--unattended`         | allowed when all required decisions exist | never                    | enabled when configured    |
| `--unattended_confirm` | allowed when confirmed                    | required prompts allowed | enabled when configured    |

Debug mode is not a non-mutating dry run. It can perform screenshots, image uploads, remote searches, tracker preparation, and later workflow effects. Add `--no-seed` when testing without client injection.

## Tracker questions

Answer tracker-specific questions on **Dupe Checking**, alongside release-name and duplicate review. Questions appear immediately for selected trackers, including defaults, using the current prepared facts. Loading these questions does not run duplicate searches, remote metadata enrichment, authentication, banned-group checks, or claim checks. If the current prepared facts are unavailable or insufficient for this review, upbrr asks you to prepare the source instead of fetching metadata in the background.

Each tracker panel starts collapsed, with **Required** and **Unapplied changes** cues visible when applicable. Open its summary with a click, Enter, or Space to review text, multiline notes, dropdowns, or multiple choices. Collapsing a panel preserves your edits. Choose **Apply tracker answers** to refresh the tracker assessment. Applying answers does not upload or start another duplicate search. Unapplied changes block dry runs and uploads.

An answer-only edit retains fresh duplicate evidence when every duplicate-relevant input still matches. Changes to the source, reviewed names, selected trackers, configuration, or rules, and expired evidence, can require a new duplicate check. Follow the stages requested after applying; retained evidence keeps its original freshness window.

PTP and GPW group metadata is not requested speculatively in the normal question panels. Some new-group or channel requirements can only be confirmed during tracker preparation. When preparation reports them, late-only fields appear in a separate **Tracker preparation details** section on **Dupe Checking**. Requirements for an existing question update its normal tracker panel. Existing-group uploads do not require new-group-only details. Tracker-local source and encoding notes are answered here; canonical metadata and language corrections remain in **Input**.

### Subtitle review

Set regular and hardcoded subtitle languages with the editable language dropdowns in Input. The hardcoded list offers English (Full) and English (Forced); the ordinary list suggests base-language names. Both lists accept custom text. Adding or removing a hardcoded language controls hardcoded handling; there is no separate hardcoded-subtitles toggle. Auto resets the language correction and any retained legacy boolean override.

PTP can require an explicit subtitle/trumpable review before its lane proceeds. Known hardcoded language corrections already provide the necessary intent and avoid another prompt. In the Web UI, open PTP under **Tracker questions** on **Dupe Checking**, and choose **Apply tracker answers** after answering. A first answer may reveal additional choices or a language field. Select every applicable subtitle choice before applying again, then run or continue the duplicate check on the same page. Accepted answers remain editable while the review applies. **English Softsubs Exist (Mislabeled)** does not correct the detected language; make language corrections separately in **Input**.

Changing a reviewed answer refreshes dependent tracker work, retaining compatible duplicate evidence as described above. Complete any workflow stages requested afterward. The final payload uses the accepted answers from the exact reviewed projection.

## Final review checklist

Before submission, verify:

- release name matches current tracker rules;
- category and type are correct for every tracker;
- movie, TV, disc, remux, encode, WEB, HDTV, pack, season, and episode handling are correct;
- source, resolution, edition, service, distributor, region, language, tag, and group are correct;
- screenshots are valid, ordered, and hosted on allowed hosts;
- description BBCode renders correctly;
- torrent contents, piece settings, and announce behavior are expected;
- client category, tags, save path, and injection target are correct;
- duplicate results, rule warnings, and manual prerequisites have been read.

When any item is uncertain, stop before upload and resolve it manually.
